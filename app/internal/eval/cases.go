package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// Expect 是期望行为五条件，缺省不判该项（E3）。
type Expect struct {
	ShouldCall   *bool          `json:"should_call,omitempty"`
	Tool         string         `json:"tool,omitempty"`
	ArgContains  map[string]any `json:"arg_contains,omitempty"`
	FinalContain []string       `json:"final_contains,omitempty"`
	MustNotCall  string         `json:"must_not_call,omitempty"`
}

// Inject 是故障注入（E3）。
type Inject struct {
	Mode    string `json:"mode"` // empty | error | timeout | slow
	Tool    string `json:"tool"`
	Times   int    `json:"times"`
	Message string `json:"message"`
	DelayMS int    `json:"delay_ms"`
}

// Case 是一个用例：输入 + 期望行为。
type Case struct {
	ID     string  `json:"id"`
	Input  string  `json:"input"`
	Expect Expect  `json:"expect"`
	Inject *Inject `json:"inject"`
}

// Cases 是用例集（带版本冻结）。
type Cases struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

// LoadCases 读取用例集。
func LoadCases(path string) (*Cases, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: 读用例: %w", err)
	}
	var c Cases
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("eval: 用例解析: %w", err)
	}
	if c.Version == "" {
		c.Version = "1"
	}
	return &c, nil
}
