// 进件（子商户入驻）的渠道无关模型。业务层只认 Applyment 与 OnboardingChannel，
// 微信 applyment4sub / 支付宝直付通各自把 Applyment 映射成自己的请求，并报告表达不了的字段。
package channel

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// 进件状态（业务层 pay_applyment.state）。渠道原始状态由适配器折叠到这几种。
const (
	ApplymentDraft           = "draft"
	ApplymentReviewing       = "reviewing"
	ApplymentApproved        = "approved"
	ApplymentSubmitted       = "submitted"
	ApplymentChannelAuditing = "channel_auditing"
	ApplymentNeedSign        = "need_sign"
	ApplymentNeedVerify      = "need_verify"
	ApplymentFinished        = "finished"
	ApplymentRejected        = "rejected"
	ApplymentCanceled        = "canceled"
)

// IsTerminalApplyment finished / canceled 不再变化；rejected 可重提，不算终态。
func IsTerminalApplyment(state string) bool {
	return state == ApplymentFinished || state == ApplymentCanceled
}

// Applyment 归一进件资料。字段名与 jsonb 列一致（snake_case），所有金额/费率均为整数。
// IDNo 与 AccountNo 在业务层落库前密封，适配器拿到的是解密后的明文。
type Applyment struct {
	Subject struct {
		Type         string `json:"type"` // individual|enterprise|institution
		Name         string `json:"name"`
		ShortName    string `json:"short_name"`
		LicenseNo    string `json:"license_no"`
		LicenseValid string `json:"license_valid"` // "2020-01-01,长期" 或 "2020-01-01,2030-01-01"
		MCC          string `json:"mcc"`           // 经营类目：微信 settlement_id，支付宝 mcc
	} `json:"subject"`
	LegalPerson struct {
		Name    string `json:"name"`
		IDType  string `json:"id_type"` // id_card|passport|hk_macao|taiwan|foreign_residence
		IDNo    string `json:"id_no"`
		IDValid string `json:"id_valid"` // 同 license_valid 格式
		Phone   string `json:"phone"`
	} `json:"legal_person"`
	Contact struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email"`
	} `json:"contact"`
	Business struct {
		ServicePhone string `json:"service_phone"`
		Address      struct {
			Province string `json:"province"`
			City     string `json:"city"`
			District string `json:"district"`
			Detail   string `json:"detail"`
			Code     string `json:"code"` // 省市编码（微信 bank_address_code / store_address_code）
		} `json:"address"`
		Scene struct {
			Types []string `json:"types"` // offline|mini_program|mp|app|web
			AppID string   `json:"appid"` // 小程序/公众号/APP 的 appid
		} `json:"scene"`
	} `json:"business"`
	SettlementAccount struct {
		Type        string `json:"type"` // corporate|personal
		Bank        string `json:"bank"`
		Branch      string `json:"branch"`
		AccountName string `json:"account_name"`
		AccountNo   string `json:"account_no"`
		AddressCode string `json:"address_code"` // 开户行省市编码
	} `json:"settlement_account"`
	Rates []struct {
		WayCode     string `json:"way_code"`
		FeeRateBps  int    `json:"fee_rate_bps"`
		SettleCycle string `json:"settle_cycle"` // T1|D1|D0
	} `json:"rates"`
	Images map[string]string `json:"images"` // kind → svc-file 文件 id
	// Extra 渠道专属补充项（不入列、不校验），如微信 wx_qualification_type / wx_contact_type、web 场景 web_domain。
	Extra map[string]string `json:"extra,omitempty"`
}

// 必填图片种类，按主体类型区分。
var requiredImages = map[string][]string{
	"individual":  {"license", "id_front", "id_back"},
	"enterprise":  {"license", "id_front", "id_back"},
	"institution": {"license", "id_front", "id_back"},
}

var (
	phoneRe = regexp.MustCompile(`^1\d{10}$|^0\d{2,3}-?\d{7,8}$|^\d{3,4}-\d{7,8}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	validRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2},(\d{4}-\d{2}-\d{2}|长期)$`)
)

// Problems 归一资料校验：返回所有问题（空 = 可提交审核）。
// 只校验结构完整性与格式；证件真伪、类目合法性由渠道审核。
func (a Applyment) Problems() []string {
	var p []string
	need := func(cond bool, msg string) {
		if !cond {
			p = append(p, msg)
		}
	}
	s := a.Subject
	need(s.Type == "individual" || s.Type == "enterprise" || s.Type == "institution",
		"subject.type 必须是 individual|enterprise|institution")
	need(s.Name != "", "subject.name 必填")
	need(s.ShortName != "", "subject.short_name 必填")
	need(s.LicenseNo != "", "subject.license_no 必填")
	need(validRe.MatchString(s.LicenseValid), "subject.license_valid 格式须为 YYYY-MM-DD,YYYY-MM-DD 或 YYYY-MM-DD,长期")
	need(s.MCC != "", "subject.mcc 必填")

	l := a.LegalPerson
	need(l.Name != "", "legal_person.name 必填")
	need(l.IDType != "", "legal_person.id_type 必填")
	need(l.IDNo != "", "legal_person.id_no 必填")
	need(validRe.MatchString(l.IDValid), "legal_person.id_valid 格式须为 YYYY-MM-DD,YYYY-MM-DD 或 YYYY-MM-DD,长期")
	need(l.Phone == "" || phoneRe.MatchString(l.Phone), "legal_person.phone 格式非法")

	c := a.Contact
	need(c.Name != "", "contact.name 必填")
	need(phoneRe.MatchString(c.Phone), "contact.phone 格式非法")
	need(c.Email == "" || emailRe.MatchString(c.Email), "contact.email 格式非法")

	b := a.Business
	need(phoneRe.MatchString(b.ServicePhone), "business.service_phone 格式非法")
	need(b.Address.Province != "" && b.Address.City != "" && b.Address.Detail != "", "business.address 省/市/详细地址必填")
	need(len(b.Scene.Types) > 0, "business.scene.types 至少一个经营场景")
	for _, t := range b.Scene.Types {
		switch t {
		case "offline", "mini_program", "mp", "app", "web":
		default:
			p = append(p, "business.scene.types 含未知场景 "+t)
		}
		if (t == "mini_program" || t == "mp" || t == "app") && b.Scene.AppID == "" {
			p = append(p, "business.scene.appid 在 "+t+" 场景下必填")
		}
	}

	sa := a.SettlementAccount
	need(sa.Type == "corporate" || sa.Type == "personal", "settlement_account.type 必须是 corporate|personal")
	need(sa.Bank != "", "settlement_account.bank 必填")
	need(sa.AccountName != "", "settlement_account.account_name 必填")
	need(sa.AccountNo != "", "settlement_account.account_no 必填")
	if sa.Type == "corporate" {
		need(sa.AccountName == s.Name, "对公账户 account_name 必须与 subject.name 一致")
	} else {
		need(sa.AccountName == l.Name, "对私账户 account_name 必须与 legal_person.name 一致")
	}

	need(len(a.Rates) > 0, "rates 至少一条费率")
	for i, r := range a.Rates {
		need(r.WayCode != "", fmt.Sprintf("rates[%d].way_code 必填", i))
		need(r.FeeRateBps >= 0 && r.FeeRateBps <= 1000, fmt.Sprintf("rates[%d].fee_rate_bps 须在 0..1000（万分位）", i))
	}

	for _, kind := range requiredImages[s.Type] {
		need(a.Images[kind] != "", "images."+kind+" 必填")
	}
	return p
}

// MaskID 证件/账号脱敏：保留前 3 后 4，中间 ***。短于 8 位整体打码。
func MaskID(s string) string {
	if len(s) < 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:3] + "***" + s[len(s)-4:]
}

// ApplymentIn 提交渠道的输入。Payload 已解密；Media 是业务层已上传好的渠道 media_id。
type ApplymentIn struct {
	BusinessCode string
	Payload      Applyment
	Media        map[string]string // image kind → media_id
	Provider     map[string]string
}

// ApplymentResult 提交结果。Unexpressed 列出渠道表达不了的归一字段（如微信不接受自定义费率）。
type ApplymentResult struct {
	ChannelApplymentID string
	Unexpressed        []string
	Raw                string
}

// ApplymentView 渠道侧进件状态（已折叠）。
type ApplymentView struct {
	ChannelApplymentID string
	State              string // channel_auditing|need_sign|need_verify|finished|rejected|canceled
	RawState           string
	Message            string
	SignURL            string
	SubMchID           string
	Raw                string
}

// OnboardingChannel 支持进件的渠道。由 PayChannel 实现者按需额外实现，用 Onboarding() 取。
type OnboardingChannel interface {
	UploadMedia(ctx context.Context, filename string, data []byte, provider map[string]string) (string, error)
	SubmitApplyment(ctx context.Context, in ApplymentIn) (ApplymentResult, error)
	QueryApplyment(ctx context.Context, businessCode string, provider map[string]string) (ApplymentView, error)
}

// Onboarding 取支持进件的渠道；渠道存在但不支持进件返回明确错误。
func Onboarding(ifCode string) (OnboardingChannel, error) {
	c, err := Get(ifCode)
	if err != nil {
		return nil, err
	}
	o, ok := c.(OnboardingChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s 不支持进件", ifCode)
	}
	return o, nil
}
