package provider

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

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

// OwnerAssertion binds an owner-signed identity to the actual API Pod UID and
// execution snapshot. Ordinary labels and service account names are insufficient.
type OwnerAssertion struct {
	Pod             biz.PodRef   `json:"pod"`
	Ref             biz.UsageRef `json:"ref"`
	ExecutionDigest string       `json:"execution_digest"`
}

func VerifyOwnerAssertion(pod biz.PodRef, labels map[string]string, annotations map[string]string, key ed25519.PublicKey) (*biz.UsageRef, string) {
	r := &biz.UsageRef{TenantID: labels["accelerator.ani.io/resource-tenant-id"], OwnerService: labels["accelerator.ani.io/owner-service"], ResourceID: labels["accelerator.ani.io/resource-id"], CreateOperationID: labels["accelerator.ani.io/create-operation-id"]}
	digest := annotations["accelerator.ani.io/execution-digest"]
	if biz.ValidateRef(r) != nil || !biz.Digest(digest) || len(key) != ed25519.PublicKeySize {
		return nil, ""
	}
	sig, err := base64.StdEncoding.DecodeString(annotations["accelerator.ani.io/owner-signature"])
	if err != nil {
		return nil, ""
	}
	payload, err := biz.Canonical(OwnerAssertion{pod, *r, digest})
	if err != nil || !ed25519.Verify(key, append([]byte("acc-owner-pod-v2\n"), payload...), sig) {
		return nil, ""
	}
	return r, digest
}
