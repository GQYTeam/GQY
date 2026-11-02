package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var secretRe = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|authorization)(["\s:=]+)"[^"]*"`)

// Redact 对 JSON 文本脱敏（E6/G6）。
func Redact(data []byte) []byte {
	return secretRe.ReplaceAll(data, []byte(`$1$2"***"`))
}

// Recorder 负责落盘（E6）。
type Recorder struct {
	Out string
	Now func() time.Time
}

// NewRecorder 构造落盘器。
func NewRecorder(out string) *Recorder {
	return &Recorder{Out: out, Now: time.Now}
}

func (r *Recorder) stamp() string { return r.Now().Format("20060102-150405") }

// WriteRun 追加一行 runs.jsonl。
func (r *Recorder) WriteRun(round string, run *RunResult) error {
	dir := filepath.Join(r.Out, "runs", round+"-"+r.stamp())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	data = append(Redact(data), '\n')
	_, err = f.Write(data)
	return err
}

// WriteTrace 写单次运行 trace。
func (r *Recorder) WriteTrace(round string, run *RunResult) error {
	dir := filepath.Join(r.Out, "runs", round+"-"+r.stamp(), "trace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, run.RunID+".json"), run)
}

// WriteCases 写用例快照。
func (r *Recorder) WriteCases(round string, cases *Cases) error {
	dir := filepath.Join(r.Out, "runs", round+"-"+r.stamp(), "cases")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, "cases.json"), cases)
}

// WriteConfig 写配置快照。
func (r *Recorder) WriteConfig(round string, cfg *EvalConfig) error {
	dir := filepath.Join(r.Out, "configs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, round+"-"+r.stamp()+".json"), cfg)
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(Redact(data), '\n')
	return os.WriteFile(path, data, 0o600)
}

var _ = strings.TrimSpace
