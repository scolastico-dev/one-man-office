package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// DefaultCommandTimeout bounds each backtick command of an environment
// expansion. A command that prompts (a password manager, gpg) or hangs must
// not block whatever waits for the environment.
const DefaultCommandTimeout = 10 * time.Second

// CommandTimeoutError reports that the backtick command in the value of
// Variable did not finish in time. It never carries the command's output.
type CommandTimeoutError struct {
	Variable string
	Timeout  time.Duration
}

func (e *CommandTimeoutError) Error() string {
	return fmt.Sprintf("environment variable %s: its command timed out after %s", e.Variable, e.Timeout)
}

// MergeEnvironment is ExpandEnvironment with DefaultCommandTimeout. A
// command that times out leaves its part of the value empty.
//
// Deprecated: use ExpandEnvironment, which reports the timeout.
func MergeEnvironment(configured map[string]string, literalLayers ...map[string]string) []string {
	env, _ := ExpandEnvironment(DefaultCommandTimeout, configured, literalLayers...)
	return env
}

// ExpandEnvironment expands the shared agents.env layer, then overlays
// profile-specific and supervisor-owned values byte-for-byte. Later layers
// win. Keeping literal profile values preserves secrets containing '$'.
//
// Each backtick command runs at most once per call and is killed, with its
// whole process group, after timeout (<= 0: DefaultCommandTimeout); the
// expansion then fails with a *CommandTimeoutError naming the variable.
func ExpandEnvironment(timeout time.Duration, configured map[string]string, literalLayers ...map[string]string) ([]string, error) {
	return ExpandEnvironmentContext(context.Background(), timeout, configured, literalLayers...)
}

// ExpandEnvironmentContext is ExpandEnvironment that also ends when ctx
// does: a running command is killed with its process group, no further
// command starts, and the expansion fails with ctx's error.
func ExpandEnvironmentContext(ctx context.Context, timeout time.Duration, configured map[string]string, literalLayers ...map[string]string) ([]string, error) {
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	execute := func(command string) (string, error) { return executeEnvironmentCommand(ctx, command, timeout) }
	return mergeEnvironmentWithExecutor(runtime.GOOS == "windows", configured, execute, literalLayers...)
}

func mergeEnvironment(caseInsensitive bool, configured map[string]string, literalLayers ...map[string]string) []string {
	execute := func(command string) (string, error) {
		return executeEnvironmentCommand(context.Background(), command, DefaultCommandTimeout)
	}
	env, _ := mergeEnvironmentWithExecutor(caseInsensitive, configured, execute, literalLayers...)
	return env
}

// errCommandTimeout is what an executor returns for a command that did not
// finish in time.
var errCommandTimeout = errors.New("command timed out")

func mergeEnvironmentWithExecutor(caseInsensitive bool, configured map[string]string, execute func(string) (string, error), literalLayers ...map[string]string) ([]string, error) {
	inherited := environmentMap(processEnvironmentForPlatform(os.Environ(), nil, caseInsensitive))
	raw := make(map[string]string, len(configured))
	mergeEnvironmentLayer(raw, configured, caseInsensitive)
	literal := make(map[string]string)
	for _, layer := range literalLayers {
		mergeEnvironmentLayer(literal, layer, caseInsensitive)
	}
	resolver := environmentResolver{
		inherited:       inherited,
		raw:             raw,
		literal:         literal,
		resolved:        make(map[string]string, len(raw)),
		resolving:       make(map[string]bool, len(raw)),
		caseInsensitive: caseInsensitive,
		execute:         execute,
		commands:        map[string]string{},
	}
	merged := make(map[string]string, len(raw)+len(literal))
	rawKeys := make([]string, 0, len(raw))
	for key := range raw {
		rawKeys = append(rawKeys, key)
	}
	sort.Strings(rawKeys)
	for _, key := range rawKeys {
		setEnvironmentValue(merged, key, resolver.resolve(key), caseInsensitive)
		if resolver.err != nil {
			return nil, resolver.err
		}
	}
	for key, value := range literal {
		setEnvironmentValue(merged, key, value, caseInsensitive)
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+merged[key])
	}
	return result, nil
}

func mergeEnvironmentLayer(destination, layer map[string]string, caseInsensitive bool) {
	keys := make([]string, 0, len(layer))
	for key := range layer {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		setEnvironmentValue(destination, key, layer[key], caseInsensitive)
	}
}

func setEnvironmentValue(destination map[string]string, key, value string, caseInsensitive bool) {
	if caseInsensitive {
		for existing := range destination {
			if strings.EqualFold(existing, key) {
				delete(destination, existing)
			}
		}
	}
	destination[key] = value
}

func environmentMap(items []string) map[string]string {
	values := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

type environmentResolver struct {
	inherited       map[string]string
	raw             map[string]string
	literal         map[string]string
	resolved        map[string]string
	resolving       map[string]bool
	caseInsensitive bool
	execute         func(string) (string, error)
	// commands caches each command's output: a command runs at most once.
	commands map[string]string
	// err is the first failure; once set, no further command runs.
	err error
}

// run executes a backtick command found in the value of variable.
func (r *environmentResolver) run(command, variable string) string {
	if r.err != nil {
		return ""
	}
	if out, ok := r.commands[command]; ok {
		return out
	}
	out, err := r.execute(command)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			r.err = err
			return ""
		}
		var timeout time.Duration
		if te, ok := err.(interface{ timeout() time.Duration }); ok {
			timeout = te.timeout()
		}
		r.err = &CommandTimeoutError{Variable: variable, Timeout: timeout}
		return ""
	}
	r.commands[command] = out
	return out
}

func (r *environmentResolver) resolve(key string) string {
	canonical := r.canonical(key)
	if value, ok := r.resolved[canonical]; ok {
		return value
	}
	value, configured := environmentLookup(r.raw, key, r.caseInsensitive)
	if !configured || r.resolving[canonical] {
		value, _ := environmentLookup(r.inherited, key, r.caseInsensitive)
		return value
	}
	r.resolving[canonical] = true
	value = r.expand(value, key)
	delete(r.resolving, canonical)
	r.resolved[canonical] = value
	return value
}

func (r *environmentResolver) expand(value, current string) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '`' {
			end := environmentCommandEnd(value, i)
			if end < 0 {
				out.WriteString(value[i:])
				break
			}
			out.WriteString(strings.TrimRight(r.run(value[i+1:end], current), "\r\n"))
			i = end + 1
			continue
		}
		if value[i] != '$' || i+1 == len(value) {
			out.WriteByte(value[i])
			i++
			continue
		}
		if value[i+1] == '{' {
			end := environmentExpansionEnd(value, i)
			if end < 0 {
				out.WriteString(value[i:])
				break
			}
			expression := value[i+2 : end]
			name, fallback, hasFallback := strings.Cut(expression, ":")
			fallback = strings.TrimPrefix(fallback, "-")
			replacement := r.lookup(name, current)
			if replacement == "" && hasFallback {
				replacement = r.expand(fallback, current)
			}
			out.WriteString(replacement)
			i = end + 1
			continue
		}
		end := i + 1
		for end < len(value) && isEnvironmentNameByte(value[end]) {
			end++
		}
		if end == i+1 {
			out.WriteByte(value[i])
			i++
			continue
		}
		out.WriteString(r.lookup(value[i+1:end], current))
		i = end
	}
	return out.String()
}

func environmentExpansionEnd(value string, start int) int {
	depth := 1
	for i := start + 2; i < len(value); i++ {
		if value[i] == '`' {
			end := environmentCommandEnd(value, i)
			if end < 0 {
				return -1
			}
			i = end
			continue
		}
		if value[i] == '$' && i+1 < len(value) && value[i+1] == '{' {
			depth++
			i++
			continue
		}
		if value[i] == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func environmentCommandEnd(value string, start int) int {
	for i := start + 1; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
			continue
		}
		if value[i] == '`' {
			return i
		}
	}
	return -1
}

// timeoutError is errCommandTimeout with the deadline that passed.
type timeoutError time.Duration

func (e timeoutError) Error() string          { return errCommandTimeout.Error() }
func (e timeoutError) Unwrap() error          { return errCommandTimeout }
func (e timeoutError) timeout() time.Duration { return time.Duration(e) }

// executeEnvironmentCommand runs command with a shell and returns its
// standard output. A failing command still yields whatever it printed; only
// a timeout is an error, or the end of parent, whose error is returned then.
// On either the command's process group is killed.
func executeEnvironmentCommand(parent context.Context, command string, timeout time.Duration) (string, error) {
	if err := parent.Err(); err != nil {
		return "", err
	}
	name := "sh"
	args := []string{"-c", command}
	if runtime.GOOS == "windows" {
		name = os.Getenv("COMSPEC")
		if name == "" {
			name = "cmd.exe"
		}
		args = []string{"/D", "/S", "/C", command}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = processEnvironment(nil)
	ownProcessGroup(cmd)
	// A grandchild outside the group may keep the output pipe open; do not
	// wait for it after the command itself ended or was killed.
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	if err := parent.Err(); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", timeoutError(timeout)
	}
	return string(out), nil
}

func (r *environmentResolver) lookup(key, current string) string {
	if r.canonical(key) == r.canonical(current) {
		value, _ := environmentLookup(r.inherited, key, r.caseInsensitive)
		return value
	}
	if value, ok := environmentLookup(r.literal, key, r.caseInsensitive); ok {
		return value
	}
	if _, configured := environmentLookup(r.raw, key, r.caseInsensitive); configured {
		return r.resolve(key)
	}
	value, _ := environmentLookup(r.inherited, key, r.caseInsensitive)
	return value
}

func (r *environmentResolver) canonical(key string) string {
	if r.caseInsensitive {
		return strings.ToUpper(key)
	}
	return key
}

func environmentLookup(values map[string]string, key string, caseInsensitive bool) (string, bool) {
	if !caseInsensitive {
		value, ok := values[key]
		return value, ok
	}
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return "", false
}

func isEnvironmentNameByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

// ProcessEnvironment overlays extra values onto the inherited process
// environment and removes parent control credentials. It is used for agent
// CLIs and other commands that must receive the agent environment safely.
func ProcessEnvironment(extra []string) []string {
	return processEnvironment(extra)
}

// Parent control credentials belong only to the office process. Agent CLI
// processes retain their socket identity without inheriting office credentials.
func processEnvironment(extra []string) []string {
	return processEnvironmentForPlatform(os.Environ(), extra, runtime.GOOS == "windows")
}

func processEnvironmentForPlatform(inherited, extra []string, caseInsensitive bool) []string {
	env := append(append([]string(nil), inherited...), extra...)
	reversed := make([]string, 0, len(env))
	seen := make(map[string]bool, len(env))
	for i := len(env) - 1; i >= 0; i-- {
		item := env[i]
		key := environmentEntryKey(item, caseInsensitive)
		if strings.EqualFold(key, "OMO_CONTROL_URL") || strings.EqualFold(key, "OMO_CONTROL_TOKEN") {
			continue
		}
		canonical := key
		if caseInsensitive {
			canonical = strings.ToUpper(key)
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		reversed = append(reversed, item)
	}
	clean := make([]string, len(reversed))
	for i := range reversed {
		clean[len(reversed)-1-i] = reversed[i]
	}
	if caseInsensitive {
		sort.SliceStable(clean, func(i, j int) bool {
			return strings.ToUpper(clean[i]) < strings.ToUpper(clean[j])
		})
	}
	return clean
}

func environmentEntryKey(item string, windows bool) string {
	start := 0
	if windows && strings.HasPrefix(item, "=") {
		start = 1
	}
	if separator := strings.IndexByte(item[start:], '='); separator >= 0 {
		return item[:start+separator]
	}
	return item
}
