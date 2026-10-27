// 包 matrix：把三维度枚举展开成实验矩阵，打 round/axis 标签并去重。
//
// 顺序是确定性的：prompt → schema → return，取值保持配置里的顺序。
package matrix

import (
	"fmt"
	"strings"

	"gqy/internal/eval/config"
)

// Experiment 是一个配置组合。
type Experiment struct {
	Prompt string `json:"prompt"`
	Schema string `json:"schema"`
	Return string `json:"return"`
	Round  string `json:"round"`
	Axis   string `json:"axis"`
	Index  int    `json:"index"`
}

// Key 是去重键。
func (e Experiment) Key() string {
	return e.Prompt + "\x00" + e.Schema + "\x00" + e.Return
}

// Generate 按阶段生成矩阵：
//
//	single: 基线 + 每次只变一个维度（其余取 fixed）
//	pair:   基线 + 任取两个维度同时变化（第三个取 fixed）
//	full:   三维全组合
//
// 调用前配置必须已通过 Validate。
func Generate(cfg *config.Config) []Experiment {
	order := []string{"prompt", "schema", "return"}
	pairs := [][2]string{{"prompt", "schema"}, {"prompt", "return"}, {"schema", "return"}}

	var raw []Experiment
	baseline := Experiment{Prompt: cfg.Fixed.Prompt, Schema: cfg.Fixed.Schema, Return: cfg.Fixed.Return, Round: cfg.Round, Axis: "baseline"}

	switch cfg.Stage {
	case config.StageSingle:
		raw = append(raw, baseline)
		for _, axis := range order {
			for _, v := range values(cfg, axis) {
				if v == fixed(cfg, axis) {
					continue
				}
				raw = append(raw, withAxis(cfg, axis, v, axis))
			}
		}
	case config.StagePair:
		raw = append(raw, baseline)
		for _, p := range pairs {
			for _, a := range values(cfg, p[0]) {
				if a == fixed(cfg, p[0]) {
					continue
				}
				for _, b := range values(cfg, p[1]) {
					if b == fixed(cfg, p[1]) {
						continue
					}
					exp := baseline
					set(&exp, p[0], a)
					set(&exp, p[1], b)
					exp.Axis = p[0] + "+" + p[1]
					raw = append(raw, exp)
				}
			}
		}
	case config.StageFull:
		for _, p := range cfg.Axes.Prompt {
			for _, s := range cfg.Axes.Schema {
				for _, r := range cfg.Axes.Return {
					exp := Experiment{Prompt: p, Schema: s, Return: r, Round: cfg.Round, Axis: "full"}
					raw = append(raw, exp)
				}
			}
		}
	default:
		// Validate 已拦截；防御性返回空矩阵。
		return nil
	}

	seen := map[string]bool{}
	out := make([]Experiment, 0, len(raw))
	for _, exp := range raw {
		key := exp.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		exp.Index = len(out)
		out = append(out, exp)
	}
	return out
}

func values(cfg *config.Config, axis string) []string {
	switch axis {
	case "prompt":
		return cfg.Axes.Prompt
	case "schema":
		return cfg.Axes.Schema
	default:
		return cfg.Axes.Return
	}
}

func fixed(cfg *config.Config, axis string) string {
	switch axis {
	case "prompt":
		return cfg.Fixed.Prompt
	case "schema":
		return cfg.Fixed.Schema
	default:
		return cfg.Fixed.Return
	}
}

func set(e *Experiment, axis, value string) {
	switch axis {
	case "prompt":
		e.Prompt = value
	case "schema":
		e.Schema = value
	default:
		e.Return = value
	}
}

func withAxis(cfg *config.Config, axis, value, label string) Experiment {
	e := Experiment{Prompt: cfg.Fixed.Prompt, Schema: cfg.Fixed.Schema, Return: cfg.Fixed.Return, Round: cfg.Round, Axis: label}
	set(&e, axis, value)
	return e
}

// Filter 是一个 key=value1,value2 过滤条件。
type Filter struct {
	Key    string
	Values []string
}

// ParseFilter 解析 key=value1,value2；key 不区分大小写。
func ParseFilter(raw string) (Filter, error) {
	raw = strings.TrimSpace(raw)
	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return Filter{}, fmt.Errorf("-filter %q: 需要 key=value 形式", raw)
	}
	key := strings.ToLower(strings.TrimSpace(raw[:idx]))
	parts := strings.Split(raw[idx+1:], ",")
	vals := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return Filter{}, fmt.Errorf("-filter %q: %s 的值为空", raw, key)
		}
		vals = append(vals, p)
	}
	return Filter{Key: key, Values: vals}, nil
}

// KnownFilterKey 判断过滤键是否受支持。case 由 CLI 在用例层处理。
func KnownFilterKey(key string) bool {
	switch key {
	case "prompt", "schema", "return", "round", "axis", "case":
		return true
	}
	return false
}

// Apply 过滤矩阵并重新编号；case 键在这里不参与矩阵过滤。
func Apply(exps []Experiment, filters []Filter) ([]Experiment, error) {
	out := append([]Experiment(nil), exps...)
	for _, f := range filters {
		if !KnownFilterKey(f.Key) {
			return nil, fmt.Errorf("-filter: 未知键 %q（可选 prompt/schema/return/round/axis/case）", f.Key)
		}
		if f.Key == "case" {
			continue
		}
		for _, v := range f.Values {
			switch f.Key {
			case "prompt":
				if !config.IsValidPrompt(v) {
					return nil, fmt.Errorf("-filter prompt=%s: 未知变体", v)
				}
			case "schema":
				if !config.IsValidSchema(v) {
					return nil, fmt.Errorf("-filter schema=%s: 未知变体", v)
				}
			case "return":
				if !config.IsValidReturn(v) {
					return nil, fmt.Errorf("-filter return=%s: 未知变体", v)
				}
			}
		}
		allowed := map[string]bool{}
		for _, v := range f.Values {
			allowed[v] = true
		}
		filtered := out[:0]
		for _, exp := range out {
			if allowed[field(exp, f.Key)] {
				filtered = append(filtered, exp)
			}
		}
		out = filtered
	}
	for i := range out {
		out[i].Index = i
	}
	return out, nil
}

// CaseValues 汇总 case 过滤值。
func CaseValues(filters []Filter) []string {
	var out []string
	for _, f := range filters {
		if f.Key == "case" {
			out = append(out, f.Values...)
		}
	}
	return out
}

func field(e Experiment, key string) string {
	switch key {
	case "prompt":
		return e.Prompt
	case "schema":
		return e.Schema
	case "return":
		return e.Return
	case "round":
		return e.Round
	case "axis":
		return e.Axis
	}
	return ""
}
