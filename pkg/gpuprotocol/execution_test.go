package gpuprotocol_test

import (
	"encoding/json"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
	"testing"
)

func TestWireDigestAgreesWithDomainAndRejectsUnknownResources(t *testing.T) {
	d := &biz.GpuExecution{SchedulerName: "volcano", QueueName: "default", RuntimeClassName: "nvidia", NodeSelector: map[string]string{biz.GroupLabel: "group", biz.ModelLabel: "model", biz.BaselineLabel: "config"}, PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core"}, Resources: map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "8192", "volcano.sh/vgpu-cores": "25"}}
	d.ExecutionDigest, _ = biz.ExecutionDigest(d)
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	wire := new(pb.GpuExecution)
	if err = json.Unmarshal(raw, wire); err != nil {
		t.Fatal(err)
	}
	if err = gpuprotocol.ValidateGpuExecution(wire); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"volcano.sh/vgpu-memory-percentage", "nvidia.com/gpu", "untrusted.example/resource"} {
		wire.Resources[bad] = "1"
		wire.ExecutionDigest, _ = gpuprotocol.GpuExecutionDigest(wire)
		if gpuprotocol.ValidateGpuExecution(wire) == nil {
			t.Fatal("accepted unknown GPU resource", bad)
		}
		delete(wire.Resources, bad)
	}
	wire.ExecutionDigest = d.ExecutionDigest
	wire.ProtoReflect().SetUnknown([]byte{0x40, 0x01})
	if gpuprotocol.ValidateGpuExecution(wire) == nil {
		t.Fatal("accepted future or retired wire fields")
	}
	wire.ProtoReflect().SetUnknown(nil)
	wire.Resources["volcano.sh/vgpu-memory"] = "1"
	if gpuprotocol.ValidateGpuExecution(wire) == nil {
		t.Fatal("accepted modified execution under old digest")
	}
}
