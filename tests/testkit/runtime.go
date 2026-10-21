// Package testkit is imported exclusively by tests, never production wiring.
package testkit

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
)

type PKI struct {
	Root   string
	CAFile string
	CACert *x509.Certificate
	CAKey  ed25519.PrivateKey
}

func NewPKI(t testing.TB) *PKI {
	t.Helper()
	root := t.TempDir()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ACC-V12 isolated test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ca.pem")
	Write(t, path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return &PKI{root, path, cert, key}
}
func (p *PKI) Issue(t testing.TB, name, identity string, server bool) (string, string) {
	t.Helper()
	dns := []string{}
	if identity != "" {
		dns = append(dns, identity)
	}
	if server {
		dns = []string{"accelerator.test"}
	}
	return p.IssueWithSAN(t, name, dns, nil, nil, nil, server)
}

// IssueWithSAN constructs explicit malformed identities for transport rejection
// tests as well as the single-DNS identities used by ordinary test clients.
func (p *PKI) IssueWithSAN(t testing.TB, name string, dns []string, uris []*url.URL, ips []net.IP, emails []string, server bool) (string, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: dns, URIs: uris, IPAddresses: ips, EmailAddresses: emails, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, p.CACert, pub, p.CAKey)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(p.Root, name+".pem"), filepath.Join(p.Root, name+".key")
	Write(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	Write(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	return certFile, keyFile
}
func Write(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func ClientTLS(t testing.TB, ca, certFile, keyFile string) *tls.Config {
	t.Helper()
	bytes, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bytes) {
		t.Fatal("invalid test CA")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "accelerator.test"}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg
}

type Runtime struct {
	Config                               *conf.Bootstrap
	PKI                                  *PKI
	GovCert, GovKey, OwnerCert, OwnerKey string
}

func NewRuntime(t testing.TB, grpcAddr, adminAddr string) *Runtime {
	t.Helper()
	dsn := os.Getenv("ACC_TEST_DSN_FILE")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL and test-generated mTLS: ACC_TEST_DSN_FILE")
	}
	pki := NewPKI(t)
	cert, key := pki.Issue(t, "server", "", true)
	govCert, govKey := pki.Issue(t, "governance", "ani-governance", false)
	ownerCert, ownerKey := pki.Issue(t, "inference", "ani-inference-service", false)
	cursor := filepath.Join(pki.Root, "cursor-key")
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	Write(t, cursor, random)
	providerFile := filepath.Join(pki.Root, "provider.json")
	Write(t, providerFile, []byte(`{"connections":[]}`))
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ownerPublicFile := filepath.Join(pki.Root, "owner.pub")
	Write(t, ownerPublicFile, []byte(base64.StdEncoding.EncodeToString(pub)))
	config := &conf.Bootstrap{Server: &conf.Server{Grpc: &conf.Server_GRPC{Network: "tcp", Addr: grpcAddr, Timeout: durationpb.New(3 * time.Second)}, Admin: &conf.Server_Admin{Network: "tcp", Addr: adminAddr, Timeout: durationpb.New(time.Second)}, ShutdownTimeout: durationpb.New(3 * time.Second)}, Database: &conf.Database{DsnFile: dsn}, Security: &conf.Security{CaFile: pki.CAFile, CertFile: cert, KeyFile: key, GovernanceDnsName: "ani-governance", InferenceDnsName: "ani-inference-service", ResourceDnsName: "ani-resource-service", ModeldevDnsName: "ani-modeldev-service", CursorKeyFile: cursor}, Accelerator: &conf.Accelerator{MaxReplicas: 16, Freshness: durationpb.New(time.Minute), CollectInterval: durationpb.New(time.Second), ProviderConfigFile: providerFile, OwnerPublicKeyFile: ownerPublicFile}}
	return &Runtime{config, pki, govCert, govKey, ownerCert, ownerKey}
}
func (r *Runtime) WriteConfig(t testing.TB) string {
	t.Helper()
	path := filepath.Join(r.PKI.Root, "config.json")
	b, err := protojson.Marshal(r.Config)
	if err != nil {
		t.Fatal(err)
	}
	Write(t, path, b)
	return path
}
