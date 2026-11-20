package relayproto

import (
	"crypto/mlkem"
	"encoding/hex"
)

// Identity contains a local ML-KEM-768 decapsulation key. Seed never goes on wire.
type Identity struct{ key *mlkem.DecapsulationKey768 }

func GenerateIdentity() (*Identity, error) {
	k, e := mlkem.GenerateKey768()
	if e != nil {
		return nil, e
	}
	return &Identity{key: k}, nil
}
func ImportIdentity(seed []byte) (*Identity, error) {
	k, e := mlkem.NewDecapsulationKey768(seed)
	if e != nil {
		return nil, ErrProtocol
	}
	return &Identity{key: k}, nil
}
func (i *Identity) Seed() []byte {
	if i == nil || i.key == nil {
		return nil
	}
	return i.key.Bytes()
}
func (i *Identity) PublicKey() []byte {
	if i == nil || i.key == nil {
		return nil
	}
	return i.key.EncapsulationKey().Bytes()
}
func ValidatePublicKey(pk []byte) error {
	_, e := mlkem.NewEncapsulationKey768(pk)
	if e != nil {
		return ErrProtocol
	}
	return nil
}

// Fingerprint is role separated; routing ids and fingerprints confer no authority.
func Fingerprint(role string, pk []byte) (string, error) {
	if role != "host" && role != "ui" {
		return "", ErrProtocol
	}
	if e := ValidatePublicKey(pk); e != nil {
		return "", e
	}
	return hex.EncodeToString(hashFields([]byte(label+role+"-key"), pk)), nil
}
