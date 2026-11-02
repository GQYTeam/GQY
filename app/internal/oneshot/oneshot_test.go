package oneshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gqy/internal/storage"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRunPlugin(t *testing.T, dir, manifest, script string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "manifest.json"), manifest)
	writeFile(t, filepath.Join(dir, "plugin.sh"), script)
}

func TestParseSource(t *testing.T) {
	cases := []struct {
		in   string
		kind sourceKind
	}{
		{"/tmp/x", srcLocal},
		{"../x", srcLocal},
		{"git+https://github.com/x/migrator.git@v1.0.0", srcGit},
		{"https://github.com/x/migrator.git#v1.0.0", srcGit},
		{"https://example.com/migrator.tar.gz", srcURL},
		{"reg:migrator", srcRegistry},
		{"migrator@1.0.0", srcRegistry},
	}
	for _, c := range cases {
		spec, err := ParseSource(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if spec.kind != c.kind {
			t.Fatalf("%s: kind=%v 期望 %v", c.in, spec.kind, c.kind)
		}
	}
	if _, err := ParseSource(""); err == nil {
		t.Fatal("空来源应报错")
	}
}

func TestRunActionAndCleanup(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "migrator")
	writeRunPlugin(t, src,
		`{"id":"migrator","name":"迁移器","version":"1.0.0","protocol":"gqy-jsonrpc-v1","mode":"oneshot","command":["sh","plugin.sh"],"timeout_ms":10000,"actions":[{"name":"run","description":"执行迁移","schema":{"type":"object"}}]}`,
		`#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"call"'*)
      mkdir -p out
      echo '{"migrated":12}' > out/migrated.json
      echo '{"jsonrpc":"2.0","id":1,"result":{"content":"{\"migrated\":12,\"files\":[\"out/migrated.json\"]}"}}'
      exit 0
      ;;
  esac
done
`)

	res := Run(context.Background(), Request{Home: home, Source: src})
	if !res.OK {
		t.Fatalf("应成功: %+v", res)
	}
	if res.ID != "migrator" || !strings.Contains(res.Content, "migrated") {
		t.Fatalf("结果不符: %+v", res)
	}
	if len(res.Files) != 1 || res.Files[0] != "out/migrated.json" {
		t.Fatalf("files 收集不符: %v", res.Files)
	}
	if res.RunDir != "" {
		t.Fatalf("默认清理后 run_dir 应为空: %s", res.RunDir)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "tmp", "oneshot"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "migrator-") {
			t.Fatalf("工作目录未清理: %s", e.Name())
		}
	}
}

func TestDirectCommandAndKeep(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "migrator")
	writeRunPlugin(t, src,
		`{"id":"migrator","name":"迁移器","version":"1.0.0","protocol":"gqy-jsonrpc-v1","mode":"oneshot","command":["sh","plugin.sh"],"timeout_ms":10000}`,
		`#!/bin/sh
IFS= read -r line
echo "direct:$line"
`)

	res := Run(context.Background(), Request{Home: home, Source: src, Keep: true, Args: map[string]any{"n": 1}})
	if !res.OK {
		t.Fatalf("应成功: %+v", res)
	}
	if !strings.Contains(res.Content, "direct:") {
		t.Fatalf("直接 command 结果不符: %+v", res)
	}
	if res.RunDir == "" {
		t.Fatal("--keep 应保留 run_dir")
	}
	if _, err := os.Stat(filepath.Join(res.RunDir, "out", "stdout.txt")); err != nil {
		t.Fatalf("stdout 产物未收集: %v", err)
	}
	_ = os.RemoveAll(res.RunDir)
}

func TestPullFailNoResidue(t *testing.T) {
	home := t.TempDir()
	res := Run(context.Background(), Request{Home: home, Source: filepath.Join(t.TempDir(), "missing")})
	if res.OK {
		t.Fatal("拉取失败应返回失败")
	}
	entries, err := os.ReadDir(filepath.Join(home, "tmp", "oneshot"))
	if err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "missing") || strings.HasPrefix(e.Name(), ".staging-") {
				t.Fatalf("拉取失败残留目录: %s", e.Name())
			}
		}
	}
}

func TestURLHashMismatch(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "migrator")
	writeRunPlugin(t, src,
		`{"id":"migrator","name":"迁移器","version":"1.0.0","protocol":"gqy-jsonrpc-v1","mode":"oneshot","command":["sh","plugin.sh"],"hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","timeout_ms":10000}`,
		`#!/bin/sh
echo unused
`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarballOf(t, src))
	}))
	defer ts.Close()

	res := Run(context.Background(), Request{Home: home, Source: ts.URL + "/migrator.tar.gz"})
	if res.OK {
		t.Fatal("哈希不匹配应拒绝执行")
	}
	if !strings.Contains(res.Error, "哈希不匹配") {
		t.Fatalf("错误信息不符: %s", res.Error)
	}
}

func TestCacheHitAndGCRecycle(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "migrator")
	writeRunPlugin(t, src,
		`{"id":"migrator","name":"迁移器","version":"1.0.0","protocol":"gqy-jsonrpc-v1","mode":"oneshot","command":["sh","plugin.sh"],"timeout_ms":10000,"actions":[{"name":"run","description":"跑","schema":{"type":"object"}}]}`,
		`#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"call"'*) echo '{"jsonrpc":"2.0","id":1,"result":{"content":"{\"ok\":true}"}}'; exit 0 ;;
  esac
done
`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarballOf(t, src))
	}))
	defer ts.Close()

	first := Run(context.Background(), Request{Home: home, Source: ts.URL + "/migrator.tar.gz"})
	if !first.OK || first.Cached {
		t.Fatalf("首次应成功且未命中缓存: %+v", first)
	}
	second := Run(context.Background(), Request{Home: home, Source: ts.URL + "/migrator.tar.gz"})
	if !second.OK || !second.Cached {
		t.Fatalf("第二次应命中缓存: %+v", second)
	}

	removed, err := storage.CleanOneshotCache(home, time.Now(), 0)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if removed < 1 {
		t.Fatalf("应回收缓存，实际 %d", removed)
	}
	if _, err := os.Stat(filepath.Join(home, "tmp", "oneshot", ".cache")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(home, "tmp", "oneshot", ".cache"))
		if len(entries) != 0 {
			t.Fatalf("缓存未清空: %v", entries)
		}
	}
}

func tarballOf(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
