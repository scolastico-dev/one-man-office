package server

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// Plugin files contain public bundled code/assets only; configuration and data
// remain behind the user-only API. Handle the raw path before ServeMux cleaning.
func (s *Server) servePluginAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/plugin-assets/"), "/", 3)
	if len(parts) != 3 || s.plugins.m == nil {
		http.NotFound(w, r)
		return
	}
	data, err := s.plugins.m.FrontendFile(parts[0], parts[1], parts[2])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := path.Ext(parts[2])
	ct := contentTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, path.Base(parts[2]), time.Time{}, bytes.NewReader(data))
}
