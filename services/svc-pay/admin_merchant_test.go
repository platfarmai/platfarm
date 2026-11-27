package main

import (
	"context"
	"net/http"
	"testing"
)

func TestUpdateMerchant_editsLedgerFieldsAndState(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-MED", "M-MED", "stub_channel")
	r := testRouter()
	put := func(user, mchNo string, body map[string]any) resp {
		return call(t, r, user, http.MethodPut, "/api/pay/admin/merchants/"+mchNo, body)
	}

	// 只改请求里出现的字段；首尾空白去掉。
	res := put("op", "M-MED", map[string]any{"mchName": " 成都某某商贸有限公司 ", "contactName": "张三", "contactPhone": "13800000000"})
	mustCode(t, res, 200)
	m, err := loadMerchant(ctx, "M-MED")
	if err != nil {
		t.Fatal(err)
	}
	if m.MchName != "成都某某商贸有限公司" || m.ContactName != "张三" || m.ContactPhone != "13800000000" {
		t.Fatalf("ledger fields: %+v", m)
	}
	if m.IsvNo != "ISV-MED" || m.MchType != 2 || m.State != stateActive {
		t.Fatalf("untouched fields changed: %+v", m)
	}

	// 列表带回联系人，编辑表单靠它回填。
	list := call(t, r, "op", http.MethodGet, "/api/pay/admin/merchants", nil)
	mustCode(t, list, 200)
	found := false
	for _, it := range list.Body["items"].([]any) {
		row := it.(map[string]any)
		if row["mchNo"] == "M-MED" {
			found = row["contactName"] == "张三" && row["contactPhone"] == "13800000000"
		}
	}
	if !found {
		t.Fatalf("list does not carry contact fields: %v", list.Body)
	}

	// 列表带回各渠道的子商户号（进件完成后写入的特约商户号），没有配置的商户给空数组。
	seedMerchantParams(t, "M-MED", "stub_channel", map[string]string{"sub_mchid": "1900000777"})
	list = call(t, r, "op", http.MethodGet, "/api/pay/admin/merchants", nil)
	mustCode(t, list, 200)
	subOK := false
	for _, it := range list.Body["items"].([]any) {
		row := it.(map[string]any)
		subs, isList := row["subAccounts"].([]any)
		if !isList {
			t.Fatalf("subAccounts must always be an array: %v", row)
		}
		if row["mchNo"] == "M-MED" && len(subs) == 1 {
			s := subs[0].(map[string]any)
			subOK = s["ifCode"] == "stub_channel" && s["subMchid"] == "1900000777"
		}
	}
	if !subOK {
		t.Fatalf("list does not carry sub merchant id: %v", list.Body)
	}

	mustCode(t, put("op", "M-MED", map[string]any{"state": stateDisabled}), 200)
	if m, _ := loadMerchant(ctx, "M-MED"); m.State != stateDisabled || m.MchName != "成都某某商贸有限公司" {
		t.Fatalf("after disable: %+v", m)
	}
	mustCode(t, put("op", "M-MED", map[string]any{"state": stateActive}), 200)

	mustCode(t, put("op", "M-MED", map[string]any{"mchName": "  "}), 400)
	mustCode(t, put("op", "M-MED", map[string]any{"state": "paused"}), 400)
	mustCode(t, put("op", "M-MED", map[string]any{"mchType": 3}), 400)
	mustCode(t, put("op", "M-NOPE", map[string]any{"mchName": "x"}), 404)
	mustCode(t, put("nobody", "M-MED", map[string]any{"mchName": "x"}), 403)
}

func TestUpdateMerchant_isvLockedOnceInUse(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	seedISVAndMerchant(t, "ISV-MED-A", "M-MED-FRESH", "stub_channel")
	seedISVAndMerchant(t, "ISV-MED-A", "M-MED-USED", "stub_channel")
	if _, err := db.Exec(ctx, `INSERT INTO pay_isv (isv_no, isv_name) VALUES ('ISV-MED-B','ISV-MED-B') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	r := testRouter()
	put := func(mchNo string, body map[string]any) resp {
		return call(t, r, "op", http.MethodPut, "/api/pay/admin/merchants/"+mchNo, body)
	}

	// 还没用过的商户可以换服务商；不存在的服务商不行。
	mustCode(t, put("M-MED-FRESH", map[string]any{"isvNo": "ISV-NOPE"}), 404)
	mustCode(t, put("M-MED-FRESH", map[string]any{"isvNo": "ISV-MED-B"}), 200)
	if m, _ := loadMerchant(ctx, "M-MED-FRESH"); m.IsvNo != "ISV-MED-B" {
		t.Fatalf("isv not changed: %+v", m)
	}

	// 有订单之后服务商与类型锁定，台账字段仍可改。
	seedApp(t, "M-MED-USED", "A-MED-USED")
	seedSuccessOrder(t, "M-MED-USED", "ISV-MED-A", "A-MED-USED", "P-MED-USED-1", 1000, 60, 20)
	mustCode(t, put("M-MED-USED", map[string]any{"isvNo": "ISV-MED-B"}), 409)
	mustCode(t, put("M-MED-USED", map[string]any{"mchType": 1}), 409)
	mustCode(t, put("M-MED-USED", map[string]any{"isvNo": "ISV-MED-A", "mchName": "改名不受影响"}), 200)
	if m, _ := loadMerchant(ctx, "M-MED-USED"); m.IsvNo != "ISV-MED-A" || m.MchType != 2 || m.MchName != "改名不受影响" {
		t.Fatalf("locked merchant: %+v", m)
	}
}

func TestUpdateMerchant_tenantIsolation(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	testUsers["t551"] = &Claims{TokenType: "access", UserId: 51, Username: "t551", Role: "user", TenantId: 551}
	testGrants[51] = map[string]bool{roleOperator: true}
	defer func() { delete(testUsers, "t551"); delete(testGrants, 51) }()

	seedISVAndMerchant(t, "ISV-MED-T552", "M-MED-T552", "stub_channel")
	seedISVAndMerchant(t, "ISV-MED-T551", "M-MED-T551", "stub_channel")
	for _, sql := range []string{
		`UPDATE pay_isv SET tenant_id=552 WHERE isv_no='ISV-MED-T552'`,
		`UPDATE pay_merchant SET tenant_id=552, mch_name='victim' WHERE mch_no='M-MED-T552'`,
		`UPDATE pay_isv SET tenant_id=551 WHERE isv_no='ISV-MED-T551'`,
		`UPDATE pay_merchant SET tenant_id=551 WHERE mch_no='M-MED-T551'`,
	} {
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	r := testRouter()

	// 别的租户的商户改不了。
	mustCode(t, call(t, r, "t551", http.MethodPut, "/api/pay/admin/merchants/M-MED-T552", map[string]any{"mchName": "pwned"}), 403)
	if m, _ := loadMerchant(ctx, "M-MED-T552"); m.MchName != "victim" {
		t.Fatalf("cross-tenant edit went through: %+v", m)
	}
	// 自己的商户也不能挂到别的租户的服务商下（等于借用对方的渠道凭据）。
	mustCode(t, call(t, r, "t551", http.MethodPut, "/api/pay/admin/merchants/M-MED-T551", map[string]any{"isvNo": "ISV-MED-T552"}), 403)
	if m, _ := loadMerchant(ctx, "M-MED-T551"); m.IsvNo != "ISV-MED-T551" {
		t.Fatalf("merchant moved under a foreign isv: %+v", m)
	}
}
