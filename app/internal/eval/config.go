package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// EvalConfig 是评测配置（E2/E10）。
type EvalConfig struct {
	Model      string   `json:"model"`
	Stage      string   `json:"stage"` // single | pair | full
	Repeat     int      `json:"repeat"`
	MaxRounds  int      `json:"max_rounds"`
	TimeoutMS  int      `json:"timeout_ms"`
	MaxConcur  int      `json:"max_concurrency"`
	PromptVars []string `json:"prompt_variants"`
	SchemaVars []string `json:"schema_variants"`
	ReturnVars []string `json:"return_variants"`
	Fixed      Combo    `json:"fixed"` // single 阶段固定两维
	JudgeModel string   `json:"judge_model"`
}

// Defaults 填充缺省值。
func (c *EvalConfig) Defaults() {
	if c.Stage == "" {
		c.Stage = "single"
	}
	if c.Repeat <= 0 {
		c.Repeat = 1
	}
	if c.MaxRounds <= 0 {
		c.MaxRounds = 5
	}
	if c.TimeoutMS <= 0 {
		c.TimeoutMS = 60000
	}
	if c.MaxConcur <= 0 {
		c.MaxConcur = 2
	}
	if len(c.PromptVars) == 0 {
		c.PromptVars = []string{"basic"}
	}
	if len(c.SchemaVars) == 0 {
		c.SchemaVars = []string{"full"}
	}
	if len(c.ReturnVars) == 0 {
		c.ReturnVars = []string{"raw"}
	}
}

// LoadConfig 读取评测配置。
func LoadConfig(path string) (*EvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: 读配置: %w", err)
	}
	var cfg EvalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("eval: 配置解析: %w", err)
	}
	cfg.Defaults()
	return &cfg, nil
}
