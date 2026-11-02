package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveUpload 把附件写入 tmp/uploads；同名不覆盖（O_EXCL，S3）。
// 文件名只取 basename，拒绝空名与穿越。
func SaveUpload(uploadsDir, name string, data []byte) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("session: 附件名非法: %q", name)
	}
	if err := os.MkdirAll(uploadsDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(uploadsDir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- 路径来自上传目录
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("session: 附件已存在: %s", name)
		}
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return path, nil
}
