package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	providerDiscoveryHistoricalGaps(t, database, adapter, cluster.ConnectionRef, telemetry)
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

func providerDiscoveryHistoricalGaps(t *testing.T, database *data.Postgres, adapter *Provider, connection string, telemetry *nodeTelemetryFixture) {
	t.Helper()
	ctx := context.Background()
	for _, test := range []struct {
		name       string
		gapEnd     time.Duration
		open       bool
		incomplete bool
		stale      bool
		zeroStart  bool
		complete   bool
	}{
		{name: "closed_before_current_full_snapshot", gapEnd: -time.Second, complete: true},
		{name: "closed_recovery_gap_unknown_start", gapEnd: -time.Second, zeroStart: true, complete: true},
		{name: "closed_at_current_full_snapshot", complete: true},
		{name: "open_gap", open: true},
		{name: "gap_after_current_full_snapshot", gapEnd: time.Second},
		{name: "incomplete_snapshot_does_not_cover_gap", gapEnd: -time.Second, incomplete: true},
		{name: "stale_snapshot_does_not_cover_gap", gapEnd: -time.Second, stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			telemetry.refresh(t)
			snapshot := *telemetry.state.Snapshot
			snapshot.Sources = append([]biz.TelemetrySource(nil), snapshot.Sources...)
			observed := snapshot.CaptureEnd
			if test.stale {
				observed = observed.Add(-2 * time.Minute)
			}
			for index := range snapshot.Sources {
				snapshot.Sources[index].ObservedAt = observed
				snapshot.Sources[index].Complete = !test.incomplete
			}
			gap := &biz.TelemetryGap{Sources: []string{"nvml", "podresources", "cri"}, Reason: "SOURCE_GAP", Start: observed.Add(-2 * time.Second), End: observed.Add(test.gapEnd), DiscoveredAt: time.Now().UTC()}
			if test.open {
				gap.End = time.Time{}
			}
			if test.zeroStart {
				gap.Start = time.Time{}
			}
			saveDiscoveryGap(t, telemetry, &snapshot, gap)
			before, err := database.NodeTelemetry(ctx, connection)
			if err != nil {
				t.Fatal(err)
			}
			discovery, err := adapter.DiscoverInitialization(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(discovery.Nodes) != 1 || len(discovery.Nodes[0].Sources) != 3 {
				t.Fatalf("missing current sources: %+v", discovery)
			}
			for _, source := range discovery.Nodes[0].Sources {
				if source.Complete != test.complete || !slices.Contains(source.Issues, "SOURCE_GAP") || slices.Contains(source.Issues, "SOURCE_UNKNOWN") == test.complete {
					t.Errorf("current source completeness must be independent of covered historical gaps: %+v", source)
				}
			}
			if slices.Contains(discovery.Nodes[0].BlockingReasons, "SOURCE_UNKNOWN") == test.complete {
				t.Errorf("unexpected source blocking reasons: %v", discovery.Nodes[0].BlockingReasons)
			}
			after, err := database.NodeTelemetry(ctx, connection)
			if err != nil || !reflect.DeepEqual(before[0].State.Gaps, after[0].State.Gaps) {
				t.Fatalf("discovery changed durable historical gaps: %v", err)
			}
		})
	}
	telemetry.refresh(t)
	closed := &biz.TelemetryGap{Sources: []string{"nvml", "podresources", "cri"}, Reason: "SOURCE_GAP", Start: time.Now().Add(-2 * time.Second), End: time.Now().Add(-time.Second), DiscoveredAt: time.Now().UTC()}
	saveDiscoveryGap(t, telemetry, nil, closed)
	events := &biz.TelemetryGap{Sources: []string{"cri_events"}, Reason: "SOURCE_GAP", Start: time.Now().Add(-time.Second), DiscoveredAt: time.Now().UTC()}
	saveDiscoveryGap(t, telemetry, nil, events)
	discovery, err := adapter.DiscoverInitialization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range discovery.Nodes[0].Sources {
		if !source.Complete {
			t.Fatalf("historical cri_events gap contaminated current full snapshot: %+v", source)
		}
	}
	views, err := database.NodeTelemetry(ctx, connection)
	if err != nil || !views[0].State.Gaps["cri_events"].End.IsZero() || len(views[0].State.Gaps) != 4 {
		t.Fatalf("historical event gap was erased or rewritten: %v", err)
	}
}

func saveDiscoveryGap(t *testing.T, telemetry *nodeTelemetryFixture, snapshot *biz.TelemetrySnapshot, gap *biz.TelemetryGap) {
	t.Helper()
	report := biz.NodeReport{Epoch: telemetry.state.Epoch, Sequence: telemetry.state.LastSequence + 1, LogID: telemetry.identity.LogID, OriginPodUID: telemetry.identity.PodUID, OriginInstanceID: telemetry.identity.InstanceID, OriginBootID: telemetry.identity.BootID, Snapshot: snapshot, Gap: gap}
	var err error
	report.PayloadDigest, err = biz.Hash(report)
	if err != nil {
		t.Fatal(err)
	}
	telemetry.state, err = telemetry.store.SaveNodeReport(context.Background(), telemetry.identity.ConnectionRef, telemetry.identity.NodeUID, report, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
}
