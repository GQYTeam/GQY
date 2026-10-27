package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Recorder 把每次执行写进 runs.jsonl 与 trace/。
type Recorder struct {
	out      string
	label    string
	runs     *os.File
	traceDir string
}

func NewRecorder(out, label string, cfg Config, set CaseSet, variants []Variant) (*Recorder, error) {
	for _, dir := range []string{
		filepath.Join(out, "configs"),
		filepath.Join(out, "runs", label, "trace"),
		filepath.Join(out, "runs", label, "cases"),
		filepath.Join(out, "report"),
		filepath.Join(out, "best"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	snapshot, _ := json.MarshalIndent(map[string]any{
		"config": cfg, "variants": variants, "generated_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "configs", label+".json"), append(snapshot, '\n'), 0o600); err != nil {
		return nil, err
	}
	caseRaw, _ := json.MarshalIndent(set, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "runs", label, "cases", "cases.json"), append(caseRaw, '\n'), 0o600); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(out, "runs", label, "runs.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Recorder{out: out, label: label, runs: f, traceDir: filepath.Join(out, "runs", label, "trace")}, nil
}

func (r *Recorder) Add(rec RunRecord) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := r.runs.Write(append(line, '\n')); err != nil {
		return err
	}
	name := safeName(rec.RunID)
	trace, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(filepath.Join(r.traceDir, name+".json"), append(trace, '\n'), 0o600)
}

func (r *Recorder) Close() error {
	if r.runs != nil {
		return r.runs.Close()
	}
	return nil
}

type variantStat struct {
	key      string
	label    string
	round    string
	axis     string
	runs     int
	passed   int
	tokens   int
	rounds   int
	failures []string
}

// Report 写汇总报告，并返回胜出变体。
func Report(out, label string, cfg Config, variants []Variant, records []RunRecord) (Variant, bool, error) {
	stats := map[string]*variantStat{}
	for _, rec := range records {
		k := rec.Config.Key()
		s := stats[k]
		if s == nil {
			s = &variantStat{key: k, label: rec.Config.Label(), round: rec.Round, axis: rec.Axis}
			stats[k] = s
		}
		s.runs++
		if rec.Judge.Pass {
			s.passed++
		} else {
			s.failures = append(s.failures, rec.CaseID+"#"+fmt.Sprint(rec.Repeat))
		}
		s.tokens += rec.Tokens.In + rec.Tokens.Out
		s.rounds += rec.RoundsUsed
	}
	list := make([]*variantStat, 0, len(stats))
	for _, s := range stats {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		ri := float64(list[i].passed) / float64(max(list[i].runs, 1))
		rj := float64(list[j].passed) / float64(max(list[j].runs, 1))
		if ri != rj {
			return ri > rj
		}
		if list[i].tokens != list[j].tokens {
			return list[i].tokens < list[j].tokens
		}
		return list[i].key < list[j].key
	})

	var b strings.Builder
	fmt.Fprintf(&b, "# %s 评测报告\n\n", cfg.Name)
	fmt.Fprintf(&b, "- 模型：`%s`\n- 工具：`%s`\n- 用例：%d\n- 重复：%d\n- 阶段：`%s`\n- 生成：%s\n\n",
		cfg.Model, cfg.Tool, countCases(records), cfg.Repeats, cfg.Stage, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "## 矩阵汇总\n\n| 配置 | round | axis | 通过率 | 平均 token | 平均轮次 |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, s := range list {
		rate := float64(s.passed) / float64(max(s.runs, 1)) * 100
		fmt.Fprintf(&b, "| %s | %s | %s | %d/%d (%.0f%%) | %.0f | %.1f |\n",
			s.label, s.round, s.axis, s.passed, s.runs, rate,
			float64(s.tokens)/float64(max(s.runs, 1)),
			float64(s.rounds)/float64(max(s.runs, 1)))
	}
	if len(list) > 0 && len(list[0].failures) > 0 {
		fmt.Fprintf(&b, "\n## 反例\n\n")
		for _, f := range list[0].failures {
			if len(f) > 0 {
				fmt.Fprintf(&b, "- %s\n", f)
			}
		}
	}
	fmt.Fprintf(&b, "\n## 结论\n\n")
	if len(list) == 0 {
		b.WriteString("没有结果。\n")
	} else {
		best := list[0]
		rate := float64(best.passed) / float64(max(best.runs, 1)) * 100
		fmt.Fprintf(&b, "当前胜出：`%s`（通过率 %.0f%%，平均 %.0f token）。结论只对本次配置快照有效。\n",
			best.label, rate, float64(best.tokens)/float64(max(best.runs, 1)))
	}

	if err := os.MkdirAll(filepath.Join(out, "report"), 0o700); err != nil {
		return Variant{}, false, err
	}
	if err := os.WriteFile(filepath.Join(out, "report", label+".md"), []byte(b.String()), 0o600); err != nil {
		return Variant{}, false, err
	}

	var bestVariant Variant
	for _, v := range variants {
		if len(list) > 0 && v.Key() == list[0].key {
			bestVariant = v
		}
	}
	complete := true
	for _, rec := range records {
		if rec.Status == "gateway_error" {
			complete = false
		}
	}
	if complete && bestVariant.Prompt != "" {
		payload, _ := json.MarshalIndent(map[string]any{
			"config": bestVariant, "tool": cfg.Tool, "model": cfg.Model,
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		}, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "best", "current.json"), append(payload, '\n'), 0o600); err != nil {
			return Variant{}, false, err
		}
	}
	return bestVariant, complete, nil
}

func countCases(records []RunRecord) int {
	seen := map[string]bool{}
	for _, r := range records {
		seen[r.CaseID] = true
	}
	return len(seen)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
