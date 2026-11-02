// Package tool 定义工具契约、注册表与核心工具。
//
// 设计依据：docs/designs/06-工具.md（T1–T7）、
// docs/designs/11-安全与沙盒.md（SEC2–SEC4）。
package tool

import (
	"context"
	"errors"
)

// Status 四取值（T1）。
const (
	StatusOK       = "ok"
	StatusError    = "error"
	StatusTimeout  = "timeout"
	StatusCanceled = "canceled"
)

// Source 标记工具来源（T2）。
type Source int

const (
	SourceCore Source = iota
	SourceExternal
)

// ErrNotFound 表示工具未注册。
var ErrNotFound = errors.New("tool: 工具未找到")

// ErrInvalidArgument 表示参数不符合 Schema。
var ErrInvalidArgument = errors.New("tool: 参数不合法")

// Tool 是工具最小契约：名称、描述、参数 Schema、执行、来源标记（T1）。
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(ctx context.Context, args map[string]any) (string, error)
	Source() Source
}

// Record 是一次工具调用的六要素记录（T1）。
type Record struct {
	Name       string         `json:"name"`
	Schema     map[string]any `json:"schema"`
	Arguments  map[string]any `json:"arguments"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Status     string         `json:"status"`
	// Artifacts 是这次调用产生/修改的文件路径（022）。
	Artifacts []string `json:"artifacts,omitempty"`
}
