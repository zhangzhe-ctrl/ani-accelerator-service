package provider

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data"
)

func TestProviderFactor10NormalizesOnlyBlockSources(t *testing.T) {
	providerControlledSources(t, "v1.16.0", "factor10")
}

func providerFactor10Contract(t *testing.T, database *data.Postgres, adapter *Provider, cluster biz.Cluster, group biz.SupplyGroup, profile biz.Profile, pod biz.PodRef, state *replayState, telemetry *nodeTelemetryFixture) {
	t.Helper()
	ctx := context.Background()
	inspection, err := adapter.Inspect(ctx, cluster, group)
	if err != nil || len(inspection.Nodes) != 1 || inspection.Nodes[0].Factor != 10 || len(inspection.Nodes[0].DeviceBudgetsMiB) != 2 {
		t.Fatalf("factor10 inspection unavailable: %+v error=%v", inspection, err)
	}
	for _, budget := range inspection.Nodes[0].DeviceBudgetsMiB {
		if budget != 24570 {
			t.Fatalf("device budget=%d, want per-card floor 24570 MiB", budget)
		}
	}
	if err = database.View(ctx, func(reader biz.Reader) error {
		claims, _, readError := reader.PodEvidence(ctx, pod)
		if readError != nil {
			return readError
		}
		if len(claims) != 3 {
			t.Fatalf("factor10 missing real source claims: %+v", claims)
		}
		for _, claim := range claims {
			if claim.MemoryMiB != 3840 || claim.MemoryBlockMiB != 10 {
				t.Fatalf("block source not normalized or runtime multiplied twice: %+v", claim)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	codec, _ := biz.NewCursorCodec([]byte("factor10-cursor-key-0123456789012345"))
	usecase, err := biz.NewUsecase(database, adapter, codec, []string{cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor := biz.Actor{Type: "user", ID: "factor10"}
	admin := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Actor: actor, Action: "AdminGetCapacity"})
	capacity, err := usecase.AdminGetCapacity(admin, &biz.AdminRead{RequestID: "factor10", Actor: &actor}, profile.ProfileID, 1, true)
	if err != nil || !capacity.Observation.Complete || capacity.KnownCommittedUnits != 1 || capacity.KnownFreeUnits != 7 {
		t.Fatalf("factor10 capacity did not preserve card/split constraints: %+v error=%v", capacity, err)
	}
	for _, fault := range []string{"factor", "missing_factor_annotation", "factor_annotation_drift"} {
		state.setFault(fault)
		telemetry.refresh(t)
		batch, collectError := adapter.Collect(ctx, cluster, []biz.SupplyGroup{group})
		if collectError != nil {
			t.Fatal(collectError)
		}
		for _, claim := range batch.Claims {
			if fault == "factor" {
				if claim.Source != "runtime" || claim.MemoryMiB != 3840 || claim.State != biz.AllocationUnknown {
					t.Fatalf("unknown factor decoded stale blocks: %+v", claim)
				}
			} else {
				t.Fatalf("frozen factor mismatch accepted new claims: %+v", claim)
			}
		}
		if fault != "factor" && batch.Watermarks[0].Complete {
			t.Fatal("unknown frozen workload factor did not fail closed")
		}
	}
}
