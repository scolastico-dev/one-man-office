// Package serverconfig reads and writes the start options of the omo server
// (server.yaml) and the persistent login token.
package serverconfig

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"gopkg.in/yaml.v3"
)

// Config holds the options that decide how the server listens and who may
// connect.
type Config struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Auth        string `yaml:"auth"`
	BasicAuth   string `yaml:"basic_auth"`
	OriginCheck bool   `yaml:"origin_check"`
	// AllowedHosts lists extra host names the user trusts in the Host and
	// Origin headers, each "name" (any port, or none) or "name:port".
	AllowedHosts []string `yaml:"allowed_hosts"`
}

// Defaults returns the options used when server.yaml is absent or a key is
// missing.
func Defaults() Config {
	return Config{Host: "127.0.0.1", Port: 8090, Auth: "token", BasicAuth: "", OriginCheck: true}
}

// Keys lists the option names in their documented order.
func Keys() []string {
	return []string{"host", "port", "auth", "basic_auth", "origin_check", "allowed_hosts"}
}

// Validate checks every option and the relations between them.
func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	switch c.Auth {
	case "token", "none":
		// basic_auth may stay set so switching back to basic needs no retyping.
	case "basic":
		user, pass, ok := strings.Cut(c.BasicAuth, ":")
		if !ok || user == "" || pass == "" {
			return errors.New("basic_auth must be USER:PASSWORD when auth is basic")
		}
	default:
		return fmt.Errorf("auth must be token, basic or none, got %q", c.Auth)
	}
	if c.Host != "localhost" && net.ParseIP(c.Host) == nil {
		return fmt.Errorf("host must be an IP address or localhost, got %q", c.Host)
	}
	for _, h := range c.AllowedHosts {
		if err := ValidateAllowedHost(h); err != nil {
			return fmt.Errorf("allowed_hosts: %w", err)
		}
	}
	return nil
}

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// SplitAllowedHost splits an allowed_hosts entry into its name and optional
// port ("" when the entry has none). IPv6 literals are written in brackets
// when they carry a port. ok is false for a malformed entry.
func SplitAllowedHost(entry string) (name, port string, ok bool) {
	switch {
	case net.ParseIP(entry) != nil:
		return entry, "", true
	case strings.HasPrefix(entry, "["):
		if h, p, err := net.SplitHostPort(entry); err == nil {
			return h, p, p != ""
		}
		if strings.HasSuffix(entry, "]") {
			return entry[1 : len(entry)-1], "", true
		}
		return "", "", false
	}
	name, port, hasPort := strings.Cut(entry, ":")
	return name, port, !hasPort || port != ""
}

// ValidateAllowedHost checks one allowed_hosts entry: a host name or IP
// literal with an optional numeric port, without scheme, path or wildcard.
func ValidateAllowedHost(entry string) error {
	if entry == "" {
		return errors.New("entry must not be empty")
	}
	if strings.ContainsAny(entry, "*/?#@ \t") || strings.Contains(entry, "://") {
		return fmt.Errorf("%q must be a bare host name with an optional port (no scheme, path or wildcard)", entry)
	}
	name, port, ok := SplitAllowedHost(entry)
	if !ok {
		return fmt.Errorf("%q is not a valid host or has an empty port", entry)
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return fmt.Errorf("%q has an invalid port", entry)
		}
	}
	if net.ParseIP(name) != nil {
		return nil
	}
	if strings.HasPrefix(entry, "[") || len(name) > 253 {
		return fmt.Errorf("%q is not a valid host", entry)
	}
	for _, l := range strings.Split(name, ".") {
		if !hostLabel.MatchString(l) {
			return fmt.Errorf("%q is not a valid host name", entry)
		}
	}
	return nil
}

// ParseAllowedHosts reads the comma-separated form used on the command line;
// an empty value is an empty list.
func ParseAllowedHosts(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// FormatAllowedHosts is the comma-separated form of the list.
func FormatAllowedHosts(hosts []string) string { return strings.Join(hosts, ",") }

// Listen returns the TCP address to listen on.
func (c Config) Listen() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load reads path. A missing file yields the defaults; unknown keys and
// invalid values are errors.
func Load(path string) (Config, error) {
	c := Defaults()
	raw, err := homefile.Read(path, homefile.MaxConfigFile)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && err != io.EOF {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("%s must contain exactly one YAML document", path)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return globalhome.WriteFileAtomic(path, data, 0600)
}

// Set changes one option and writes the file. The whole result is validated
// before anything is written.
func Set(path, key, value string) (Config, error) {
	c, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	switch key {
	case "host":
		c.Host = value
	case "port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("port must be a number, got %q", value)
		}
		c.Port = n
	case "auth":
		c.Auth = value
	case "basic_auth":
		c.BasicAuth = value
	case "origin_check":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("origin_check must be true or false, got %q", value)
		}
		c.OriginCheck = b
	case "allowed_hosts":
		c.AllowedHosts = ParseAllowedHosts(value)
	default:
		return Config{}, unknownKey(key)
	}
	if err := save(path, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Unset resets one option to its default and writes the file.
func Unset(path, key string) (Config, error) {
	c, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	d := Defaults()
	switch key {
	case "host":
		c.Host = d.Host
	case "port":
		c.Port = d.Port
	case "auth":
		c.Auth = d.Auth
	case "basic_auth":
		c.BasicAuth = d.BasicAuth
	case "origin_check":
		c.OriginCheck = d.OriginCheck
	case "allowed_hosts":
		c.AllowedHosts = nil
	default:
		return Config{}, unknownKey(key)
	}
	if err := save(path, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func unknownKey(key string) error {
	return fmt.Errorf("unknown key %q (valid keys: %s)", key, strings.Join(Keys(), ", "))
}

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// LoadOrCreateToken returns the stored login token and creates it on first
// use.
func LoadOrCreateToken(path string) (string, error) {
	raw, err := homefile.Read(path, homefile.MaxTokenFile)
	if os.IsNotExist(err) {
		return RotateToken(path)
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if !tokenPattern.MatchString(token) {
		return "", fmt.Errorf("%s is not a valid login token (expected 64 hex characters); run `omo config rotate-token` to replace it", filepath.Base(path))
	}
	return token, nil
}

// RotateToken writes a new login token and returns it.
func RotateToken(path string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := globalhome.WriteFileAtomic(path, []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	return token, nil
}

// AllowedHostsNote returns a one-line hint when an allowed_hosts entry names
// port 80 or 443, or "" when no entry does. Browsers omit the default port
// from the Host header, so such an entry never matches; the entry without a
// port is the one for a proxy on the default port.
func AllowedHostsNote(entries []string) string {
	for _, e := range entries {
		if _, port, ok := SplitAllowedHost(e); ok && (port == "80" || port == "443") {
			return "note: " + e + ": browsers omit the default port; use `name` without a port for a proxy on the default port"
		}
	}
	return ""
}
