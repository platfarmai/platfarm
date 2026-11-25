package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"platfarm/svc-pay/channel"
)

// 每个真实渠道都声明了参数字段，且密钥类字段标了 Secret —— 管理台靠它决定哪些值不回显。
func TestChannelList_declaresParamFields(t *testing.T) {
	secret := map[string][]string{
		"wxpay_partner":   {"private_key", "api_v3_key"},
		"wxpay_ecommerce": {"private_key", "api_v3_key"},
		"alipay_zft":      {"private_key"},
	}
	seen := map[string]bool{}
	for _, info := range channel.List() {
		want, ok := secret[info.IfCode]
		if !ok {
			continue
		}
		seen[info.IfCode] = true
		if len(info.Ways) == 0 || len(info.Sub) == 0 {
			t.Errorf("%s: ways=%v sub=%v", info.IfCode, info.Ways, info.Sub)
		}
		flags := map[string]bool{}
		for _, f := range info.Provider {
			flags[f.Key] = f.Secret
		}
		for _, k := range want {
			if !flags[k] {
				t.Errorf("%s: %s must be declared as a secret provider field", info.IfCode, k)
			}
		}
	}
	for code := range secret {
		if !seen[code] {
			t.Errorf("%s not registered", code)
		}
	}
}

func TestAppsAndPassages_lifecycle(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-APPS", "M-APPS", "stub_channel")
	r := testRouter()

	created := call(t, r, "op", http.MethodPost, "/api/pay/admin/apps", map[string]any{"mchNo": "M-APPS", "appName": "收银台"})
	mustCode(t, created, 200)
	appID, secret := created.Body["appId"].(string), created.Body["appSecret"].(string)
	if appID == "" || secret == "" {
		t.Fatalf("create app: %v", created.Body)
	}

	mustCode(t, call(t, r, "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": appID, "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 60}), 200)
	mustCode(t, call(t, r, "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": appID, "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 10001}), 400)

	list := call(t, r, "op", http.MethodGet, "/api/pay/admin/apps?mchNo=M-APPS", nil)
	mustCode(t, list, 200)
	raw, _ := json.Marshal(list.Body)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "appSecret") {
		t.Fatalf("app list leaks the secret: %s", raw)
	}
	items := list.Body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %v", items)
	}
	app := items[0].(map[string]any)
	passages := app["passages"].([]any)
	if app["appId"] != appID || app["state"] != stateActive || len(passages) != 1 {
		t.Fatalf("app: %v", app)
	}
	if p := passages[0].(map[string]any); p["wayCode"] != "STUB_PAY" || p["rateBps"].(float64) != 60 || p["state"] != stateActive {
		t.Fatalf("passage: %v", p)
	}

	// 停用通道后下单路由找不到它；重新 PUT 即恢复。
	mustCode(t, call(t, r, "op", http.MethodDelete, "/api/pay/admin/passages?appId="+appID+"&wayCode=STUB_PAY", nil), 200)
	if _, err := loadPassage(ctx, appID, "STUB_PAY"); !notFound(err) {
		t.Fatalf("disabled passage still routable: %v", err)
	}
	mustCode(t, call(t, r, "op", http.MethodDelete, "/api/pay/admin/passages?appId="+appID+"&wayCode=NOPE", nil), 404)
	mustCode(t, call(t, r, "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": appID, "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 38}), 200)
	if p, err := loadPassage(ctx, appID, "STUB_PAY"); err != nil || p.RateBps != 38 {
		t.Fatalf("re-enabled passage: %+v %v", p, err)
	}

	// 换发密钥：旧密钥不再是库里那把，新密钥只在响应里出现一次。
	rotated := call(t, r, "op", http.MethodPost, "/api/pay/admin/apps/"+appID+"/secret", nil)
	mustCode(t, rotated, 200)
	next := rotated.Body["appSecret"].(string)
	stored, _ := loadApp(ctx, appID)
	if plain, err := open(stored.AppSecret); err != nil || plain != next || next == secret {
		t.Fatalf("rotate: stored=%q next=%q old=%q err=%v", plain, next, secret, err)
	}

	mustCode(t, call(t, r, "op", http.MethodPut, "/api/pay/admin/apps/"+appID+"/state", map[string]any{"state": "paused"}), 400)
	mustCode(t, call(t, r, "op", http.MethodPut, "/api/pay/admin/apps/"+appID+"/state", map[string]any{"state": stateDisabled}), 200)
	if a, _ := loadApp(ctx, appID); a.State != stateDisabled {
		t.Fatalf("state = %s", a.State)
	}
	mustCode(t, call(t, r, "op", http.MethodPost, "/api/pay/admin/apps/A-NOPE/secret", nil), 404)
	mustCode(t, call(t, r, "nobody", http.MethodGet, "/api/pay/admin/apps", nil), 403)
}

func TestChannelConfig_viewMasksSecretsAndMergeKeepsOtherKeys(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-CFG", "M-CFG", "stub_channel")
	r := testRouter()
	put := func(body map[string]any) resp {
		return call(t, r, "op", http.MethodPut, "/api/pay/admin/channel-config", body)
	}

	mustCode(t, put(map[string]any{"ownerType": "isv", "ownerId": "ISV-CFG", "ifCode": "wxpay_partner", "params": map[string]string{
		"sp_mchid": "1900000109", "sp_appid": "wx8888", "private_key": "-----BEGIN PRIVATE KEY-----TOPSECRET",
		"api_v3_key": "0123456789abcdef0123456789abcdef", "legacy_token": "undeclared-secret",
	}}), 200)
	// 进件 / 证书同步写进来的键：合并保存不能把它们冲掉。
	if err := mergeChannelParams(ctx, "isv", "ISV-CFG", "wxpay_partner",
		map[string]string{"wx_platform_cert_serial": "SERIAL-1"}, 0); err != nil {
		t.Fatal(err)
	}

	view := call(t, r, "op", http.MethodGet, "/api/pay/admin/channel-config?ownerType=isv&ownerId=ISV-CFG", nil)
	mustCode(t, view, 200)
	raw, _ := json.Marshal(view.Body)
	for _, leak := range []string{"TOPSECRET", "0123456789abcdef", "undeclared-secret"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("channel-config view leaks %q: %s", leak, raw)
		}
	}
	fields := map[string]map[string]any{}
	for _, it := range view.Body["items"].([]any) {
		cfg := it.(map[string]any)
		if cfg["ifCode"] != "wxpay_partner" {
			continue
		}
		for _, f := range cfg["fields"].([]any) {
			fields[f.(map[string]any)["key"].(string)] = f.(map[string]any)
		}
	}
	if f := fields["sp_mchid"]; f["value"] != "1900000109" || f["set"] != true || f["secret"] != false {
		t.Fatalf("sp_mchid: %v", f)
	}
	for _, k := range []string{"private_key", "api_v3_key", "legacy_token"} {
		if f := fields[k]; f["set"] != true || f["secret"] != true || f["value"] != "" {
			t.Fatalf("%s: %v", k, f)
		}
	}
	if f := fields["wx_public_key"]; f == nil || f["set"] != false {
		t.Fatalf("declared but unset field must be listed as unset: %v", f)
	}

	// 合并：只带改动的键。非空覆盖、空串删除、没带的（私钥、平台证书序列号）原样保留。
	mustCode(t, put(map[string]any{"ownerType": "isv", "ownerId": "ISV-CFG", "ifCode": "wxpay_partner", "merge": true,
		"params": map[string]string{"sp_appid": "wx9999", "legacy_token": ""}}), 200)
	got, err := loadChannelParams(ctx, "isv", "ISV-CFG", "wxpay_partner")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"sp_mchid": "1900000109", "sp_appid": "wx9999", "private_key": "-----BEGIN PRIVATE KEY-----TOPSECRET",
		"api_v3_key": "0123456789abcdef0123456789abcdef", "wx_platform_cert_serial": "SERIAL-1",
	}
	if len(got) != len(want) {
		t.Fatalf("merged params: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("merged %s = %q, want %q", k, got[k], v)
		}
	}

	// 合并到还没有配置的渠道等于新建。
	mustCode(t, put(map[string]any{"ownerType": "merchant", "ownerId": "M-CFG", "ifCode": "wxpay_partner", "merge": true,
		"params": map[string]string{"sub_mchid": "1900000300"}}), 200)
	if p, _ := loadChannelParams(ctx, "merchant", "M-CFG", "wxpay_partner"); p["sub_mchid"] != "1900000300" {
		t.Fatalf("merge-create: %v", p)
	}

	mustCode(t, call(t, r, "op", http.MethodGet, "/api/pay/admin/channel-config?ownerType=isv&ownerId=ISV-NOPE", nil), 404)
	mustCode(t, call(t, r, "op", http.MethodGet, "/api/pay/admin/channel-config?ownerType=bank&ownerId=ISV-CFG", nil), 400)
}

// 新增的读写接口同样受租户闸门约束：别的租户看不到、改不了。
func TestTenantIsolation_AppsAndChannelConfig(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	testUsers["t111"] = &Claims{TokenType: "access", UserId: 21, Username: "t111", Role: "user", TenantId: 111}
	testGrants[21] = map[string]bool{roleOperator: true}
	defer func() { delete(testUsers, "t111"); delete(testGrants, 21) }()

	seedISVAndMerchant(t, "ISV-T222", "M-T222", "stub_channel")
	seedMerchantParams(t, "M-T222", "stub_channel", map[string]string{"sub_mchid": "1900000222"})
	seedApp(t, "M-T222", "A-T222")
	for _, sql := range []string{
		`UPDATE pay_isv SET tenant_id=222 WHERE isv_no='ISV-T222'`,
		`UPDATE pay_merchant SET tenant_id=222 WHERE mch_no='M-T222'`,
		`UPDATE pay_app SET tenant_id=222 WHERE app_id='A-T222'`,
		`INSERT INTO pay_passage (app_id, mch_no, if_code, way_code, rate_bps, tenant_id)
			VALUES ('A-T222','M-T222','stub_channel','STUB_PAY',60,222) ON CONFLICT DO NOTHING`,
	} {
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := loadApp(ctx, "A-T222")

	r := testRouter()
	deny := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/pay/admin/apps/A-T222/secret", nil},
		{http.MethodPut, "/api/pay/admin/apps/A-T222/state", map[string]any{"state": stateDisabled}},
		{http.MethodDelete, "/api/pay/admin/passages?appId=A-T222&wayCode=STUB_PAY", nil},
		{http.MethodGet, "/api/pay/admin/channel-config?ownerType=merchant&ownerId=M-T222", nil},
		{http.MethodGet, "/api/pay/admin/channel-config?ownerType=isv&ownerId=ISV-T222", nil},
		{http.MethodPut, "/api/pay/admin/channel-config", map[string]any{"ownerType": "merchant", "ownerId": "M-T222",
			"ifCode": "stub_channel", "merge": true, "params": map[string]string{"sub_mchid": "1900000666"}}},
	}
	for _, tc := range deny {
		res := call(t, r, "t111", tc.method, tc.path, tc.body)
		if msg, _ := res.Body["error"].(string); res.Code != http.StatusForbidden || !strings.Contains(msg, "租户") {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, res.Code, res.Body)
		}
	}
	after, _ := loadApp(ctx, "A-T222")
	if after.AppSecret != before.AppSecret || after.State != stateActive {
		t.Fatal("other tenant's app was changed")
	}
	if _, err := loadPassage(ctx, "A-T222", "STUB_PAY"); err != nil {
		t.Fatalf("other tenant's passage was disabled: %v", err)
	}
	if p, _ := loadChannelParams(ctx, "merchant", "M-T222", "stub_channel"); p["sub_mchid"] != "1900000222" {
		t.Fatalf("other tenant's params changed: %v", p)
	}

	list := call(t, r, "t111", http.MethodGet, "/api/pay/admin/apps", nil)
	mustCode(t, list, 200)
	for _, it := range list.Body["items"].([]any) {
		if it.(map[string]any)["appId"] == "A-T222" {
			t.Fatal("t111 sees tenant 222's app")
		}
	}
	seen := false
	all := call(t, r, "admin", http.MethodGet, "/api/pay/admin/apps?mchNo=M-T222", nil)
	for _, it := range all.Body["items"].([]any) {
		seen = seen || it.(map[string]any)["appId"] == "A-T222"
	}
	if !seen {
		t.Fatal("platform admin must see every tenant's apps")
	}
}
