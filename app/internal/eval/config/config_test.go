package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validJSON = `{
  "name": "echo",
  "tool": "plugin__echo__echo",
  "model": "test-model",
  "cases": "cases.json",
  "stage": "single",
  "axes": {"prompt": ["basic", "when"], "schema": ["full"], "return": ["raw"]},
  "fixed": {"prompt": "basic", "schema": "full", "return": "raw"}
}`

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config.json", validJSON)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Round != "r1" || cfg.Repeats != 1 || cfg.Concurrency != 1 || cfg.MaxRounds != 6 {
		t.Fatalf("默认值不对: %+v", cfg)
	}
	if cfg.Timeouts.RequestMS != 60000 || cfg.Timeouts.ToolMS != 30000 || cfg.Timeouts.TotalMS != 300000 {
		t.Fatalf("超时默认值不对: %+v", cfg.Timeouts)
	}
	wantPath := filepath.Join(dir, "cases.json")
	if cfg.CasesPath != wantPath {
		t.Fatalf("CasesPath=%q, want %q", cfg.CasesPath, wantPath)
	}
}

func TestUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config.json", strings.Replace(validJSON, `"stage"`, `"out": "/tmp/x", "stage"`, 1))
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `unknown field "out"`) {
		t.Fatalf("期望 unknown field 错误，实际: %v", err)
	}
}

func TestUnknownVariantHasPath(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validJSON, `["basic", "when"]`, `["basic", "nope"]`, 1)
	path := write(t, dir, "config.json", body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望校验错误")
	}
	if !strings.Contains(err.Error(), "axes.prompt[1]") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("错误缺少定位: %v", err)
	}

	body = strings.Replace(validJSON, `"schema": "full"`, `"schema": "wat"`, 1)
	path = write(t, dir, "bad-fixed.json", body)
	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "fixed.schema") {
		t.Fatalf("期望 fixed.schema 错误，实际: %v", err)
	}
}

func TestSyntaxErrorLocation(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config.json", "{\n  \"name\": \"x\",\n  \"tool\": \n}\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "JSON 解析失败") {
		t.Fatalf("期望 JSON 解析错误，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("错误缺少文件名: %v", err)
	}
}

func TestInvalidNumbersAndStage(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(validJSON, `"stage": "single"`, `"stage": "nope"`, 1)
	body = strings.Replace(body, `"return": ["raw"]`, `"return": []`, 1)
	path := write(t, dir, "config.json", body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望校验错误")
	}
	for _, want := range []string{"stage", "axes.return"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误缺少 %q: %v", want, err)
		}
	}

	body = strings.Replace(validJSON, `"stage": "single"`, `"stage": "single", "repeats": -1, "concurrency": -2, "max_rounds": -3, "timeouts": {"retries": -1, "backoff_ms": 100, "backoff_max_ms": 10}`, 1)
	path = write(t, dir, "negative.json", body)
	_, err = Load(path)
	if err == nil {
		t.Fatal("期望负数校验错误")
	}
	for _, want := range []string{"repeats", "concurrency", "max_rounds", "timeouts.retries", "timeouts.backoff_max_ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误缺少 %q: %v", want, err)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config.json", "  \n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "文件为空") {
		t.Fatalf("期望文件为空错误，实际: %v", err)
	}
}
