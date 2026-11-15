package server

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"time"
)

//go:embed web
var webFiles embed.FS

// registerSPA serves /assets/<file> from the embedded web folder and the
// single-page app for every other GET outside /api, so deep links such as
// /123 load the app.
func (s *Server) registerSPA(mux *http.ServeMux) {
	mux.HandleFunc("GET /assets/{file}", serveAsset)
	mux.HandleFunc("/", serveSPA)
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	data, err := fs.ReadFile(webFiles, path.Join("web", name))
	if err != nil || name == "index.html" {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}
