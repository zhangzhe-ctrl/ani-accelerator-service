package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type factor10PreviewProvider struct{ testProvider }

func (provider factor10PreviewProvider) Inspect(ctx context.Context, cluster biz.Cluster, group biz.SupplyGroup) (biz.GroupInspection, error) {
	inspection, err := provider.testProvider.Inspect(ctx, cluster, group)
	for index := range inspection.Nodes {
		inspection.Nodes[index].DeviceBudgetsMiB = []int64{24570}
	}
	return inspection, err
}

func TestPostgresFactor10PreviewCreatesAnExactlyRepresentableProfile(t *testing.T) {
	fixture := pgSetup(t)
	codec, _ := biz.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	usecase, err := biz.NewUsecase(fixture.db, factor10PreviewProvider{}, codec, []string{fixture.cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	baseline := *fixture.group.Baseline
	baseline.BaselineID, baseline.MemoryBlockMiB = biz.NewID(), 10
	baseline.BaselineDigest, _ = biz.BaselineDigest(&baseline)
	group, err := usecase.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), adminMutation(), biz.AdoptInput{PoolID: fixture.pool.PoolID, DisplayName: "factor10", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: biz.NewID(), Name: "factor10-node"}}, ModelKey: fixture.group.ModelKey, Baseline: &baseline, QueueName: fixture.group.QueueName})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := usecase.PreviewProfile(adminContext("PreviewProfile"), &biz.AdminRead{RequestID: "factor10-preview", Actor: &adminActor}, group.GroupID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Spec.MemoryMiBPerDevice != 6140 || preview.UnusedMemoryMiB != 10 {
		t.Fatalf("preview not representable: %+v", preview)
	}
	profile, err := usecase.CreateProfile(adminContext("CreateProfile"), adminMutation(), biz.CreateProfileInput{DisplayName: "four parts", Spec: preview.Spec, ExpectedGroupVersion: group.Version})
	if err != nil || profile.Spec.MemoryMiBPerDevice != 6140 {
		t.Fatalf("preview cannot be created: %+v %v", profile, err)
	}
}
