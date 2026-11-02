package httpapi

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gqy/internal/storage"
)

var staticExtensions = map[string]bool{
	".html":        true,
	".css":         true,
	".js":          true,
	".mjs":         true,
	".svg":         true,
	".png":         true,
	".jpg":         true,
	".jpeg":        true,
	".webp":        true,
	".ico":         true,
	".woff2":       true,
	".webmanifest": true,
}

// SetWebDir 设置前端静态目录；空值表示不提供 UI。
func (s *Server) SetWebDir(dir string) {
	s.webDir = dir
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.webDir == "" {
		http.NotFound(w, r)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	if clean == "/" {
		clean = "/index.html"
	}
	ext := strings.ToLower(path.Ext(clean))
	if !staticExtensions[ext] {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.webDir, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if err := storage.EnsureInside(s.webDir, full); err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	if ext == ".webmanifest" {
		w.Header().Set("Content-Type", "application/manifest+json")
	}
	http.ServeFile(w, r, full)
}
