package main

import (
	"encoding/json"
	"testing"
)

// d4Payload 取自《D4 装备属性推送对接说明》§3 的真实样例。
const d4Payload = `{
  "sku": "3195316",
  "affixes": [
    {"key": "strength", "name": "Strength", "value": 225, "unit": "flat", "text": "+225 Strength(Only)"},
    {"key": "maximum_life", "name": "Maximum Life", "value": 1120, "unit": "flat", "text": "+1,120 Maximum Life"}
  ],
  "aspect": {"key": "Affix_legendary_necro_126", "name": "Aphotic Aspect"},
  "unique_item": null,
  "set": {"name": "Flesh of Abaddon", "members": ["Phoba", "Fer"]}
}`

func d4Rules() []IndexRule {
	return []IndexRule{
		{Field: "aspect", Path: "aspect.key", Type: "text"},
		{Field: "unique_item", Path: "unique_item.key", Type: "text"},
		{Field: "set", Path: "set.name", Type: "text"},
		{Field: "affix", Mode: "array", Path: "affixes", KeyPath: "key", ValuePath: "value", Type: "number"},
	}
}

func TestExtractEntries_d4Sample(t *testing.T) {
	// Given: D4 文档样例 + 与之匹配的规则
	// When: 抽取索引行
	entries := extractEntries(d4Rules(), []byte(d4Payload))

	// Then: 标量 2 行（aspect/set；unique_item 为 null 跳过）+ 数组 2 行（词条）
	if len(entries) != 4 {
		t.Fatalf("want 4 entries, got %d: %+v", len(entries), entries)
	}
	byField := map[string][]indexEntry{}
	for _, e := range entries {
		byField[e.Field] = append(byField[e.Field], e)
	}
	if got := *byField["aspect"][0].Txt; got != "Affix_legendary_necro_126" {
		t.Errorf("aspect txt = %q", got)
	}
	if _, has := byField["unique_item"]; has {
		t.Error("null unique_item must be skipped")
	}
	affixes := map[string]float64{}
	for _, e := range byField["affix"] {
		affixes[e.K] = *e.Num
	}
	if affixes["strength"] != 225 || affixes["maximum_life"] != 1120 {
		t.Errorf("affix values = %v", affixes)
	}
}

func TestExtractEntries_ruleChangeExtractsNewField(t *testing.T) {
	// Given: 后台把规则改为额外抽取 aspect.name
	rules := append(d4Rules(), IndexRule{Field: "aspect_name", Path: "aspect.name", Type: "text"})

	// When
	entries := extractEntries(rules, []byte(d4Payload))

	// Then: 新字段生效，无需改代码
	found := false
	for _, e := range entries {
		if e.Field == "aspect_name" && e.Txt != nil && *e.Txt == "Aphotic Aspect" {
			found = true
		}
	}
	if !found {
		t.Fatalf("aspect_name not extracted: %+v", entries)
	}
}

func TestItemPK(t *testing.T) {
	tests := []struct {
		name, pkField, payload, want string
		ok                           bool
	}{
		{"字符串主键", "sku", `{"sku":"3190046"}`, "3190046", true},
		{"数字主键转字符串", "skuId", `{"skuId":3190046}`, "3190046", true},
		{"点路径主键", "meta.id", `{"meta":{"id":"x1"}}`, "x1", true},
		{"缺主键", "sku", `{"name":"n"}`, "", false},
		{"空字符串主键", "sku", `{"sku":""}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := itemPK(json.RawMessage(tt.payload), tt.pkField)
			if got != tt.want || ok != tt.ok {
				t.Errorf("itemPK(%s) = (%q,%v), want (%q,%v)", tt.payload, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseFilter(t *testing.T) {
	tests := []struct {
		spec  string
		want  filterCond
		valid bool
	}{
		{"affix.strength>=200", filterCond{"affix", "strength", ">=", "200"}, true},
		{"aspect=Affix_legendary_necro_126", filterCond{"aspect", "", "=", "Affix_legendary_necro_126"}, true},
		{"affix.maximum_life", filterCond{"affix", "maximum_life", "", ""}, true},
		{"set=Flesh of Abaddon", filterCond{"set", "", "=", "Flesh of Abaddon"}, true},
		{"=200", filterCond{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := parseFilter(tt.spec)
			if tt.valid != (err == nil) {
				t.Fatalf("err = %v, want valid=%v", err, tt.valid)
			}
			if tt.valid && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateRules(t *testing.T) {
	if err := validateRules(d4Rules()); err != nil {
		t.Fatalf("d4 rules should be valid: %v", err)
	}
	bad := []IndexRule{{Field: "x", Mode: "array", Path: "p"}} // 缺 key_path
	if validateRules(bad) == nil {
		t.Error("array rule without key_path must fail")
	}
	dup := []IndexRule{{Field: "x", Path: "a"}, {Field: "x", Path: "b"}}
	if validateRules(dup) == nil {
		t.Error("duplicate field must fail")
	}
}
