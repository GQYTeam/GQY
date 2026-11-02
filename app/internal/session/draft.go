package session

import (
	"errors"
	"os"

	"gqy/internal/storage"
)

// SaveDraft 按会话保存草稿（原子写，S4）。
func SaveDraft(path, content string) error {
	return storage.AtomicWriteFile(path, []byte(content), 0o600)
}

// LoadDraft 读取草稿；不存在返回空串。
func LoadDraft(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- 路径来自会话目录
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
