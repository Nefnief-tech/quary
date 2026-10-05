// Package state persists the daemon identity on disk. The private key never
// leaves this directory; identity files are 0600.
package state

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Identity is the daemon's enrolled credential set.
type Identity struct {
	NodeID  uuid.UUID `json:"node_id"`
	CertPEM []byte    `json:"cert_pem"`
	KeyPEM  []byte    `json:"key_pem"`
}

type Store struct {
	dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) identityPath() string {
	return filepath.Join(s.dir, "identity.json")
}

// Load returns the stored identity, or nil if the daemon is not enrolled.
func (s *Store) Load() (*Identity, error) {
	raw, err := os.ReadFile(s.identityPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read identity: %w", err)
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("parse identity: %w", err)
	}
	return &id, nil
}

// Save persists the identity with owner-only permissions.
func (s *Store) Save(id *Identity) error {
	raw, err := json.Marshal(id)
	if err != nil {
		return err
	}
	tmp := s.identityPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	return os.Rename(tmp, s.identityPath())
}

// LoadOrCreateKey returns the daemon's long-term key, generating and storing
// it on first boot. The key exists before enrollment so the CSR can be
// signed by the same key that later holds the certificate.
func (s *Store) LoadOrCreateKey() (cryptoKey, bool, error) {
	id, err := s.Load()
	if err != nil {
		return nil, false, err
	}
	if id != nil && len(id.KeyPEM) > 0 {
		key, err := decodeKey(id.KeyPEM)
		if err == nil {
			return key, false, nil
		}
		// Corrupt key material: regenerate (a re-enroll is required anyway).
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, false, err
	}
	if err := s.Save(&Identity{KeyPEM: pem.EncodeToMemory(&pem.Block{
		Type: "EC PRIVATE KEY", Bytes: der,
	})}); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// Clear removes the identity (e.g. after a failed enrollment leaves it
// unusable).
func (s *Store) Clear() error {
	err := os.Remove(s.identityPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type cryptoKey = *ecdsa.PrivateKey

func decodeKey(pemBytes []byte) (cryptoKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block in stored key")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}
