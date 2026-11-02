// Package storage 负责数据根布局与文件的原子写。
//
// 设计依据：docs/designs/09-存储.md（ST1–ST6）。
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureRoot 创建数据根及固定子目录，并把权限收紧到 0700（ST1、ST6）。
func EnsureRoot(home string) error {
	if home == "" {
		return errors.New("storage: 数据根为空")
	}
	dirs := []string{
		home,
		filepath.Join(home, "gateway"),
		filepath.Join(home, "sessions"),
		filepath.Join(home, "plugins"),
		filepath.Join(home, "memory"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("storage: 创建 %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- 数据根目录按 ST6 使用 0700
			return fmt.Errorf("storage: 设置权限 %s: %w", dir, err)
		}
	}
	return nil
}

// AtomicWriteFile 写临时文件、落盘后 rename，任何时刻读到的都是完整旧版或完整新版（ST4）。
func AtomicWriteFile(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("storage: 创建目录 %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".gqy-tmp-*")
	if err != nil {
		return fmt.Errorf("storage: 创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("storage: 写临时文件: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("storage: 落盘: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("storage: 关闭临时文件: %w", err)
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("storage: 设置权限: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("storage: 替换 %s: %w", path, err)
	}
	return nil
}

// AtomicWriteJSON 用缩进 JSON 写文件，便于人工查看与 diff。
func AtomicWriteJSON(path string, value any, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: 序列化 JSON: %w", err)
	}
	data = append(data, '\n')
	return AtomicWriteFile(path, data, perm)
}
