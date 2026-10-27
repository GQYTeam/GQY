package recorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/eval/cases"
	"gqy/internal/eval/config"
	"gqy/internal/eval/result"
	"gqy/internal/provider"
)

func sampleConfig() *config.Config {
	return &config.Config{
		Name: "n", Tool: "plugin__echo__echo", Model: "m", Cases: "cases.json", CasesPath: "/tmp/cases.json",
		Repeats: 1, Concurrency: 1, MaxRounds: 3, Stage: "single", Round: "r1",
		Axes:  config.Axes{Prompt: []string{"basic"}, Schema: []string{"full"}, Return: []string{"raw"}},
		Fixed: config.Fixed{Prompt: "basic", Schema: "full", Return: "raw"},
	}
}

func sampleTrace() *result.Trace {
	return &result.Trace{
		RunID:     "r1-0001-c1",
		CaseID:    "c1",
		RepeatIdx: 0,
		Config: result.RunConfig{
			Prompt: "basic", Schema: "full", Return: "raw", Model: "m",
			GatewayVer: result.GatewayVersion, Tool: "plugin__echo__echo",
			CaseSetHash: "hash", JudgeVer: result.JudgeVersion,
		},
		Round: "r1", Axis: "baseline",
		Messages: []provider.Message{
			{Role: "user", Content: "请用 sk-abcdef1234567890 调用"},
			{Role: "assistant", Content: "好的"},
		},
		ToolCalls: []result.ToolCallRecord{{
			Name:        "plugin__echo__echo",
			ArgsRaw:     `{"api_key":"sk-secret-123456","text":"hello"}`,
			ArgsParsed:  map[string]any{"api_key": "sk-secret-123456", "text": "hello"},
			SchemaValid: true,
			ExecOK:      true,
			RetRaw:      `{"ok":true,"token":"sk-secret-123456"}`,
			RetRendered: `{"ok":true,"token":"sk-secret-123456"}`,
			LatencyMS:   12,
		}},
		FinalAnswer: "完成 sk-abcdef1234567890",
		RoundsUsed:  1,
		Tokens:      result.Tokens{In: 10, Out: 5, Total: 15},
		TotalMS:     120,
		Status:      result.StatusOK,
		StartedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Judge: &result.Verdict{
			Pass:    true,
			Reasons: []string{},
			Rules:   map[string]bool{"status_ok": true},
		},
	}
}

func TestRecorderStructureAndMasking(t *testing.T) {
	out := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec, err := New(out, "r1", now)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	if err := rec.WriteConfig(sampleConfig(), "casehash", "1", result.JudgeVersion, []string{"cases.json"}); err != nil {
		t.Fatal(err)
	}
	set := &cases.Set{Version: "1", Hash: "casehash", Files: []string{"cases.json"}, Cases: []cases.Case{{ID: "c1", Input: "x"}}}
	if err := rec.WriteCaseSnapshot(set); err != nil {
		t.Fatal(err)
	}
	tr := sampleTrace()
	if err := rec.Record(tr); err != nil {
		t.Fatal(err)
	}
	if err := rec.LogError(tr.RunID, os.ErrPermission); err != nil {
		t.Fatal(err)
	}
	if err := rec.LogError(tr.RunID, errString("连接失败 sk-abcdef1234567890")); err != nil {
		t.Fatal(err)
	}
	if err := rec.WriteReport("# report\n\nsecret sk-abcdef1234567890"); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		rec.ConfigPath(), rec.RunsPath(), rec.ErrorsPath(), rec.ReportPath(),
		filepath.Join(rec.RunDir(), "cases", "cases.json"),
		filepath.Join(rec.TraceDir(), tr.RunID+".json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("缺少产物 %s: %v", path, err)
		}
	}

	runs, err := os.ReadFile(rec.RunsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runs), "sk-secret-123456") || strings.Contains(string(runs), "sk-abcdef1234567890") {
		t.Fatalf("runs.jsonl 未脱敏: %s", runs)
	}
	lines := strings.Split(strings.TrimSpace(string(runs)), "\n")
	if len(lines) != 1 {
		t.Fatalf("runs.jsonl 行数=%d", len(lines))
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"run_id", "case_id", "repeat_idx", "config", "round", "axis", "messages", "tool_calls", "final_answer", "rounds_used", "tokens", "total_ms", "judge", "status"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("runs.jsonl 缺少字段 %q: %+v", key, row)
		}
	}
	if row["run_id"] != tr.RunID || row["case_id"] != "c1" {
		t.Fatalf("run_id/case_id 不对: %+v", row)
	}
	cfgRow := row["config"].(map[string]any)
	if cfgRow["prompt"] != "basic" || cfgRow["gateway_ver"] != result.GatewayVersion {
		t.Fatalf("config 字段不对: %+v", cfgRow)
	}
	judgeRow := row["judge"].(map[string]any)["rule"].(map[string]any)
	if judgeRow["pass"] != true {
		t.Fatalf("judge.rule.pass 不对: %+v", judgeRow)
	}

	traceData, _ := os.ReadFile(filepath.Join(rec.TraceDir(), tr.RunID+".json"))
	if strings.Contains(string(traceData), "sk-secret-123456") {
		t.Fatalf("trace 未脱敏")
	}
	errLog, _ := os.ReadFile(rec.ErrorsPath())
	if strings.Contains(string(errLog), "sk-abcdef1234567890") || !strings.Contains(string(errLog), "***") {
		t.Fatalf("errors.log 未脱敏: %s", errLog)
	}
	reportData, _ := os.ReadFile(rec.ReportPath())
	if strings.Contains(string(reportData), "sk-abcdef1234567890") {
		t.Fatalf("report 未脱敏")
	}

	// 不应残留临时文件。
	_ = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".tmp-") {
			t.Fatalf("残留临时文件 %s", path)
		}
		return nil
	})
}

func TestRecorderCollisionAndCloseTwice(t *testing.T) {
	out := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a, err := New(out, "r1", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(out, "r1", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.RunDir() == b.RunDir() {
		t.Fatalf("同秒新建应避免覆盖: %s", a.RunDir())
	}
	if !strings.HasSuffix(b.Timestamp(), "-2") {
		t.Fatalf("第二个目录应带 -2: %s", b.Timestamp())
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewEmptyOut(t *testing.T) {
	if _, err := New("  ", "r1", time.Now()); err == nil {
		t.Fatal("空输出目录应报错")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
