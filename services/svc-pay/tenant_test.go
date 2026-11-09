package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTenantIsolation_AdminEndpoints(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	testUsers["t101"] = &Claims{TokenType: "access", UserId: 11, Username: "t101", Role: "user", TenantId: 101}
	testGrants[11] = map[string]bool{roleOperator: true, roleReviewer: true}
	defer func() { delete(testUsers, "t101"); delete(testGrants, 11) }()

	// 租户 202 的一整套数据。
	seedISVAndMerchant(t, "ISV-T202", "M-T202", "stub_channel")
	seedMerchantParams(t, "M-T202", "stub_channel", map[string]string{"sub_mchid": "1900000300"})
	seedApp(t, "M-T202", "A-T202")
	seedSuccessOrder(t, "M-T202", "ISV-T202", "A-T202", "P-T202-1", 1000, 60, 20)
	for _, sql := range []string{
		`UPDATE pay_isv SET tenant_id=202, isv_name='victim', rebate_rate_bps=10 WHERE isv_no='ISV-T202'`,
		`UPDATE pay_merchant SET tenant_id=202 WHERE mch_no='M-T202'`,
		`UPDATE pay_app SET tenant_id=202 WHERE app_id='A-T202'`,
		`UPDATE pay_order SET tenant_id=202 WHERE pay_order_id='P-T202-1'`,
		`INSERT INTO pay_division (division_no, pay_order_id, mch_no, app_id, if_code, way_code, order_amount, division_amount,
			receivers, state, tenant_id) VALUES ('D-T202','P-T202-1','M-T202','A-T202','stub_channel','STUB_PAY',1000,100,'[]','accepted',202)`,
		`INSERT INTO pay_withdrawal (withdraw_no, mch_no, if_code, mch_withdraw_no, amount, state, tenant_id)
			VALUES ('W-T202','M-T202','stub_controlled','MW-T202',100,'accepted',202)`,
		`INSERT INTO pay_ledger (mch_no, if_code, kind, ref_type, ref_id, amount, tenant_id)
			VALUES ('M-T202','stub_channel','fee','order','P-T202-1',-6,202)`,
		`INSERT INTO pay_isv (isv_no, isv_name, tenant_id) VALUES ('ISV-PLAT','平台服务商',0) ON CONFLICT DO NOTHING`,
	} {
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	setStub(func(s *stubChannel) { s.billRows, s.billErr = nil, nil })
	batch, err := runRecon(ctx, "stub_channel", "ISV-T202", time.Now().In(cst), "ALL", false)
	if err != nil || batch.TenantID != 202 {
		t.Fatalf("recon batch tenant: %+v %v", batch, err)
	}
	var diffID int64
	if err := db.QueryRow(ctx, `INSERT INTO pay_recon_diff (batch_id, diff_type) VALUES ($1,'missing_local') RETURNING id`,
		batch.ID).Scan(&diffID); err != nil {
		t.Fatal(err)
	}

	r := testRouter()
	deny := []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/pay/admin/channel-config", map[string]any{"ownerType": "merchant", "ownerId": "M-T202",
			"ifCode": "stub_channel", "params": map[string]string{"sub_mchid": "1900000666"}}},
		{http.MethodPut, "/api/pay/admin/channel-config", map[string]any{"ownerType": "isv", "ownerId": "ISV-T202",
			"ifCode": "stub_channel", "params": map[string]string{"sp_mchid": "x"}}},
		{http.MethodPost, "/api/pay/admin/channel-config/wxpay/platform-cert", map[string]any{"ownerType": "isv", "ownerId": "ISV-T202"}},
		{http.MethodPost, "/api/pay/admin/apps", map[string]any{"mchNo": "M-T202", "appName": "x"}},
		{http.MethodPost, "/api/pay/admin/isv", map[string]any{"isvNo": "ISV-T202", "isvName": "hijacked", "rebateRateBps": 1000}},
		{http.MethodPost, "/api/pay/admin/merchants", map[string]any{"mchName": "x", "isvNo": "ISV-T202"}},
		{http.MethodPost, "/api/pay/admin/merchants", map[string]any{"mchName": "x", "tenantId": 202}},
		{http.MethodPut, "/api/pay/admin/passages", map[string]any{"appId": "A-T202", "ifCode": "stub_channel", "wayCode": "STUB_PAY", "rateBps": 1}},
		{http.MethodGet, "/api/pay/admin/division/receivers?mchNo=M-T202", nil},
		{http.MethodPost, "/api/pay/admin/divisions/D-T202/sync", nil},
		{http.MethodPost, "/api/pay/admin/recon/run", map[string]any{"ifCode": "stub_channel", "isvNo": "ISV-T202", "billDate": "2026-11-01"}},
		{http.MethodGet, fmt.Sprintf("/api/pay/admin/recon/batches/%d/diffs", batch.ID), nil},
		{http.MethodPost, fmt.Sprintf("/api/pay/admin/recon/diffs/%d/resolve", diffID), map[string]any{"note": "x"}},
		{http.MethodPost, "/api/pay/admin/recon/schedule", map[string]any{}},
		{http.MethodGet, "/api/pay/admin/balance?mchNo=M-T202&ifCode=stub_controlled", nil},
		{http.MethodPost, "/api/pay/admin/withdrawals/W-T202/sync", nil},
	}
	for _, tc := range deny {
		// t101 持有 operator/reviewer 角色：403 必须来自租户/平台管理员闸门，而不是角色缺失。
		res := call(t, r, "t101", tc.method, tc.path, tc.body)
		msg, _ := res.Body["error"].(string)
		if res.Code != http.StatusForbidden || !(strings.Contains(msg, "租户") || strings.Contains(msg, "平台管理员")) {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, res.Code, res.Body)
		}
	}
	if p, _ := loadChannelParams(ctx, "merchant", "M-T202", "stub_channel"); p["sub_mchid"] != "1900000300" {
		t.Fatalf("other tenant's sub_mchid changed to %s", p["sub_mchid"])
	}
	var name string
	var bps int
	_ = db.QueryRow(ctx, `SELECT isv_name, rebate_rate_bps FROM pay_isv WHERE isv_no='ISV-T202'`).Scan(&name, &bps)
	if name != "victim" || bps != 10 {
		t.Fatalf("other tenant's ISV rewritten: %s/%d", name, bps)
	}
	var resolved bool
	_ = db.QueryRow(ctx, `SELECT resolved FROM pay_recon_diff WHERE id=$1`, diffID).Scan(&resolved)
	if resolved {
		t.Fatal("other tenant's recon diff resolved")
	}

	// 列表只给本租户的数据；租户 0 的普通操作员也不再“看全部”。
	lists := map[string]string{
		"/api/pay/admin/merchants": "mchNo", "/api/pay/admin/orders": "payOrderId", "/api/pay/admin/divisions": "divisionNo",
		"/api/pay/admin/withdrawals": "withdrawNo", "/api/pay/admin/ledger": "mchNo", "/api/pay/admin/recon/batches": "isvNo",
	}
	other := map[string]bool{"M-T202": true, "P-T202-1": true, "D-T202": true, "W-T202": true, "ISV-T202": true}
	for _, user := range []string{"t101", "op"} {
		for path, key := range lists {
			res := call(t, r, user, http.MethodGet, path, nil)
			mustCode(t, res, 200)
			for _, it := range res.Body["items"].([]any) {
				if other[fmt.Sprint(it.(map[string]any)[key])] {
					t.Errorf("%s sees tenant 202 row via %s: %v", user, path, it)
				}
			}
		}
	}
	// 平台管理员跨租户。
	res := call(t, r, "admin", http.MethodGet, "/api/pay/admin/merchants", nil)
	seen := false
	for _, it := range res.Body["items"].([]any) {
		seen = seen || it.(map[string]any)["mchNo"] == "M-T202"
	}
	if !seen {
		t.Fatal("platform admin must see every tenant")
	}

	// 租户可以把商户挂在平台服务商下并配置自己的商户参数；平台管理员可以替其他租户建商户。
	mustCode(t, call(t, r, "t101", http.MethodPost, "/api/pay/admin/merchants",
		map[string]any{"mchNo": "M-T101", "mchName": "mine", "isvNo": "ISV-PLAT"}), 200)
	mustCode(t, call(t, r, "t101", http.MethodPut, "/api/pay/admin/channel-config", map[string]any{"ownerType": "merchant",
		"ownerId": "M-T101", "ifCode": "stub_channel", "params": map[string]string{"sub_mchid": "1900000101"}}), 200)
	mustCode(t, call(t, r, "admin", http.MethodPost, "/api/pay/admin/merchants",
		map[string]any{"mchNo": "M-T303", "mchName": "x", "tenantId": 303}), 200)
	for mchNo, want := range map[string]int{"M-T101": 101, "M-T303": 303} {
		if m, _ := loadMerchant(ctx, mchNo); m.TenantID != want {
			t.Fatalf("%s tenant = %d, want %d", mchNo, m.TenantID, want)
		}
	}
	var cfgTenant int
	_ = db.QueryRow(ctx, `SELECT tenant_id FROM pay_channel_config WHERE owner_type='merchant' AND owner_id='M-T101'`).Scan(&cfgTenant)
	if cfgTenant != 101 {
		t.Fatalf("channel config tenant = %d", cfgTenant)
	}
}
