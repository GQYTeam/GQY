// 包 engine：按配置分组并发执行矩阵，组内串行。
//
// 分组键是 prompt/schema/return 组合；同一组合的用例与重复次数串行跑，
// 保证同配置的多次重复受同等外部条件影响。结果按确定的槽位顺序返回。
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/judge"
	"gqy/internal/eval/matrix"
	"gqy/internal/eval/recorder"
	"gqy/internal/eval/result"
	"gqy/internal/eval/runner"
	"gqy/internal/tool"
)

// Options 是矩阵执行的全部输入。
type Options struct {
	Config      *config.Config
	Cases       *cases.Set
	Experiments []matrix.Experiment
	Registry    *tool.Registry
	Completer   runner.Completer
	Recorder    *recorder.Recorder

	ModelVer string
	Progress func(done, total int, tr *result.Trace)
}

type job struct {
	caseIdx int
	repeat  int
	slot    int
}

type group struct {
	exp  matrix.Experiment
	jobs []job
}

// Run 执行整个矩阵，返回按实验/用例/重复顺序排列的 trace。
func Run(ctx context.Context, opts Options) ([]*result.Trace, error) {
	if opts.Config == nil || opts.Cases == nil {
		return nil, errors.New("engine: Config/Cases 不能为空")
	}
	if opts.Registry == nil || opts.Completer == nil {
		return nil, errors.New("engine: Registry/Completer 不能为空")
	}
	if len(opts.Experiments) == 0 {
		return nil, errors.New("engine: 实验矩阵为空")
	}
	if len(opts.Cases.Cases) == 0 {
		return nil, errors.New("engine: 用例集为空")
	}
	cfg := opts.Config
	repeats := cfg.Repeats
	if repeats < 1 {
		repeats = 1
	}
	total := len(opts.Experiments) * len(opts.Cases.Cases) * repeats

	if opts.Recorder != nil {
		if err := opts.Recorder.WriteConfig(cfg, opts.Cases.Hash, opts.Cases.Version, result.JudgeVersion, opts.Cases.Files); err != nil {
			return nil, err
		}
		if err := opts.Recorder.WriteCaseSnapshot(opts.Cases); err != nil {
			return nil, err
		}
	}

	groups := buildGroups(opts.Experiments, len(opts.Cases.Cases), repeats)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	traces := make([]*result.Trace, total)
	var mu sync.Mutex
	var firstErr error
	done := 0

	workers := cfg.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(groups) {
		workers = len(groups)
	}

	queue := make(chan *group)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range queue {
				for _, j := range g.jobs {
					if runCtx.Err() != nil {
						return
					}
					c := opts.Cases.Cases[j.caseIdx]
					runID := ""
					if opts.Recorder != nil {
						runID = opts.Recorder.NextRunID(c.ID)
					} else {
						runID = result.NewRunID(cfg.Round, c.ID, j.slot+1)
					}
					tr, err := runner.RunOne(runCtx, runner.Options{
						Config:      cfg,
						Case:        c,
						Experiment:  g.exp,
						Registry:    opts.Registry,
						Completer:   opts.Completer,
						RepeatIdx:   j.repeat,
						RunID:       runID,
						ModelVer:    opts.ModelVer,
						CaseSetHash: opts.Cases.Hash,
					})
					if err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						cancel()
						return
					}
					v := judge.Judge(c, tr, cfg.StrictSchema)
					if cfg.JudgeModel != "" && opts.Completer != nil {
						v.Model = judge.Model(ctx, opts.Completer, cfg.JudgeModel, c, tr)
					}
					tr.Judge = &v

					if opts.Recorder != nil {
						if err := opts.Recorder.Record(tr); err != nil {
							mu.Lock()
							if firstErr == nil {
								firstErr = err
							}
							mu.Unlock()
							cancel()
							return
						}
						if tr.Status == result.StatusGatewayError || tr.Status == result.StatusTimeout || tr.Status == result.StatusExecError {
							msg := tr.Error
							if msg == "" {
								msg = string(tr.Status)
							}
							_ = opts.Recorder.LogError(tr.RunID, errors.New(msg))
						}
					}

					mu.Lock()
					traces[j.slot] = tr
					done++
					if opts.Progress != nil {
						opts.Progress(done, total, tr)
					}
					mu.Unlock()
				}
			}
		}()
	}

feed:
	for i := range groups {
		select {
		case queue <- &groups[i]:
		case <-runCtx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()

	if firstErr != nil {
		return compact(traces), firstErr
	}
	if err := ctx.Err(); err != nil {
		return compact(traces), err
	}
	out := compact(traces)
	if len(out) != total {
		return out, fmt.Errorf("engine: 期望 %d 条结果，实际完成 %d 条", total, len(out))
	}
	return out, nil
}

func buildGroups(exps []matrix.Experiment, caseCount, repeats int) []group {
	var out []group
	index := map[string]int{}
	for pos, exp := range exps {
		key := exp.Key()
		gi, ok := index[key]
		if !ok {
			gi = len(out)
			index[key] = gi
			out = append(out, group{exp: exp})
		}
		for ci := 0; ci < caseCount; ci++ {
			for r := 0; r < repeats; r++ {
				slot := (pos*caseCount+ci)*repeats + r
				out[gi].jobs = append(out[gi].jobs, job{caseIdx: ci, repeat: r, slot: slot})
			}
		}
	}
	return out
}

func compact(in []*result.Trace) []*result.Trace {
	out := make([]*result.Trace, 0, len(in))
	for _, tr := range in {
		if tr != nil {
			out = append(out, tr)
		}
	}
	return out
}
