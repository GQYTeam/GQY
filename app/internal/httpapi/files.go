// 包 httpapi：附件上传。文件只落到 $GQY_HOME/sessions/<id>/tmp/uploads，
// 文本附件抽出一段文本供模型使用；不执行、不解析二进制。
package httpapi

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"gqy/internal/session"
)

const (
	maxUploadBytes = 10 << 20
	maxTextRunes   = 20000
)

var textUploadExt = map[string]bool{
	".txt": true, ".md": true, ".json": true, ".jsonl": true, ".csv": true,
	".go": true, ".js": true, ".mjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".py": true, ".rb": true, ".java": true, ".rs": true, ".c": true, ".h": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true,
	".css": true, ".sql": true, ".sh": true, ".log": true, ".env": true,
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxUploadBytes + (1 << 20)); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_multipart", "上传格式不正确")
		return
	}
	sessionID := r.FormValue("session")
	if !session.IDRe.MatchString(sessionID) {
		writeErr(w, http.StatusBadRequest, "bad_session_id", "会话 id 不合法")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file_required", "缺少 file 字段")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_failed", "读取文件失败")
		return
	}
	if len(data) > maxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file_too_large", "单个文件上限 10 MB")
		return
	}
	name := safeUploadName(header.Filename)
	text, truncated := extractUploadText(name, data)
	dataURL := imageDataURL(name, data)
	rel, size, err := s.saveUpload(sessionID, name, data)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": name, "path": rel, "size": size,
		"text": text, "truncated": truncated, "dataUrl": dataURL,
	})
}

func (s *Server) saveUpload(sessionID, name string, data []byte) (string, int, error) {
	dir := filepath.Join(s.Cfg.Home, "sessions", sessionID, "tmp", "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	// 用 O_EXCL 逐个抢占文件名：并发上传同名文件不会互相覆盖，也不存在
	// “先 Stat 再 rename”的竞态。
	for i := 0; i < 1000; i++ {
		name := numberedUploadName(name, i)
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(path) // 不留半截文件
			if werr != nil {
				return "", 0, werr
			}
			return "", 0, cerr
		}
		rel := filepath.ToSlash(filepath.Join("sessions", sessionID, "tmp", "uploads", name))
		return rel, len(data), nil
	}
	return "", 0, errors.New("同名文件过多，请重命名后重试")
}

func safeUploadName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if out == "" {
		out = "upload"
	}
	if len([]rune(out)) > 120 {
		out = string([]rune(out)[:120])
	}
	return out
}

// numberedUploadName 返回第 i 个候选名：a.txt → a-1.txt、a-2.txt…
func numberedUploadName(name string, i int) string {
	if i == 0 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(i) + ext
}

func imageDataURL(name string, data []byte) string {
	const limit = 2 << 20
	if len(data) > limit {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(name))
	var mime string
	switch ext {
	case ".png":
		mime = "image/png"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	default:
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func extractUploadText(name string, data []byte) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if !textUploadExt[ext] || !utf8.Valid(data) {
		return "", false
	}
	runes := []rune(string(data))
	if len(runes) > maxTextRunes {
		return string(runes[:maxTextRunes]), true
	}
	return string(runes), false
}
