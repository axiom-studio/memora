// Package certgen generates self-signed TLS certificates for memora-core.
package certgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Opts controls certificate generation.
type Opts struct {
	Host      string
	OutputDir string
	UseRSA    bool
	ExtraSANs []string
	Validity  time.Duration
}

// Result is returned after successful generation.
type Result struct {
	CertPath    string
	KeyPath     string
	Fingerprint string
}

func (o *Opts) defaults() {
	if o.OutputDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			o.OutputDir = filepath.Join(home, ".memora", "tls")
		} else {
			o.OutputDir = "./tls"
		}
	}
	if o.Validity == 0 {
		o.Validity = 365 * 24 * time.Hour
	}
}

// Generate creates a self-signed certificate and private key.
func Generate(opts Opts) (Result, error) {
	opts.defaults()
	if opts.Host == "" {
		return Result{}, fmt.Errorf("--host is required")
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir %s: %w", opts.OutputDir, err)
	}

	var privKey any
	var pubKey any
	if opts.UseRSA {
		k, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return Result{}, fmt.Errorf("generate RSA key: %w", err)
		}
		privKey = k
		pubKey = &k.PublicKey
	} else {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return Result{}, fmt.Errorf("generate ECDSA key: %w", err)
		}
		privKey = k
		pubKey = &k.PublicKey
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return Result{}, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now().UTC()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: opts.Host, Organization: []string{"Memora (self-signed)"}},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(opts.Validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	sans := collectSANs(opts.Host, opts.ExtraSANs)
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, san)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, pubKey, privKey)
	if err != nil {
		return Result{}, fmt.Errorf("create certificate: %w", err)
	}

	fingerprint := sha256.Sum256(certDER)
	fpHex := hex.EncodeToString(fingerprint[:])

	certPath := filepath.Join(opts.OutputDir, "server.crt")
	keyPath := filepath.Join(opts.OutputDir, "server.key")

	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return Result{}, fmt.Errorf("create cert file: %w", err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return Result{}, fmt.Errorf("encode cert: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return Result{}, fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("create key file: %w", err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}); err != nil {
		return Result{}, fmt.Errorf("encode key: %w", err)
	}

	return Result{CertPath: certPath, KeyPath: keyPath, Fingerprint: fpHex}, nil
}

func collectSANs(host string, extra []string) []string {
	seen := map[string]bool{}
	var result []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	add(host)
	add("127.0.0.1")
	add("::1")
	if net.ParseIP(host) == nil {
		add("*." + host)
	}
	for _, s := range extra {
		add(s)
	}
	return result
}

// ConfigSnippet returns a TOML snippet for the config file.
func (r Result) ConfigSnippet() string {
	return fmt.Sprintf(`[server.tls]
enabled = true
cert_file = %q
key_file = %q`, r.CertPath, r.KeyPath)
}
