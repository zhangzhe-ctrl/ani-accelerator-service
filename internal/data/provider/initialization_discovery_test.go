package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
)

func TestInitializationDiscoveryUsesDurableFactsAndStableStructure(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "discovery_contract")
}

func providerDiscoveryContract(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, state *replayState, telemetry *nodeTelemetryFixture) {
	t.Helper()
	ctx := context.Background()
	first, err := adapter.DiscoverInitialization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 1 || len(first.Nodes[0].GPUs) != 2 || first.Nodes[0].GroupID != group.GroupID || first.Nodes[0].Selectable {
		t.Fatalf("discovery lost actual inventory or business membership: %+v", first)
	}
	for _, gpu := range first.Nodes[0].GPUs {
		if gpu.SafeMemoryBudgetMiB == nil || *gpu.SafeMemoryBudgetMiB != 24576 || gpu.BudgetReason != "" {
			t.Fatalf("safe budget was not cross-checked: %+v", gpu)
		}
	}
	if len(first.Nodes[0].Baselines) != 1 || first.Nodes[0].Baselines[0].BaselineRef != group.Baseline.BaselineID+":"+group.Baseline.BaselineDigest {
		t.Fatal("manual baseline lost stable provenance")
	}
	state.setFault("node_rv")
	state.mu.Lock()
	state.memory = 8192
	state.mu.Unlock()
	telemetry.refresh(t)
	views, err := database.NodeTelemetry(ctx, cluster.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.HeartbeatNodeSession(ctx, cluster.ConnectionRef, telemetry.source.UID, views[0].State.Epoch, false, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	identity := views[0].State.Identity
	identity.TokenExpiresAt = time.Now().Add(time.Minute)
	if _, err = database.RenewNodeSession(ctx, identity, views[0].State.Epoch, time.Now().Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	refreshed, err := adapter.DiscoverInitialization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Environment.DiscoveryRevision != first.Environment.DiscoveryRevision {
		t.Fatal("heartbeat, runtime fact or Node resourceVersion changed structural discovery revision")
	}
	reader := adapter.connections[cluster.ConnectionRef]
	valid := Config{Connections: []Connection{reader.config}}
	path := filepath.Join(t.TempDir(), "provider.json")
	write := func(config Config) {
		t.Helper()
		payload, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	loaded, err := LoadConfig(path)
	if err != nil || len(loaded.Connections[0].InstallationBaselines) != 1 {
		t.Fatalf("formal installation config could not load: %v", err)
	}
	for _, legacy := range []string{"nodes", "local_node_uid", "runtime_events_directory", "runtime_journal"} {
		copyConfig := valid
		copyConfig.Connections = append([]Connection(nil), valid.Connections...)
		switch legacy {
		case "nodes":
			copyConfig.Connections[0].Nodes = []NodeSource{{UID: "legacy"}}
		case "local_node_uid":
			copyConfig.Connections[0].LocalNodeUID = "legacy"
		case "runtime_events_directory":
			copyConfig.RuntimeEventsDirectory = "/legacy"
		case "runtime_journal":
			copyConfig.RuntimeJournal = &journalRecoveryConfig{}
		}
		write(copyConfig)
		if _, err = LoadConfig(path); err == nil || err.Error() != "PROVIDER_LOCAL_COLLECTION_REMOVED" {
			t.Fatalf("legacy local collection was silently retained for %s: %v", legacy, err)
		}
	}
	reader.config.InstallationBaselines = nil
	unknown, err := adapter.DiscoverInitialization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.ConfigurationState != "CONFIGURATION_REQUIRED" || unknown.Nodes[0].Selectable || len(unknown.Nodes[0].GPUs) != 2 || unknown.Nodes[0].GPUs[0].SafeMemoryBudgetMiB != nil {
		t.Fatal("missing baseline fabricated a usable supply or erased observed inventory")
	}
	if unknown.Environment.DiscoveryRevision == first.Environment.DiscoveryRevision {
		t.Fatal("operator baseline change did not invalidate discovery revision")
	}
	adapter.connections["ambiguous"] = reader
	ambiguous, err := adapter.DiscoverInitialization(ctx)
	if err != nil || ambiguous.Environment.ConnectionRef != "" || ambiguous.ConfigurationState != "CONFIGURATION_REQUIRED" {
		t.Fatal("ambiguous managed connections were guessed")
	}
}
