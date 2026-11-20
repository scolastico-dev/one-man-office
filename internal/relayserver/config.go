// Package relayserver routes opaque relay protocol messages without omo state.
package relayserver

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
)

// Config contains only relay listener and admission settings. PasswordFile can
// point at a Docker secret; its contents never enter endpoint messages.
type Config struct {
	ListenAddr   string
	PasswordFile string
}

func passwordHash(path string) ([32]byte, bool, error) {
	if path == "" {
		return [32]byte{}, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, false, errors.New("cannot read relay password file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return [32]byte{}, false, errors.New("relay password file must be regular")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	secret := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if err != nil || len(b) > 4096 || secret == "" || strings.ContainsAny(secret, "\r\n\x00") {
		return [32]byte{}, false, errors.New("invalid relay password file")
	}
	return sha256.Sum256([]byte(secret)), true, nil
}
