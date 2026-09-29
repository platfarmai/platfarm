// svc-data 抽取引擎：index_rules（后台可编辑）声明如何从原文 JSON 抽出可筛选的索引行。
//
// 规则形态（存于 dataset.index_rules，JSONB）：
//   标量：{"field":"aspect", "path":"aspect.key", "type":"text"}
//   数组：{"field":"affix", "mode":"array", "path":"affixes",
//          "key_path":"key", "value_path":"value", "type":"number"}
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IndexRule 单条抽取规则。Mode 为空或 "scalar" 表示标量；"array" 表示数组展开。
type IndexRule struct {
	Field     string `json:"field"`
	Mode      string `json:"mode,omitempty"`
	Path      string `json:"path"`
	KeyPath   string `json:"key_path,omitempty"`
	ValuePath string `json:"value_path,omitempty"`
	Type      string `json:"type,omitempty"` // text | number（提示用途，抽取按实际值类型落列）
}

func validateRules(rules []IndexRule) error {
	seen := map[string]bool{}
	for i, r := range rules {
		if r.Field == "" || r.Path == "" {
			return fmt.Errorf("rule[%d]: field 与 path 必填", i)
		}
		switch r.Mode {
		case "", "scalar":
		case "array":
			if r.KeyPath == "" {
				return fmt.Errorf("rule[%d] (%s): array 规则需要 key_path", i, r.Field)
			}
		default:
			return fmt.Errorf("rule[%d] (%s): mode 只能是 scalar 或 array", i, r.Field)
		}
		if seen[r.Field] {
			return fmt.Errorf("rule[%d]: field %q 重复", i, r.Field)
		}
		seen[r.Field] = true
	}
	return nil
}

// indexEntry 一行索引：num/txt 按值的实际类型落列。
type indexEntry struct {
	Field string
	K     string
	Num   *float64
	Txt   *string
}

// extractEntries 按规则从原文抽取索引行。原文解析失败或路径不存在时静默跳过（字段可选）。
func extractEntries(rules []IndexRule, payload []byte) []indexEntry {
	var doc map[string]any
	if json.Unmarshal(payload, &doc) != nil {
		return nil
	}
	entries := []indexEntry{}
	for _, r := range rules {
		switch r.Mode {
		case "array":
			arr, ok := walk(doc, r.Path).([]any)
			if !ok {
				continue
			}
			for _, elem := range arr {
				obj, ok := elem.(map[string]any)
				if !ok {
					continue
				}
				k, ok := walk(obj, r.KeyPath).(string)
				if !ok || k == "" {
					continue
				}
				e := indexEntry{Field: r.Field, K: k}
				if r.ValuePath != "" {
					e.Num, e.Txt = numOrTxt(walk(obj, r.ValuePath))
				}
				entries = append(entries, e)
			}
		default: // scalar
			v := walk(doc, r.Path)
			if v == nil {
				continue
			}
			e := indexEntry{Field: r.Field}
			e.Num, e.Txt = numOrTxt(v)
			if e.Num == nil && e.Txt == nil {
				continue
			}
			entries = append(entries, e)
		}
	}
	return entries
}

// walk 按点路径取值（如 "aspect.key"）；路径不存在返回 nil。
func walk(doc any, path string) any {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[seg]
		if !ok {
			return nil
		}
	}
	return cur
}

func numOrTxt(v any) (*float64, *string) {
	switch t := v.(type) {
	case float64:
		return &t, nil
	case string:
		if t == "" {
			return nil, nil
		}
		return nil, &t
	case bool:
		s := fmt.Sprintf("%v", t)
		return nil, &s
	}
	return nil, nil
}
