package oneshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pull 把来源内容落到 staging，返回内容哈希（NX2）。
// 本地路径无哈希（本地可信）；git 返回 commit SHA；url / 注册表返回 sha256。
func pull(ctx context.Context, home string, spec sourceSpec, staging string) (string, error) {
	switch spec.kind {
	case srcLocal:
		return "", copyTree(spec.url, staging, nil)
	case srcGit:
		return pullGit(ctx, spec.url, spec.ref, staging)
	case srcURL:
		return pullURL(ctx, spec.url, staging)
	case srcRegistry:
		return pullRegistry(ctx, home, spec, staging)
	default:
		return "", fmt.Errorf("oneshot: 未知来源类型")
	}
}

func pullGit(ctx context.Context, url, ref, staging string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("oneshot: 系统未安装 git，无法拉取 git 来源")
	}
	clone := exec.CommandContext(ctx, "git", "clone", "--quiet", url, staging)
	if out, err := clone.CombinedOutput(); err != nil {
		return "", fmt.Errorf("oneshot: git clone 失败: %s", strings.TrimSpace(string(out)))
	}
	if ref != "" {
		checkout := exec.CommandContext(ctx, "git", "-C", staging, "checkout", "--quiet", ref)
		if out, err := checkout.CombinedOutput(); err != nil {
			return "", fmt.Errorf("oneshot: git checkout %s 失败: %s", ref, strings.TrimSpace(string(out)))
		}
	}
	rev := exec.CommandContext(ctx, "git", "-C", staging, "rev-parse", "HEAD")
	out, err := rev.Output()
	if err != nil {
		return "", fmt.Errorf("oneshot: git rev-parse 失败: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// registry 是内置注册表文件 oneshot-registry.json 的形状（NX2 第四级）。
type registry struct {
	Entries []registryEntry `json:"entries"`
}

type registryEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

func pullRegistry(ctx context.Context, home string, spec sourceSpec, staging string) (string, error) {
	data, err := os.ReadFile(filepath.Join(home, "oneshot-registry.json"))
	if err != nil {
		return "", fmt.Errorf("oneshot: 注册表未配置（%s）", filepath.Join(home, "oneshot-registry.json"))
	}
	var reg registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return "", fmt.Errorf("oneshot: 注册表解析失败: %w", err)
	}
	for _, e := range reg.Entries {
		if e.Name != spec.registryName {
			continue
		}
		if spec.registryVersion != "" && e.Version != spec.registryVersion {
			continue
		}
		hash, err := pullURL(ctx, e.URL, staging)
		if err != nil {
			return "", err
		}
		want := strings.TrimPrefix(strings.ToLower(e.SHA256), "sha256:")
		if want == "" {
			return hash, nil
		}
		if hash != want {
			return "", fmt.Errorf("oneshot: 注册表哈希不匹配：期望 %s 实际 %s", want, hash)
		}
		return hash, nil
	}
	return "", fmt.Errorf("oneshot: 注册表无条目 %s", spec.raw)
}

func pullURL(ctx context.Context, rawURL, staging string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("oneshot: 构造下载请求失败: %w", err)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oneshot: 下载失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oneshot: 下载返回 %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", fmt.Errorf("oneshot: 读下载内容失败: %w", err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	lower := strings.ToLower(rawURL)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return hash, extractTarball(data, true, staging)
	case strings.HasSuffix(lower, ".tar"):
		return hash, extractTarball(data, false, staging)
	default:
		return "", errors.New("oneshot: url 源仅支持 .tar.gz / .tgz / .tar 包")
	}
}

func extractTarball(data []byte, gzipped bool, dst string) error {
	var r io.Reader = bytes.NewReader(data)
	if gzipped {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("oneshot: gzip 打开失败: %w", err)
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("oneshot: 解包失败: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		target := filepath.Join(dst, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(target, dst) {
			return errors.New("oneshot: tarball 内含越界路径")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
}

// copyTree 递归复制目录；skip 非空时跳过命中的相对路径。
func copyTree(src, dst string, skip func(rel string) bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("oneshot: 读取来源失败: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("oneshot: 来源不是目录: %s", src)
	}
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
