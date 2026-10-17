package provider

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type capabilityRuntime struct {
	cri.UnimplementedRuntimeServiceServer
	version  *cri.VersionResponse
	listed   []*cri.Container
	response *cri.ContainerStatusResponse
	failure  string
}

func (r *capabilityRuntime) Version(context.Context, *cri.VersionRequest) (*cri.VersionResponse, error) {
	if r.failure == "version" {
		return nil, status.Error(codes.Unavailable, "controlled Version failure")
	}
	return r.version, nil
}

func (r *capabilityRuntime) ListContainers(context.Context, *cri.ListContainersRequest) (*cri.ListContainersResponse, error) {
	if r.failure == "list" {
		return nil, status.Error(codes.Unavailable, "controlled ListContainers failure")
	}
	return &cri.ListContainersResponse{Containers: r.listed}, nil
}

func (r *capabilityRuntime) ContainerStatus(_ context.Context, request *cri.ContainerStatusRequest) (*cri.ContainerStatusResponse, error) {
	if r.failure == "status" {
		return nil, status.Error(codes.Unavailable, "controlled ContainerStatus failure")
	}
	if !request.Verbose || request.ContainerId != "container-id" {
		return nil, status.Error(codes.InvalidArgument, "verbose status for listed ID required")
	}
	return r.response, nil
}

func newCapabilityRuntime(version string) *capabilityRuntime {
	return &capabilityRuntime{
		version: &cri.VersionResponse{Version: "0.1.0", RuntimeName: "containerd", RuntimeVersion: version, RuntimeApiVersion: "v1"},
		listed:  []*cri.Container{{Id: "container-id", Metadata: &cri.ContainerMetadata{Name: "inference"}, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}}},
		response: &cri.ContainerStatusResponse{
			Status: &cri.ContainerStatus{Id: "container-id", Metadata: &cri.ContainerMetadata{Name: "inference"}, State: cri.ContainerState_CONTAINER_RUNNING, Labels: map[string]string{"io.kubernetes.pod.uid": "pod-uid"}},
			Info:   map[string]string{"info": `{"runtimeSpec":{"process":{"env":["NVIDIA_VISIBLE_DEVICES=` + gpuUUID + `","CUDA_DEVICE_MEMORY_LIMIT_0=6144m","CUDA_DEVICE_SM_LIMIT=25"]}}}`},
		},
	}
}

func readCapabilityNode(t *testing.T, runtime *capabilityRuntime) nodeSnapshot {
	t.Helper()
	resources := unixServer(t, "resources", func(server *grpc.Server) {
		pr.RegisterPodResourcesListerServer(server, &replayResources{state: &replayState{absent: true}})
	})
	runtimeSocket := unixServer(t, "runtime", func(server *grpc.Server) {
		cri.RegisterRuntimeServiceServer(server, runtime)
	})
	snapshot := readNode(context.Background(), NodeSource{PodResourcesSocket: resources, CRISocket: runtimeSocket})
	if !snapshot.ResourcesOK {
		t.Fatal("independent PodResources source failed")
	}
	return snapshot
}

func TestReadNodeContainerdCapabilities(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		change  func(*capabilityRuntime)
		wantOK  bool
	}{
		{name: "reviewed_2.1.5", version: "2.1.5", wantOK: true},
		{name: "live_2.3.4", version: "2.3.4", wantOK: true},
		{name: "different_runtime", change: func(r *capabilityRuntime) { r.version.RuntimeName = "cri-o" }},
		{name: "missing_runtime_version", change: func(r *capabilityRuntime) { r.version.RuntimeVersion = "" }},
		{name: "different_api", change: func(r *capabilityRuntime) { r.version.RuntimeApiVersion = "v1alpha2" }},
		{name: "missing_api", change: func(r *capabilityRuntime) { r.version.RuntimeApiVersion = "" }},
		{name: "version_failure", change: func(r *capabilityRuntime) { r.failure = "version" }},
		{name: "list_failure", change: func(r *capabilityRuntime) { r.failure = "list" }},
		{name: "status_failure", change: func(r *capabilityRuntime) { r.failure = "status" }},
		{name: "missing_status", change: func(r *capabilityRuntime) { r.response.Status = nil }},
		{name: "wrong_status_id", change: func(r *capabilityRuntime) { r.response.Status.Id = "another-container" }},
		{name: "missing_status_id", change: func(r *capabilityRuntime) { r.response.Status.Id = "" }},
		{name: "exited_without_gpu_env", change: func(r *capabilityRuntime) {
			r.response.Status.State = cri.ContainerState_CONTAINER_EXITED
			r.response.Status.FinishedAt = 1
			r.response.Info = nil
		}, wantOK: true},
		{name: "system_container_without_gpu_env", change: func(r *capabilityRuntime) {
			r.listed[0].Metadata = nil
			r.listed[0].Labels = nil
			r.response.Status.Metadata = nil
			r.response.Status.Labels = nil
			r.response.Info = nil
		}, wantOK: true},
		{name: "empty_list_is_readable", change: func(r *capabilityRuntime) { r.listed = nil }, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			version := test.version
			if version == "" {
				version = "2.1.5"
			}
			runtime := newCapabilityRuntime(version)
			if test.change != nil {
				test.change(runtime)
			}
			snapshot := readCapabilityNode(t, runtime)
			if snapshot.RuntimeOK != test.wantOK {
				t.Fatalf("RuntimeOK = %v, want %v", snapshot.RuntimeOK, test.wantOK)
			}
			if test.version != "" && test.wantOK {
				statuses, ok := runtimeFor(snapshot, "pod-uid", "inference")
				if !ok || len(statuses) != 1 {
					t.Fatalf("GPU container identity unavailable: ok=%v statuses=%d", ok, len(statuses))
				}
				allocation, err := ParseContainerdInfo(statuses[0].Info["info"])
				if err != nil || allocation.UUID != gpuUUID || allocation.MemoryMiB != 6144 || allocation.CorePercent != 25 {
					t.Fatalf("GPU allocation = %+v, error = %v", allocation, err)
				}
			}
		})
	}
}

func TestReadNodeGPUFieldsRemainConservative(t *testing.T) {
	for _, version := range []string{"2.1.5", "2.3.4"} {
		t.Run(version, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				change func(*capabilityRuntime)
			}{
				{name: "wrong_pod_uid", change: func(r *capabilityRuntime) { r.response.Status.Labels["io.kubernetes.pod.uid"] = "other-pod" }},
				{name: "missing_container_metadata", change: func(r *capabilityRuntime) { r.response.Status.Metadata = nil }},
				{name: "wrong_container_name", change: func(r *capabilityRuntime) { r.response.Status.Metadata.Name = "sidecar" }},
				{name: "missing_info", change: func(r *capabilityRuntime) { r.response.Info = nil }},
				{name: "missing_runtime_spec", change: func(r *capabilityRuntime) { r.response.Info["info"] = `{}` }},
				{name: "missing_process_env", change: func(r *capabilityRuntime) { r.response.Info["info"] = `{"runtimeSpec":{"process":{}}}` }},
				{name: "unknown_memory_unit", change: func(r *capabilityRuntime) {
					r.response.Info["info"] = strings.Replace(r.response.Info["info"], "6144m", "6144", 1)
				}},
				{name: "nonphysical_gpu_id", change: func(r *capabilityRuntime) {
					r.response.Info["info"] = strings.Replace(r.response.Info["info"], gpuUUID, "0", 1)
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					runtime := newCapabilityRuntime(version)
					test.change(runtime)
					snapshot := readCapabilityNode(t, runtime)
					if !snapshot.RuntimeOK {
						t.Fatal("valid CRI source became unreadable")
					}
					statuses, ok := runtimeFor(snapshot, "pod-uid", "inference")
					if !ok {
						return
					}
					if len(statuses) != 1 {
						t.Fatalf("expected listed GPU container, got %d", len(statuses))
					}
					if _, err := ParseContainerdInfo(statuses[0].Info["info"]); err == nil {
						t.Fatal("incomplete GPU identity/allocation accepted")
					}
				})
			}
		})
	}
}
