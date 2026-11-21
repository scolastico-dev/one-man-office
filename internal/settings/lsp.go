package settings

import (
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

const (
	maxLSPServers   = 32
	maxLSPSelectors = 100
	maxLSPArgument  = 4096
	maxLSPPattern   = 1024
	maxLSPConfig    = 64 << 10
)

// cloneLSP isolates all mutable registration data and normalizes absent
// collections to arrays/objects, including the opaque nested JSON values.
func cloneLSP(l api.LSPSettings) api.LSPSettings {
	l.ForceChat = append([]string{}, l.ForceChat...)
	l.Servers = append([]api.LSPServer{}, l.Servers...)
	for i := range l.Servers {
		m := &l.Servers[i]
		m.Args = append([]string{}, m.Args...)
		m.Files = slices.Clone(m.Files)
		if m.Files == nil {
			m.Files = []api.LSPFileSelector{}
		}
		m.Env = maps.Clone(m.Env)
		if m.Env == nil {
			m.Env = map[string]string{}
		}
		m.InitializationOptions = cloneConfig(m.InitializationOptions)
		m.Configuration = cloneConfig(m.Configuration)
	}
	return l
}

func validateLSP(l api.LSPSettings) error {
	if l.Debounce < 0 || time.Duration(l.Debounce) > time.Minute {
		return fieldErr("lsp.debounce", "lsp.debounce must be between 0s and 1m")
	}
	seenCLI := map[string]bool{}
	for i, cli := range l.ForceChat {
		key := "lsp.force_chat." + strconv.Itoa(i)
		switch cli {
		case api.CLIClaude, api.CLICodex, api.CLIAgy, api.CLIGemini, api.CLICustom:
		default:
			return fieldErr(key, "%s must be claude, codex, agy, gemini or custom", key)
		}
		if seenCLI[cli] {
			return fieldErr(key, "%s repeats an agent program", key)
		}
		seenCLI[cli] = true
	}
	if len(l.Servers) > maxLSPServers {
		return fieldErr("lsp.servers", "lsp.servers may hold at most %d registrations", maxLSPServers)
	}
	seen := map[string]bool{}
	for i, m := range l.Servers {
		key := "lsp.servers." + strconv.Itoa(i)
		if !idPattern.MatchString(m.Name) {
			return fieldErr(key+".name", "%s.name must match %s", key, idPattern)
		}
		if seen[m.Name] {
			return fieldErr(key+".name", "%s.name repeats a registration name", key)
		}
		seen[m.Name] = true
		if strings.TrimSpace(m.Command) == "" || len(m.Command) > maxLSPArgument || hasHidden(m.Command) || !utf8.ValidString(m.Command) {
			return fieldErr(key+".command", "%s.command must name an executable, at most %d bytes without hidden characters", key, maxLSPArgument)
		}
		if err := validateArgs(key+".args", m.Args); err != nil {
			return err
		}
		for j, arg := range m.Args {
			if len(arg) > maxLSPArgument || strings.ContainsRune(arg, 0) || !utf8.ValidString(arg) {
				field := key + ".args." + strconv.Itoa(j)
				return fieldErr(field, "%s must be at most %d bytes of UTF-8 text without NUL", field, maxLSPArgument)
			}
		}
		if err := validateEnv(key+".env", m.Env); err != nil {
			return err
		}
		for _, name := range sortedKeys(m.Env) {
			if strings.ContainsRune(m.Env[name], 0) || !utf8.ValidString(m.Env[name]) {
				return fieldErr(key+".env."+name, "%s.env values must be UTF-8 text without NUL", key)
			}
		}
		if len(m.Files) == 0 || len(m.Files) > maxLSPSelectors {
			return fieldErr(key+".files", "%s.files must hold between 1 and %d selectors", key, maxLSPSelectors)
		}
		for j, f := range m.Files {
			field := key + ".files." + strconv.Itoa(j)
			if !validLSPPattern(f.Pattern) {
				return fieldErr(field+".pattern", "%s.pattern must be a relative path.Match glob of at most %d bytes, without **, backslashes, colons or empty, dot or parent components", field, maxLSPPattern)
			}
			if f.LanguageID == "" || len(f.LanguageID) > 64 || hasHidden(f.LanguageID) || !utf8.ValidString(f.LanguageID) || strings.ContainsFunc(f.LanguageID, unicode.IsSpace) {
				return fieldErr(field+".language_id", "%s.language_id must be nonempty UTF-8 text of at most 64 bytes without whitespace or hidden characters", field)
			}
		}
		for _, c := range []struct {
			name  string
			value map[string]any
		}{{"initialization_options", m.InitializationOptions}, {"configuration", m.Configuration}} {
			field := key + "." + c.name
			if !jsonValue(c.value, 0) {
				return fieldErr(field, "%s must be a JSON object with text keys and JSON values, nested at most %d levels (quote dates and times)", field, maxConfigDepth)
			}
			data, err := json.Marshal(c.value)
			if err != nil || len(data) > maxLSPConfig {
				return fieldErr(field, "%s must be a JSON object of at most %d KiB", field, maxLSPConfig>>10)
			}
		}
	}
	return nil
}

func validLSPPattern(pattern string) bool {
	if pattern == "" || len(pattern) > maxLSPPattern || hasHidden(pattern) || !utf8.ValidString(pattern) || strings.ContainsAny(pattern, `\:`) || strings.Contains(pattern, "**") {
		return false
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	_, err := path.Match(pattern, "")
	return err == nil
}
