package provider

import (
	"context"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestInitializationDiscoveryUsesRegistrationAndStableStructure(t *testing.T) {
	fixture := newNodePluginFixture(t)
	reader := &providerReader{clusters: []biz.Cluster{fixture.cluster}, devices: []biz.DeviceRecord{{Device: biz.PhysicalDevice{
		DeviceID: biz.NewID(), ClusterID: fixture.cluster.ClusterID, Node: &fixture.groups[0].Nodes[0], VendorDeviceID: gpuUUID, PhysicalMemoryBytes: 24576 << 20,
	}}}}
	fixture.repo = providerRepository{reader: reader}
	adapter := fixture.provider(t, fixture.references)
	first, err := adapter.DiscoverInitialization(context.Background())
	if err != nil || len(first.Nodes) != 2 || first.Environment.DiscoveryRevision == "" {
		t.Fatalf("registration discovery failed: %+v, %v", first, err)
	}
	for _, discovered := range first.Nodes {
		if !discovered.Selectable || len(discovered.GPUs) != 1 || discovered.GPUs[0].SafeMemoryBudgetMiB == nil || *discovered.GPUs[0].SafeMemoryBudgetMiB != 24570 {
			t.Fatalf("no-agent registration was not selectable: %+v", discovered)
		}
		if discovered.Node.UID == fixture.groups[0].Nodes[0].UID && discovered.GPUs[0].PhysicalMemoryBytes != 24576<<20 {
			t.Fatalf("current display bytes were not reused: %+v", discovered)
		}
	}
	reader.devices[0].Device.PhysicalMemoryBytes = 0
	second, err := adapter.DiscoverInitialization(context.Background())
	if err != nil || second.Environment.DiscoveryRevision != first.Environment.DiscoveryRevision {
		t.Fatalf("display bytes or observation refresh changed structural revision: %+v, %v", second, err)
	}
	reader.devicesErr = biz.Unavailable()
	third, err := adapter.DiscoverInitialization(context.Background())
	if err != nil || third.Environment.DiscoveryRevision != first.Environment.DiscoveryRevision {
		t.Fatalf("optional metadata failure blocked initialization: %+v, %v", third, err)
	}
	for _, discovered := range third.Nodes {
		if !discovered.Selectable || discovered.GPUs[0].PhysicalMemoryBytes != 0 {
			t.Fatalf("metadata failure affected selection or reused old bytes: %+v", discovered)
		}
	}
	fixture.fault = "unhealthy"
	changed, err := adapter.DiscoverInitialization(context.Background())
	if err != nil || changed.Environment.DiscoveryRevision == first.Environment.DiscoveryRevision {
		t.Fatalf("changed registered device health was absent from revision: %+v, %v", changed, err)
	}
}
