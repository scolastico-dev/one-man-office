package company

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) serveOverlay(w http.ResponseWriter, r *http.Request) bool {
	if s.httpRoot == "" {
		return false
	}
	if overlayHasDotDot(r) {
		http.NotFound(w, r)
		return true
	}
	relative := strings.TrimPrefix(strings.ReplaceAll(r.URL.Path, "\\", "/"), "/")
	if relative == "" {
		relative = "index.html"
	}
	if strings.HasPrefix(relative, "/") {
		return false
	}
	path := filepath.Join(s.httpRoot, filepath.FromSlash(strings.ReplaceAll(relative, "\\", "/")))
	file, err := os.Open(path)
	if err != nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			http.NotFound(w, r)
			return true
		}
		return false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err == nil {
			http.NotFound(w, r)
			return true
		}
		return false
	}
	defer file.Close()
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	return true
}

func overlayHasDotDot(r *http.Request) bool {
	rawRequestPath := r.RequestURI
	if query := strings.IndexByte(rawRequestPath, '?'); query >= 0 {
		rawRequestPath = rawRequestPath[:query]
	}
	paths := []string{r.URL.Path, r.URL.EscapedPath(), rawRequestPath}
	for _, candidate := range paths {
		candidate = strings.ReplaceAll(candidate, "\\", "/")
		for _, segment := range strings.Split(candidate, "/") {
			if segment == ".." {
				return true
			}
			decoded, err := url.PathUnescape(segment)
			if err != nil || decoded == ".." {
				return true
			}
		}
	}
	return false
}
