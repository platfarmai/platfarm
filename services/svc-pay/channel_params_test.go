package main

import (
	"strings"
	"testing"
)

// 下单前的必填参数核对：缺 sp_appid 时要点名是哪一方缺哪一项，而不是把空值发给微信。
func TestMissingChannelParams(t *testing.T) {
	full := map[string]string{"sp_mchid": "1900000109", "sp_appid": "wx0000000000000000", "mch_serial_no": "A",
		"private_key": "k", "api_v3_key": "v"}
	noAppID := map[string]string{"sp_mchid": "1900000109", "sp_appid": "  ", "mch_serial_no": "A",
		"private_key": "k", "api_v3_key": "v"}
	sub := map[string]string{"sub_mchid": "1900000222"}
	mch := Merchant{MchNo: "M-1", IsvNo: "ISV-1", MchType: 2}

	if got := missingChannelParams(mch, "wxpay_partner", full, sub); got != "" {
		t.Fatalf("complete params reported as missing: %s", got)
	}
	got := missingChannelParams(mch, "wxpay_partner", noAppID, sub)
	if !strings.Contains(got, "服务商 ISV-1") || !strings.Contains(got, "服务商 AppID") || !strings.Contains(got, "sp_appid") {
		t.Fatalf("missing sp_appid not named: %q", got)
	}
	if strings.Contains(got, "商户 M-1") || strings.Contains(got, "wx_public_key") {
		t.Fatalf("reported fields that are not required or not missing: %q", got)
	}
	got = missingChannelParams(mch, "wxpay_partner", full, map[string]string{})
	if !strings.Contains(got, "商户 M-1") || !strings.Contains(got, "sub_mchid") || strings.Contains(got, "sub_appid") {
		t.Fatalf("missing sub_mchid not named: %q", got)
	}
	// 普通商户没有服务商一层；没声明字段的渠道不拦。
	if got := missingChannelParams(Merchant{MchNo: "M-2", MchType: 1}, "wxpay_partner", map[string]string{}, map[string]string{}); got != "" {
		t.Fatalf("direct merchant checked against provider fields: %q", got)
	}
	if got := missingChannelParams(mch, "no_such_channel", map[string]string{}, map[string]string{}); got != "" {
		t.Fatalf("undeclared channel blocked: %q", got)
	}
}
