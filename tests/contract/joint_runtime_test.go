package contract_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	conf "github.com/zhangzhe-ctrl/ani-accelerator-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/server"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Explicitly invoked persistent B fixture. All certificates are task-local and
// the signing CA key and owner signing key are never written to disk.
func TestJointPersistentRuntime(t *testing.T) {
	root := os.Getenv("ACC_JOINT_ROOT")
	if root == "" {
		t.Skip("persistent isolated B helper")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute isolated fixture root required")
	}
	if _, e := os.Stat(filepath.Join(root, "ready.json")); e == nil {
		t.Fatal("existing fixture must be stopped and reviewed before reuse")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	pki := testkit.NewPKI(t)
	ca, e := os.ReadFile(pki.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	pki.Root = root
	pki.CAFile = filepath.Join(root, "ca.pem")
	testkit.Write(t, pki.CAFile, ca)
	cert, key := jointCertificate(t, pki, "acc", "", []string{"ani-accelerator", "accelerator.test"})
	govCert, govKey := jointCertificate(t, pki, "gov", "spiffe://ani.internal/service/ani-governance", []string{"ani-governance"})
	ownerCert, ownerKey := jointCertificate(t, pki, "owner", "spiffe://ani.internal/service/ani-inference", []string{"ani-inference"})
	cursor := make([]byte, 32)
	if _, e = rand.Read(cursor); e != nil {
		t.Fatal(e)
	}
	cursorPath := filepath.Join(root, "cursor.key")
	testkit.Write(t, cursorPath, cursor)
	for _, name := range []string{"owner.pub"} {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		testkit.Write(t, filepath.Join(root, name), []byte(base64.StdEncoding.EncodeToString(pub)))
	}
	providerConfig := filepath.Join(root, "provider.json")
	testkit.Write(t, providerConfig, []byte(`{"connections":[]}`))
	grpcAddr := os.Getenv("ACC_JOINT_ADDR")
	if grpcAddr == "" {
		grpcAddr = "127.0.0.1:29092"
	}
	adminActions := []string{}
	for action := range server.AdminActions {
		adminActions = append(adminActions, action)
	}
	sort.Strings(adminActions)
	tenantActions := []string{}
	for action := range server.TenantActions {
		tenantActions = append(tenantActions, action)
	}
	sort.Strings(tenantActions)
	cfg := &conf.Bootstrap{Server: &conf.Server{Grpc: &conf.Server_GRPC{Network: "tcp", Addr: grpcAddr, Timeout: durationpb.New(5 * time.Second)}, Admin: &conf.Server_Admin{Network: "tcp", Addr: "127.0.0.1:29093", Timeout: durationpb.New(time.Second)}, ShutdownTimeout: durationpb.New(2 * time.Second)}, Database: &conf.Database{DsnFile: os.Getenv("ACC_TEST_DSN_FILE")}, Security: &conf.Security{CaFile: pki.CAFile, CertFile: cert, KeyFile: key, GovernanceUri: "spiffe://ani.internal/service/ani-governance", InferenceUri: "spiffe://ani.internal/service/ani-inference", ResourceUri: "spiffe://ani.internal/service/ani-resource-service", CursorKeyFile: cursorPath, Grants: []*conf.DelegationGrant{{ActorType: "user", ActorId: "1", Actions: adminActions, Platform: true, ClusterIds: []string{"*"}}}}, Accelerator: &conf.Accelerator{MaxReplicas: 16, Freshness: durationpb.New(time.Minute), CollectInterval: durationpb.New(time.Second), ProviderConfigFile: providerConfig, OwnerPublicKeyFile: filepath.Join(root, "owner.pub")}}
	runtime := &testkit.Runtime{Config: cfg, PKI: pki, GovCert: govCert, GovKey: govKey, OwnerCert: ownerCert, OwnerKey: ownerKey}
	connection := "joint-b:" + biz.NewID()
	marker := filepath.Join(root, "interrupt.marker")
	process := startChild(t, runtime, connection, marker)
	t.Setenv("ACC_CONTRACT_CONFIG", runtime.WriteConfig(t))
	t.Setenv("ACC_CONTRACT_CONNECTION", connection)
	t.Setenv("ACC_JOINT_GOV_CERT", govCert)
	t.Setenv("ACC_JOINT_GOV_KEY", govKey)
	t.Setenv("ACC_JOINT_ACTOR_ID", "1")
	t.Setenv("ACC_JOINT_SEED_OUTPUT", filepath.Join(root, "seed.json"))
	TestJointCatalogSeed(t)
	var seed struct {
		Cluster struct {
			ClusterID string `json:"cluster_id"`
		} `json:"cluster"`
	}
	readJointJSON(t, filepath.Join(root, "seed.json"), &seed)
	process.kill(t)
	tenant := os.Getenv("ACC_JOINT_TENANT")
	if tenant == "" {
		tenant = "11111111-1111-4111-8111-111111111111"
	}
	actor := os.Getenv("ACC_JOINT_TENANT_ACTOR")
	if actor == "" {
		actor = "1"
	}
	cfg.Security.Grants = append(cfg.Security.Grants, &conf.DelegationGrant{ActorType: "user", ActorId: actor, Actions: tenantActions, TenantIds: []string{tenant}, ClusterIds: []string{seed.Cluster.ClusterID}})
	process = startChild(t, runtime, connection, marker)
	writeJointJSON(t, filepath.Join(root, "ready.json"), map[string]any{"address": grpcAddr, "server_name": "ani-accelerator", "ca_file": pki.CAFile, "gov_cert_file": govCert, "gov_key_file": govKey, "owner_cert_file": ownerCert, "owner_key_file": ownerKey, "config_file": runtime.WriteConfig(t), "connection_ref": connection, "tenant_id": tenant, "actor_type": "user", "actor_id": "1", "cluster_id": seed.Cluster.ClusterID, "seed_file": filepath.Join(root, "seed.json"), "dsn_file": cfg.Database.DsnFile, "scope": "test-only B layer; no hardware evidence"})
	t.Log("B-layer helper ready; remove only task-owned resources after consumers stop")
	for {
		if _, e = os.Stat(filepath.Join(root, "stop")); e == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	process.kill(t)
}
func jointCertificate(t *testing.T, pki *testkit.PKI, name, identity string, dns []string) (string, string) {
	t.Helper()
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: dns, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	if identity != "" {
		u, e := url.Parse(identity)
		if e != nil {
			t.Fatal(e)
		}
		template.URIs = []*url.URL{u}
	}
	der, e := x509.CreateCertificate(rand.Reader, template, pki.CACert, public, pki.CAKey)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPath, keyPath := filepath.Join(pki.Root, name+".pem"), filepath.Join(pki.Root, name+".key")
	testkit.Write(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	testkit.Write(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	return certPath, keyPath
}
