package serverconfig

import (
	"fmt"
	"net"
	"strings"
)

// Forward ids are 32 lowercase hexadecimal characters. Checking a sample of
// that fixed shape validates both fragment boundaries and the final label size.
func (c Config) validateProxy() error {
	label := c.ProxyLabelPrefix + strings.Repeat("a", 32) + c.ProxyLabelSuffix
	if !hostLabel.MatchString(label) {
		return fmt.Errorf("proxy_label_prefix and proxy_label_suffix must compose one DNS label with the 32-character forward id (at most 63 characters, alphanumeric ends)")
	}
	if c.ProxyBaseHost == "" {
		return nil
	}
	base := c.ProxyBaseHost
	if net.ParseIP(base) != nil || !strings.Contains(base, ".") || strings.ContainsAny(base, ":/\\") {
		return fmt.Errorf("proxy_base_host must be a DNS host with at least two labels, without scheme, port, path or wildcard")
	}
	if err := ValidateAllowedHost(base); err != nil {
		return fmt.Errorf("proxy_base_host: %w", err)
	}
	if len(label)+1+len(base) > 253 {
		return fmt.Errorf("proxy_base_host with composed forward label must not exceed 253 characters")
	}
	// Only an exact composed forward host conflicts with a board alias.
	// Other explicitly listed board hosts retain their existing meaning.
	if p, ok := c.Public(); ok && c.ProxyForwardID(p.Host) != "" {
		return fmt.Errorf("public_url conflicts with a composed proxy forward host")
	}
	for _, entry := range c.AllowedHosts {
		name, _, _ := SplitAllowedHost(entry)
		if c.ProxyForwardID(name) != "" {
			return fmt.Errorf("allowed_hosts conflicts with a composed proxy forward host")
		}
	}
	return nil
}

// ProxyForwardID recognizes exactly one prefix+32-hex-id+suffix label below
// the configured base. Registry existence and accepted ports are server checks.
// DNS host and label fragments match case-insensitively.
func (c Config) ProxyForwardID(host string) string {
	if c.ProxyBaseHost == "" {
		return ""
	}
	label, ok := strings.CutSuffix(strings.ToLower(host), "."+strings.ToLower(c.ProxyBaseHost))
	if !ok {
		return ""
	}
	label, ok = strings.CutPrefix(label, strings.ToLower(c.ProxyLabelPrefix))
	if !ok {
		return ""
	}
	label, ok = strings.CutSuffix(label, strings.ToLower(c.ProxyLabelSuffix))
	if !ok || len(label) != 32 {
		return ""
	}
	for _, ch := range label {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return ""
		}
	}
	return label
}
