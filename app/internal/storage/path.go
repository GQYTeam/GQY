package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureInside 校验 target 规范化并解析符号链接后仍在 root 内（S7、ST7）。
// target 可以尚不存在；不存在的尾部会挂在已存在祖先的真实路径后。
func EnsureInside(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("storage: 解析根目录: %w", err)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("storage: 解析目标路径: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("storage: 解析根目录符号链接: %w", err)
	}
	targetReal, err := resolveExistingPrefix(targetAbs)
	if err != nil {
		return fmt.Errorf("storage: 解析目标符号链接: %w", err)
	}

	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return fmt.Errorf("storage: 计算相对路径: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("storage: 路径越界: %s", target)
	}
	return nil
}

// resolveExistingPrefix 找到 path 已存在的最近祖先做 EvalSymlinks，再拼回尾部。
func resolveExistingPrefix(path string) (string, error) {
	path = filepath.Clean(path)
	suffix := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		suffix = append(suffix, filepath.Base(path))
		path = parent
	}
}
