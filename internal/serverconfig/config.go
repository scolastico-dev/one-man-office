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
}

// Defaults returns the options used when server.yaml is absent or a key is
// missing.
func Defaults() Config {
	return Config{Host: "127.0.0.1", Port: 8090, Auth: "token", BasicAuth: "", OriginCheck: true}
}

// Keys lists the option names in their documented order.
func Keys() []string {
	return []string{"host", "port", "auth", "basic_auth", "origin_check"}
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
	return nil
}

// Listen returns the TCP address to listen on.
func (c Config) Listen() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load reads path. A missing file yields the defaults; unknown keys and
// invalid values are errors.
func Load(path string) (Config, error) {
	c := Defaults()
	raw, err := os.ReadFile(path)
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
	raw, err := os.ReadFile(path)
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
