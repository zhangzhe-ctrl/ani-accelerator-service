// Package gpuprotocol contains deterministic wire-contract checks only.
package gpuprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"strconv"
)

func GpuExecutionDigest(e *pb.GpuExecution) (string, error) {
	if e == nil {
		return "", fmt.Errorf("missing GPU execution")
	}
	fields := map[string]any{"scheduler_name": e.SchedulerName, "queue_name": e.QueueName, "runtime_class_name": e.RuntimeClassName, "node_selector": e.NodeSelector, "pod_annotations": e.PodAnnotations, "resources": e.Resources}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte("acc-execution-v1\n"), bytes.TrimSuffix(out.Bytes(), []byte{'\n'})...))
	return hex.EncodeToString(h[:]), nil
}
func ValidateGpuExecution(e *pb.GpuExecution) error {
	if e == nil || len(e.ProtoReflect().GetUnknown()) != 0 || e.SchedulerName != "volcano" || e.QueueName == "" || e.RuntimeClassName == "" || len(e.Resources) != 3 || len(e.NodeSelector) != 3 || len(e.PodAnnotations) != 1 {
		return fmt.Errorf("invalid GPU execution")
	}
	for _, k := range []string{"accelerator.ani.io/supply-group", "accelerator.ani.io/model-key", "accelerator.ani.io/baseline-id"} {
		if e.NodeSelector[k] == "" {
			return fmt.Errorf("invalid GPU selector")
		}
	}
	if e.PodAnnotations["volcano.sh/vgpu-mode"] != "hami-core" {
		return fmt.Errorf("invalid GPU annotations")
	}
	for _, k := range []string{"volcano.sh/vgpu-number", "volcano.sh/vgpu-memory", "volcano.sh/vgpu-cores"} {
		s := e.Resources[k]
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil || v <= 0 || strconv.FormatInt(v, 10) != s {
			return fmt.Errorf("invalid GPU resource %s", k)
		}
		if k == "volcano.sh/vgpu-cores" && v > 100 {
			return fmt.Errorf("invalid GPU core limit")
		}
	}
	d, err := GpuExecutionDigest(e)
	if err != nil {
		return err
	}
	if d != e.ExecutionDigest {
		return fmt.Errorf("GPU execution digest mismatch")
	}
	return nil
}
