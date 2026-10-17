package provider

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
	pr "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type Inventory struct {
	UUID, Model string
	Bytes       uint64
}
type inventoryReader interface {
	Read(context.Context) ([]Inventory, error)
}
type nvmlInventory struct{}

var nvmlMu sync.Mutex

func (nvmlInventory) Read(ctx context.Context) ([]Inventory, error) {
	nvmlMu.Lock()
	defer nvmlMu.Unlock()
	if ctx.Err() != nil {
		return nil, biz.Unavailable()
	}
	if nvml.Init() != nvml.SUCCESS {
		return nil, biz.Precondition("NVML_UNAVAILABLE")
	}
	defer nvml.Shutdown()
	count, rc := nvml.DeviceGetCount()
	if rc != nvml.SUCCESS {
		return nil, biz.Precondition("NVML_UNAVAILABLE")
	}
	result := []Inventory{}
	for i := 0; i < count; i++ {
		device, rc := nvml.DeviceGetHandleByIndex(i)
		if rc != nvml.SUCCESS {
			return nil, biz.Precondition("NVML_UNAVAILABLE")
		}
		id, r1 := device.GetUUID()
		memory, r2 := device.GetMemoryInfo()
		name, r3 := device.GetName()
		if r1 != nvml.SUCCESS || r2 != nvml.SUCCESS || r3 != nvml.SUCCESS || !physicalUUID.MatchString(id) {
			return nil, biz.Precondition("NVML_UNAVAILABLE")
		}
		result = append(result, Inventory{id, "NVIDIA-" + name, memory.Total})
	}
	return result, nil
}

// The upstream PodResources/CRI protocols use local Unix sockets, not TLS.
// No TCP fallback is accepted; mounted sockets are explicit operator inputs.
func unixClient(path string) (*grpc.ClientConn, error) {
	return grpc.NewClient("passthrough:///local-node-source", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", path)
	}), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)))
}

type nodeSnapshot struct {
	Resources              *pr.ListPodResourcesResponse
	Containers             []*cri.Container
	Statuses               map[string]*cri.ContainerStatusResponse
	ObservedAt             time.Time
	ResourcesOK, RuntimeOK bool
}

func readNode(ctx context.Context, n NodeSource) nodeSnapshot {
	s := nodeSnapshot{Statuses: map[string]*cri.ContainerStatusResponse{}, ObservedAt: time.Now().UTC()}
	pconn, err := unixClient(n.PodResourcesSocket)
	if err == nil {
		defer pconn.Close()
		call, cancel := context.WithTimeout(ctx, 3*time.Second)
		s.Resources, err = pr.NewPodResourcesListerClient(pconn).List(call, &pr.ListPodResourcesRequest{})
		cancel()
		s.ResourcesOK = err == nil
	}
	rconn, err := unixClient(n.CRISocket)
	if err != nil {
		return s
	}
	defer rconn.Close()
	client := cri.NewRuntimeServiceClient(rconn)
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	version, err := client.Version(call, &cri.VersionRequest{Version: "0.1.0"})
	// RuntimeVersion is diagnostic metadata, not the verbose status contract.
	// Require containerd's CRI v1 endpoint; GPU identity and limits are still
	// validated from each matching container's actual runtimeSpec.process.env.
	if err != nil || version.RuntimeName != "containerd" || version.RuntimeApiVersion != "v1" || version.RuntimeVersion == "" {
		return s
	}
	list, err := client.ListContainers(call, &cri.ListContainersRequest{})
	if err != nil {
		return s
	}
	s.Containers = list.Containers
	s.RuntimeOK = true
	for _, c := range s.Containers {
		response, err := client.ContainerStatus(call, &cri.ContainerStatusRequest{ContainerId: c.Id, Verbose: true})
		if err != nil || response.Status == nil || c.Id == "" || response.Status.Id != c.Id {
			s.RuntimeOK = false
			continue
		}
		s.Statuses[c.Id] = response
	}
	return s
}
func resourcesFor(s nodeSnapshot, namespace, name, container string) ([]*pr.ContainerDevices, bool) {
	if !s.ResourcesOK || s.Resources == nil {
		return nil, false
	}
	for _, p := range s.Resources.PodResources {
		if p.Namespace == namespace && p.Name == name {
			for _, c := range p.Containers {
				if c.Name == container {
					return c.Devices, true
				}
			}
		}
	}
	return nil, true
}
func runtimeFor(s nodeSnapshot, uid, container string) ([]*cri.ContainerStatusResponse, bool) {
	if !s.RuntimeOK {
		return nil, false
	}
	var out []*cri.ContainerStatusResponse
	for _, c := range s.Containers {
		if c.Labels["io.kubernetes.pod.uid"] == uid && c.Metadata != nil && c.Metadata.Name == container {
			st := s.Statuses[c.Id]
			if st == nil || st.Status.Labels["io.kubernetes.pod.uid"] != uid || st.Status.Metadata == nil || st.Status.Metadata.Name != container {
				return nil, false
			}
			out = append(out, st)
		}
	}
	return out, true
}
func hasGPUResources(ds []*pr.ContainerDevices) bool {
	for _, d := range ds {
		if (d.ResourceName == "volcano.sh/vgpu-number" || d.ResourceName == "volcano.sh/vgpu-memory" || d.ResourceName == "volcano.sh/vgpu-cores") && len(d.DeviceIds) > 0 {
			return true
		}
	}
	return false
}
