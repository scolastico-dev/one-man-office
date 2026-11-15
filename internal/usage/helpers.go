package usage

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/codexauth"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Bounds of the helpers.
const (
	// MaxFile is the largest file omo.read_file reads.
	MaxFile = 1 << 20
	// MaxFileReads is how many files one run may read.
	MaxFileReads = 16
	// MaxKeychainSecret is the largest keychain entry omo.keychain returns.
	MaxKeychainSecret = 64 << 10
	// KeychainTimeout bounds one keychain lookup.
	KeychainTimeout = 5 * time.Second
	// CodexRefreshTimeout bounds omo.codex_refresh; it counts against the
	// run's time.
	CodexRefreshTimeout = 45 * time.Second
	maxServiceRunes     = 200
)

// Fixed phrases of the helpers. A path, a value or a file's content is
// never put into one.
const (
	phrasePath          = "path must be absolute or start with ~/"
	phraseNotFound      = "file not found"
	phraseLink          = "file is a link"
	phraseNotRegular    = "file is not a regular file"
	phraseTooLarge      = "file exceeds 1048576 bytes"
	phraseUnreadable    = "file could not be read"
	phraseTooManyReads  = "too many file reads"
	phraseEnvFailed     = "environment could not be expanded"
	phraseNotAvailable  = "not available"
	phraseServiceName   = "keychain service name not allowed"
	phraseNoEntry       = "keychain entry not found"
	phraseNotCodex      = "codex_refresh is only available for codex profiles"
	phraseRefreshOnce   = "codex_refresh runs at most once per run"
	phraseRefreshFailed = "codex refresh failed"
	phraseUnchanged     = "codex did not refresh its credentials"
	phraseRefreshTime   = "codex refresh timed out"
)

// securityPath is the macOS keychain tool. Tests point it at a fake.
var securityPath = "/usr/bin/security"

// errNotAvailable is the keychain's answer on systems without one.
var errNotAvailable = errors.New(phraseNotAvailable)

// helpers are the usage helpers of one run.
type helpers struct {
	box *luabox.Box
	job Job
	o   RunOptions

	reads     int
	refreshed bool

	envDone bool
	envErr  error
	layered []string          // the expanded agent, account and profile layers
	full    map[string]string // omo's environment overlaid with layered
}

func newHelpers(box *luabox.Box, j Job, o RunOptions) *helpers {
	return &helpers{box: box, job: j, o: o}
}

// register adds omo.http, read_file, env, keychain and codex_refresh.
func (h *helpers) register() {
	web := &luabox.HTTP{Client: h.o.HTTPClient}
	h.box.Set("http", web.Func(h.box))
	h.box.Set("read_file", h.readFile)
	h.box.Set("env", h.env)
	h.box.Set("keychain", h.keychain)
	h.box.Set("codex_refresh", h.codexRefresh)
}

// runHelpers are the helpers that reach outside the box.
var runHelpers = []string{"http", "read_file", "env", "keychain", "codex_refresh"}

// account is the argument of usage(account).
func (h *helpers) account() map[string]any {
	return map[string]any{
		"profile":     h.job.Profile.Name,
		"cli":         h.job.Profile.CLI,
		"account_dir": h.job.Profile.AccountDir,
		"home":        h.o.Home,
		"os":          goos,
	}
}

func fail(L *lua.LState, phrase string) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(phrase))
	return 2
}

// expandPath turns an absolute or ~/ path into a clean absolute path.
func (h *helpers) expandPath(p string) (string, bool) {
	if strings.ContainsRune(p, 0) {
		return "", false
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h.o.Home == "" || !filepath.IsAbs(h.o.Home) {
			return "", false
		}
		return filepath.Join(h.o.Home, rest), true
	}
	if !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// readFile is omo.read_file(path): the text of a regular file of at most
// 1 MiB, registered with MarkSource so that it and every string
// omo.json_decode reads from it are secrets.
func (h *helpers) readFile(L *lua.LState) int {
	path, ok := h.expandPath(L.CheckString(1))
	if !ok {
		return fail(L, phrasePath)
	}
	if h.reads >= MaxFileReads {
		return fail(L, phraseTooManyReads)
	}
	h.reads++
	read := h.o.ReadFile
	if read == nil {
		read = homefile.Read
	}
	data, err := read(path, MaxFile)
	if err != nil {
		return fail(L, fileRefusal(err))
	}
	if len(data) > MaxFile {
		return fail(L, phraseTooLarge)
	}
	text := string(data)
	h.box.Redactor().MarkSource(text)
	L.Push(lua.LString(text))
	return 1
}

// fileRefusal maps a read error to its fixed phrase.
func fileRefusal(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return phraseNotFound
	case errors.Is(err, homefile.ErrLink):
		return phraseLink
	case errors.Is(err, homefile.ErrNotRegular):
		return phraseNotRegular
	case errors.Is(err, homefile.ErrTooLarge):
		return phraseTooLarge
	}
	return phraseUnreadable
}

// expand builds the run's environment the first time it is needed: the
// general agent environment expanded as for agents ($VAR and backtick
// commands, 10 s per command), then the account folder as the CLI's
// variable, then the profile's environment (literal), the same layers an
// agent of the profile gets; on top of omo's own environment without its
// control credentials. Every value of the layers is a secret.
func (h *helpers) expand() error {
	if h.envDone {
		return h.envErr
	}
	h.envDone = true
	account := map[string]string{}
	if name := agents.AccountDirVariable(h.job.Profile.CLI); name != "" && h.job.Profile.AccountDir != "" {
		account[name] = h.job.Profile.AccountDir
	}
	layered, err := session.ExpandEnvironmentContext(h.box.Context(), session.DefaultCommandTimeout, h.job.AgentEnv, account, h.job.Profile.Env)
	if err != nil {
		h.envErr = err
		return err
	}
	red := h.box.Redactor()
	for _, entry := range layered {
		if _, value, ok := strings.Cut(entry, "="); ok {
			red.Add(value)
		}
	}
	h.layered = layered
	h.full = map[string]string{}
	for _, entry := range session.ProcessEnvironment(layered) {
		if key, value, ok := strings.Cut(entry, "="); ok {
			h.full[key] = value
		}
	}
	return nil
}

// env is omo.env(name): the value of name in the run's environment, or nil.
// The value is a secret.
func (h *helpers) env(L *lua.LState) int {
	name := L.CheckString(1)
	if err := h.expand(); err != nil {
		return fail(L, phraseEnvFailed)
	}
	value, ok := h.full[name]
	if !ok && goos == "windows" {
		for k, v := range h.full {
			if strings.EqualFold(k, name) {
				value, ok = v, true
				break
			}
		}
	}
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	h.box.Redactor().Add(value)
	L.Push(lua.LString(value))
	return 1
}

// validService reports whether s may be passed to the keychain tool: 1 to
// 200 printable characters, not starting with "-".
func validService(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxServiceRunes {
		return false
	}
	for _, c := range s {
		if !unicode.IsPrint(c) || textsafe.IsHidden(c) {
			return false
		}
	}
	return true
}

// keychain is omo.keychain(service): the secret of a macOS generic
// password, registered with MarkSource.
func (h *helpers) keychain(L *lua.LState) int {
	service := L.CheckString(1)
	if !validService(service) {
		return fail(L, phraseServiceName)
	}
	read := h.o.Keychain
	if read == nil {
		read = defaultKeychain
	}
	ctx, cancel := context.WithTimeout(h.box.Context(), KeychainTimeout)
	defer cancel()
	secret, err := read(ctx, service)
	if err != nil {
		if errors.Is(err, errNotAvailable) {
			return fail(L, phraseNotAvailable)
		}
		return fail(L, phraseNoEntry)
	}
	if len(secret) > MaxKeychainSecret {
		return fail(L, phraseNoEntry)
	}
	h.box.Redactor().MarkSource(secret)
	L.Push(lua.LString(secret))
	return 1
}

// defaultKeychain runs `security find-generic-password -s <service> -w` on
// macOS with fixed arguments; elsewhere there is no keychain.
func defaultKeychain(ctx context.Context, service string) (string, error) {
	if goos != "darwin" {
		return "", errNotAvailable
	}
	cmd := exec.CommandContext(ctx, securityPath, "find-generic-password", "-s", service, "-w")
	out := &boundedBuffer{max: MaxKeychainSecret}
	cmd.Stdout = out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if out.over {
		return "", errors.New("keychain entry too large")
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}

// boundedBuffer keeps at most max bytes and notes when more were written.
type boundedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// codexRefresh is omo.codex_refresh(auth_path): for a profile of CLI codex,
// it runs the profile's own command in a private terminal until auth_path
// changes (at most 45 s, once per run). omo never writes the file.
func (h *helpers) codexRefresh(L *lua.LState) int {
	raw := L.CheckString(1)
	if h.job.Profile.CLI != api.CLICodex {
		return fail(L, phraseNotCodex)
	}
	path, ok := h.expandPath(raw)
	if !ok {
		return fail(L, phrasePath)
	}
	if h.refreshed {
		return fail(L, phraseRefreshOnce)
	}
	h.refreshed = true
	if err := h.expand(); err != nil {
		return fail(L, phraseEnvFailed)
	}
	refresh := h.o.RefreshCodex
	if refresh == nil {
		refresh = codexauth.Refresher{}.Refresh
	}
	ctx, cancel := context.WithTimeout(h.box.Context(), CodexRefreshTimeout)
	defer cancel()
	err := refresh(ctx, h.job.Profile.Command, append([]string(nil), h.layered...), path)
	switch {
	case err == nil:
		L.Push(lua.LTrue)
		return 1
	case ctx.Err() != nil:
		return fail(L, phraseRefreshTime)
	case errors.Is(err, codexauth.ErrUnchanged):
		return fail(L, phraseUnchanged)
	}
	return fail(L, phraseRefreshFailed)
}
