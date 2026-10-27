package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gqy/internal/eval/cases"
)

// Injector 是故障注入接口。返回 injected=false 表示本次调用不注入。
type Injector interface {
	Inject(ctx context.Context, caseID, tool string, callIdx int) (out string, err error, injected bool)
}

// CaseInjector 按用例的 inject 字段注入故障，每个 run 新建一份。
type CaseInjector struct {
	mu   sync.Mutex
	spec *cases.Inject
	used int
}

func NewCaseInjector(spec *cases.Inject) *CaseInjector {
	if spec == nil {
		return nil
	}
	return &CaseInjector{spec: spec}
}

func (i *CaseInjector) Inject(ctx context.Context, caseID, tool string, callIdx int) (string, error, bool) {
	if i == nil || i.spec == nil {
		return "", nil, false
	}
	i.mu.Lock()
	if i.spec.Tool != "" && i.spec.Tool != tool {
		i.mu.Unlock()
		return "", nil, false
	}
	times := i.spec.Times
	if times <= 0 {
		times = 1
	}
	if i.used >= times {
		i.mu.Unlock()
		return "", nil, false
	}
	i.used++
	spec := *i.spec
	i.mu.Unlock()

	switch spec.Mode {
	case "empty":
		return "", nil, true
	case "error":
		msg := spec.Message
		if msg == "" {
			msg = "注入故障：工具执行失败"
		}
		return "", errors.New(msg), true
	case "timeout":
		return "", fmt.Errorf("注入故障：工具执行超时: %w", context.DeadlineExceeded), true
	case "slow":
		delay := spec.DelayMS
		if delay <= 0 {
			delay = 100
		}
		timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err(), true
		case <-timer.C:
			return "（慢响应）工具在 " + fmt.Sprint(delay) + "ms 后返回", nil, true
		}
	default:
		return "", nil, false
	}
}
