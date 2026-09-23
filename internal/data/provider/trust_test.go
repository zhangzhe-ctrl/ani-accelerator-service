package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/tests/testkit"
)

func TestAttestationAndOwnerTrustBoundaries(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "public")
	testkit.Write(t, path, []byte(base64.StdEncoding.EncodeToString(pub)))
	store, err := NewEvidenceStore(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &biz.ProviderBaseline{BaselineID: biz.NewID(), BaselineDigest: strings.Repeat("b", 64), MemoryBlockMiB: 1024, SplitCount: 4, CoreBudgetAdditive: true}
	g := biz.SupplyGroup{GroupID: biz.NewID(), ClusterID: biz.NewID(), ModelKey: "nvidia-test", Nodes: []biz.NodeRef{{UID: "node-1", Name: "node"}}, Baseline: baseline}
	spec := &biz.ProfileSpec{GroupID: g.GroupID, Mode: biz.Shared, ModelKey: g.ModelKey, SharedMemoryMiB: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}
	digest, _ := biz.SpecDigest(spec, baseline.BaselineID)
	a := Attestation{SchemaVersion: 1, Kind: "gpu-hardware-consumption-v1", EvidenceRef: "test:signed", ClusterID: g.ClusterID, GroupID: g.GroupID, BaselineDigest: baseline.BaselineDigest, SpecDigest: digest, NodeUIDs: []string{"node-1"}, Devices: []HardwareDevice{{"node-1", gpuUUID, g.ModelKey, 24576 << 20, 24576}}, VerifiedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), ExperimentManifestDigest: strings.Repeat("e", 64)}
	write := func(a Attestation) {
		payload, _ := biz.Canonical(a)
		signed := SignedAttestation{a, base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-hardware-attestation-v1\n"), payload...)))}
		b, _ := json.Marshal(signed)
		testkit.Write(t, filepath.Join(dir, EvidenceFilename(a.EvidenceRef)), b)
	}
	write(a)
	if _, err = store.Verify(context.Background(), g, spec, a.EvidenceRef); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Attestation){func(a *Attestation) { a.ExpiresAt = a.VerifiedAt }, func(a *Attestation) { a.GroupID = biz.NewID() }, func(a *Attestation) { a.NodeUIDs = []string{"rebuilt-node"} }, func(a *Attestation) {
		a.Devices = []HardwareDevice{{"node-1", gpuUUID, g.ModelKey, 24576 << 20, 24577}}
	}} {
		copy := a
		mutate(&copy)
		write(copy)
		if _, err = store.Verify(context.Background(), g, spec, a.EvidenceRef); err == nil {
			t.Fatal("bad hardware attestation accepted")
		}
	}
	write(a)
	store.public = make(ed25519.PublicKey, 32)
	if _, err = store.Load(a.EvidenceRef); err == nil {
		t.Fatal("wrong signature accepted")
	}
	pod := biz.PodRef{ClusterID: g.ClusterID, Namespace: "test", Name: "pod", UID: "pod-1", ContainerName: "inference"}
	ref := biz.UsageRef{TenantID: biz.NewID(), OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}
	labels := map[string]string{"accelerator.ani.io/resource-tenant-id": ref.TenantID, "accelerator.ani.io/owner-service": ref.OwnerService, "accelerator.ani.io/resource-id": ref.ResourceID, "accelerator.ani.io/create-operation-id": ref.CreateOperationID}
	annotations := map[string]string{"accelerator.ani.io/plan-digest": digest}
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got != nil {
		t.Fatal("ordinary labels trusted")
	}
	payload, _ := biz.Canonical(OwnerAssertion{pod, ref, digest})
	annotations["accelerator.ani.io/owner-signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("acc-owner-pod-v1\n"), payload...)))
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got == nil || *got != ref {
		t.Fatal("valid owner assertion failed")
	}
	pod.UID = "recreated-pod"
	if got, _ := VerifyOwnerAssertion(pod, labels, annotations, pub); got != nil {
		t.Fatal("Pod UID replay accepted")
	}
	for _, raw := range []string{`{"connections":[],"connections":[]}`, `{"connections":[{"ref":"a","ref":"b"}]}`, `{"connections":[]} {}`, `{"unexpected":true}`} {
		var c Config
		if decodeStrict(strings.NewReader(raw), &c) == nil {
			t.Fatal("ambiguous or unknown config accepted", raw)
		}
	}
}
