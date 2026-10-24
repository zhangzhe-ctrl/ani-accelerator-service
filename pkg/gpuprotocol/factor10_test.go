package gpuprotocol_test

import (
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
)

func TestFactor10WireExecutionAcceptsFrozenUnitAndRejectsDrift(t *testing.T) {
	execution := &pb.GpuExecution{SchedulerName: "volcano", QueueName: "default", RuntimeClassName: "nvidia", NodeSelector: map[string]string{"accelerator.ani.io/supply-group": "group", "accelerator.ani.io/model-key": "t4", "accelerator.ani.io/baseline-id": "baseline"}, PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core", "accelerator.ani.io/memory-block-mib": "10"}, Resources: map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "384", "volcano.sh/vgpu-cores": "25"}}
	execution.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(execution)
	if err := gpuprotocol.ValidateGpuExecution(execution); err != nil {
		t.Fatal(err)
	}
	execution.NodeSelector["kubernetes.io/hostname"] = "node3"
	execution.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(execution)
	if err := gpuprotocol.ValidateGpuExecution(execution); err != nil {
		t.Fatalf("factor10 execution with RuntimeClass placement rejected: %v", err)
	}
	execution.PodAnnotations["accelerator.ani.io/memory-block-mib"] = "1"
	if gpuprotocol.ValidateGpuExecution(execution) == nil {
		t.Fatal("changed frozen factor accepted under old digest")
	}
	for _, factor := range []string{"0", "1", "2", "010", "256", ""} {
		execution.PodAnnotations["accelerator.ani.io/memory-block-mib"] = factor
		execution.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(execution)
		if gpuprotocol.ValidateGpuExecution(execution) == nil {
			t.Fatalf("accepted factor %q", factor)
		}
	}
}
