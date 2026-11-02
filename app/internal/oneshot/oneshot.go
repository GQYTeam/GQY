package oneshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultCacheTTL 是一次性插件缓存的默认保留时长（NX6）。
const DefaultCacheTTL = 7 * 24 * time.Hour

// Request 是一次运行的入参。
type Request struct {
	Home    string
	Source  string
	Args    map[string]any
	Keep    bool
	NoCache bool
	ID      string // 可选：覆盖目录名与缓存键里的 id
}

// Result 是一次运行的结果。
type Result struct {
	OK         bool     `json:"ok"`
	ID         string   `json:"id,omitempty"`
	Version    string   `json:"version,omitempty"`
	RunDir     string   `json:"run_dir,omitempty"`
	Cached     bool     `json:"cached,omitempty"`
	Content    string   `json:"content,omitempty"`
	Result     any      `json:"result,omitempty"`
	Files      []string `json:"files,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
}

// Run 执行一次一次性插件（NX4 生命周期）。
func Run(ctx context.Context, req Request) Result {
	start := time.Now()
	fail := func(err error) Result {
		return Result{OK: false, DurationMS: time.Since(start).Milliseconds(), Error: err.Error()}
	}
	if req.Home == "" {
		return fail(fmt.Errorf("oneshot: Home 为空"))
	}
	spec, err := ParseSource(req.Source)
	if err != nil {
		return fail(err)
	}

	base := filepath.Join(req.Home, "tmp", "oneshot")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fail(err)
	}

	runTag := fmt.Sprintf("%d-%04x", time.Now().UnixNano(), time.Now().UnixNano()%0xffff)
	staging := filepath.Join(base, ".staging-"+runTag)
	if err := os.RemoveAll(staging); err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return fail(err)
	}

	srcHash, err := pull(ctx, req.Home, spec, staging)
	if err != nil {
		return fail(err)
	}
	m, err := parseManifestFile(manifestPath(staging))
	if err != nil {
		return fail(err)
	}
	// git 的锚点是 commit SHA；本地路径可信不校验；只有 url / 注册表比对 manifest.hash。
	if spec.kind == srcURL || spec.kind == srcRegistry {
		if err := verifyHash(m.Hash, srcHash); err != nil {
			return fail(err)
		}
	}

	id := m.ID
	if req.ID != "" {
		id = req.ID
	}
	version := m.Version
	runDir := filepath.Join(base, id+"-"+runTag)

	cached := false
	cacheDir := filepath.Join(base, ".cache", id+"-"+version)
	if !req.NoCache && srcHash != "" {
		if cacheHit(cacheDir, srcHash) {
			if err := copyTree(cacheDir, runDir, skipRunArtifacts); err != nil {
				return fail(err)
			}
			cached = true
		}
	}
	if !cached {
		if err := os.Rename(staging, runDir); err != nil {
			return fail(err)
		}
	}

	timeout := time.Duration(m.TimeoutMS) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	content, stdout, exitErr := execute(runCtx, m, runDir, req.Args)
	duration := time.Since(start).Milliseconds()

	if exitErr != nil {
		writeRunRecord(runDir, id, version, req.Source, srcHash, "error", duration)
		if !req.Keep {
			_ = os.RemoveAll(runDir)
		}
		res := fail(fmt.Errorf("oneshot: 执行失败: %w", exitErr))
		res.ID, res.Version = id, version
		res.RunDir = ""
		if req.Keep {
			res.RunDir = runDir
		}
		res.Content = content
		res.DurationMS = duration
		return res
	}

	files := collect(runDir, content, stdout)
	if !req.NoCache && srcHash != "" {
		_ = copyTree(runDir, cacheDir, skipRunArtifacts)
		_ = writeCacheMeta(cacheDir, srcHash)
	}
	writeRunRecord(runDir, id, version, req.Source, srcHash, "ok", duration)

	if !req.Keep {
		_ = os.RemoveAll(runDir)
	}

	var structured any
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &structured) != nil {
		structured = nil
	}
	res := Result{
		OK:         true,
		ID:         id,
		Version:    version,
		Cached:     cached,
		Content:    content,
		Result:     structured,
		Files:      files,
		DurationMS: duration,
	}
	if req.Keep {
		res.RunDir = runDir
	}
	return res
}

func verifyHash(want, got string) error {
	want = strings.TrimPrefix(strings.ToLower(want), "sha256:")
	if want == "" {
		return nil
	}
	if got == "" {
		return fmt.Errorf("oneshot: 来源无哈希可校验，但 manifest 声明了 hash")
	}
	if got != want {
		return fmt.Errorf("oneshot: 哈希不匹配：期望 %s 实际 %s", want, got)
	}
	return nil
}

func execute(ctx context.Context, m *manifest, dir string, args map[string]any) (content, stdout string, err error) {
	if m.hasRunAction() {
		return executeRunAction(ctx, m, dir, args)
	}
	return executeDirect(ctx, m, dir, args)
}

func executeRunAction(ctx context.Context, m *manifest, dir string, args map[string]any) (string, string, error) {
	p, err := startProc(ctx, m.Manifest, dir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = p.Close() }()

	raw, err := newRPC(p).call(ctx, "call", map[string]any{"action": "run", "arguments": args})
	if err != nil {
		return "", "", err
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("call_failed: result.content 非法")
	}
	return out.Content, "", nil
}

func executeDirect(ctx context.Context, m *manifest, dir string, args map[string]any) (string, string, error) {
	p, err := startProc(ctx, m.Manifest, dir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = p.Close() }()

	line, err := json.Marshal(args)
	if err != nil {
		return "", "", err
	}
	line = append(line, '\n')
	if _, err := p.stdin.Write(line); err != nil {
		return "", "", err
	}
	_ = p.stdin.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(p.stdout, 64<<20)); err != nil {
		return "", "", err
	}
	if err := p.cmd.Wait(); err != nil {
		return buf.String(), buf.String(), err
	}
	return buf.String(), buf.String(), nil
}

// collect 写产物三形状并返回收集的文件相对路径列表（NX4）。
func collect(dir, content, stdout string) []string {
	outDir := filepath.Join(dir, "out")
	_ = os.MkdirAll(outDir, 0o700)
	if stdout != "" {
		_ = os.WriteFile(filepath.Join(outDir, "stdout.txt"), []byte(stdout), 0o600)
	}
	var files []string
	trimmed := strings.TrimSpace(content)
	if trimmed != "" {
		var obj map[string]any
		if json.Unmarshal([]byte(trimmed), &obj) == nil {
			_ = os.WriteFile(filepath.Join(outDir, "result.json"), []byte(content), 0o600)
			if rawFiles, ok := obj["files"].([]any); ok {
				filesDir := filepath.Join(outDir, "files")
				for _, f := range rawFiles {
					name, ok := f.(string)
					if !ok {
						continue
					}
					src := filepath.Join(dir, filepath.Clean(name))
					if !strings.HasPrefix(src, dir) {
						continue
					}
					if _, err := os.Stat(src); err != nil {
						continue
					}
					_ = os.MkdirAll(filesDir, 0o700)
					if copyFile(src, filepath.Join(filesDir, filepath.Base(name))) == nil {
						files = append(files, name)
					}
				}
			}
		}
	}
	return files
}

func skipRunArtifacts(rel string) bool {
	return rel == ".run.json" || strings.HasPrefix(rel, "out/") || rel == "out"
}

func writeRunRecord(dir, id, version, source, hash, status string, durationMS int64) {
	record := map[string]any{
		"id":          id,
		"version":     version,
		"source":      source,
		"hash":        hash,
		"status":      status,
		"started_at":  time.Now().UTC().Format(time.RFC3339),
		"duration_ms": durationMS,
	}
	data, _ := json.MarshalIndent(record, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, ".run.json"), append(data, '\n'), 0o600)
}

func cacheHit(dir, hash string) bool {
	if hash == "" {
		return false
	}
	meta, err := os.ReadFile(filepath.Join(dir, ".cache.json"))
	if err != nil {
		return false
	}
	var got struct {
		Hash string `json:"hash"`
	}
	if json.Unmarshal(meta, &got) != nil {
		return false
	}
	return strings.EqualFold(got.Hash, hash)
}

func writeCacheMeta(dir, hash string) error {
	data, _ := json.Marshal(map[string]string{"hash": hash})
	return os.WriteFile(filepath.Join(dir, ".cache.json"), append(data, '\n'), 0o600)
}
