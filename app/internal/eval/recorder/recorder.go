// 包 recorder：评测结果落盘。单文件原子写，逐行 jsonl，所有产物落盘前脱敏。
package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/result"
)

// ConfigSnapshot 是 configs/{round}-{ts}.json 的内容。
type ConfigSnapshot struct {
	Config         *config.Config `json:"config"`
	CasesPath      string         `json:"cases_path"`
	CaseSetHash    string         `json:"case_set_hash"`
	CaseSetVersion string         `json:"case_set_version"`
	CaseFiles      []string       `json:"case_files,omitempty"`
	GatewayVersion string         `json:"gateway_ver"`
	JudgeVersion   string         `json:"judge_ver"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Recorder 负责一次实验的输出目录。
type Recorder struct {
	out        string
	round      string
	ts         string
	runDir     string
	configPath string
	reportPath string

	mu       sync.Mutex
	runsFile *os.File
	errFile  *os.File
	closed   bool

	seq atomic.Int64
	now time.Time
}

// New 创建输出目录；同一秒重名时自动追加序号，不覆盖历史结果。
func New(out, round string, now time.Time) (*Recorder, error) {
	if strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("输出目录不能为空")
	}
	round = result.SafeToken(round, "r1")
	if now.IsZero() {
		now = time.Now()
	}
	baseTS := now.Format("20060102-150405")

	var runDir, ts string
	for i := 1; ; i++ {
		ts = baseTS
		if i > 1 {
			ts = fmt.Sprintf("%s-%d", baseTS, i)
		}
		runDir = filepath.Join(out, "runs", round+"-"+ts)
		if _, err := os.Stat(runDir); os.IsNotExist(err) {
			break
		}
		if i > 1000 {
			return nil, fmt.Errorf("无法为 %s 分配唯一输出目录", runDir)
		}
	}
	r := &Recorder{
		out:        out,
		round:      round,
		ts:         ts,
		runDir:     runDir,
		configPath: filepath.Join(out, "configs", round+"-"+ts+".json"),
		reportPath: filepath.Join(out, "report", round+"-"+ts+".md"),
		now:        now,
	}
	for _, dir := range []string{
		filepath.Join(out, "configs"),
		filepath.Join(out, "report"),
		runDir,
		filepath.Join(runDir, "trace"),
		filepath.Join(runDir, "cases"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建目录 %s: %w", dir, err)
		}
	}
	var err error
	r.runsFile, err = os.OpenFile(filepath.Join(runDir, "runs.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("创建 runs.jsonl: %w", err)
	}
	r.errFile, err = os.OpenFile(filepath.Join(runDir, "errors.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = r.runsFile.Close()
		return nil, fmt.Errorf("创建 errors.log: %w", err)
	}
	return r, nil
}

func (r *Recorder) OutDir() string      { return r.out }
func (r *Recorder) Round() string       { return r.round }
func (r *Recorder) Timestamp() string   { return r.ts }
func (r *Recorder) RunDir() string      { return r.runDir }
func (r *Recorder) ConfigPath() string  { return r.configPath }
func (r *Recorder) ReportPath() string  { return r.reportPath }
func (r *Recorder) RunsPath() string    { return filepath.Join(r.runDir, "runs.jsonl") }
func (r *Recorder) ErrorsPath() string  { return filepath.Join(r.runDir, "errors.log") }
func (r *Recorder) RunRelDir() string   { return filepath.Join("runs", r.round+"-"+r.ts) }
func (r *Recorder) TraceRelDir() string { return filepath.Join(r.RunRelDir(), "trace") }
func (r *Recorder) TraceDir() string    { return filepath.Join(r.runDir, "trace") }
func (r *Recorder) CasesDir() string    { return filepath.Join(r.runDir, "cases") }

// NextRunID 生成进程内唯一的 run_id。
func (r *Recorder) NextRunID(caseID string) string {
	return result.NewRunID(r.round, caseID, int(r.seq.Add(1)))
}

// WriteConfig 原子写配置快照。
func (r *Recorder) WriteConfig(cfg *config.Config, caseHash, caseVersion, judgeVer string, caseFiles []string) error {
	snap := ConfigSnapshot{
		Config:         cfg,
		CasesPath:      cfg.CasesPath,
		CaseSetHash:    caseHash,
		CaseSetVersion: caseVersion,
		CaseFiles:      caseFiles,
		GatewayVersion: result.GatewayVersion,
		JudgeVersion:   judgeVer,
		CreatedAt:      r.now,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置快照: %w", err)
	}
	data, err = result.MaskJSON(data, true)
	if err != nil {
		return fmt.Errorf("脱敏配置快照: %w", err)
	}
	return writeAtomic(r.configPath, append(data, '\n'))
}

// WriteCaseSnapshot 原子写用例快照。
func (r *Recorder) WriteCaseSnapshot(set *cases.Set) error {
	data, err := set.Snapshot()
	if err != nil {
		return fmt.Errorf("序列化用例快照: %w", err)
	}
	data, err = result.MaskJSON(data, true)
	if err != nil {
		return fmt.Errorf("脱敏用例快照: %w", err)
	}
	return writeAtomic(filepath.Join(r.runDir, "cases", "cases.json"), append(data, '\n'))
}

// Record 写一条运行记录：trace 单文件 + runs.jsonl 追加一行。
func (r *Recorder) Record(tr *result.Trace) error {
	if tr == nil {
		return fmt.Errorf("trace 不能为空")
	}
	if strings.TrimSpace(tr.RunID) == "" {
		return fmt.Errorf("trace.run_id 不能为空")
	}
	traceData, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 trace %s: %w", tr.RunID, err)
	}
	traceData, err = result.MaskJSON(traceData, true)
	if err != nil {
		return fmt.Errorf("脱敏 trace %s: %w", tr.RunID, err)
	}
	name := result.SafeToken(tr.RunID, "run") + ".json"
	if err := writeAtomic(filepath.Join(r.runDir, "trace", name), append(traceData, '\n')); err != nil {
		return err
	}

	rec := result.NewRunRecord(tr)
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("序列化 run 记录 %s: %w", tr.RunID, err)
	}
	line, err = result.MaskJSON(line, false)
	if err != nil {
		return fmt.Errorf("脱敏 run 记录 %s: %w", tr.RunID, err)
	}
	line = append(line, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("recorder 已关闭")
	}
	if _, err := r.runsFile.Write(line); err != nil {
		return fmt.Errorf("写 runs.jsonl: %w", err)
	}
	if err := r.runsFile.Sync(); err != nil {
		return fmt.Errorf("刷 runs.jsonl: %w", err)
	}
	return nil
}

// LogError 记录网关/工具运行期异常（脱敏）。
func (r *Recorder) LogError(runID string, err error) error {
	if err == nil {
		return nil
	}
	msg := result.MaskString(err.Error())
	line := fmt.Sprintf("%s\t%s\t%s\n", time.Now().Format(time.RFC3339), runID, msg)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("recorder 已关闭")
	}
	if _, werr := r.errFile.WriteString(line); werr != nil {
		return fmt.Errorf("写 errors.log: %w", werr)
	}
	return r.errFile.Sync()
}

// WriteReport 原子写报告。
func (r *Recorder) WriteReport(md string) error {
	md = result.MaskString(md)
	if !strings.HasSuffix(md, "\n") {
		md += "\n"
	}
	return writeAtomic(r.reportPath, []byte(md))
}

// Close 关闭追加文件；可重复调用。
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var first error
	if r.runsFile != nil {
		if err := r.runsFile.Close(); err != nil {
			first = err
		}
	}
	if r.errFile != nil {
		if err := r.errFile.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件 %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("设置权限 %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("同步 %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("关闭临时文件 %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("替换 %s: %w", path, err)
	}
	return nil
}
