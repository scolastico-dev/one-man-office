package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
	"github.com/scolastico-dev/one-man-office/internal/relaytransport"
)

type relayCapabilityKey struct{}

func (s *Server) serveRelaySession(ctx context.Context, cap relayhost.Capability, records relayproto.RecordIO) error {
	if s.relay == nil || s.relay.Validate(cap) != nil {
		records.Close()
		return relayhost.ErrDenied
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(cap.Context(), cancel)
	defer stop()
	return relaytransport.ServeHTTP(ctx, records, func() error { return s.relay.Validate(cap) }, s.RelayHandler(), func(ctx context.Context, _ net.Conn) context.Context {
		return context.WithValue(ctx, relayCapabilityKey{}, cap)
	})
}

// RelayHandler derives the human actor only from authenticated connection
// authority. Socket headers and browser credentials never confer relay access.
func (s *Server) RelayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		cap, ok := r.Context().Value(relayCapabilityKey{}).(relayhost.Capability)
		if !ok || s.relay == nil || s.relay.Validate(cap) != nil {
			writeError(w, r, api.Errorf(api.CodeUnauthorized, "active relay device required"))
			return
		}
		if !validRelayRequest(r) {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "relay request rejected"))
			return
		}
		if relayPrivatePath(r.URL.Path) {
			writeError(w, r, api.Errorf(api.CodeForbidden, "route unavailable through relay"))
			return
		}
		original := r
		r = r.Clone(r.Context())
		if !prepareRelayBody(w, r, original) {
			return
		}
		for key := range r.Header {
			if relayUntrustedHeader(strings.ToLower(key)) {
				delete(r.Header, key)
			}
		}
		r = withRequestContext(r, board.User, s.log)
		if !requireJSON(w, r) {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/plugin-assets/") {
			s.servePluginAsset(w, r)
			return
		}
		s.mux.ServeHTTP(s.withEventID(w, r), r)
	})
}

func relayPrivatePath(p string) bool {
	if rest, ok := strings.CutPrefix(p, "/api/mcp/servers/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && (parts[1] == "connect" || parts[1] == "disconnect") {
			return true
		}
	}

	for _, prefix := range []string{"/api/relay", "/mcp", "/oauth", "/api/oauth", "/.well-known", "/api/mcp/tokens", "/proxy"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

func validRelayRequest(r *http.Request) bool {
	if r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.Host != "omo-relay.invalid" || r.URL == nil || r.URL.Host != "" || r.URL.Scheme != "" || r.URL.User != nil || r.URL.Opaque != "" || r.URL.Fragment != "" {
		return false
	}
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return false
	}
	target, _, _ := strings.Cut(r.RequestURI, "?")
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return false
	}
	parsed, e := url.ParseRequestURI(r.RequestURI)
	if e != nil || parsed.Path != r.URL.Path || parsed.EscapedPath() != r.URL.EscapedPath() {
		return false
	}
	decoded, e := url.PathUnescape(target)
	if e != nil || decoded != r.URL.Path || !utf8.ValidString(decoded) || strings.ContainsAny(decoded, "\\%") {
		return false
	}
	for _, c := range decoded {
		if c < 32 || c == 127 {
			return false
		}
	}
	lower := strings.ToLower(target)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return false
	}
	if decoded != "/" && path.Clean(decoded) != strings.TrimSuffix(decoded, "/") {
		return false
	}
	// Declared trailers have no application meaning here and must not introduce
	// identity or credentials after this admission check while a body is read.
	if len(r.Trailer) != 0 {
		return false
	}
	origins := 0
	for key, values := range r.Header {
		k := strings.ToLower(key)
		if strings.HasPrefix(k, "x-omo-") || k == "proxy-authorization" || strings.HasPrefix(k, "omo-relay-") || strings.HasPrefix(k, "omo-notification-") || k == "trailer" {
			return false
		}
		if k == "origin" {
			for _, v := range values {
				origins++
				if v != "http://omo-relay.invalid" {
					return false
				}
			}
			if len(values) == 0 {
				return false
			}
		}
		if k == "authorization" {
			for _, v := range values {
				scheme, value, ok := strings.Cut(v, " ")
				if ok && strings.EqualFold(scheme, "Basic") {
					b, e := base64.StdEncoding.DecodeString(value)
					user, _, _ := strings.Cut(string(b), ":")
					if e == nil && strings.EqualFold(user, "relay") {
						return false
					}
				}
			}
		}
		if k == "sec-websocket-protocol" {
			for _, v := range values {
				for _, p := range strings.Split(v, ",") {
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), webSocketTokenPrefix) {
						return false
					}
				}
			}
		}
	}
	return origins <= 1
}
func relayUntrustedHeader(k string) bool {
	return k == "authorization" || k == "cookie" || k == "forwarded" || k == "x-real-ip" || k == "remote-user" || strings.HasPrefix(k, "x-forwarded-") || strings.HasPrefix(k, "x-auth-") || strings.HasPrefix(k, "x-authenticated-") || strings.HasPrefix(k, "x-remote-") || strings.HasPrefix(k, "x-original-") || strings.HasPrefix(k, "x-rewrite-")
}

// Go discovers undeclared trailers only when the parsed body reaches EOF.
// Keep the original request: Clone deliberately isolates its Trailer map.
type relayBody struct {
	io.ReadCloser
	original *http.Request
}

func (b relayBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if errors.Is(e, io.EOF) && len(b.original.Trailer) != 0 {
		return n, api.Errorf(api.CodeBadRequest, "relay request trailers rejected")
	}
	return n, e
}
func prepareRelayBody(w http.ResponseWriter, r, original *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || !isAPI(r.URL.Path) {
		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "unexpected relay request body"))
			return false
		}
		return true
	}
	if len(r.TransferEncoding) == 0 {
		return true
	}
	r.Body = relayBody{ReadCloser: r.Body, original: original}
	if rawUpload(r) {
		return true
	} // upload handlers validate EOF before committing
	// JSON is already limited to maxBody. Validate chunked framing/trailers even
	// for mutation handlers that otherwise ignore their bodies, then replay the
	// exact original bytes to the normal decoder. Raw uploads stay streaming.
	data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if e != nil {
		writeError(w, r, decodeError(e))
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.TransferEncoding = nil
	return true
}
