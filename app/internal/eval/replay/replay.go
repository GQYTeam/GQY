// 包 replay：从录制的 trace 回放，不发起真实模型请求。
//
// 回放使用 trace 里记录的模型响应和工具原始返回值，重新执行 return 渲染与规则判定，
// 用于验证渲染器/判定器改动是否改变结论。
package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gqy/internal/eval/judge"
	"gqy/internal/eval/result"
	"gqy/internal/eval/runner"
)

// Result 是一次 trace 的回放结果。
type Result struct {
	Trace       *result.Trace
	Verdict     result.Verdict
	Rendered    []string
	RenderMatch bool
	JudgeMatch  bool
}

// Matches 表示渲染与判定都与录制时一致。
func (r *Result) Matches() bool { return r.RenderMatch && r.JudgeMatch }

// Run 回放单条 trace。不会调用 Completer，工具也不会再次执行。
func Run(tr *result.Trace, strict bool) (*Result, error) {
	if tr == nil {
		return nil, errors.New("replay: trace 不能为空")
	}
	clone, err := cloneTrace(tr)
	if err != nil {
		return nil, err
	}
	out := &Result{Trace: clone, RenderMatch: true}
	for i := range clone.ToolCalls {
		rec := &clone.ToolCalls[i]
		var execErr error
		if rec.TimedOut {
			if rec.Error != "" {
				execErr = fmt.Errorf("%w: %s", context.DeadlineExceeded, rec.Error)
			} else {
				execErr = context.DeadlineExceeded
			}
		} else if rec.Error != "" {
			execErr = errors.New(rec.Error)
		}
		rendered, rerr := runner.RenderReturn(clone.Config.Return, rec.RetRaw, execErr)
		if rerr != nil {
			return nil, fmt.Errorf("回放 %s 第 %d 次工具调用: %w", tr.RunID, i+1, rerr)
		}
		if rendered != rec.RetRendered {
			out.RenderMatch = false
		}
		out.Rendered = append(out.Rendered, rendered)
		rec.RetRendered = rendered
	}

	v := judge.Judge(clone.Case, clone, strict)
	clone.Judge = &v
	out.Verdict = v
	if tr.Judge == nil {
		out.JudgeMatch = true
	} else {
		out.JudgeMatch = reflect.DeepEqual(*tr.Judge, v)
	}
	return out, nil
}

// LoadDir 读取一个 trace 目录（也接受包含 trace/ 子目录的 run 目录）。
func LoadDir(dir string) ([]*result.Trace, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("读取 trace 目录 %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", dir)
	}
	if sub := filepath.Join(dir, "trace"); isDir(sub) {
		dir = sub
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取 trace 目录 %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("trace 目录 %s 下没有 *.json", dir)
	}
	out := make([]*result.Trace, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", path, err)
		}
		var tr result.Trace
		dec := json.NewDecoder(bytes.NewReader(data))
		if err := dec.Decode(&tr); err != nil {
			return nil, fmt.Errorf("%s: trace 解析失败: %w", name, err)
		}
		if tr.RunID == "" {
			return nil, fmt.Errorf("%s: trace 缺少 run_id", name)
		}
		out = append(out, &tr)
	}
	return out, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func cloneTrace(tr *result.Trace) (*result.Trace, error) {
	data, err := json.Marshal(tr)
	if err != nil {
		return nil, fmt.Errorf("复制 trace: %w", err)
	}
	var out result.Trace
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("复制 trace: %w", err)
	}
	return &out, nil
}
