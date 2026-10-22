package gpuprotocol

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
)

func TestOwnerAssertionExistingCanonicalContract(t *testing.T) {
	pod := &pb.PodRef{ClusterId: "cluster", Namespace: "training", Name: "job-node-0", Uid: "actual-pod-uid", ContainerName: "node"}
	ref := &pb.GpuUsageRef{TenantId: "tenant", OwnerService: "ani-modeldev-service", ResourceId: "execution", CreateOperationId: "create"}
	actual, err := OwnerAssertionSigningBytes(pod, ref, strings.Repeat("a", 64))
	want := "acc-owner-pod-v2\n" + `{"execution_digest":"` + strings.Repeat("a", 64) + `","pod":{"cluster_id":"cluster","container_name":"node","name":"job-node-0","namespace":"training","uid":"actual-pod-uid"},"ref":{"create_operation_id":"create","owner_service":"ani-modeldev-service","resource_id":"execution","tenant_id":"tenant"}}`
	if err != nil || string(actual) != want {
		t.Fatalf("existing signature bytes changed: %q %v", actual, err)
	}
	pod.Uid = "anticipated-or-replaced-uid"
	changed, err := OwnerAssertionSigningBytes(pod, ref, strings.Repeat("a", 64))
	if err != nil || bytes.Equal(actual, changed) {
		t.Fatal("actual Pod UID absent from signature bytes", err)
	}
	pod.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, err := OwnerAssertionSigningBytes(pod, ref, strings.Repeat("a", 64)); err == nil {
		t.Fatal("unknown Pod fields signed")
	}
	pod.ProtoReflect().SetUnknown(nil)
	for _, owner := range []string{"trainer", "ani-modeldev", ""} {
		ref.OwnerService = owner
		if _, err := OwnerAssertionSigningBytes(pod, ref, strings.Repeat("a", 64)); err == nil {
			t.Fatal("unregistered owner signed")
		}
	}
}
