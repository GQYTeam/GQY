package eval

import "fmt"

// MatrixEntry 是一次实验组合及标签（E2）。
type MatrixEntry struct {
	Combo Combo
	Round string
	Axis  string
}

// BuildMatrix 按阶段生成实验矩阵；single 固定两维扫第三维，pair 两两组合，full 全组合。
func BuildMatrix(cfg *EvalConfig, filter string) ([]MatrixEntry, error) {
	combos := make([]MatrixEntry, 0)
	switch cfg.Stage {
	case "single":
		combos = append(combos, sweepAxis("prompt", cfg.PromptVars, func(v string) Combo {
			return Combo{Prompt: v, Schema: cfg.Fixed.Schema, Return: cfg.Fixed.Return}
		})...)
		combos = append(combos, sweepAxis("schema", cfg.SchemaVars, func(v string) Combo {
			return Combo{Prompt: cfg.Fixed.Prompt, Schema: v, Return: cfg.Fixed.Return}
		})...)
		combos = append(combos, sweepAxis("return", cfg.ReturnVars, func(v string) Combo {
			return Combo{Prompt: cfg.Fixed.Prompt, Schema: cfg.Fixed.Schema, Return: v}
		})...)
	case "pair":
		for _, p := range cfg.PromptVars {
			for _, s := range cfg.SchemaVars {
				combos = append(combos, MatrixEntry{Combo: Combo{Prompt: p, Schema: s, Return: cfg.Fixed.Return}, Round: "pair", Axis: "pair"})
			}
		}
	case "full":
		for _, p := range cfg.PromptVars {
			for _, s := range cfg.SchemaVars {
				for _, r := range cfg.ReturnVars {
					combos = append(combos, MatrixEntry{Combo: Combo{Prompt: p, Schema: s, Return: r}, Round: "full", Axis: "full"})
				}
			}
		}
	default:
		return nil, fmt.Errorf("eval: 未知 stage %q", cfg.Stage)
	}
	return filterMatrix(combos, filter), nil
}

func sweepAxis(axis string, vars []string, mk func(string) Combo) []MatrixEntry {
	out := make([]MatrixEntry, 0, len(vars))
	for _, v := range vars {
		out = append(out, MatrixEntry{Combo: mk(v), Round: "single", Axis: axis})
	}
	return out
}

// filterMatrix 按 filter=value 过滤组合。
func filterMatrix(in []MatrixEntry, filter string) []MatrixEntry {
	if filter == "" {
		return in
	}
	axis, want := splitFilter(filter)
	if axis == "" {
		return in
	}
	out := make([]MatrixEntry, 0, len(in))
	for _, e := range in {
		val := ""
		switch axis {
		case "prompt":
			val = e.Combo.Prompt
		case "schema":
			val = e.Combo.Schema
		case "return":
			val = e.Combo.Return
		}
		if val == want {
			out = append(out, e)
		}
	}
	return out
}

func splitFilter(filter string) (string, string) {
	for i := 0; i < len(filter); i++ {
		if filter[i] == '=' {
			return filter[:i], filter[i+1:]
		}
	}
	return filter, ""
}
