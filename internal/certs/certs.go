// Package certs issues the local certificates Pier serves for .local names.
//
// One certificate authority per user signs one leaf per project. The CA is
// name-constrained to .local, so its key cannot mint a certificate that any
// browser would accept for another domain.
package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 397 * 24 * time.Hour
	renewBefore  = 30 * 24 * time.Hour

	caCertFile   = "ca.pem"
	caKeyFile    = "ca-key.pem"
	leafCertFile = "cert.pem"
	leafKeyFile  = "key.pem"
)

// PermittedDomain is the only DNS suffix the Pier CA can sign.
const PermittedDomain = "local"

// Authority is the per-user CA that signs project certificates.
type Authority struct {
	Cert    *x509.Certificate
	CertPEM []byte
	key     *ecdsa.PrivateKey
	dir     string
}

// CertPath is the CA certificate file that trust stores and phones install.
func (a *Authority) CertPath() string { return filepath.Join(a.dir, caCertFile) }

// Fingerprint identifies this CA in trust markers.
func (a *Authority) Fingerprint() string { return Fingerprint(a.Cert) }

// DefaultCADir is the per-user CA location.
func DefaultCADir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "pier", "ca"), nil
}

// LeafDir is where a project's certificate and key live.
func LeafDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".pier", "certs")
}

// LoadOrCreateCA returns the CA in dir, creating it when it is missing or expired.
// created reports that a new CA was written and must be trusted again.
func LoadOrCreateCA(dir string, now time.Time) (*Authority, bool, error) {
	ca, err := loadCA(dir, now)
	if err == nil {
		return ca, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errExpired) {
		return nil, false, err
	}
	ca, err = createCA(dir, now)
	if err != nil {
		return nil, false, err
	}
	return ca, true, nil
}

// LoadCA reads an existing CA without creating one.
func LoadCA(dir string, now time.Time) (*Authority, error) {
	return loadCA(dir, now)
}

var errExpired = errors.New("certificate expires soon")

func loadCA(dir string, now time.Time) (*Authority, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, caCertFile))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, caKeyFile))
	if err != nil {
		return nil, err
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, fmt.Errorf("read Pier CA: %w", err)
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("read Pier CA key: %w", err)
	}
	if !cert.IsCA || now.Add(renewBefore).After(cert.NotAfter) {
		return nil, errExpired
	}
	return &Authority{Cert: cert, CertPEM: certPEM, key: key, dir: dir}, nil
}

func createCA(dir string, now time.Time) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Pier CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:                serial,
		Subject:                     pkix.Name{CommonName: caName(), Organization: []string{"Pier local development"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(caValidity),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{PermittedDomain},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create Pier CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM, err := encodeKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create Pier CA directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, caKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, caCertFile), certPEM, 0o644); err != nil {
		return nil, err
	}
	return &Authority{Cert: cert, CertPEM: certPEM, key: key, dir: dir}, nil
}

func caName() string {
	host, _ := os.Hostname()
	name := "Pier Local CA"
	if current, err := user.Current(); err == nil && current.Username != "" {
		username := current.Username
		if i := strings.LastIndexAny(username, `\/`); i >= 0 {
			username = username[i+1:]
		}
		if host != "" {
			return name + " (" + username + "@" + host + ")"
		}
		return name + " (" + username + ")"
	}
	if host != "" {
		return name + " (" + host + ")"
	}
	return name
}

// EnsureLeaf makes dir hold a certificate for exactly names, signed by a.
// An existing leaf is kept while it covers the same names, verifies against a,
// and has more than 30 days left. changed reports that files were rewritten.
func (a *Authority) EnsureLeaf(dir string, names []string, now time.Time) (changed bool, err error) {
	names = normalizeNames(names)
	if len(names) == 0 {
		return false, nil
	}
	if a.leafIsCurrent(dir, names, now) {
		return false, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generate certificate key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return false, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Pier local development"}},
		DNSNames:     names,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.Cert, &key.PublicKey, a.key)
	if err != nil {
		return false, fmt.Errorf("sign certificate for %s: %w", strings.Join(names, ", "), err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain := append(certPEM, a.CertPEM...)
	keyPEM, err := encodeKey(key)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create certificate directory: %w", err)
	}
	if err := writeFile(filepath.Join(dir, leafKeyFile), keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := writeFile(filepath.Join(dir, leafCertFile), chain, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Authority) leafIsCurrent(dir string, names []string, now time.Time) bool {
	certPEM, err := os.ReadFile(filepath.Join(dir, leafCertFile))
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, leafKeyFile)); err != nil {
		return false
	}
	leaf, err := parseCert(certPEM)
	if err != nil {
		return false
	}
	if now.Add(renewBefore).After(leaf.NotAfter) {
		return false
	}
	if !slices.Equal(normalizeNames(leaf.DNSNames), names) {
		return false
	}
	return a.Verify(leaf, now) == nil
}

// Verify checks that leaf chains to a for server authentication.
func (a *Authority) Verify(leaf *x509.Certificate, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(a.Cert)
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

// Sign issues a leaf for names without writing files. Tests use it to check
// that the CA's name constraint rejects names outside .local.
func (a *Authority) Sign(names []string, now time.Time) (*x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		DNSNames:     names,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.Cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// LoadLeaf reads the TLS key pair a project serves.
func LoadLeaf(dir string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(filepath.Join(dir, leafCertFile), filepath.Join(dir, leafKeyFile))
}

// LeafModTime reports when a project's certificate last changed, for reloads.
func LeafModTime(dir string) (time.Time, error) {
	info, err := os.Stat(filepath.Join(dir, leafCertFile))
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// Fingerprint is the lowercase hex SHA-256 of cert's DER bytes.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// ReadCertPEM parses the first certificate in a PEM file.
func ReadCertPEM(path string) (*x509.Certificate, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseCert(contents)
}

func normalizeNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
		if name != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func parseCert(contents []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(contents)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKey(contents []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, errors.New("no PEM key found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not ECDSA")
	}
	return ecKey, nil
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func newSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial, nil
}

// writeFile replaces path atomically so a reader never sees half a key.
func writeFile(path string, contents []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, contents) {
		return os.Chmod(path, mode)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
