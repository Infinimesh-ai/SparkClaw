package localwebchat

import (
	"net/http"
	"os"
	"path"
	"strings"
)

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	filename := strings.TrimPrefix(r.URL.Path, "/")
	if filename == "" {
		filename = "index.html"
	}
	if path.Clean(filename) != filename || strings.Contains(filename, "\\") {
		http.NotFound(w, r)
		return
	}
	for _, part := range strings.Split(filename, "/") {
		if strings.HasPrefix(part, ".") {
			http.NotFound(w, r)
			return
		}
	}
	extension := strings.ToLower(path.Ext(filename))
	switch extension {
	case "":
		filename = "index.html" // Browser routes use the same public shell.
	case ".html", ".js", ".css", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".woff", ".woff2", ".ttf", ".otf", ".webmanifest":
	default:
		http.NotFound(w, r)
		return
	}
	// Root confines traversal even if files change during the request. Lstat
	// rejects symlinks inside the public tree as well as links escaping it.
	parts := strings.Split(filename, "/")
	var info os.FileInfo
	for i := range parts {
		var err error
		info, err = s.assets.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	file, err := s.assets.Open(filename)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		http.NotFound(w, r)
		return
	}
	if strings.EqualFold(path.Ext(filename), ".html") {
		w.Header().Set("X-SparkClaw-Local-Access", "1")
	}
	http.ServeContent(w, r, filename, info.ModTime(), file)
}
