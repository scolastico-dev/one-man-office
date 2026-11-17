package mcprelay

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
)

// Separator joins a server's name and an upstream tool's name. omo's own
// tool names never contain it, so a relayed tool cannot take an omo tool's
// name.
const Separator = "__"

// clientToolPrefix is what the agent programs put before omo's tool names
// (mcp__omo__<tool>); the whole name must stay within their 128 characters.
const clientToolPrefix = "mcp__omo__"

// maxClientToolName is the agent programs' limit of a tool name.
const maxClientToolName = 128

// Bounds of what omo takes from an upstream's tool list.
const (
	maxDescription = 4096
	maxSchemaBytes = 64 << 10
)

// toolSet is the tools omo offers for one instance of a server.
type toolSet struct {
	tools []mcpserver.RelayedTool
	// upstream maps an offered name to the upstream tool's name.
	upstream map[string]string
	omitted  []api.MCPOmittedTool
}

func (s toolSet) names() []string {
	out := make([]string, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t.Name)
	}
	return out
}

// sanitizeToolName keeps the characters MCP allows in a tool name and
// replaces every other one with "_".
func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// offerTools turns an upstream's tool list into the tools omo offers:
// filtered by allow and deny, renamed to <server>__<tool>, with the
// description marked as relayed. Tools omo cannot offer are listed in
// omitted with the reason. At most limit tools are offered.
func offerTools(server string, filter api.MCPToolFilter, upstream []*mcp.Tool, limit int) toolSet {
	set := toolSet{upstream: map[string]string{}}
	seen := map[string]bool{}
	omit := func(name, reason string) {
		set.omitted = append(set.omitted, api.MCPOmittedTool{Tool: shortName(name), Reason: reason})
	}
	for _, t := range upstream {
		if t == nil {
			continue
		}
		if len(filter.Allow) > 0 && !slices.Contains(filter.Allow, t.Name) {
			continue
		}
		if slices.Contains(filter.Deny, t.Name) {
			continue
		}
		if seen[t.Name] {
			omit(t.Name, "the server lists this name twice")
			continue
		}
		seen[t.Name] = true
		if len(set.tools) >= limit {
			omit(t.Name, fmt.Sprintf("the server offers more than %d tools", limit))
			continue
		}
		input, ok := objectSchema(t.InputSchema)
		if !ok {
			omit(t.Name, "its input schema is not a JSON object schema of at most 64 KiB")
			continue
		}
		base := server + Separator + sanitizeToolName(t.Name)
		name := base
		for n := 2; set.upstream[name] != ""; n++ {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		if len(clientToolPrefix)+len(name) > maxClientToolName {
			omit(t.Name, fmt.Sprintf("its name is longer than the %d characters agent programs accept", maxClientToolName))
			continue
		}
		offered := &mcp.Tool{
			Name:        name,
			Title:       t.Title,
			Description: describe(t.Description),
			InputSchema: input,
			Annotations: t.Annotations,
		}
		if output, ok := objectSchema(t.OutputSchema); ok {
			offered.OutputSchema = output
		}
		set.upstream[name] = t.Name
		set.tools = append(set.tools, mcpserver.RelayedTool{Name: name, Server: server, Tool: offered})
	}
	return set
}

func describe(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	if len(d) > maxDescription {
		d = d[:maxDescription]
		for !utf8.ValidString(d) {
			d = d[:len(d)-1]
		}
		d += "…"
	}
	return d
}

// objectSchema returns schema as a JSON object of type "object", which is
// what omo's server needs to offer the tool, or false.
func objectSchema(schema any) (map[string]any, bool) {
	if schema == nil {
		return nil, false
	}
	raw, err := json.Marshal(schema)
	if err != nil || len(raw) > maxSchemaBytes {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	if m["type"] != "object" {
		return nil, false
	}
	return maps.Clone(m), true
}

// shortName bounds an upstream name that is shown on the settings page.
func shortName(name string) string {
	const max = 200
	if len(name) <= max {
		return name
	}
	name = name[:max]
	for !utf8.ValidString(name) {
		name = name[:len(name)-1]
	}
	return name + "…"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}
