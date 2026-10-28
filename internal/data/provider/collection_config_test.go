package provider

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
)

// Only external protocols are replayed; observations, reconciliation, release
// authorization, and capacity use the production adapter/use case and real PG.
func TestProviderConfigurationIndependentCollection(t *testing.T) {
	for _, fault := range []string{"scheduler_config", "scheduler_image", "factor", "registration", "inventory_missing", "podresources", "cri"} {
		t.Run(fault, func(t *testing.T) { providerControlledSources(t, "v1.15.2", fault) })
	}
}

func TestProviderRuntimeEnvironmentDuplicatesPreserveUnknownUnits(t *testing.T) {
	providerControlledSources(t, "v1.15.2", "factor_duplicate_env")
}

type unavailableInventory struct{}

func (unavailableInventory) Read(context.Context) ([]Inventory, error) {
	return nil, biz.Precondition("NVML_UNAVAILABLE")
}

func configurationObservation(t *testing.T, fault string, adapter *Provider, telemetry *nodeTelemetryFixture, state *replayState, cluster biz.Cluster, group biz.SupplyGroup, db *data.Postgres, u *biz.Usecase, owner context.Context, ref *biz.UsageRef, pod biz.PodRef, capacity func() *biz.CapacityView) {
	t.Helper()
	ctx := context.Background()
	if fault == "factor_duplicate_env" {
		state.mu.Lock()
		state.runtimeEnvironment = duplicateRuntimeGPUEnvironment(6144)
		state.mu.Unlock()
		fault = "factor"
	}
	switch fault {
	case "inventory_missing":
		telemetry.inventory = unavailableInventory{}
	case "cri":
		state.mu.Lock()
		state.runtimeFailed = true
		state.mu.Unlock()
	default:
		state.setFault(fault)
	}
	sourceFailure := fault == "cri" || fault == "podresources"
	telemetry.refresh(t)
	if !sourceFailure {
		if _, err := adapter.Inspect(ctx, cluster, group); err == nil {
			t.Fatalf("admission accepted configuration drift %s", fault)
		}
	}
	collect := func() biz.ObservationBatch {
		t.Helper()
		telemetry.refresh(t)
		batch, err := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) }); err != nil {
			t.Fatal(err)
		}
		return batch
	}
	batch := collect()
	if !sourceFailure {
		for _, watermark := range batch.Watermarks {
			if !watermark.Complete || watermark.Issue != "" {
				t.Fatalf("admission drift reported as source outage: %+v", watermark)
			}
		}
		if len(batch.Claims) == 0 {
			t.Fatal("configuration drift stopped live occupancy sampling")
		}
	}
	v := capacity()
	if v.Observation.Complete || v.KnownFreeUnits != 0 || v.KnownCommittedUnits != 1 {
		t.Fatalf("drift/outage must retain occupancy and expose no free capacity: %+v", v)
	}
	if fault == "factor" || fault == "registration" || fault == "scheduler_image" {
		for _, claim := range batch.Claims {
			if claim.Source != "runtime" || claim.MemoryMiB != 6144 || claim.State != biz.AllocationUnknown {
				t.Fatalf("untrusted scheduler units were reinterpreted with old factor: %+v", claim)
			}
		}
	}
	state.mu.Lock()
	state.exited, state.absent, state.apiDeleted = true, true, true
	state.mu.Unlock()
	batch = collect()
	if sourceFailure {
		if len(batch.Releases) != 0 {
			t.Fatal("missing actual CRI/PodResources source produced release proof")
		}
	} else if len(batch.Releases) != 1 {
		t.Fatalf("configuration or registration failure blocked positive release proof: %+v", batch)
	}
	observed, err := u.ObserveRelease(owner, "configuration-release", ref, biz.NewID(), cluster.ClusterID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), []biz.PodRef{pod})
	if err != nil {
		t.Fatal(err)
	}
	if sourceFailure {
		if observed.Finding != biz.ReleaseUnknown || observed.Observation.Complete {
			t.Fatalf("real source failure falsely released occupancy: %+v", observed)
		}
	} else if observed.Finding != biz.AllocationReleased || !observed.Observation.Complete {
		t.Fatalf("positive CRI exit and allocation absence must release without matching configuration: %+v", observed)
	}
	if !sourceFailure {
		v = capacity()
		if v.KnownCommittedUnits != 0 || v.KnownFreeUnits != 0 || v.Observation.Complete {
			t.Fatalf("released facts may not restore unknown supply: %+v", v)
		}
	}
}
