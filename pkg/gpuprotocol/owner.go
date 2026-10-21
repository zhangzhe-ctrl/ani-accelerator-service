package gpuprotocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
)

const OwnerAssertionDomain = "acc-owner-pod-v2\n"

var ownerExecutionDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// OwnerAssertionSigningBytes binds the actual API Pod identity to its original
// owner reference and frozen execution digest. Callers obtain the Pod UID from
// the API; a workload template or anticipated UID cannot establish ownership.
// The returned bytes retain the existing signature domain and canonical format.
func OwnerAssertionSigningBytes(pod *pb.PodRef, ref *pb.GpuUsageRef, executionDigest string) ([]byte, error) {
	if pod == nil || ref == nil || len(pod.ProtoReflect().GetUnknown()) != 0 || len(ref.ProtoReflect().GetUnknown()) != 0 || !ownerExecutionDigest.MatchString(executionDigest) {
		return nil, fmt.Errorf("invalid owner assertion")
	}
	if ref.OwnerService != "ani-inference" && ref.OwnerService != "ani-resource-service" && ref.OwnerService != "ani-modeldev-service" {
		return nil, fmt.Errorf("unsupported owner")
	}
	for _, value := range []string{pod.ClusterId, pod.Namespace, pod.Name, pod.Uid, pod.ContainerName, ref.TenantId, ref.ResourceId, ref.CreateOperationId} {
		if value == "" || !utf8.ValidString(value) {
			return nil, fmt.Errorf("incomplete owner assertion")
		}
	}
	// Explicit maps preserve acc-c14n-v1 lexical keys without protojson's
	// alternate field naming or omission behavior. All assertion fields are strings.
	value := map[string]any{
		"pod":              map[string]string{"cluster_id": pod.ClusterId, "namespace": pod.Namespace, "name": pod.Name, "uid": pod.Uid, "container_name": pod.ContainerName},
		"ref":              map[string]string{"tenant_id": ref.TenantId, "owner_service": ref.OwnerService, "resource_id": ref.ResourceId, "create_operation_id": ref.CreateOperationId},
		"execution_digest": executionDigest,
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return append([]byte(OwnerAssertionDomain), bytes.TrimSuffix(out.Bytes(), []byte{'\n'})...), nil
}
