package company

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type clientExtension struct {
	Plugin     string         `json:"plugin"`
	Javascript string         `json:"javascript"`
	Config     map[string]any `json:"config"`
}

func pluginFileURL(plugin, path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "/plugins/" + url.PathEscape(plugin) + "/" + strings.Join(parts, "/")
}

func (s *Server) extensionList(w http.ResponseWriter, _ *http.Request) {
	loaded := s.plugins.CompanyExtensions()
	result := make([]clientExtension, 0, len(loaded))
	for _, extension := range loaded {
		result = append(result, clientExtension{
			Plugin: extension.Plugin, Javascript: pluginFileURL(extension.Plugin, extension.Javascript), Config: extension.Config,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) pluginFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.plugins.CompanyFile(r.PathValue("plugin"), r.PathValue("path"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !serveCompanyFile(w, r, path) {
		http.NotFound(w, r)
	}
}

func serveCompanyFile(w http.ResponseWriter, r *http.Request, path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	return true
}
