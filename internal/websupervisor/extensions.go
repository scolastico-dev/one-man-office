package websupervisor

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type clientExtension struct {
	Plugin     string            `json:"plugin"`
	Javascript string            `json:"javascript"`
	Files      map[string]string `json:"files"`
}

func pluginFileURL(plugin, path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "/plugins/" + url.PathEscape(plugin) + "/files/" + strings.Join(parts, "/")
}

func (s *Server) extensionList(w http.ResponseWriter, _ *http.Request) {
	loaded := s.plugins.SupervisorExtensions()
	result := make([]clientExtension, 0, len(loaded))
	for _, extension := range loaded {
		files := make(map[string]string, len(extension.Files))
		for _, path := range extension.Files {
			files[path] = pluginFileURL(extension.Plugin, path)
		}
		result = append(result, clientExtension{
			Plugin: extension.Plugin, Javascript: pluginFileURL(extension.Plugin, extension.Javascript), Files: files,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) pluginFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.plugins.SupervisorFile(r.PathValue("plugin"), r.PathValue("path"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}
