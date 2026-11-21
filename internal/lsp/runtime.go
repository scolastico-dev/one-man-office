package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/processtree"
)

type worktree struct {
	manager *Manager
	root    string
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	scans   chan chan error
	issues  *issues
	servers map[string]*languageServer
}

func (r *worktree) run() {
	defer close(r.done)
	defer r.issues.close()
	defer func() {
		for _, s := range r.servers {
			s.stop()
		}
	}()
	ticker := time.NewTicker(r.manager.o.PollInterval)
	defer ticker.Stop()
	r.scan()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.scan()
		case reply := <-r.scans:
			reply <- r.scan()
		}
	}
}
func (r *worktree) scan() error {
	cfg := r.manager.settings()
	r.issues.mu.Lock()
	r.issues.debounce = time.Duration(cfg.Debounce)
	r.issues.mu.Unlock()
	enabled := map[string]api.LSPServer{}
	for _, reg := range cfg.Servers {
		if reg.Enabled {
			enabled[reg.Name] = reg
		}
	}
	for name, s := range r.servers {
		reg, ok := enabled[name]
		if !ok || !reflect.DeepEqual(reg, s.reg) {
			s.stop()
			r.issues.clearServer(name)
			delete(r.servers, name)
		}
	}
	var errs []error
	for _, reg := range cfg.Servers {
		if !reg.Enabled {
			continue
		}
		docs, err := scanFiles(r.ctx, r.root, reg)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s := r.servers[reg.Name]
		if s == nil && len(docs) > 0 {
			s = &languageServer{runtime: r, reg: reg, docs: map[string]document{}, seen: map[string]bool{}, initial: map[string]bool{}, resultIDs: map[string]string{}, refresh: make(chan struct{}, 1), watchers: map[string][]fileWatcher{}, watched: map[string]string{}, watchBaselines: map[string]map[string]string{}}
			r.servers[reg.Name] = s
			s.err = s.start()
			if s.err != nil {
				r.manager.o.Log.Printf("lsp: %s in %s: %v", reg.Name, r.root, s.err)
			}
		}
		if s == nil {
			continue
		}
		if s.err != nil {
			errs = append(errs, s.err)
			continue
		}
		s.clearMissing()
		if err := s.sync(docs); err != nil {
			var disk *diskError
			if errors.As(err, &disk) {
				errs = append(errs, err)
				continue
			}
			s.err = err
			r.issues.clearServer(reg.Name)
			errs = append(errs, err)
			s.stop()
			r.manager.o.Log.Printf("lsp: %s in %s: %v", reg.Name, r.root, err)
		}
	}
	return errors.Join(errs...)
}
func (s *issues) clearServer(server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, set := range s.current {
		if strings.HasPrefix(key, server+"\x00") {
			for id := range set {
				delete(s.pending, id)
			}
			delete(s.current, key)
		}
	}
}

type syncOptions struct {
	OpenClose bool            `json:"openClose"`
	Change    int             `json:"change"`
	Save      json.RawMessage `json:"save"`
}
type diagnosticOptions struct {
	Identifier           string `json:"identifier"`
	WorkspaceDiagnostics bool   `json:"workspaceDiagnostics"`
}
type languageServer struct {
	runtime     *worktree
	reg         api.LSPServer
	peer        *peer
	group       *processtree.Group
	cancel      context.CancelFunc
	waited      chan struct{}
	err         error
	syncOptions syncOptions
	pull        *diagnosticOptions
	refresh     chan struct{}
	// mu orders document versions/baselines against the protocol reader's push
	// reports. Disk synchronization itself runs only on the worktree worker.
	mu             sync.Mutex
	docs           map[string]document
	seen           map[string]bool
	initial        map[string]bool
	resultIDs      map[string]string
	initialized    bool
	changed        bool
	watchers       map[string][]fileWatcher
	watchBaselines map[string]map[string]string
	watched        map[string]string
}

func (s *languageServer) start() error {
	ctx, cancel := context.WithCancel(s.runtime.ctx)
	s.cancel = cancel
	cmd := exec.CommandContext(ctx, s.reg.Command, s.reg.Args...)
	cmd.Dir = s.runtime.root
	env := childenv.Inherited()
	for k, v := range s.reg.Env {
		for i := len(env) - 1; i >= 0; i-- {
			name, _, _ := strings.Cut(env[i], "=")
			if strings.EqualFold(name, k) {
				env = append(env[:i], env[i+1:]...)
			}
		}
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	cmd.Stderr = io.Discard
	group, err := processtree.New(cmd)
	if err != nil {
		cancel()
		return err
	}
	s.group = group
	in, err := cmd.StdoutPipe()
	if err != nil {
		s.stop()
		return err
	}
	out, err := cmd.StdinPipe()
	if err != nil {
		_ = in.Close()
		s.stop()
		return err
	}
	if err = group.Start(cmd); err != nil {
		_ = in.Close()
		_ = out.Close()
		s.stop()
		return err
	}
	if err = group.Started(cmd); err != nil {
		_ = group.Kill()
		_ = cmd.Wait()
		_ = in.Close()
		_ = out.Close()
		s.stop()
		return err
	}
	s.waited = make(chan struct{})
	go func() { _ = cmd.Wait(); group.Close(); close(s.waited) }()
	s.peer = newPeer(in, out, s.handle)
	var result struct {
		Capabilities struct {
			TextDocumentSync   json.RawMessage `json:"textDocumentSync"`
			DiagnosticProvider json.RawMessage `json:"diagnosticProvider"`
		} `json:"capabilities"`
	}
	params := map[string]any{
		"processId": nil, "clientInfo": map[string]any{"name": "omo"}, "rootUri": fileURI(s.runtime.root), "rootPath": s.runtime.root,
		"workspaceFolders":      []any{map[string]any{"uri": fileURI(s.runtime.root), "name": filepath.Base(s.runtime.root)}},
		"initializationOptions": s.reg.InitializationOptions,
		"capabilities": map[string]any{
			"general":      map[string]any{"positionEncodings": []string{"utf-16"}},
			"workspace":    map[string]any{"configuration": true, "workspaceFolders": true, "diagnostics": map[string]any{"refreshSupport": true}, "didChangeWatchedFiles": map[string]any{"dynamicRegistration": true}},
			"textDocument": map[string]any{"synchronization": map[string]any{"didSave": true}, "diagnostic": map[string]any{"relatedDocumentSupport": true}, "publishDiagnostics": map[string]any{"versionSupport": true}},
		},
	}
	if err = s.peer.call(ctx, "initialize", params, &result); err != nil {
		s.stop()
		return err
	}
	raw := result.Capabilities.TextDocumentSync
	var kind int
	if json.Unmarshal(raw, &kind) == nil {
		s.syncOptions = syncOptions{OpenClose: kind != 0, Change: kind}
	} else {
		_ = json.Unmarshal(raw, &s.syncOptions)
	}
	raw = result.Capabilities.DiagnosticProvider
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "false" {
		s.pull = &diagnosticOptions{}
		_ = json.Unmarshal(raw, s.pull)
	}
	if err = s.peer.notify("initialized", map[string]any{}); err != nil {
		s.stop()
		return err
	}
	if err = s.peer.notify("workspace/didChangeConfiguration", map[string]any{"settings": s.reg.Configuration}); err != nil {
		s.stop()
		return err
	}
	return nil
}
func (s *languageServer) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.group != nil {
		_ = s.group.Kill()
	}
	if s.peer != nil {
		s.peer.close()
	}
	if s.waited != nil {
		<-s.waited
	}
	if s.group != nil {
		s.group.Close()
	}
}
func (s *languageServer) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         string       `json:"uri"`
			Version     *int         `json:"version"`
			Diagnostics []diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		s.accept(p.URI, p.Version, p.Diagnostics)
		return nil, nil
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		out := []any{}
		for _, item := range p.Items {
			var value any = s.reg.Configuration
			for _, key := range strings.Split(item.Section, ".") {
				if key == "" {
					continue
				}
				obj, ok := value.(map[string]any)
				if !ok {
					value = nil
					break
				}
				value = obj[key]
			}
			out = append(out, value)
		}
		return out, nil
	case "workspace/workspaceFolders":
		return []any{map[string]any{"uri": fileURI(s.runtime.root), "name": filepath.Base(s.runtime.root)}}, nil
	case "workspace/diagnostic/refresh":
		select {
		case s.refresh <- struct{}{}:
		default:
		}
		return nil, nil
	case "client/registerCapability":
		var p struct {
			Registrations []struct {
				ID              string `json:"id"`
				Method          string `json:"method"`
				RegisterOptions struct {
					Watchers []fileWatcher `json:"watchers"`
				} `json:"registerOptions"`
			} `json:"registrations"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		for _, r := range p.Registrations {
			if r.Method != "workspace/didChangeWatchedFiles" {
				continue
			}
			baseline, err := s.watchSnapshot(map[string][]fileWatcher{r.ID: r.RegisterOptions.Watchers})
			if err != nil {
				return nil, err
			}
			s.mu.Lock()
			s.watchers[r.ID] = r.RegisterOptions.Watchers
			s.watchBaselines[r.ID] = baseline
			s.mu.Unlock()
		}
		return nil, nil
	case "client/unregisterCapability":
		var p struct {
			Unregistrations []struct {
				ID string `json:"id"`
			} `json:"unregisterations"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		s.mu.Lock()
		for _, r := range p.Unregistrations {
			delete(s.watchers, r.ID)
			delete(s.watchBaselines, r.ID)
		}
		s.mu.Unlock()
		return nil, nil
	case "window/workDoneProgress/create", "$/progress", "window/logMessage", "window/showMessage", "telemetry/event":
		return nil, nil
	default:
		return nil, &rpcError{-32601, "method not supported"}
	}
}
func (s *languageServer) accept(uri string, version *int, ds []diagnostic) {
	file, ok := relativeFile(s.runtime.root, uri)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, exists := s.docs[file]
	if !exists {
		info, err := os.Lstat(filepath.Join(s.runtime.root, filepath.FromSlash(file)))
		if err != nil || !info.Mode().IsRegular() {
			s.runtime.issues.update(s.reg.Name, file, nil, true)
			return
		}
	}
	if version != nil && *version < d.version {
		return
	}
	baseline := !s.changed || (s.initial[file] && !s.seen[file])
	s.seen[file] = true
	s.runtime.issues.update(s.reg.Name, file, ds, baseline)
}
func (s *languageServer) sync(next map[string]document) error {
	select {
	case <-s.peer.done:
		return errors.New("language server exited")
	default:
	}
	s.mu.Lock()
	previous := s.docs
	s.docs = map[string]document{}
	for file, d := range next {
		old, exists := previous[file]
		d.version = old.version
		if !exists || old.text != d.text {
			if s.initialized {
				s.initial[file] = false
				s.changed = true
			}
			d.version++
		}
		s.docs[file] = d
		if !exists {
			s.initial[file] = !s.initialized
			s.seen[file] = false
		}
	}
	if s.initialized && len(previous) != len(next) {
		s.changed = true
	}
	s.mu.Unlock()
	files := make([]string, 0, len(next))
	for f := range next {
		files = append(files, f)
	}
	sort.Strings(files)
	changed := !s.initialized
	for _, file := range files {
		d := next[file]
		old, exists := previous[file]
		uri := fileURI(filepath.Join(s.runtime.root, filepath.FromSlash(file)))
		s.mu.Lock()
		version := s.docs[file].version
		s.mu.Unlock()
		doc := map[string]any{"uri": uri, "version": version}
		if !exists {
			changed = true
			if s.syncOptions.OpenClose {
				if err := s.peer.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": d.language, "version": version, "text": d.text}}); err != nil {
					return err
				}
			}
		} else if old.text != d.text {
			changed = true
			if s.syncOptions.Change != 0 {
				change := map[string]any{"text": d.text}
				if s.syncOptions.Change == 2 {
					change["range"] = Range{End: endPosition(old.text)}
				}
				if err := s.peer.notify("textDocument/didChange", map[string]any{"textDocument": doc, "contentChanges": []any{change}}); err != nil {
					return err
				}
			}
		} else {
			continue
		}
		save := s.syncOptions.Save
		if len(save) > 0 && string(save) != "false" && string(save) != "null" {
			params := map[string]any{"textDocument": map[string]any{"uri": uri}}
			var option struct {
				IncludeText bool `json:"includeText"`
			}
			_ = json.Unmarshal(save, &option)
			if option.IncludeText {
				params["text"] = d.text
			}
			if err := s.peer.notify("textDocument/didSave", params); err != nil {
				return err
			}
		}
	}
	for file := range previous {
		if _, exists := next[file]; !exists {
			uri := fileURI(filepath.Join(s.runtime.root, filepath.FromSlash(file)))
			changed = true
			s.runtime.issues.update(s.reg.Name, file, nil, true)
			delete(s.resultIDs, uri)
			if s.syncOptions.OpenClose {
				if err := s.peer.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}); err != nil {
					return err
				}
			}
		}
	}
	s.mu.Lock()
	if s.initialized && changed {
		s.changed = true
	}
	s.mu.Unlock()
	watchedChanged, watchErr := s.syncWatched()
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()
	select {
	case <-s.refresh:
		changed = true
	default:
	}
	if (changed || watchedChanged) && s.pull != nil {
		if err := s.diagnostics(); err != nil {
			return err
		}
	}
	return watchErr
}

type documentReport struct {
	Kind             string                    `json:"kind"`
	ResultID         string                    `json:"resultId"`
	Items            []diagnostic              `json:"items"`
	RelatedDocuments map[string]documentReport `json:"relatedDocuments"`
}

func (s *languageServer) applyPull(uri string, version *int, report documentReport) {
	if report.Kind == "full" {
		s.accept(uri, version, report.Items)
	}
	if report.ResultID != "" {
		s.resultIDs[uri] = report.ResultID
	}
	for related, r := range report.RelatedDocuments {
		s.applyPull(related, nil, r)
	}
}
func (s *languageServer) diagnostics() error {
	if s.pull.WorkspaceDiagnostics {
		previous := []any{}
		for uri, id := range s.resultIDs {
			previous = append(previous, map[string]any{"uri": uri, "value": id})
		}
		var result struct {
			Items []struct {
				URI     string `json:"uri"`
				Version *int   `json:"version"`
				documentReport
			} `json:"items"`
		}
		if err := s.peer.call(s.runtime.ctx, "workspace/diagnostic", map[string]any{"identifier": s.pull.Identifier, "previousResultIds": previous}, &result); err != nil {
			return fmt.Errorf("workspace diagnostics: %w", err)
		}
		for _, item := range result.Items {
			s.applyPull(item.URI, item.Version, item.documentReport)
		}
	}
	s.mu.Lock()
	files := make([]string, 0, len(s.docs))
	for file := range s.docs {
		files = append(files, file)
	}
	s.mu.Unlock()
	sort.Strings(files)
	for _, file := range files {
		uri := fileURI(filepath.Join(s.runtime.root, filepath.FromSlash(file)))
		params := map[string]any{"textDocument": map[string]any{"uri": uri}, "identifier": s.pull.Identifier}
		if id := s.resultIDs[uri]; id != "" {
			params["previousResultId"] = id
		}
		var report documentReport
		if err := s.peer.call(s.runtime.ctx, "textDocument/diagnostic", params, &report); err != nil {
			return fmt.Errorf("document diagnostics: %w", err)
		}
		s.applyPull(uri, nil, report)
	}
	return nil
}

// Related/workspace reports may cover files that have no open document. A
// removed dependency must disappear from snapshots and pending batches even
// when its server does not send a clearing report.
func (s *languageServer) clearMissing() {
	checked := map[string]bool{}
	for _, i := range s.runtime.issues.snapshot() {
		if i.Server != s.reg.Name || checked[i.File] {
			continue
		}
		checked[i.File] = true
		info, err := os.Lstat(filepath.Join(s.runtime.root, filepath.FromSlash(i.File)))
		if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
			s.runtime.issues.update(s.reg.Name, i.File, nil, true)
		}
	}
}
