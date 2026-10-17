package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type HardwareDevice struct {
	NodeUID       string `json:"node_uid"`
	VendorUUID    string `json:"vendor_uuid"`
	ModelKey      string `json:"model_key"`
	PhysicalBytes uint64 `json:"physical_bytes"`
	SafeMemoryMiB int64  `json:"safe_memory_mib"`
}
type Attestation struct {
	SchemaVersion  int              `json:"schema_version"`
	Kind           string           `json:"kind"`
	EvidenceRef    string           `json:"evidence_ref"`
	ClusterID      string           `json:"cluster_id"`
	GroupID        string           `json:"group_id"`
	BaselineDigest string           `json:"baseline_digest"`
	SpecDigest     string           `json:"spec_digest"`
	NodeUIDs       []string         `json:"node_uids"`
	Devices        []HardwareDevice `json:"devices"`
	VerifiedAt     string           `json:"verified_at"`
	ExpiresAt      string           `json:"expires_at,omitempty"`
	// Immutable raw experiment manifest digest, not an administrator's boolean.
	ExperimentManifestDigest string `json:"experiment_manifest_digest"`
}
type SignedAttestation struct {
	Payload   Attestation `json:"payload"`
	Signature string      `json:"signature"`
}
type EvidenceStore struct {
	directory string
	public    ed25519.PublicKey
	now       func() time.Time
}

func NewEvidenceStore(directory, keyFile string) (*EvidenceStore, error) {
	if !filepath.IsAbs(directory) {
		return nil, biz.Invalid("ATTESTATION_DIRECTORY_REQUIRED")
	}
	key, err := ReadPublicKey(keyFile)
	if err != nil {
		return nil, err
	}
	return &EvidenceStore{directory: directory, public: key, now: time.Now}, nil
}
func ReadPublicKey(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, biz.Invalid("PUBLIC_KEY_UNAVAILABLE")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, biz.Invalid("PUBLIC_KEY_INVALID")
	}
	return ed25519.PublicKey(key), nil
}
func EvidenceFilename(ref string) string {
	h := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(h[:]) + ".json"
}
func (s *EvidenceStore) Load(ref string) (*Attestation, error) {
	if !biz.Ref(ref) {
		return nil, biz.Invalid("INVALID_EVIDENCE_REF")
	}
	f, err := os.Open(filepath.Join(s.directory, EvidenceFilename(ref)))
	if err != nil {
		return nil, biz.Precondition("CONSUMPTION_NOT_VERIFIED")
	}
	defer f.Close()
	var signed SignedAttestation
	if decodeStrict(f, &signed) != nil {
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	p := signed.Payload
	domain := ""
	switch {
	case p.SchemaVersion == 1 && p.Kind == "gpu-hardware-consumption-v1":
		domain = "acc-hardware-attestation-v1\n"
	case p.SchemaVersion == 2 && p.Kind == "gpu-hardware-qualification-v2":
		domain = "acc-hardware-qualification-v2\n"
	default:
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	sig, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	bytes, err := biz.Canonical(signed.Payload)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(s.public, append([]byte(domain), bytes...), sig) {
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	if p.EvidenceRef != ref || !biz.Digest(p.ExperimentManifestDigest) || !biz.ValidTime(p.VerifiedAt) {
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	verified, _ := time.Parse(time.RFC3339Nano, p.VerifiedAt)
	now := s.now()
	// V1 retains its signed expiry. V2 is an explicit baseline qualification;
	// elapsed time does not extend or invalidate the original hardware result.
	if p.SchemaVersion == 1 {
		if !biz.ValidTime(p.ExpiresAt) {
			return nil, biz.Precondition("ATTESTATION_INVALID")
		}
		expires, _ := time.Parse(time.RFC3339Nano, p.ExpiresAt)
		if verified.After(now) || !expires.After(now) || !expires.After(verified) {
			return nil, biz.Precondition("ATTESTATION_EXPIRED")
		}
	} else if p.ExpiresAt != "" || verified.After(now) {
		return nil, biz.Precondition("ATTESTATION_INVALID")
	}
	return &p, nil
}
func (s *EvidenceStore) Verify(_ context.Context, g biz.SupplyGroup, spec *biz.ProfileSpec, ref string) (*biz.Verification, error) {
	p, err := s.Load(ref)
	if err != nil {
		return nil, err
	}
	digest, err := biz.SpecDigest(spec, g.Baseline.BaselineID)
	if err != nil {
		return nil, err
	}
	if p.ClusterID != g.ClusterID || p.GroupID != g.GroupID || p.BaselineDigest != g.Baseline.BaselineDigest || p.SpecDigest != digest {
		return nil, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
	}
	want := []string{}
	for _, node := range g.Nodes {
		want = append(want, node.UID)
	}
	sort.Strings(want)
	got := append([]string{}, p.NodeUIDs...)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return nil, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, device := range p.Devices {
		key := device.NodeUID + "/" + device.VendorUUID
		if seen[key] || !physicalUUID.MatchString(device.VendorUUID) || device.ModelKey != g.ModelKey || device.SafeMemoryMiB <= 0 || device.PhysicalBytes == 0 {
			return nil, biz.Precondition("ATTESTATION_DEVICE_INVALID")
		}
		seen[key] = true
		counts[device.NodeUID]++
		blocks, err := biz.QuantizedBlocks([]uint64{device.PhysicalBytes}, g.Baseline.MemoryBlockMiB)
		if err != nil {
			return nil, err
		}
		budget, err := biz.Multiply(blocks, int64(g.Baseline.MemoryBlockMiB))
		if err != nil {
			return nil, err
		}
		if device.SafeMemoryMiB > budget {
			return nil, biz.Precondition("CAPACITY_INVARIANT_VIOLATION")
		}
		if spec.Mode == biz.Shared && int64(g.Baseline.SplitCount) > device.SafeMemoryMiB/spec.SharedMemoryMiB {
			return nil, biz.Precondition("CAPACITY_INVARIANT_VIOLATION")
		}
		if g.Baseline.CoreBudgetAdditive && spec.Mode == biz.Shared && uint64(g.Baseline.SplitCount)*uint64(spec.CoreLimitPercent) > 100 {
			return nil, biz.Precondition("CAPACITY_INVARIANT_VIOLATION")
		}
	}
	for _, uid := range want {
		if counts[uid] == 0 {
			return nil, biz.Precondition("ATTESTATION_DEVICE_INVALID")
		}
		delete(counts, uid)
	}
	if len(counts) > 0 {
		return nil, biz.Precondition("ATTESTATION_SCOPE_MISMATCH")
	}
	return &biz.Verification{State: biz.Verified, EvidenceRef: ref, VerifiedAt: p.VerifiedAt, BaselineDigest: p.BaselineDigest}, nil
}

// OwnerAssertion binds an owner-signed identity to the actual API Pod UID and
// frozen plan. Ordinary labels and service account names are insufficient.
type OwnerAssertion struct {
	Pod        biz.PodRef   `json:"pod"`
	Ref        biz.UsageRef `json:"ref"`
	PlanDigest string       `json:"plan_digest"`
}

func VerifyOwnerAssertion(pod biz.PodRef, labels map[string]string, annotations map[string]string, key ed25519.PublicKey) (*biz.UsageRef, string) {
	r := &biz.UsageRef{TenantID: labels["accelerator.ani.io/resource-tenant-id"], OwnerService: labels["accelerator.ani.io/owner-service"], ResourceID: labels["accelerator.ani.io/resource-id"], CreateOperationID: labels["accelerator.ani.io/create-operation-id"]}
	digest := annotations["accelerator.ani.io/plan-digest"]
	if biz.ValidateRef(r) != nil || !biz.Digest(digest) || len(key) != ed25519.PublicKeySize {
		return nil, ""
	}
	sig, err := base64.StdEncoding.DecodeString(annotations["accelerator.ani.io/owner-signature"])
	if err != nil {
		return nil, ""
	}
	payload, err := biz.Canonical(OwnerAssertion{pod, *r, digest})
	if err != nil || !ed25519.Verify(key, append([]byte("acc-owner-pod-v1\n"), payload...), sig) {
		return nil, ""
	}
	return r, digest
}
