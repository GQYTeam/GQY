package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxInstallBytes = 128 << 20

// Install 从 https Git 仓库安装插件：clone 到临时目录 → 校验 manifest → 复制到 plugins/<id> → 打安装标记 → 扫描。
// 安装 = 信任；插件目录只读，设置存 plugin-settings（PL2/PL3）。
func (m *Manager) Install(ctx context.Context, source, id string) error {
	source = strings.TrimSpace(source)
	id = strings.TrimSpace(id)
	if !ValidID(id) {
		return fmt.Errorf("plugin: 非法插件 id %q", id)
	}
	if !strings.HasPrefix(source, "https://") {
		return fmt.Errorf("plugin: 安装地址必须是 https Git 仓库")
	}
	target := filepath.Join(m.pluginsDir(), id)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("plugin: %s 已存在", id)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("plugin: 需要 git 才能安装: %w", err)
	}
	if err := os.MkdirAll(m.home, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(m.home, ".gqy-install-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cloneDir := filepath.Join(tmp, "repo")
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", source, cloneDir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("plugin: git clone 失败: %w: %s", err, msg)
	}
	return m.installFromDir(cloneDir, id)
}

// installFromDir 校验并复制一个已下载的插件目录。
func (m *Manager) installFromDir(src, id string) error {
	data, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		return fmt.Errorf("plugin: 读取 manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return err
	}
	if manifest.ID != id {
		return fmt.Errorf("plugin: manifest id %q 与目标 %q 不一致", manifest.ID, id)
	}
	if !ValidID(manifest.ID) {
		return fmt.Errorf("plugin: 非法插件 id")
	}

	root := m.pluginsDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	target := filepath.Join(root, id)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("plugin: %s 已存在", id)
	}
	tmp := target + ".installing"
	_ = os.RemoveAll(tmp)
	if err := copyPluginTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, InstalledMarker), []byte("registry\n"), 0o600); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return m.Scan()
}

func copyPluginTree(src, dst string) error {
	var total int64
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("plugin: 安装目录越界")
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o750)
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin: 安装拒绝符号链接 %s", rel)
		}
		total += info.Size()
		if total > maxInstallBytes {
			return fmt.Errorf("plugin: 安装包超过 %d MB", maxInstallBytes>>20)
		}
		in, err := os.Open(path) // #nosec G122 -- 拒绝符号链接，路径来自刚 clone 的临时目录
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(filepath.Join(dst, rel), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
