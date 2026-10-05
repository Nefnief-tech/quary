// Package nodes owns the panel side of the node trust chain: the internal
// CA, daemon certificate enrollment (single-use tokens + CSRs), and the
// gRPC service daemons connect to.
package nodes

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/internal/seal"
)

// NodeURIScheme identifies node certificates: quarry://node/<uuid>.
const NodeURIScheme = "quarry"

// CA is the panel's internal certificate authority. The private key is
// sealed at rest with the panel's secrets key.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// LoadOrCreateCA reads the CA from dataDir, or generates one on first boot.
func LoadOrCreateCA(dataDir string, box *seal.Box) (*CA, bool, error) {
	certPath := filepath.Join(dataDir, "ca.crt")
	keyPath := filepath.Join(dataDir, "ca.key.sealed")

	certPEM, certErr := os.ReadFile(certPath)
	sealedKey, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		block, _ := pem.Decode(certPEM)
		if block == nil {
			return nil, false, errors.New("ca.crt is not valid PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, false, fmt.Errorf("parse ca.crt: %w", err)
		}
		plain, err := box.Open(sealedKey)
		if err != nil {
			return nil, false, fmt.Errorf("open CA key (wrong QUARRY_SECRETS_KEY?): %w", err)
		}
		key, err := x509.ParseECPrivateKey(plain)
		if err != nil {
			return nil, false, fmt.Errorf("parse CA key: %w", err)
		}
		return &CA{cert: cert, key: key}, false, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, false, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Quarry Panel CA", Organization: []string{"Quarry"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, false, fmt.Errorf("create CA certificate: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, false, err
	}
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certOut, 0o644); err != nil {
		return nil, false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, false, err
	}
	sealed, err := box.Seal(keyDER)
	if err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(keyPath, sealed, 0o600); err != nil {
		return nil, false, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, false, err
	}
	return &CA{cert: cert, key: key}, true, nil
}

// CACertPEM returns the CA certificate for daemons to trust.
func (c *CA) CACertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw})
}

// CertPool returns a pool containing only this CA.
func (c *CA) CertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)
	return pool
}

// IssueServerCert mints the panel's gRPC serving certificate (per boot).
func (c *CA) IssueServerCert(dnsNames []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Quarry Panel gRPC"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("sign serving certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der, c.cert.Raw},
		PrivateKey:  key,
	}, nil
}

// IssueClientCert signs a daemon's CSR, stamping the node identity as a URI
// SAN. SANs from the CSR itself are ignored.
func (c *CA) IssueClientCert(csrPEM []byte, nodeID uuid.UUID) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("csr is not valid PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse csr: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("csr signature invalid: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "node-" + nodeID.String(), Organization: []string{"Quarry Nodes"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{{Scheme: NodeURIScheme, Opaque: "node/" + nodeID.String()}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, csr.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("sign client certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// NodeIDFromCert extracts the node identity from a verified leaf certificate.
func NodeIDFromCert(leaf *x509.Certificate) (uuid.UUID, bool) {
	for _, u := range leaf.URIs {
		if u.Scheme != NodeURIScheme {
			continue
		}
		if idStr, ok := strings.CutPrefix(u.Opaque, "node/"); ok {
			if id, err := uuid.Parse(idStr); err == nil {
				return id, true
			}
		}
	}
	return uuid.Nil, false
}
