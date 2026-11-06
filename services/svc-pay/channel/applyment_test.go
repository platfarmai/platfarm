package channel

import (
	"strings"
	"testing"
)

func validApplyment() Applyment {
	var a Applyment
	a.Subject.Type = "individual"
	a.Subject.Name = "张三小吃店"
	a.Subject.ShortName = "张三小吃"
	a.Subject.LicenseNo = "92110108MA00ABCD1X"
	a.Subject.LicenseValid = "2021-05-01,长期"
	a.Subject.MCC = "719"
	a.LegalPerson.Name = "张三"
	a.LegalPerson.IDType = "id_card"
	a.LegalPerson.IDNo = "110101199001011234"
	a.LegalPerson.IDValid = "2015-01-01,2035-01-01"
	a.Contact.Name = "张三"
	a.Contact.Phone = "13800138000"
	a.Business.ServicePhone = "13800138000"
	a.Business.Address.Province = "北京市"
	a.Business.Address.City = "北京市"
	a.Business.Address.Detail = "某路1号"
	a.Business.Scene.Types = []string{"offline"}
	a.SettlementAccount.Type = "personal"
	a.SettlementAccount.Bank = "招商银行"
	a.SettlementAccount.AccountName = "张三"
	a.SettlementAccount.AccountNo = "6225881234567890"
	a.Rates = []struct {
		WayCode     string `json:"way_code"`
		FeeRateBps  int    `json:"fee_rate_bps"`
		SettleCycle string `json:"settle_cycle"`
	}{{WayCode: "WX_JSAPI", FeeRateBps: 38, SettleCycle: "T1"}}
	a.Images = map[string]string{"license": "11", "id_front": "12", "id_back": "13"}
	return a
}

func TestApplymentProblems_ValidPayloadHasNone(t *testing.T) {
	if p := validApplyment().Problems(); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestApplymentProblems_ReportsEveryGroup(t *testing.T) {
	var a Applyment // 全空
	p := strings.Join(a.Problems(), "\n")
	for _, want := range []string{"subject.type", "subject.name", "legal_person.id_no", "contact.phone",
		"business.service_phone", "business.scene.types", "settlement_account.type", "rates 至少一条费率"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing problem for %s in:\n%s", want, p)
		}
	}
}

func TestApplymentProblems_AccountNameMustMatchSubject(t *testing.T) {
	a := validApplyment()
	a.SettlementAccount.Type = "corporate" // 对公户名应为主体名，而当前是法人名
	p := strings.Join(a.Problems(), "\n")
	if !strings.Contains(p, "对公账户 account_name 必须与 subject.name 一致") {
		t.Fatalf("got %s", p)
	}
	a.SettlementAccount.AccountName = a.Subject.Name
	if p := a.Problems(); len(p) != 0 {
		t.Fatalf("corporate fixed should be valid: %v", p)
	}
}

func TestApplymentProblems_SceneAppIDAndValidFormat(t *testing.T) {
	a := validApplyment()
	a.Business.Scene.Types = []string{"mini_program", "bogus"}
	a.Subject.LicenseValid = "2021/05/01"
	p := strings.Join(a.Problems(), "\n")
	for _, want := range []string{"business.scene.appid 在 mini_program 场景下必填", "未知场景 bogus", "subject.license_valid 格式"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q in %s", want, p)
		}
	}
}

func TestApplymentProblems_RequiredImagesAndRateRange(t *testing.T) {
	a := validApplyment()
	delete(a.Images, "id_back")
	a.Rates[0].FeeRateBps = 5000
	p := strings.Join(a.Problems(), "\n")
	if !strings.Contains(p, "images.id_back 必填") || !strings.Contains(p, "rates[0].fee_rate_bps") {
		t.Fatalf("got %s", p)
	}
}

func TestMaskID(t *testing.T) {
	if got := MaskID("110101199001011234"); got != "110***1234" {
		t.Fatalf("got %s", got)
	}
	if got := MaskID("1234"); got != "****" {
		t.Fatalf("short: got %s", got)
	}
}

func TestIsTerminalApplyment(t *testing.T) {
	if !IsTerminalApplyment(ApplymentFinished) || !IsTerminalApplyment(ApplymentCanceled) {
		t.Fatal("finished/canceled must be terminal")
	}
	if IsTerminalApplyment(ApplymentRejected) {
		t.Fatal("rejected is resubmittable, not terminal")
	}
}
