package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

const MaxActionURLBytes = 8192

var (
	errAnswer         = errors.New(`the action's answer is neither text nor an object with message, url or forward_id strings`)
	errActionURL      = errors.New("the action URL must be an absolute HTTP(S) URL without credentials, login tokens or hidden characters, at most 8192 bytes")
	forwardIDPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	boardTokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// answer parses raw results before any display cleaning, so escaping and
// redaction can never turn navigation into a different address.
func answer(result any) (api.PluginRunResult, error) {
	switch v := result.(type) {
	case nil:
		return api.PluginRunResult{}, nil
	case string:
		return api.PluginRunResult{Message: v}, nil
	case []byte:
		out := bytes.TrimSpace(v)
		if len(out) == 0 {
			return api.PluginRunResult{}, nil
		}
		var parsed any
		if json.Unmarshal(out, &parsed) == nil {
			switch p := parsed.(type) {
			case string:
				return answer(p)
			case map[string]any:
				_, hasURL := p["url"]
				_, hasForward := p["forward_id"]
				_, hasMessage := p["message"].(string)
				if hasURL || hasForward || hasMessage {
					return answer(p)
				}
			}
		}
		return api.PluginRunResult{Message: strings.ToValidUTF8(string(out), "\uFFFD")}, nil
	case map[string]any:
		// Legacy message tables ignored extra metadata. Navigation objects are strict;
		// a malformed navigation answer must never fall back to a display message.
		_, hasURL := v["url"]
		_, hasForward := v["forward_id"]
		if !hasURL && !hasForward {
			if msg, ok := v["message"].(string); ok {
				return api.PluginRunResult{Message: msg}, nil
			}
		}
		if len(v) == 0 {
			return api.PluginRunResult{}, errAnswer
		}
		out := api.PluginRunResult{}
		for k, value := range v {
			s, ok := value.(string)
			if !ok {
				return api.PluginRunResult{}, errAnswer
			}
			switch k {
			case "message":
				out.Message = s
			case "url":
				if !validActionURL(s) {
					return api.PluginRunResult{}, errActionURL
				}
				out.URL = s
			case "forward_id":
				if !forwardIDPattern.MatchString(s) {
					return api.PluginRunResult{}, errors.New("forward_id must be a lowercase 32-character hexadecimal proxy id")
				}
				out.ForwardID = s
			default:
				return api.PluginRunResult{}, errAnswer
			}
		}
		if out.URL != "" && out.ForwardID != "" {
			return api.PluginRunResult{}, errors.New("an action may return url or forward_id, not both")
		}
		return out, nil
	}
	return api.PluginRunResult{}, errAnswer
}

func validActionURL(raw string) bool {
	if raw == "" || len(raw) > MaxActionURLBytes || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\") || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || textsafe.IsHidden(r) }) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil || strings.ContainsFunc(decoded, func(r rune) bool { return unicode.IsControl(r) || textsafe.IsHidden(r) }) {
		return false
	}
	normalizedPath := path.Clean(u.Path)
	if normalizedPath == "/_omo/proxy" || strings.HasPrefix(normalizedPath, "/_omo/proxy/") || boardTokenPattern.MatchString(u.Fragment) {
		return false
	}
	// Omo's bearer token fragments, proxy grants and conventional authentication
	// query keys are refused rather than redacted. Normal app queries/fragments
	// (folder paths, line anchors) survive byte-for-byte.
	for _, part := range []string{u.RawQuery, u.Fragment} {
		low := strings.ToLower(part)
		if strings.Contains(low, "omo-token.") {
			return false
		}
		q, err := url.ParseQuery(strings.ReplaceAll(part, ";", "&"))
		if err != nil {
			return false
		}
		for key := range q {
			key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			switch key {
			case "token", "access_token", "refresh_token", "id_token", "api_key", "apikey", "key", "password", "passwd", "authorization", "auth", "bearer", "__omo_proxy_grant", "grant", "code":
				return false
			}
		}
	}
	return true
}
