package session

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

// MergeEnvironment expands the shared agents.env layer, then overlays
// profile-specific and supervisor-owned values byte-for-byte. Later layers
// win. Keeping literal profile values preserves secrets containing '$'.
func MergeEnvironment(configured map[string]string, literalLayers ...map[string]string) []string {
	return mergeEnvironment(runtime.GOOS == "windows", configured, literalLayers...)
}

func mergeEnvironment(caseInsensitive bool, configured map[string]string, literalLayers ...map[string]string) []string {
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
	}
	merged := make(map[string]string, len(raw)+len(literal))
	for key := range raw {
		setEnvironmentValue(merged, key, resolver.resolve(key), caseInsensitive)
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
	return result
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
