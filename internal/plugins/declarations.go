package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

const (
	maxButtons      = 64
	maxConfigFields = 64
	maxChoices      = 100
	maxLabelRunes   = 200
)

// checkDeclarationJSON preserves strict nested decoding, including JSON null
// (which encoding/json otherwise silently accepts into strings and structs).
func checkDeclarationJSON(fields map[string]json.RawMessage) error {
	for _, name := range []string{"buttons", "config_fields"} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		list, err := declarationList(raw, name)
		if err != nil {
			return err
		}
		for _, obj := range list {
			allowed := map[string]bool{"label": true, "place": true, "action": true, "per_worktree": true}
			if name == "config_fields" {
				allowed = map[string]bool{"key": true, "label": true, "type": true, "choices": true}
			}
			if err := checkDeclarationObject(obj, allowed, name); err != nil {
				return err
			}
			if raw, ok := obj["choices"]; ok {
				choices, err := declarationList(raw, "choices")
				if err != nil {
					return err
				}
				for _, choice := range choices {
					if err := checkDeclarationObject(choice, map[string]bool{"value": true, "label": true}, "choice"); err != nil {
						return err
					}
				}
			}
		}
	}
	if raw, ok := fields["frontend"]; ok {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return errors.New("frontend must be an object")
		}
		if err := checkDeclarationObject(obj, map[string]bool{"script": true, "assets": true}, "frontend"); err != nil {
			return err
		}
	}
	return nil
}

func declarationList(raw json.RawMessage, name string) ([]map[string]json.RawMessage, error) {
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil || list == nil {
		return nil, fmt.Errorf("%s must be a list of objects", name)
	}
	for _, obj := range list {
		if obj == nil {
			return nil, fmt.Errorf("%s entries must be objects", name)
		}
	}
	return list, nil
}

func checkDeclarationObject(obj map[string]json.RawMessage, allowed map[string]bool, name string) error {
	if err := checkFields(obj, allowed, name+": "); err != nil {
		return err
	}
	for _, v := range obj {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("%s has a value of the wrong type (null)", name)
		}
	}
	return nil
}

func validLabel(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= maxLabelRunes && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || textsafe.IsHidden(r) })
}

func checkDeclarations(m *Manifest, raw map[string]json.RawMessage) error {
	if len(m.Buttons) > maxButtons {
		return fmt.Errorf("buttons has more than %d entries", maxButtons)
	}
	actions := map[string]string{}
	for _, h := range m.Hooks {
		if h.Event == EventManual {
			actions[h.Name] = h.Scope
		}
	}
	seen := map[string]bool{}
	for i, b := range m.Buttons {
		prefix := fmt.Sprintf("button %d: ", i+1)
		if !validLabel(b.Label) {
			return errors.New(prefix + "label must have 1 to 200 visible characters on one line")
		}
		scope := ""
		switch b.Place {
		case "ticket":
			scope = ScopeTicket
		case "sidebar", "project":
			scope = ScopeGlobal
		default:
			return errors.New(prefix + "place must be ticket, sidebar or project")
		}
		if actions[b.Action] == "" {
			return errors.New(prefix + "action must name an existing manual hook")
		}
		if actions[b.Action] != scope {
			return errors.New(prefix + "action scope does not match place")
		}
		if b.PerWorktree && b.Place != "ticket" {
			return errors.New(prefix + "per_worktree is for ticket buttons only")
		}
		key := b.Place + "/" + b.Action
		if seen[key] {
			return errors.New(prefix + "duplicate action at this place")
		}
		seen[key] = true
	}
	if _, ok := raw["frontend"]; ok {
		if err := publicPath(m.Frontend.Script); err != nil {
			return fmt.Errorf("frontend.script: %w", err)
		}
		if m.Frontend.Assets != "" {
			if err := publicPath(m.Frontend.Assets); err != nil {
				return fmt.Errorf("frontend.assets: %w", err)
			}
		}
	}
	if len(m.ConfigFields) > maxConfigFields {
		return fmt.Errorf("config_fields has more than %d entries", maxConfigFields)
	}
	seen = map[string]bool{}
	// Presence matters: even an empty choices list belongs only to selects.
	var fields []map[string]json.RawMessage
	_ = json.Unmarshal(raw["config_fields"], &fields)
	for i := range m.ConfigFields {
		f := &m.ConfigFields[i]
		prefix := fmt.Sprintf("config_fields %d: ", i+1)
		if !actionPattern.MatchString(f.Key) {
			return errors.New(prefix + "key must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
		}
		if seen[f.Key] {
			return errors.New(prefix + "duplicate key")
		}
		seen[f.Key] = true
		if !validLabel(f.Label) {
			return errors.New(prefix + "label must have 1 to 200 visible characters on one line")
		}
		if f.Choices == nil {
			f.Choices = []api.PluginConfigChoice{}
		}
		values := map[string]bool{}
		switch f.Type {
		case "select":
			if len(f.Choices) == 0 || len(f.Choices) > maxChoices {
				return errors.New(prefix + "select choices must have 1 to 100 entries")
			}
			for _, c := range f.Choices {
				if !actionPattern.MatchString(c.Value) {
					return errors.New(prefix + "choice value must be a stable name of 1 to 64 characters")
				}
				if !validLabel(c.Label) {
					return errors.New(prefix + "choice label must have 1 to 200 visible characters on one line")
				}
				if values[c.Value] {
					return errors.New(prefix + "duplicate choice value")
				}
				values[c.Value] = true
			}
		case "boolean", "string", "number":
			if _, ok := fields[i]["choices"]; ok {
				return errors.New(prefix + "choices is for select fields only")
			}
		default:
			return errors.New(prefix + "type must be select, boolean, string or number")
		}
		if def, ok := m.DefaultConfig[f.Key]; ok {
			valid := false
			switch f.Type {
			case "select":
				s, ok := def.(string)
				valid = ok && values[s]
			case "string":
				_, valid = def.(string)
			case "boolean":
				_, valid = def.(bool)
			case "number":
				if n, ok := def.(json.Number); ok {
					_, err := n.Float64()
					valid = err == nil
				}
			}
			if !valid {
				return errors.New(prefix + "default_config value does not match the declared type or choices")
			}
		}
	}
	return nil
}
