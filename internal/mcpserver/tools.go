package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
)

// omoTool is an `omo ai` command offered as an MCP tool.
type omoTool struct {
	cmd    aicmd.Command
	tool   *mcp.Tool
	schema *jsonschema.Resolved
}

// commands are every `omo ai` command by tool name, the command line's
// only ones included; tools are those that are MCP tools, in the order of
// aicmd.Commands.
var commands = sync.OnceValues(func() (map[string]aicmd.Command, []omoTool) {
	byName := map[string]aicmd.Command{}
	var tools []omoTool
	for _, c := range aicmd.Commands() {
		byName[c.Tool] = c
		if c.CommandLineOnly {
			continue
		}
		t, err := newOmoTool(c)
		if err != nil {
			// The inputs are fixed at build time; the tests build every one.
			panic(err)
		}
		tools = append(tools, t)
	}
	return byName, tools
})

func newOmoTool(c aicmd.Command) (omoTool, error) {
	schema, err := jsonschema.ForType(c.Input, &jsonschema.ForOptions{})
	if err != nil {
		return omoTool{}, fmt.Errorf("input schema of %s: %w", c.Tool, err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return omoTool{}, fmt.Errorf("input schema of %s: %w", c.Tool, err)
	}
	return omoTool{
		cmd: c,
		tool: &mcp.Tool{
			Name:        c.Tool,
			Description: fmt.Sprintf("%s. The same as `omo ai %s`.", strings.TrimSuffix(c.Short, "."), strings.Join(c.Path, " ")),
			InputSchema: schema,
		},
		schema: resolved,
	}, nil
}

// input decodes a tool call's arguments into a pointer to the command's
// input. A relative local path is taken relative to workdir, the caller's
// folder, never to omo's own.
func (t omoTool) input(args json.RawMessage, workdir string) (any, error) {
	if len(bytes.TrimSpace(args)) == 0 || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		args = json.RawMessage("{}")
	}
	var generic map[string]any
	if err := json.Unmarshal(args, &generic); err != nil {
		return nil, fmt.Errorf("the arguments are not a JSON object: %v", err)
	}
	if err := t.schema.Validate(generic); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}
	in := reflect.New(t.cmd.Input)
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(in.Interface()); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}
	if err := localPaths(in.Elem(), workdir); err != nil {
		return nil, err
	}
	return in.Interface(), nil
}

// localPaths makes the input's local file paths (string fields named
// "path" in JSON) absolute against workdir.
func localPaths(v reflect.Value, workdir string) error {
	for i := range v.NumField() {
		f := v.Type().Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "path" || f.Type.Kind() != reflect.String {
			continue
		}
		p := v.Field(i).String()
		if p == "" || filepath.IsAbs(p) {
			continue
		}
		if workdir == "" {
			return fmt.Errorf("give %s as an absolute path", name)
		}
		v.Field(i).SetString(filepath.Join(workdir, p))
	}
	return nil
}

// toolResult turns a command's answer into a tool result: the command
// line's text, and the route's JSON as structured content. A JSON array is
// wrapped as {"items": [...]}, since structured content is an object.
func toolResult(r aicmd.Result) *mcp.CallToolResult {
	out := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: r.Text}}}
	v := bytes.TrimSpace(r.Value)
	switch {
	case len(v) == 0 || bytes.Equal(v, []byte("null")):
	case v[0] == '{':
		out.StructuredContent = json.RawMessage(v)
	case v[0] == '[':
		out.StructuredContent = map[string]json.RawMessage{"items": v}
	}
	return out
}

// toolError is a tool result that reports a failure to the model.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
