package gpuprotocol_test

import (
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
	"strings"
	"testing"
)

func TestWireExecutionFreezesOnlyBoundRuntimeClassHostname(t *testing.T) {
	e := &pb.GpuExecution{SchedulerName: "volcano", QueueName: "gpu", RuntimeClassName: "nvidia", NodeSelector: map[string]string{"accelerator.ani.io/supply-group": "group", "accelerator.ani.io/model-key": "model", "accelerator.ani.io/baseline-id": "baseline", "kubernetes.io/hostname": "ani-03"}, PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core"}, Resources: map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "8192", "volcano.sh/vgpu-cores": "25"}}
	e.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(e)
	if err := gpuprotocol.ValidateGpuExecution(e); err != nil {
		t.Fatalf("frozen RuntimeClass hostname rejected: %v", err)
	}
	original := e.ExecutionDigest
	e.NodeSelector["kubernetes.io/hostname"] = "ani-02"
	if gpuprotocol.ValidateGpuExecution(e) == nil {
		t.Fatal("changed placement accepted with original digest")
	}
	e.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(e)
	if e.ExecutionDigest == original {
		t.Fatal("hostname not committed by digest")
	}
	for _, bad := range []string{"", "bad/value", strings.Repeat("x", 64)} {
		e.NodeSelector["kubernetes.io/hostname"] = bad
		e.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(e)
		if gpuprotocol.ValidateGpuExecution(e) == nil {
			t.Fatalf("invalid hostname label accepted: %q", bad)
		}
	}
	delete(e.NodeSelector, "kubernetes.io/hostname")
	e.NodeSelector["untrusted.example/selector"] = "any"
	e.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(e)
	if gpuprotocol.ValidateGpuExecution(e) == nil {
		t.Fatal("unknown fourth selector accepted")
	}
}
