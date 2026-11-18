package server

import (
	"bytes"
	_ "embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"time"

	"github.com/scolastico-dev/one-man-office/web"
)

//go:embed notbuilt.html
var notBuiltPage []byte

// appFiles returns the built browser app; tests replace it.
var appFiles = web.Files

// uiBuilt reports whether the browser app is part of this binary.
func uiBuilt() bool {
	_, ok := appFiles()
	return ok
}

// assetCacheControl marks content-hashed files as safe to keep forever.
const assetCacheControl = "public, max-age=31536000, immutable"

// registerSPA serves /assets/<path> from the built app and the app's
// index.html for every other GET or HEAD outside /api, so deep links such as
// /123 load the app.
func (s *Server) registerSPA(mux *http.ServeMux) {
	mux.HandleFunc("/plugin-assets/", s.servePluginAsset)
	mux.HandleFunc("GET /assets/{path...}", serveAsset)
	mux.HandleFunc("/", serveSPA)
}

// contentTypes covers files the system's MIME table may not know.
var contentTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".txt":   "text/plain; charset=utf-8",
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	files, ok := appFiles()
	// fs.ValidPath refuses ".." and empty elements, so an encoded "../"
	// cannot leave the assets folder.
	name := "assets/" + r.PathValue("path")
	if !ok || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	if info, err := fs.Stat(files, name); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(files, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := path.Ext(name)
	ct, known := contentTypes[ext]
	if !known {
		ct = mime.TypeByExtension(ext)
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", assetCacheControl)
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page := notBuiltPage
	if files, ok := appFiles(); ok {
		data, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "page missing", http.StatusInternalServerError)
			return
		}
		page = data
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
}
