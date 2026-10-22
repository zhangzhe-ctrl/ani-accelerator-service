package server

import (
	"strings"
	"testing"
	"time"

	nodepb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/node/v1"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNodeJournalResultRequiresOriginalScopeAndPositiveChain(testContext *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*nodepb.JournalExitResult, *biz.NodeReport)
		allow  bool
	}{
		{name: "positive_chain", allow: true},
		{name: "different_pod", change: func(_ *nodepb.JournalExitResult, report *biz.NodeReport) { report.Attempts[0].PodUID = biz.NewID() }},
		{name: "different_container", change: func(_ *nodepb.JournalExitResult, report *biz.NodeReport) { report.Attempts[0].ContainerName = "other" }},
		{name: "not_exited", change: func(_ *nodepb.JournalExitResult, report *biz.NodeReport) { report.Attempts[0].State = 2 }},
		{name: "missing_observation", change: func(_ *nodepb.JournalExitResult, report *biz.NodeReport) { report.Attempts[0].ObservedAt = time.Time{} }},
		{name: "observed_before_exit", change: func(_ *nodepb.JournalExitResult, report *biz.NodeReport) {
			report.Attempts[0].ObservedAt = report.Attempts[0].FinishedAt.Add(-time.Second)
		}},
		{name: "untrusted_uid", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) { journal.Provenance[0].Uid = "1000" }},
		{name: "untrusted_unit", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) {
			journal.Provenance[0].Unit = "application.service"
		}},
		{name: "untrusted_exe", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) {
			journal.Provenance[0].Exe = "/usr/bin/not-containerd"
		}},
		{name: "untrusted_transport", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) {
			journal.Provenance[0].Transport = "journal"
		}},
		{name: "missing_create_cursor", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) { journal.Provenance[0].CreateCursor = "" }},
		{name: "exit_before_create", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) { journal.Provenance[0].ExitMonotonic = 1 }},
		{name: "exit_time_mismatch", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) {
			journal.Provenance[0].ExitedAt = timestamppb.New(time.Now())
		}},
		{name: "false_with_attempts", change: func(journal *nodepb.JournalExitResult, _ *biz.NodeReport) { journal.Found = false }},
	} {
		testContext.Run(scenario.name, func(caseContext *testing.T) {
			now := time.Now().UTC()
			pod := biz.PodRef{ClusterID: biz.NewID(), UID: biz.NewID(), Namespace: "tenant", Name: "workload", ContainerName: "workspace"}
			id := strings.Repeat("a", 64)
			report := biz.NodeReport{Attempts: []biz.RuntimeAttempt{{ContainerID: id, PodUID: pod.UID, PodNamespace: pod.Namespace, PodName: pod.Name, ContainerName: pod.ContainerName, State: 3, ObservedAt: now, FinishedAt: now.Add(-time.Second)}}}
			journal := &nodepb.JournalExitResult{Found: true, Attempts: []*nodepb.RuntimeFact{{ContainerId: id}}, Provenance: []*nodepb.JournalProvenance{{MachineId: strings.Repeat("a", 32), BootId: strings.Repeat("b", 32), Unit: "containerd.service", Comm: "containerd", Uid: "0", Exe: "/usr/local/bin/containerd", Transport: "stdout", SandboxId: strings.Repeat("c", 64), SandboxCursor: "sandbox-cursor", CreateCursor: "create-cursor", Cursor: "exit-cursor", SandboxMonotonic: 1, CreateMonotonic: 2, ExitMonotonic: 3, CreatedAt: timestamppb.New(now.Add(-2 * time.Second)), ExitedAt: timestamppb.New(now.Add(-time.Second))}}}
			if scenario.change != nil {
				scenario.change(journal, &report)
			}
			err := validateNodeJournalResult(journal, pod, []string{id}, &report, now.Add(5*time.Second))
			if (err == nil) != scenario.allow {
				caseContext.Fatalf("positive chain acceptance mismatch: %v", err)
			}
			if scenario.allow && (report.Attempts[0].SandboxID != strings.Repeat("c", 64) || len(report.Attempts[0].JournalProvenance) == 0 || !report.Attempts[0].JournalObservedAt.Equal(now)) {
				caseContext.Fatal("validated attempt lost original provenance")
			}
			if !scenario.allow && !report.Attempts[0].JournalObservedAt.IsZero() {
				caseContext.Fatal("invalid journal acquired coverage")
			}
		})
	}
}

func TestNodeRecoverySessionNeverRestoresAnOlderSender(testContext *testing.T) {
	center := NewNodeTelemetryServer(nil, nil)
	current := center.registerRecoverySession("connection", "node", 2)
	stale := center.registerRecoverySession("connection", "node", 1)
	select {
	case <-stale.closed:
	default:
		testContext.Fatal("older stream remained eligible for server commands")
	}
	if center.sessions["connection\x00node"] != current {
		testContext.Fatal("older Welcome replaced a newer active sender")
	}
	waiter := &nodeRecoveryWaiter{done: make(chan nodeRecoveryResult, 1)}
	current.pending["request"] = waiter
	newest := center.registerRecoverySession("connection", "node", 3)
	select {
	case result := <-waiter.done:
		if result.err == nil {
			testContext.Fatal("epoch fencing reported success")
		}
	default:
		testContext.Fatal("fenced recovery caller remained blocked")
	}
	center.closeRecoverySession("connection", "node", current)
	if center.sessions["connection\x00node"] != newest {
		testContext.Fatal("older disconnect removed the current sender")
	}
}
