package certgen

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate_ECDSA(t *testing.T) {
	dir := t.TempDir()
	res, err := Generate(Opts{Host: "memora.local", OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	verifyCert(t, res, dir, "memora.local")
}

func TestGenerate_RSA(t *testing.T) {
	dir := t.TempDir()
	res, err := Generate(Opts{Host: "memora.local", OutputDir: dir, UseRSA: true})
	if err != nil {
		t.Fatal(err)
	}
	verifyCert(t, res, dir, "memora.local")
}

func TestGenerate_ExtraSANs(t *testing.T) {
	dir := t.TempDir()
	res, err := Generate(Opts{
		Host:      "node1.memora.internal",
		OutputDir: dir,
		ExtraSANs: []string{"10.0.0.5", "node1-alt.memora.internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, res.CertPath)
	found := map[string]bool{}
	for _, ip := range cert.IPAddresses {
		found[ip.String()] = true
	}
	for _, dns := range cert.DNSNames {
		found[dns] = true
	}
	for _, want := range []string{"127.0.0.1", "::1", "node1.memora.internal", "10.0.0.5", "node1-alt.memora.internal"} {
		if !found[want] {
			t.Errorf("missing SAN: %s (have %v)", want, found)
		}
	}
}

func TestGenerate_MissingHost(t *testing.T) {
	_, err := Generate(Opts{OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestGenerate_KeyPermissions(t *testing.T) {
	dir := t.TempDir()
	_, err := Generate(Opts{Host: "test.local", OutputDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		t.Errorf("key file too permissive: %04o (want 0600)", perm)
	}
}

func TestConfigSnippet(t *testing.T) {
	r := Result{CertPath: "/etc/memora/server.crt", KeyPath: "/etc/memora/server.key"}
	snippet := r.ConfigSnippet()
	if snippet == "" {
		t.Fatal("empty snippet")
	}
}

func verifyCert(t *testing.T, res Result, dir, host string) {
	t.Helper()
	if res.CertPath != filepath.Join(dir, "server.crt") {
		t.Errorf("unexpected cert path: %s", res.CertPath)
	}
	if res.KeyPath != filepath.Join(dir, "server.key") {
		t.Errorf("unexpected key path: %s", res.KeyPath)
	}
	if len(res.Fingerprint) != 64 {
		t.Errorf("unexpected fingerprint length: %d", len(res.Fingerprint))
	}

	_, err := tls.LoadX509KeyPair(res.CertPath, res.KeyPath)
	if err != nil {
		t.Fatalf("cert/key pair invalid: %v", err)
	}

	cert := parseCert(t, res.CertPath)
	if cert.Subject.CommonName != host {
		t.Errorf("CN=%s, want %s", cert.Subject.CommonName, host)
	}
}

func parseCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
