package channel

import "sort"

// ParamField 渠道参数的一个字段。管理端按它渲染表单；Secret 字段保存后不再回显原文。
type ParamField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Secret    bool   `json:"secret"`
	Required  bool   `json:"required"`
	Multiline bool   `json:"multiline"`
	Hint      string `json:"hint"`
}

// ParamDescriber 渠道声明自己读取哪些参数（可选能力）。
// provider 是服务商侧（pay_channel_config.owner_type=isv），sub 是商户侧（owner_type=merchant）。
type ParamDescriber interface {
	ParamFields() (provider, sub []ParamField)
}

// Info 一个已注册渠道的概况。
type Info struct {
	IfCode   string       `json:"ifCode"`
	FundMode string       `json:"fundMode"`
	Ways     []string     `json:"ways"`
	Provider []ParamField `json:"providerFields"`
	Sub      []ParamField `json:"subFields"`
}

// List 所有已注册渠道，按 ifCode 排序。没有实现 ParamDescriber 的渠道字段列表为空。
func List() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(registry))
	for _, c := range registry {
		info := Info{IfCode: c.IfCode(), FundMode: c.FundMode(), Ways: c.Ways(),
			Provider: []ParamField{}, Sub: []ParamField{}}
		if d, ok := c.(ParamDescriber); ok {
			p, s := d.ParamFields()
			info.Provider = append(info.Provider, p...)
			info.Sub = append(info.Sub, s...)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfCode < out[j].IfCode })
	return out
}

// Fields 某渠道某一侧（isv|merchant）的参数字段；渠道不存在或未声明返回 nil。
func Fields(ifCode, ownerType string) []ParamField {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := registry[ifCode].(ParamDescriber)
	if !ok {
		return nil
	}
	p, s := d.ParamFields()
	if ownerType == "merchant" {
		return s
	}
	return p
}
