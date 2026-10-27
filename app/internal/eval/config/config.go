// 包 config：评测实验配置的 JSON 解析与严格校验。
//
// 三维度（prompt / schema / return）的取值必须显式枚举；配置只读，不边跑边改。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 三个维度的合法变体。新增变体时必须同步 runner 渲染实现。
var (
	PromptVariants = []string{"none", "basic", "when", "when-how", "fewshot", "strict"}
	SchemaVariants = []string{"min", "full", "example", "negative", "alias"}
	ReturnVariants = []string{"raw", "trim", "summary", "typed", "error-rich", "error-min"}
)

var roundRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Axes 是三个维度的候选值。
type Axes struct {
	Prompt []string `json:"prompt"`
	Schema []string `json:"schema"`
	Return []string `json:"return"`
}

// Fixed 是单因素/两两组合时固定其余维度的默认值。
type Fixed struct {
	Prompt string `json:"prompt"`
	Schema string `json:"schema"`
	Return string `json:"return"`
}

// Timeouts 是并发/重试/超时参数。0 表示使用默认值。
type Timeouts struct {
	RequestMS    int `json:"request_ms"`
	ToolMS       int `json:"tool_ms"`
	TotalMS      int `json:"total_ms"`
	Retries      int `json:"retries"`
	BackoffMS    int `json:"backoff_ms"`
	BackoffMaxMS int `json:"backoff_max_ms"`
}

// Config 是一份实验配置。
type Config struct {
	Name         string   `json:"name"`
	Tool         string   `json:"tool"`
	Model        string   `json:"model"`
	Cases        string   `json:"cases"`
	Repeats      int      `json:"repeats"`
	Concurrency  int      `json:"concurrency"`
	MaxRounds    int      `json:"max_rounds"`
	StrictSchema bool     `json:"strict_schema"`
	Stage        string   `json:"stage"`
	Round        string   `json:"round"`
	Axes         Axes     `json:"axes"`
	Fixed        Fixed    `json:"fixed"`
	Timeouts     Timeouts `json:"timeouts"`

	// CasesPath 是相对配置文件目录解析后的绝对/相对路径，不进入配置快照。
	CasesPath string `json:"-"`
}

// 阶段常量。
const (
	StageSingle = "single"
	StagePair   = "pair"
	StageFull   = "full"
)

// ValidationError 汇总所有校验问题，便于一次定位多个字段。
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return strings.Join(e.Problems, "; ")
}

// Load 读取并校验配置文件；cases 路径相对于配置文件所在目录解析。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("配置 %s: 文件为空", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, jsonLocError(path, data, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("配置 %s: JSON 末尾有多余内容", path)
		}
		return nil, jsonLocError(path, data, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	absConfig, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置路径 %s: %w", path, err)
	}
	if filepath.IsAbs(cfg.Cases) {
		cfg.CasesPath = filepath.Clean(cfg.Cases)
	} else {
		cfg.CasesPath = filepath.Clean(filepath.Join(filepath.Dir(absConfig), cfg.Cases))
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Round == "" {
		c.Round = "r1"
	}
	if c.Repeats == 0 {
		c.Repeats = 1
	}
	if c.Concurrency == 0 {
		c.Concurrency = 1
	}
	if c.MaxRounds == 0 {
		c.MaxRounds = 6
	}
	if c.Timeouts.RequestMS == 0 {
		c.Timeouts.RequestMS = 60_000
	}
	if c.Timeouts.ToolMS == 0 {
		c.Timeouts.ToolMS = 30_000
	}
	if c.Timeouts.TotalMS == 0 {
		c.Timeouts.TotalMS = 300_000
	}
	if c.Timeouts.BackoffMS == 0 {
		c.Timeouts.BackoffMS = 200
	}
	if c.Timeouts.BackoffMaxMS == 0 {
		c.Timeouts.BackoffMaxMS = 2_000
	}
}

// Validate 返回全部字段问题；路径形如 axes.prompt[1]。
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.Name) == "" {
		add("name: 不能为空")
	}
	if strings.TrimSpace(c.Tool) == "" {
		add("tool: 不能为空")
	} else if strings.TrimSpace(c.Tool) != c.Tool {
		add("tool: 不能包含首尾空白")
	}
	if strings.TrimSpace(c.Model) == "" {
		add("model: 不能为空")
	}
	if strings.TrimSpace(c.Cases) == "" {
		add("cases: 不能为空")
	}
	switch c.Stage {
	case StageSingle, StagePair, StageFull:
	case "":
		add("stage: 不能为空（可选 single/pair/full）")
	default:
		add("stage: 未知阶段 %q（可选 single/pair/full）", c.Stage)
	}
	if c.Round != "" && !roundRE.MatchString(c.Round) {
		add("round: %q 只能包含字母、数字、点、下划线和连字符", c.Round)
	}
	if c.Repeats < 1 {
		add("repeats: 必须 >= 1")
	}
	if c.Concurrency < 1 {
		add("concurrency: 必须 >= 1")
	}
	if c.MaxRounds < 1 {
		add("max_rounds: 必须 >= 1")
	}

	checkAxis := func(name string, values []string, valid func(string) bool) {
		if len(values) == 0 {
			add("axes.%s: 至少需要一个变体", name)
			return
		}
		seen := map[string]bool{}
		for i, v := range values {
			if !valid(v) {
				add("axes.%s[%d]: 未知变体 %q", name, i, v)
			}
			if seen[v] {
				add("axes.%s[%d]: 重复变体 %q", name, i, v)
			}
			seen[v] = true
		}
	}
	checkAxis("prompt", c.Axes.Prompt, IsValidPrompt)
	checkAxis("schema", c.Axes.Schema, IsValidSchema)
	checkAxis("return", c.Axes.Return, IsValidReturn)

	if !IsValidPrompt(c.Fixed.Prompt) {
		add("fixed.prompt: 未知变体 %q", c.Fixed.Prompt)
	}
	if !IsValidSchema(c.Fixed.Schema) {
		add("fixed.schema: 未知变体 %q", c.Fixed.Schema)
	}
	if !IsValidReturn(c.Fixed.Return) {
		add("fixed.return: 未知变体 %q", c.Fixed.Return)
	}

	if c.Timeouts.RequestMS < 0 {
		add("timeouts.request_ms: 不能为负数")
	}
	if c.Timeouts.ToolMS < 0 {
		add("timeouts.tool_ms: 不能为负数")
	}
	if c.Timeouts.TotalMS < 0 {
		add("timeouts.total_ms: 不能为负数")
	}
	if c.Timeouts.Retries < 0 {
		add("timeouts.retries: 不能为负数")
	}
	if c.Timeouts.BackoffMS < 0 {
		add("timeouts.backoff_ms: 不能为负数")
	}
	if c.Timeouts.BackoffMaxMS < 0 {
		add("timeouts.backoff_max_ms: 不能为负数")
	}
	if c.Timeouts.BackoffMS > 0 && c.Timeouts.BackoffMaxMS > 0 && c.Timeouts.BackoffMaxMS < c.Timeouts.BackoffMS {
		add("timeouts.backoff_max_ms: 不能小于 backoff_ms")
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func IsValidPrompt(v string) bool { return contains(PromptVariants, v) }
func IsValidSchema(v string) bool { return contains(SchemaVariants, v) }
func IsValidReturn(v string) bool { return contains(ReturnVariants, v) }

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// jsonLocError 给 JSON 语法/类型错误补上行列号。
func jsonLocError(path string, data []byte, err error) error {
	offset := int64(-1)
	var se *json.SyntaxError
	var ute *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		offset = se.Offset
	case errors.As(err, &ute):
		offset = ute.Offset
	}
	where := path
	if offset >= 0 {
		line, col := lineCol(data, offset)
		where = fmt.Sprintf("%s:%d:%d", path, line, col)
	}
	return fmt.Errorf("%s: JSON 解析失败: %v", where, err)
}

func lineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line, col := 1, 1
	for i := int64(0); i < offset; i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
