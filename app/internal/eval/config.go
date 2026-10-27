// 包 eval：工具调用评测网关。
//
// 设计见 docs/design/EvalGateway.md。配置用 JSON，避免引入 YAML 依赖。
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config 是一次评测的配置。
type Config struct {
	Name      string              `json:"name"`
	Model     string              `json:"model"`
	Tool      string              `json:"tool"`
	Cases     string              `json:"cases"`
	Repeats   int                 `json:"repeats"`
	MaxRounds int                 `json:"max_rounds"`
	Stage     string              `json:"stage"`
	Axes      map[string][]string `json:"axes"`
	Fixed     map[string]string   `json:"fixed"`
	Out       string              `json:"out"`
	BaseDir   string              `json:"-"`
}

// Case 是一条评测用例。
type Case struct {
	ID     string `json:"id"`
	Input  string `json:"input"`
	Inject string `json:"inject,omitempty"` // empty / error / timeout / slow
	Expect Expect `json:"expect"`
}

// Expect 是规则判定条件。
type Expect struct {
	ShouldCall    *bool             `json:"should_call,omitempty"`
	Tool          string            `json:"tool,omitempty"`
	ArgContains   map[string]string `json:"arg_contains,omitempty"`
	FinalContains []string          `json:"final_contains,omitempty"`
	MustNotCall   bool              `json:"must_not_call,omitempty"`
}

// CaseSet 是用例集。
type CaseSet struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

// Variant 是一组实验变量。
type Variant struct {
	Prompt string `json:"prompt"`
	Schema string `json:"schema"`
	Return string `json:"return"`
	Round  string `json:"round"`
	Axis   string `json:"axis"`
}

func (v Variant) Key() string {
	return v.Prompt + "/" + v.Schema + "/" + v.Return
}

func (v Variant) Label() string {
	return "p=" + v.Prompt + " s=" + v.Schema + " r=" + v.Return
}

// LoadConfig 读取配置并补默认值。
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Name == "" {
		cfg.Name = "eval"
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("model 不能为空")
	}
	if cfg.Tool == "" {
		return Config{}, fmt.Errorf("tool 不能为空")
	}
	if cfg.Cases == "" {
		cfg.Cases = "cases.json"
	}
	if cfg.Repeats <= 0 {
		cfg.Repeats = 1
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 6
	}
	if cfg.Stage == "" {
		cfg.Stage = "single"
	}
	if cfg.Out == "" {
		cfg.Out = filepath.Join("docs", "test_call_"+safeName(cfg.Tool))
	}
	cfg.BaseDir = filepath.Dir(path)
	if !filepath.IsAbs(cfg.Cases) {
		cfg.Cases = filepath.Join(cfg.BaseDir, cfg.Cases)
	}
	if !filepath.IsAbs(cfg.Out) {
		cfg.Out = filepath.Join(cfg.BaseDir, cfg.Out)
	}
	if cfg.Axes == nil {
		cfg.Axes = map[string][]string{
			"prompt": {"basic", "when-how"},
			"schema": {"min", "full"},
			"return": {"raw", "summary"},
		}
	}
	applyDefaults(cfg.Axes)
	if cfg.Fixed == nil {
		cfg.Fixed = map[string]string{}
	}
	for _, axis := range []string{"prompt", "schema", "return"} {
		if cfg.Fixed[axis] == "" {
			cfg.Fixed[axis] = first(cfg.Axes[axis])
		}
	}
	return cfg, nil
}

func applyDefaults(axes map[string][]string) {
	if len(axes["prompt"]) == 0 {
		axes["prompt"] = []string{"basic"}
	}
	if len(axes["schema"]) == 0 {
		axes["schema"] = []string{"full"}
	}
	if len(axes["return"]) == 0 {
		axes["return"] = []string{"raw"}
	}
}

// LoadCases 读取用例集。
func LoadCases(path string) (CaseSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return CaseSet{}, err
	}
	var set CaseSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return CaseSet{}, err
	}
	if len(set.Cases) == 0 {
		return set, fmt.Errorf("用例为空：%s", path)
	}
	for i := range set.Cases {
		if set.Cases[i].ID == "" {
			set.Cases[i].ID = fmt.Sprintf("case-%d", i+1)
		}
	}
	return set, nil
}

// GenerateMatrix 按 stage 生成变体。
func GenerateMatrix(cfg Config) []Variant {
	axes := [3]string{"prompt", "schema", "return"}
	values := map[string][]string{
		"prompt": cfg.Axes["prompt"],
		"schema": cfg.Axes["schema"],
		"return": cfg.Axes["return"],
	}
	fixed := map[string]string{
		"prompt": cfg.Fixed["prompt"],
		"schema": cfg.Fixed["schema"],
		"return": cfg.Fixed["return"],
	}
	var out []Variant
	add := func(round, axis string, overrides map[string]string) {
		v := Variant{
			Prompt: pick(overrides, fixed, "prompt"),
			Schema: pick(overrides, fixed, "schema"),
			Return: pick(overrides, fixed, "return"),
			Round:  round, Axis: axis,
		}
		for _, exists := range out {
			if exists.Key() == v.Key() {
				return
			}
		}
		out = append(out, v)
	}

	switch cfg.Stage {
	case "full":
		for _, p := range values["prompt"] {
			for _, s := range values["schema"] {
				for _, r := range values["return"] {
					add("full", "all", map[string]string{"prompt": p, "schema": s, "return": r})
				}
			}
		}
	case "pair":
		for i := 0; i < len(axes); i++ {
			for j := i + 1; j < len(axes); j++ {
				a, b := axes[i], axes[j]
				for _, av := range values[a] {
					for _, bv := range values[b] {
						add("pair", a+"+"+b, map[string]string{a: av, b: bv})
					}
				}
			}
		}
	default: // single
		for _, axis := range axes {
			for _, v := range values[axis] {
				add("single", axis, map[string]string{axis: v})
			}
		}
	}
	return out
}

func pick(overrides, fixed map[string]string, key string) string {
	if v := overrides[key]; v != "" {
		return v
	}
	return fixed[key]
}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "tool"
	}
	return out
}

// SortedKeys 返回稳定排序的键，供报告使用。
func SortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
