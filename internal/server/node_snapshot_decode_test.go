package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runtimeDecodeReport(t *testing.T, record *nodepb.ImmutableRecord, epoch uint64) *nodepb.AgentReport {
	t.Helper()
	record.ProtocolVersion = 1
	record.LogId, record.Sequence = biz.NewID(), 1
	record.OriginPodUid, record.OriginInstanceId, record.OriginBootId = biz.NewID(), biz.NewID(), biz.NewID()
	record.PayloadDigest = ""
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	record.PayloadDigest = hex.EncodeToString(digest[:])
	return &nodepb.AgentReport{Epoch: epoch, Record: record}
}

func TestNodeSnapshotExitWithoutFinishedAtRequiresCompleteCurrentIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*nodepb.NodeSnapshot, *nodepb.RuntimeFact)
		want   nodepb.ContainerState
	}{
		{name: "complete current CRI", want: nodepb.ContainerState_CONTAINER_STATE_EXITED},
		{name: "incomplete CRI", want: nodepb.ContainerState_CONTAINER_STATE_UNKNOWN, change: func(snapshot *nodepb.NodeSnapshot, _ *nodepb.RuntimeFact) {
			snapshot.Sources[2].Complete = false
		}},
		{name: "previous capture epoch", want: nodepb.ContainerState_CONTAINER_STATE_UNKNOWN, change: func(snapshot *nodepb.NodeSnapshot, _ *nodepb.RuntimeFact) {
			snapshot.CaptureEpoch--
		}},
		{name: "missing Pod UID", want: nodepb.ContainerState_CONTAINER_STATE_UNKNOWN, change: func(_ *nodepb.NodeSnapshot, fact *nodepb.RuntimeFact) {
			fact.PodUid = ""
		}},
		{name: "missing container name", want: nodepb.ContainerState_CONTAINER_STATE_UNKNOWN, change: func(_ *nodepb.NodeSnapshot, fact *nodepb.RuntimeFact) {
			fact.ContainerName = ""
		}},
		{name: "missing container ID", want: nodepb.ContainerState_CONTAINER_STATE_UNKNOWN, change: func(_ *nodepb.NodeSnapshot, fact *nodepb.RuntimeFact) {
			fact.ContainerId = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			fact := &nodepb.RuntimeFact{ContainerId: strings.Repeat("a", 64), PodUid: biz.NewID(), PodNamespace: "tenant", PodName: "workload", ContainerName: "workspace", State: nodepb.ContainerState_CONTAINER_STATE_EXITED, ObservedAt: timestamppb.New(now)}
			snapshot := &nodepb.NodeSnapshot{CaptureEpoch: 7, CaptureStart: timestamppb.New(now.Add(-time.Second)), CaptureEnd: timestamppb.New(now), Containers: []*nodepb.RuntimeFact{fact}}
			for _, source := range []nodepb.Source{nodepb.Source_SOURCE_NVML, nodepb.Source_SOURCE_POD_RESOURCES, nodepb.Source_SOURCE_CRI} {
				snapshot.Sources = append(snapshot.Sources, &nodepb.SourceHealth{Source: source, ObservedAt: timestamppb.New(now), Complete: true})
			}
			if test.change != nil {
				test.change(snapshot, fact)
			}
			record := &nodepb.ImmutableRecord{Payload: &nodepb.ImmutableRecord_Snapshot{Snapshot: snapshot}}
			decoded, err := decodeNodeReport(runtimeDecodeReport(t, record, 7), now)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Snapshot == nil || len(decoded.Snapshot.Containers) != 1 {
				t.Fatalf("runtime snapshot was lost: %+v", decoded)
			}
			attempt := decoded.Snapshot.Containers[0]
			if attempt.State != uint32(test.want) || !attempt.FinishedAt.IsZero() {
				t.Fatalf("wrong current state or fabricated exit time: %+v", attempt)
			}
		})
	}
}

func TestRuntimeEventsAndJournalKeepExitTimestampRequirement(t *testing.T) {
	for _, kind := range []string{"event", "journal"} {
		for _, hasFinishedAt := range []bool{false, true} {
			name := kind + " missing exit time"
			if hasFinishedAt {
				name = kind + " with exit time"
			}
			t.Run(name, func(t *testing.T) {
				now := time.Now().UTC()
				fact := &nodepb.RuntimeFact{ContainerId: strings.Repeat("b", 64), PodUid: biz.NewID(), PodNamespace: "tenant", PodName: "workload", ContainerName: "workspace", State: nodepb.ContainerState_CONTAINER_STATE_EXITED, ObservedAt: timestamppb.New(now)}
				var wantFinished time.Time
				if hasFinishedAt {
					wantFinished = now.Add(-time.Second)
					fact.FinishedAt = timestamppb.New(wantFinished)
				}
				record := &nodepb.ImmutableRecord{}
				if kind == "event" {
					event := nodepb.RuntimeEventType_RUNTIME_EVENT_TYPE_STOPPED
					fact.EventType = &event
					record.Payload = &nodepb.ImmutableRecord_RuntimeFacts{RuntimeFacts: &nodepb.RuntimeFacts{Items: []*nodepb.RuntimeFact{fact}}}
				} else {
					record.Payload = &nodepb.ImmutableRecord_JournalExit{JournalExit: &nodepb.JournalExitResult{RequestId: biz.NewID(), Found: true, Attempts: []*nodepb.RuntimeFact{fact}}}
				}
				decoded, err := decodeNodeReport(runtimeDecodeReport(t, record, 7), now)
				if err != nil {
					t.Fatal(err)
				}
				want := uint32(nodepb.ContainerState_CONTAINER_STATE_UNKNOWN)
				if hasFinishedAt {
					want = uint32(nodepb.ContainerState_CONTAINER_STATE_EXITED)
				}
				if len(decoded.Attempts) != 1 || decoded.Attempts[0].State != want || !decoded.Attempts[0].FinishedAt.Equal(wantFinished) {
					t.Fatalf("legacy exit fact changed meaning: %+v", decoded.Attempts)
				}
			})
		}
	}
}
