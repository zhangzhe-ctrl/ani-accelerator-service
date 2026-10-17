package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
)

const qualificationDomain = "acc-hardware-qualification-v2\n"

type qualificationFixture struct {
	store *EvidenceStore
	key   ed25519.PrivateKey
	group biz.SupplyGroup
	spec  *biz.ProfileSpec
	proof Attestation
	now   time.Time
}

func newQualificationFixture(t *testing.T) qualificationFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "public")
	testkit.Write(t, publicPath, []byte(base64.StdEncoding.EncodeToString(pub)))
	store, err := NewEvidenceStore(dir, publicPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), BaselineDigest: strings.Repeat("b", 64), MemoryBlockMiB: 1024, SplitCount: 4, CoreBudgetAdditive: true}
	group := biz.SupplyGroup{GroupID: biz.NewID(), ClusterID: biz.NewID(), ModelKey: "nvidia-test", Nodes: []biz.NodeRef{{UID: "node-1", Name: "node"}}, Baseline: baseline}
	spec := &biz.ProfileSpec{GroupID: group.GroupID, Mode: biz.Shared, ModelKey: group.ModelKey, SharedMemoryMiB: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	digest, err := biz.SpecDigest(spec, baseline.BaselineID)
	if err != nil {
		t.Fatal(err)
	}
	proof := Attestation{SchemaVersion: 2, Kind: "gpu-hardware-qualification-v2", EvidenceRef: "test:qualification", ClusterID: group.ClusterID, GroupID: group.GroupID, BaselineDigest: baseline.BaselineDigest, SpecDigest: digest, NodeUIDs: []string{"node-1"}, Devices: []HardwareDevice{{"node-1", gpuUUID, group.ModelKey, 24576 << 20, 24576}}, VerifiedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ExperimentManifestDigest: strings.Repeat("e", 64)}
	return qualificationFixture{store, key, group, spec, proof, now}
}

func (f qualificationFixture) write(t *testing.T, proof Attestation, domain string) []byte {
	t.Helper()
	payload, err := biz.Canonical(proof)
	if err != nil {
		t.Fatal(err)
	}
	signed := SignedAttestation{Payload: proof, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, append([]byte(domain), payload...)))}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	testkit.Write(t, filepath.Join(f.store.directory, EvidenceFilename(proof.EvidenceRef)), raw)
	return raw
}

func TestHardwareQualificationV2DoesNotExpire(t *testing.T) {
	f := newQualificationFixture(t)
	raw := f.write(t, f.proof, qualificationDomain)
	if strings.Contains(string(raw), `"expires_at"`) {
		t.Error("v2 qualification must omit expires_at")
	}
	for _, advance := range []time.Duration{0, 30 * 24 * time.Hour} {
		t.Run(advance.String(), func(t *testing.T) {
			f.store.now = func() time.Time { return f.now.Add(advance) }
			loaded, err := f.store.Load(f.proof.EvidenceRef)
			if err != nil {
				t.Fatal("baseline qualification rejected", err)
			}
			if loaded.VerifiedAt != f.proof.VerifiedAt || loaded.ExpiresAt != "" {
				t.Fatal("original verification time or non-expiring qualification changed")
			}
			verified, err := f.store.Verify(context.Background(), f.group, f.spec, f.proof.EvidenceRef)
			if err != nil {
				t.Fatal("baseline qualification rejected", err)
			}
			if verified.State != biz.Verified || verified.VerifiedAt != f.proof.VerifiedAt || verified.BaselineDigest != f.proof.BaselineDigest {
				t.Fatal("qualification verification lost its baseline or original time", verified)
			}
		})
	}
}

func TestHardwareQualificationV2RejectsInvalidAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Attestation)
	}{
		{"expiry", func(p *Attestation) { p.ExpiresAt = "2026-10-18T00:00:00Z" }},
		{"future", func(p *Attestation) { p.VerifiedAt = "2026-10-17T00:00:01Z" }},
		{"invalid-time", func(p *Attestation) { p.VerifiedAt = "yesterday" }},
		{"missing-manifest", func(p *Attestation) { p.ExperimentManifestDigest = "" }},
		{"wrong-kind", func(p *Attestation) { p.Kind = "gpu-hardware-consumption-v1" }},
		{"unknown-version", func(p *Attestation) { p.SchemaVersion = 3 }},
		{"cluster", func(p *Attestation) { p.ClusterID = biz.NewID() }},
		{"group", func(p *Attestation) { p.GroupID = biz.NewID() }},
		{"baseline", func(p *Attestation) { p.BaselineDigest = strings.Repeat("c", 64) }},
		{"spec", func(p *Attestation) { p.SpecDigest = strings.Repeat("d", 64) }},
		{"node", func(p *Attestation) { p.NodeUIDs = []string{"rebuilt-node"} }},
		{"device-node", func(p *Attestation) { p.Devices[0].NodeUID = "rebuilt-node" }},
		{"device-uuid", func(p *Attestation) { p.Devices[0].VendorUUID = "not-a-gpu" }},
		{"device-model", func(p *Attestation) { p.Devices[0].ModelKey = "another-model" }},
		{"duplicate-device", func(p *Attestation) { p.Devices = append(p.Devices, p.Devices[0]) }},
		{"missing-device", func(p *Attestation) { p.Devices = nil }},
		{"safe-budget", func(p *Attestation) { p.Devices[0].SafeMemoryMiB++ }},
		{"physical-budget", func(p *Attestation) { p.Devices[0].PhysicalBytes = 0 }},
		{"split-budget", func(p *Attestation) { p.Devices[0].SafeMemoryMiB = 24575 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQualificationFixture(t)
			proof := f.proof
			proof.Devices = append([]HardwareDevice{}, proof.Devices...)
			tc.mutate(&proof)
			f.write(t, proof, qualificationDomain)
			if _, err := f.store.Verify(context.Background(), f.group, f.spec, proof.EvidenceRef); err == nil {
				t.Fatal("invalid hardware authority accepted")
			}
		})
	}
}

func TestHardwareQualificationV2PreservesTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		domain string
		mutate func([]byte) []byte
	}{
		{"v1-domain", "acc-hardware-attestation-v1\n", func(raw []byte) []byte { return raw }},
		{"tampered-device", qualificationDomain, func(raw []byte) []byte { return []byte(strings.Replace(string(raw), gpuUUID, gpuUUID2, 1)) }},
		{"tampered-ref", qualificationDomain, func(raw []byte) []byte {
			return []byte(strings.Replace(string(raw), "test:qualification", "test:tampered", 1))
		}},
		{"unknown-field", qualificationDomain, func(raw []byte) []byte { return append([]byte(`{"unexpected":true,`), raw[1:]...) }},
		{"duplicate-field", qualificationDomain, func(raw []byte) []byte {
			return []byte(strings.Replace(string(raw), `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1))
		}},
		{"trailing-document", qualificationDomain, func(raw []byte) []byte { return append(raw, []byte(` {}`)...) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQualificationFixture(t)
			raw := f.write(t, f.proof, tc.domain)
			testkit.Write(t, filepath.Join(f.store.directory, EvidenceFilename(f.proof.EvidenceRef)), tc.mutate(raw))
			if _, err := f.store.Load(f.proof.EvidenceRef); err == nil {
				t.Fatal("untrusted or ambiguous qualification accepted")
			}
		})
	}
}

func TestHardwareQualificationPreservesV1Expiry(t *testing.T) {
	f := newQualificationFixture(t)
	proof := f.proof
	proof.SchemaVersion, proof.Kind = 1, "gpu-hardware-consumption-v1"
	proof.ExpiresAt = f.now.Add(24 * time.Hour).Format(time.RFC3339Nano)
	f.write(t, proof, "acc-hardware-attestation-v1\n")
	if _, err := f.store.Verify(context.Background(), f.group, f.spec, proof.EvidenceRef); err != nil {
		t.Fatal("legacy signed attestation rejected before expiry", err)
	}
	f.store.now = func() time.Time { return f.now.Add(30 * 24 * time.Hour) }
	if _, err := f.store.Load(proof.EvidenceRef); err == nil || err.Error() != "ATTESTATION_EXPIRED" {
		t.Fatal("legacy expired authority was silently extended", err)
	}
	proof.ExpiresAt = ""
	f.write(t, proof, "acc-hardware-attestation-v1\n")
	if _, err := f.store.Load(proof.EvidenceRef); err == nil {
		t.Fatal("legacy authority without expiry accepted")
	}
	if err := os.Remove(filepath.Join(f.store.directory, EvidenceFilename(proof.EvidenceRef))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Load(proof.EvidenceRef); err == nil || err.Error() != "CONSUMPTION_NOT_VERIFIED" {
		t.Fatal("missing qualification accepted", err)
	}
}
