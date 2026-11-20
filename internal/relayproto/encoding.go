// Package relayproto defines the version 1 endpoint protocol. It owns no routing,
// device authorization, persistent storage, or human trust policy.
package relayproto

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const Version uint16 = 1
const Suite = "MLKEM768-HKDF384-AES256GCM"
const label = "omo-relay-v1/"

var ErrProtocol = errors.New("relay protocol violation")
var ErrAuthentication = errors.New("relay authentication failed")

// Bytes is canonical, unpadded base64url on the wire.
type Bytes []byte

func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(base64.RawURLEncoding.EncodeToString(b))
}
func (b *Bytes) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || raw[0] != '"' {
		return ErrProtocol
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ErrProtocol
	}
	v, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || base64.RawURLEncoding.EncodeToString(v) != s {
		return ErrProtocol
	}
	*b = v
	return nil
}

// Counter is a canonical unsigned decimal JSON string, including zero.
type Counter uint64

func (n Counter) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(n), 10))
}
func (n *Counter) UnmarshalJSON(raw []byte) error {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return ErrProtocol
	}
	s := string(raw[1 : len(raw)-1])
	if len(s) > 1 && s[0] == '0' {
		return ErrProtocol
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return ErrProtocol
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return ErrProtocol
	}
	*n = Counter(v)
	return nil
}

// EncodeFields is E: each field is prefixed by its unsigned 32-bit byte length.
// Inputs are locally bounded protocol fields, never unbounded readers.
func EncodeFields(fields ...[]byte) []byte {
	var out []byte
	for _, f := range fields {
		if uint64(len(f)) > 1<<32-1 {
			panic("relay field exceeds u32")
		}
		out = binary.BigEndian.AppendUint32(out, uint32(len(f)))
		out = append(out, f...)
	}
	return out
}
func u16(n uint16) []byte                { return binary.BigEndian.AppendUint16(nil, n) }
func u32(n uint32) []byte                { return binary.BigEndian.AppendUint32(nil, n) }
func u64(n uint64) []byte                { return binary.BigEndian.AppendUint64(nil, n) }
func hashFields(fields ...[]byte) []byte { h := sha512.Sum384(EncodeFields(fields...)); return h[:] }
func mac(key, input []byte) []byte {
	h := hmac.New(sha512.New384, key)
	h.Write(input)
	return h.Sum(nil)
}
func expand(prk []byte, info []byte, size int) []byte {
	v, err := hkdf.Expand(sha512.New384, prk, string(info), size)
	if err != nil {
		panic(err)
	}
	return v
}
func kdf(secret, salt []byte, name string) []byte {
	v, err := hkdf.Key(sha512.New384, secret, salt, string(EncodeFields([]byte(label+name))), 32)
	if err != nil {
		panic(err)
	}
	return v
}
func validHostID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validFingerprint(s string) bool {
	if len(s) != 96 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Require ASCII DNS labels, including preconverted IDNA A-labels. Silently
// punycoding a Unicode origin would change the transcript used by native peers.
func validDNSHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Route binds cryptographic operations to a canonical configured relay and host.
// Construct it through NewRoute; all cryptographic entry points validate it.
type Route struct {
	Origin string
	HostID string
}

func NewRoute(origin, hostID string, allowLoopbackHTTP bool) (Route, error) {
	u, err := url.Parse(origin)
	if err != nil || u == nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") || u.Opaque != "" || u.Host == "" || !validHostID(hostID) {
		return Route{}, ErrProtocol
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) == nil && !validDNSHost(host) {
		return Route{}, ErrProtocol
	}
	port := u.Port()
	if host == "" || strings.ContainsAny(host, "% \\\t\r\n") || strings.HasSuffix(host, ".") {
		return Route{}, ErrProtocol
	}
	if port != "" {
		n, e := strconv.ParseUint(port, 10, 16)
		if e != nil || n == 0 || strconv.FormatUint(n, 10) != port {
			return Route{}, ErrProtocol
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return Route{}, ErrProtocol
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(host)
		if u.Scheme != "http" || !allowLoopbackHTTP || !(host == "localhost" || ip != nil && ip.IsLoopback()) {
			return Route{}, ErrProtocol
		}
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return Route{Origin: u.Scheme + "://" + host, HostID: hostID}, nil
}
func (r Route) validate() error {
	v, e := NewRoute(r.Origin, r.HostID, true)
	if e != nil || v != r {
		return ErrProtocol
	}
	return nil
}
