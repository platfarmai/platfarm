package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// authStub 顶替 auth 的内网管理接口：记下每次调用，按路径回最小可用的响应。
type authStub struct {
	mu       sync.Mutex
	calls    []string // "METHOD path"
	tokens   []string // 每次调用带来的代持用户 token
	users    map[string]int
	apps     map[string]int // appKey → 已签发次数
	nextID   int
	failApps bool
}

func useAuthStub(t *testing.T, firstUserID int) *authStub {
	t.Helper()
	s := &authStub{users: map[string]int{}, apps: map[string]int{}, nextID: firstUserID}
	prev := authAdmin
	authAdmin = s.handle
	t.Cleanup(func() { authAdmin = prev })
	return s
}

func (s *authStub) handle(_ context.Context, userToken, method, path string, body any) (int, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method+" "+path)
	s.tokens = append(s.tokens, userToken)
	raw, _ := json.Marshal(body)
	reply := func(code int, v any) (int, []byte, error) {
		out, _ := json.Marshal(v)
		return code, out, nil
	}
	switch {
	case method == http.MethodPost && path == "/internal/auth/users":
		var in struct{ Username string }
		_ = json.Unmarshal(raw, &in)
		if _, taken := s.users[in.Username]; taken {
			return reply(409, map[string]string{"error": "username exists"})
		}
		s.users[in.Username] = s.nextID
		s.nextID++
		return reply(201, map[string]any{"id": s.users[in.Username], "username": in.Username, "password": "pw-" + in.Username})
	case method == http.MethodPost && path == "/internal/auth/apps":
		if s.failApps {
			return reply(500, map[string]string{"error": "boom"})
		}
		var in struct{ AppKey string }
		_ = json.Unmarshal(raw, &in)
		if s.apps[in.AppKey] > 0 {
			return reply(409, map[string]string{"error": "appKey exists"})
		}
		s.apps[in.AppKey] = 1
		return reply(201, map[string]any{"appKey": in.AppKey, "appSecret": "as-" + in.AppKey + "-1"})
	case method == http.MethodPost && strings.HasPrefix(path, "/internal/auth/apps/") && strings.HasSuffix(path, "/rotate"):
		key := strings.TrimSuffix(strings.TrimPrefix(path, "/internal/auth/apps/"), "/rotate")
		if s.apps[key] == 0 {
			return reply(404, map[string]string{"error": "not found"})
		}
		s.apps[key]++
		return reply(200, map[string]any{"appKey": key, "appSecret": fmt.Sprintf("as-%s-%d", key, s.apps[key])})
	case method == http.MethodPost && strings.HasSuffix(path, "/reset-password"):
		return reply(200, map[string]any{"password": "pw-reset"})
	case method == http.MethodPatch && strings.HasPrefix(path, "/internal/auth/users/"):
		return reply(200, map[string]any{"ok": true})
	}
	return reply(404, map[string]string{"error": "unexpected " + method + " " + path})
}

func (s *authStub) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// portalUser 把一个测试身份登记成某个登录账号。
func portalUser(name string, userID int) {
	testUsers[name] = &Claims{TokenType: "access", UserId: userID, Username: name, Role: "user"}
}

// bindPortal 直接落一行门户绑定（绕过开通流程）。
func bindPortal(t *testing.T, mchNo, name string, userID int) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO pay_merchant_portal (mch_no, user_id, username, open_app_key)
		VALUES ($1,$2,$3,$4) ON CONFLICT (mch_no) DO NOTHING`, mchNo, userID, name, openAppKeyFor(mchNo)); err != nil {
		t.Fatal(err)
	}
	portalUser(name, userID)
}

func TestOpenAppKeyFor(t *testing.T) {
	if got := openAppKeyFor("Ma876790C7B6"); got != "mch-ma876790c7b6" {
		t.Fatalf("plain: %s", got)
	}
	a, b := openAppKeyFor("M 1"), openAppKeyFor("M/1")
	if a == b || !strings.HasPrefix(a, "mch-m-1-") || !strings.HasPrefix(b, "mch-m-1-") {
		t.Fatalf("含特殊字符的商户号要折成 '-' 且互不相撞: %s %s", a, b)
	}
}

func TestPortal_provisionAndScope(t *testing.T) {
	requireDB(t)
	auth := useAuthStub(t, 601)
	seedISVAndMerchant(t, "ISV-PT", "M-PT1", "stub_channel")
	seedMerchantParams(t, "M-PT1", "stub_channel", map[string]string{"sub_mchid": "1900777"})
	seedISVAndMerchant(t, "ISV-PT", "M-PT2", "stub_channel")
	seedApp(t, "M-PT2", "A-PT2")
	r := testRouter()
	base := "/api/pay/admin/merchants/M-PT1/portal"

	// 只有平台管理员能开账号：operator 不行，参数不合规不行，商户不存在不行。
	mustCode(t, call(t, r, "op", http.MethodPost, base, map[string]any{"username": "shop-pt1"}), 403)
	mustCode(t, call(t, r, "nobody", http.MethodPost, base, map[string]any{"username": "shop-pt1"}), 403)
	mustCode(t, call(t, r, "admin", http.MethodPost, base, map[string]any{"username": "x"}), 400)
	mustCode(t, call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants/M-NOPE/portal", map[string]any{"username": "shop-pt1"}), 404)
	if n := auth.count(""); n != 0 {
		t.Fatalf("被拒绝的请求不该碰到账号服务: %v", auth.calls)
	}

	got := call(t, r, "admin", http.MethodPost, base, map[string]any{"username": "shop-pt1"})
	mustCode(t, got, 200)
	if got.Body["username"] != "shop-pt1" || got.Body["password"] != "pw-shop-pt1" ||
		got.Body["appKey"] != "mch-m-pt1" || got.Body["appSecret"] != "as-mch-m-pt1-1" {
		t.Fatalf("provision: %v", got.Body)
	}
	// 商户原本没有应用：顺手建了一个，签名密钥只在这里出现一次。
	payApp, _ := got.Body["payApp"].(map[string]any)
	appID, _ := payApp["appId"].(string)
	if appID == "" || payApp["appSecret"] == "" {
		t.Fatalf("default app: %v", got.Body)
	}
	for _, tok := range auth.tokens {
		if tok != "test-admin" {
			t.Fatalf("要带着操作员本人的 token 去找账号服务: %v", auth.tokens)
		}
	}
	portalUser("pt1", 601)

	// 再点一次不会重复开通，也不会重置任何东西。
	mustCode(t, call(t, r, "admin", http.MethodPost, base, map[string]any{"username": "shop-pt1b"}), 409)
	if auth.count("POST /internal/auth/users") != 1 || auth.apps["mch-m-pt1"] != 1 {
		t.Fatalf("auth calls: %v", auth.calls)
	}

	// 商户列表带出开通情况。
	list := call(t, r, "admin", http.MethodGet, "/api/pay/admin/merchants", nil)
	mustCode(t, list, 200)
	seen := false
	for _, it := range list.Body["items"].([]any) {
		m := it.(map[string]any)
		portal, has := m["portal"].(map[string]any)
		switch m["mchNo"] {
		case "M-PT1":
			seen = has && portal["username"] == "shop-pt1" && portal["appKey"] == "mch-m-pt1" && portal["state"] == stateActive
		case "M-PT2":
			if has {
				t.Fatalf("没开通的商户不该有 portal: %v", m)
			}
		}
	}
	if !seen {
		t.Fatalf("merchant list: %v", list.Body)
	}

	// 商户登录后只看得到自己。
	me := call(t, r, "pt1", http.MethodGet, "/api/pay/portal/me", nil)
	mustCode(t, me, 200)
	mch := me.Body["merchant"].(map[string]any)
	if me.Body["mode"] != "merchant" || mch["mchNo"] != "M-PT1" || me.Body["openAppKey"] != "mch-m-pt1" || me.Body["username"] != "shop-pt1" {
		t.Fatalf("me: %v", me.Body)
	}
	if _, leaked := mch["isvNo"]; leaked {
		t.Fatalf("商户视图不该带服务商归属: %v", mch)
	}
	if subs := me.Body["subAccounts"].([]any); len(subs) != 1 || subs[0].(map[string]any)["subMchid"] != "1900777" {
		t.Fatalf("subAccounts: %v", me.Body)
	}
	apps := call(t, r, "pt1", http.MethodGet, "/api/pay/portal/apps", nil)
	mustCode(t, apps, 200)
	if items := apps.Body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["appId"] != appID {
		t.Fatalf("apps: %v", apps.Body)
	}
	if raw, _ := json.Marshal(apps.Body); strings.Contains(string(raw), "ecret") {
		t.Fatalf("应用列表不该带任何密钥字段: %s", raw)
	}
	mustCode(t, call(t, r, "pt1", http.MethodGet, "/api/pay/portal/orders", nil), 200)

	// 越权：管理接口、别家商户、别家应用，一律不通。
	mustCode(t, call(t, r, "pt1", http.MethodGet, "/api/pay/admin/merchants", nil), 403)
	mustCode(t, call(t, r, "pt1", http.MethodGet, "/api/pay/portal/me?mchNo=M-PT2", nil), 403)
	mustCode(t, call(t, r, "pt1", http.MethodGet, "/api/pay/portal/apps?mchNo=M-PT2", nil), 403)
	mustCode(t, call(t, r, "pt1", http.MethodPost, "/api/pay/portal/apps/A-PT2/secret", nil), 404)
	mustCode(t, call(t, r, "pt1", http.MethodPost, base+"/password", nil), 403)
	// 没绑商户的账号：404 + code，前端据此回到运营视图。
	if none := call(t, r, "nobody", http.MethodGet, "/api/pay/portal/me", nil); none.Code != 404 || none.Body["code"] != "no_portal" {
		t.Fatalf("unbound: %d %v", none.Code, none.Body)
	}
	// 运营可以带 mchNo 替商户看。
	if seenAs := call(t, r, "op", http.MethodGet, "/api/pay/portal/me?mchNo=M-PT1", nil); seenAs.Code != 200 || seenAs.Body["mode"] != "operator" {
		t.Fatalf("operator view: %d %v", seenAs.Code, seenAs.Body)
	}

	// 商户自助换发签名密钥：旧的立即失效。
	before, _ := loadApp(context.Background(), appID)
	rotated := call(t, r, "pt1", http.MethodPost, "/api/pay/portal/apps/"+appID+"/secret", nil)
	mustCode(t, rotated, 200)
	after, _ := loadApp(context.Background(), appID)
	plain, _ := open(after.AppSecret)
	if rotated.Body["appSecret"] == "" || rotated.Body["appSecret"] != plain || before.AppSecret == after.AppSecret {
		t.Fatalf("rotate: %v", rotated.Body)
	}

	// 重置密码 / appSecret。
	if pw := call(t, r, "admin", http.MethodPost, base+"/password", nil); pw.Code != 200 || pw.Body["password"] != "pw-reset" {
		t.Fatalf("reset password: %d %v", pw.Code, pw.Body)
	}
	if auth.count("POST /internal/auth/users/601/reset-password") != 1 {
		t.Fatalf("reset 应作用在绑定的账号上: %v", auth.calls)
	}
	if sec := call(t, r, "admin", http.MethodPost, base+"/app-secret", nil); sec.Code != 200 || sec.Body["appSecret"] != "as-mch-m-pt1-2" {
		t.Fatalf("reset app secret: %d %v", sec.Code, sec.Body)
	}
	mustCode(t, call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants/M-PT2/portal/password", nil), 404)

	// 停用 → 门户 403；恢复 → 可用。
	mustCode(t, call(t, r, "op", http.MethodPut, base+"/state", map[string]any{"state": "gone"}), 400)
	mustCode(t, call(t, r, "op", http.MethodPut, base+"/state", map[string]any{"state": stateDisabled}), 200)
	if off := call(t, r, "pt1", http.MethodGet, "/api/pay/portal/me", nil); off.Code != 403 || off.Body["code"] != "portal_disabled" {
		t.Fatalf("disabled: %d %v", off.Code, off.Body)
	}
	mustCode(t, call(t, r, "pt1", http.MethodPost, "/api/pay/portal/apps/"+appID+"/secret", nil), 403)
	mustCode(t, call(t, r, "op", http.MethodPut, base+"/state", map[string]any{"state": stateActive}), 200)
	mustCode(t, call(t, r, "pt1", http.MethodGet, "/api/pay/portal/me", nil), 200)
}

// 开通到一半（账号建好了、appKey 没签成）：初始密码必须交到操作员手里，再点一次只补没做完的。
func TestPortal_resumeAfterAppKeyFailure(t *testing.T) {
	requireDB(t)
	auth := useAuthStub(t, 701)
	seedISVAndMerchant(t, "ISV-PT", "M-PT3", "stub_channel")
	seedApp(t, "M-PT3", "A-PT3")
	r := testRouter()
	base := "/api/pay/admin/merchants/M-PT3/portal"

	auth.failApps = true
	half := call(t, r, "admin", http.MethodPost, base, map[string]any{"username": "shop-pt3"})
	mustCode(t, half, 200)
	if half.Body["partial"] != true || half.Body["password"] != "pw-shop-pt3" || half.Body["appSecret"] != nil {
		t.Fatalf("partial: %v", half.Body)
	}
	// appKey 没签发前换发 appSecret 没有对象。
	mustCode(t, call(t, r, "admin", http.MethodPost, base+"/app-secret", nil), 409)

	auth.failApps = false
	done := call(t, r, "admin", http.MethodPost, base, nil)
	mustCode(t, done, 200)
	if done.Body["appKey"] != "mch-m-pt3" || done.Body["appSecret"] != "as-mch-m-pt3-1" || done.Body["username"] != "shop-pt3" {
		t.Fatalf("resume: %v", done.Body)
	}
	if _, again := done.Body["password"]; again {
		t.Fatalf("补做时不该再出现密码: %v", done.Body)
	}
	// 商户已有应用：不新建，也不动它的密钥。
	if _, made := done.Body["payApp"]; made {
		t.Fatalf("已有应用的商户不该再建默认应用: %v", done.Body)
	}
	if auth.count("POST /internal/auth/users") != 1 {
		t.Fatalf("账号只该建一次: %v", auth.calls)
	}
	if p, err := loadPortalByMch(context.Background(), "M-PT3"); err != nil || p.UserID != 701 || p.OpenAppKey != "mch-m-pt3" {
		t.Fatalf("binding: %+v %v", p, err)
	}
}

// 开放平台里 appKey 已存在但绑定没写回（上次中断在两步之间）：换发一把，而不是卡死。
func TestPortal_existingAppKeyIsRotated(t *testing.T) {
	requireDB(t)
	auth := useAuthStub(t, 801)
	auth.apps["mch-m-pt4"] = 1
	seedISVAndMerchant(t, "ISV-PT", "M-PT4", "stub_channel")
	r := testRouter()

	got := call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants/M-PT4/portal", map[string]any{"username": "shop-pt4"})
	mustCode(t, got, 200)
	if got.Body["appSecret"] != "as-mch-m-pt4-2" || got.Body["password"] != "pw-shop-pt4" {
		t.Fatalf("rotate on conflict: %v", got.Body)
	}
	// 登录名被占用：不落绑定，换个名字还能开。
	seedISVAndMerchant(t, "ISV-PT", "M-PT5", "stub_channel")
	mustCode(t, call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants/M-PT5/portal", map[string]any{"username": "shop-pt4"}), 409)
	if _, err := loadPortalByMch(context.Background(), "M-PT5"); !notFound(err) {
		t.Fatalf("占用登录名时不该留下绑定: %v", err)
	}
	mustCode(t, call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants/M-PT5/portal", map[string]any{"username": "shop-pt5"}), 200)
}

// 其它租户的运营碰不到这个商户的门户。
func TestPortal_tenantIsolation(t *testing.T) {
	requireDB(t)
	seedISVAndMerchant(t, "ISV-PT", "M-PT6", "stub_channel")
	bindPortal(t, "M-PT6", "pt6", 811)
	testUsers["t909"] = &Claims{TokenType: "access", UserId: 91, Username: "t909", Role: "user", TenantId: 909}
	testGrants[91] = map[string]bool{roleOperator: true}
	r := testRouter()

	mustCode(t, call(t, r, "t909", http.MethodPut, "/api/pay/admin/merchants/M-PT6/portal/state", map[string]any{"state": stateDisabled}), 403)
	mustCode(t, call(t, r, "t909", http.MethodGet, "/api/pay/portal/me?mchNo=M-PT6", nil), 403)
	mustCode(t, call(t, r, "t909", http.MethodGet, "/api/pay/portal/orders?mchNo=M-PT6", nil), 403)
	mustCode(t, call(t, r, "pt6", http.MethodGet, "/api/pay/portal/me", nil), 200)
}

func playground(t *testing.T, user, appID, op string, dryRun bool, params map[string]any) resp {
	t.Helper()
	return call(t, testRouter(), user, http.MethodPost, "/api/pay/portal/playground",
		map[string]any{"appId": appID, "op": op, "dryRun": dryRun, "params": params})
}

// played 取调试响应里那次开放 API 调用的状态码与响应体。
func played(t *testing.T, r resp) (int, map[string]any) {
	t.Helper()
	mustCode(t, r, 200)
	inner, ok := r.Body["response"].(map[string]any)
	if !ok {
		t.Fatalf("没有 response: %v", r.Body)
	}
	body, _ := inner["body"].(map[string]any)
	return int(inner["status"].(float64)), body
}

func TestPlayground_sandboxFlow(t *testing.T) {
	requireDB(t)
	app, secret := seedSandboxApp(t, "ISV-PG", "M-PG1", "A-PG1", "WX_NATIVE")
	bindPortal(t, "M-PG1", "pg1", 821)
	seedISVAndMerchant(t, "ISV-PG", "M-PG2", "stub_channel")
	seedApp(t, "M-PG2", "A-PG2")

	// 只签名不发送：签名串、签名与对外验签用的是同一套算法；没有落单。
	dry := playground(t, "pg1", app.AppID, "order.create", true, map[string]any{"wayCode": "WX_NATIVE", "amount": 1000})
	mustCode(t, dry, 200)
	req := dry.Body["request"].(map[string]any)
	hdr := map[string]string{}
	for _, h := range req["headers"].([]any) {
		kv := h.(map[string]any)
		hdr[kv["name"].(string)] = kv["value"].(string)
	}
	body := req["body"].(string)
	signing := dry.Body["signing"].(map[string]any)
	want := signPayload(secret, app.AppID, hdr["X-Pay-Timestamp"], hdr["X-Pay-Nonce"], http.MethodPost, "/api/pay/v1/orders", body)
	if signing["signature"] != want || hdr["X-Pay-Signature"] != want || hdr["X-Pay-App-Id"] != app.AppID {
		t.Fatalf("signature: %v", dry.Body)
	}
	if signing["stringToSign"] != strings.Join([]string{app.AppID, hdr["X-Pay-Timestamp"], hdr["X-Pay-Nonce"], "POST", "/api/pay/v1/orders", body}, "\n") {
		t.Fatalf("stringToSign: %q", signing["stringToSign"])
	}
	curl := dry.Body["curl"].(string)
	if !strings.Contains(curl, "/api/pay/v1/orders") || !strings.Contains(curl, want) || !strings.Contains(curl, tokenHint) {
		t.Fatalf("curl: %s", curl)
	}
	if _, sent := dry.Body["response"]; sent || dry.Body["scope"] != "pay.order.create" {
		t.Fatalf("dry run: %v", dry.Body)
	}
	var orders int
	_ = db.QueryRow(context.Background(), `SELECT count(*) FROM pay_order WHERE mch_no='M-PG1'`).Scan(&orders)
	if orders != 0 {
		t.Fatalf("dry run 不该落单: %d", orders)
	}

	// 下单 → 模拟支付 → 查单 → 退款 → 查退款 → 账单。
	created := playground(t, "pg1", app.AppID, "order.create", false, map[string]any{"wayCode": "WX_NATIVE", "amount": 1000})
	code, out := played(t, created)
	order, _ := out["order"].(map[string]any)
	mchOrderNo, _ := order["mchOrderNo"].(string)
	if code != 200 || order["state"] != orderPaying || !isDebugNo(mchOrderNo) {
		t.Fatalf("create: %d %v", code, out)
	}
	if _, drawn := created.Body["response"].(map[string]any)["qr"]; drawn {
		t.Fatalf("沙箱的占位链接不该画成二维码")
	}
	if raw, _ := json.Marshal(created.Body); strings.Contains(string(raw), secret) {
		t.Fatalf("签名密钥明文不该出现在调试响应里")
	}

	if code, out = played(t, playground(t, "pg1", app.AppID, "order.mock", false, map[string]any{"mchOrderNo": mchOrderNo})); code != 200 ||
		out["order"].(map[string]any)["state"] != orderSuccess {
		t.Fatalf("mock: %d %v", code, out)
	}
	if code, out = played(t, playground(t, "pg1", app.AppID, "order.query", false, map[string]any{"mchOrderNo": mchOrderNo})); code != 200 ||
		out["order"].(map[string]any)["state"] != orderSuccess {
		t.Fatalf("query: %d %v", code, out)
	}
	refunded := playground(t, "pg1", app.AppID, "refund.create", false, map[string]any{"mchOrderNo": mchOrderNo, "refundAmount": 400})
	code, out = played(t, refunded)
	refund, _ := out["refund"].(map[string]any)
	mchRefundNo, _ := refund["mchRefundNo"].(string)
	if code != 200 || !isDebugNo(mchRefundNo) || refund["refundAmount"].(float64) != 400 {
		t.Fatalf("refund: %d %v", code, out)
	}
	if code, out = played(t, playground(t, "pg1", app.AppID, "refund.query", false, map[string]any{"mchRefundNo": mchRefundNo})); code != 200 {
		t.Fatalf("refund query: %d %v", code, out)
	}
	today := time.Now().In(billZone).Format("2006-01-02")
	sum := playground(t, "pg1", app.AppID, "bill.summary", false, map[string]any{"startDate": today})
	if code, out = played(t, sum); code != 200 {
		t.Fatalf("bill summary: %d %v", code, out)
	}
	if uri := sum.Body["request"].(map[string]any)["uri"]; uri != "/api/pay/v1/bills/summary?startDate="+today {
		t.Fatalf("bill uri: %v", uri)
	}
	if code, out = played(t, playground(t, "pg1", app.AppID, "bill.transactions", false,
		map[string]any{"startDate": today, "endDate": today, "type": "pay", "page": 1, "pageSize": 10})); code != 200 {
		t.Fatalf("bill transactions: %d %v", code, out)
	}

	// 护栏：别家应用当不存在；真实订单动不了；未知操作与缺参被拒。
	mustCode(t, playground(t, "pg1", "A-PG2", "order.query", false, map[string]any{"mchOrderNo": mchOrderNo}), 404)
	mustCode(t, playground(t, "pg1", app.AppID, "refund.create", false, map[string]any{"mchOrderNo": "REAL-1", "refundAmount": 1}), 400)
	mustCode(t, playground(t, "pg1", app.AppID, "order.close", false, map[string]any{"mchOrderNo": "REAL-1"}), 400)
	mustCode(t, playground(t, "pg1", app.AppID, "order.create", false, map[string]any{"mchOrderNo": "REAL-2", "wayCode": "WX_NATIVE", "amount": 100}), 400)
	mustCode(t, playground(t, "pg1", app.AppID, "division.create", false, map[string]any{}), 400)
	mustCode(t, playground(t, "pg1", app.AppID, "bill.summary", false, map[string]any{}), 400)
	mustCode(t, playground(t, "nobody", app.AppID, "order.query", false, map[string]any{"mchOrderNo": mchOrderNo}), 404)
	// 开放 API 自己的报错原样透出（查不存在的单是 404，不是调试工具的 404）。
	if code, _ = played(t, playground(t, "pg1", app.AppID, "order.query", false, map[string]any{"mchOrderNo": "DBG-NOPE"})); code != 404 {
		t.Fatalf("passthrough status: %d", code)
	}
}

// 走真实渠道的调试单会真扣钱：单笔封顶 1 元。
func TestPlayground_realChannelCap(t *testing.T) {
	requireDB(t)
	stub.resetLifecycle()
	app, _ := seedStubMerchant(t, "ISV-PG", "M-PG3", "A-PG3")
	bindPortal(t, "M-PG3", "pg3", 831)
	mustCode(t, call(t, testRouter(), "op", http.MethodPut, "/api/pay/admin/passages",
		map[string]any{"appId": app.AppID, "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 60}), 200)

	over := playground(t, "pg3", app.AppID, "order.create", false, map[string]any{"wayCode": "STUB_PAY", "amount": debugRealMax + 1})
	mustCode(t, over, 400)
	ok := playground(t, "pg3", app.AppID, "order.create", false, map[string]any{"wayCode": "STUB_PAY", "amount": debugRealMax})
	if code, out := played(t, ok); code != 200 {
		t.Fatalf("at cap: %d %v", code, out)
	}
}
