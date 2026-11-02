package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Options 是运行选项（E1/E2/E7）。
type Options struct {
	DryRun    bool
	ReplayDir string
	Filter    string
}

// Runner 编排矩阵执行与落盘（E2/E6/E10）。
type Runner struct {
	Cfg      *EvalConfig
	Cases    *Cases
	Engine   *Engine
	Recorder *Recorder
}

// Run 执行评测；DryRun 只打印矩阵不发请求。
func (r *Runner) Run(ctx context.Context, opts Options) ([]*RunResult, error) {
	matrix, err := BuildMatrix(r.Cfg, opts.Filter)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		fmt.Printf("stage=%s 组合数=%d 用例数=%d 重复=%d\n", r.Cfg.Stage, len(matrix), len(r.Cases.Cases), r.Cfg.Repeat)
		for _, m := range matrix {
			fmt.Printf("- %s/%s/%s [round=%s axis=%s]\n", m.Combo.Prompt, m.Combo.Schema, m.Combo.Return, m.Round, m.Axis)
		}
		return nil, nil
	}

	if opts.ReplayDir != "" {
		return r.runReplay(opts.ReplayDir)
	}

	roundLabel := r.Cfg.Stage
	if err := r.Recorder.WriteConfig(roundLabel, r.Cfg); err != nil {
		return nil, err
	}
	if err := r.Recorder.WriteCases(roundLabel, r.Cases); err != nil {
		return nil, err
	}

	concur := r.Cfg.MaxConcur
	if concur < 1 {
		concur = 1
	}
	sem := make(chan struct{}, concur)
	var mu sync.Mutex
	results := make([]*RunResult, 0)
	var wg sync.WaitGroup

	for _, entry := range matrix {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, tc := range r.Cases.Cases {
				for rep := 0; rep < r.Cfg.Repeat; rep++ {
					runID := fmt.Sprintf("%s-%s-%d", tc.ID, entry.Axis, rep)
					run := r.Engine.Run(ctx, entry.Combo, tc, rep, entry.Round, entry.Axis, runID)
					run.Judge.Rule = JudgeRules(tc, run.FinalAnswer, run.ToolCalls)
					if run.Status != StatusOK {
						run.Judge.Rule.Pass = false
						run.Judge.Rule.Reasons = append(run.Judge.Rule.Reasons, "status="+run.Status)
					}
					_ = r.Recorder.WriteRun(roundLabel, run)
					_ = r.Recorder.WriteTrace(roundLabel, run)
					mu.Lock()
					results = append(results, run)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].RunID < results[j].RunID })
	if err := r.writeReportAndBest(results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *Runner) runReplay(traceDir string) ([]*RunResult, error) {
	runs, err := ReplayResults(traceDir)
	if err != nil {
		return nil, err
	}
	roundLabel := r.Cfg.Stage
	for _, run := range runs {
		_ = r.Recorder.WriteRun(roundLabel, run)
	}
	return runs, r.writeReportAndBest(runs)
}

func (r *Runner) writeReportAndBest(runs []*RunResult) error {
	report := Report(r.Cfg, r.Cases, runs)
	dir := filepath.Join(r.Recorder.Out, "report")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, r.Cfg.Stage+"-"+r.Recorder.stamp()+".md"), []byte(report), 0o600); err != nil {
		return err
	}

	groups := groupByCombo(runs)
	best := bestCombo(groups)
	if best != nil && r.Cfg.Stage == "full" {
		bestDir := filepath.Join(r.Recorder.Out, "best")
		if err := os.MkdirAll(bestDir, 0o700); err != nil {
			return err
		}
		return writeJSONFile(filepath.Join(bestDir, "current.json"), map[string]any{
			"combo":   best.combo,
			"rate":    successRate(best.runs),
			"samples": len(best.runs),
		})
	}
	return nil
}
