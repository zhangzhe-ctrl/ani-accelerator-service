package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const journalTestMachine = "2e5cc7ce6b9c4666a1943a6fa65c1858"
const journalTestBoot = "6644a471edb642618328f5a5a02fc2f0"

func journalFixture() (journalRecoveryConfig, biz.PodRef, [][]byte) {
	cfg := journalRecoveryConfig{Directory: "/host/journal/" + journalTestMachine, MachineID: journalTestMachine, NodeUID: "a59bc8aa-8f31-41a7-a402-4c88d0211dac", JournalctlPath: "/usr/bin/journalctl"}
	pod := biz.PodRef{ClusterID: "cluster", Namespace: "tenant", Name: "instance-0", UID: "481c72aa-da10-4544-ba08-29f090cf6e4d", ContainerName: "workspace"}
	sandbox, container := strings.Repeat("d", 64), strings.Repeat("1", 64)
	lines := [][]byte{
		journalLine(`RunPodSandbox for name:"instance-0" uid:"`+pod.UID+`" namespace:"tenant" returns sandbox id "`+sandbox+`"`, 100),
		journalLine(`CreateContainer within sandbox "`+sandbox+`" for name:"workspace" returns container id "`+container+`"`, 200),
		journalLine(`received container exit event container_id:"`+container+`" id:"`+container+`" pid:2672423 exited_at:{seconds:1600000000 nanos:424252708}`, 300),
	}
	return cfg, pod, lines
}

func journalLine(message string, monotonic int) []byte {
	record := map[string]string{
		"MESSAGE":     `time="2020-09-13T12:26:41Z" level=info msg=` + strconv.Quote(message),
		"_MACHINE_ID": journalTestMachine, "_BOOT_ID": journalTestBoot,
		"_SYSTEMD_UNIT": "containerd.service", "_COMM": "containerd", "_UID": "0",
		"_EXE": "/usr/local/bin/containerd", "_TRANSPORT": "stdout",
		"__REALTIME_TIMESTAMP": "1600000001000000", "__MONOTONIC_TIMESTAMP": strconv.Itoa(monotonic),
		"__CURSOR": "cursor-" + strconv.Itoa(monotonic),
	}
	line, _ := json.Marshal(record)
	return line
}

func journalQuery(lines [][]byte) journalQueryFunc {
	return func(_ context.Context, _ journalRecoveryConfig, _ string) ([]journalRecord, error) {
		return decodeJournalRecords(bytes.Join(lines, []byte("\n")))
	}
}

func TestJournalRecoveryUsesCompleteOriginalIdentityAndPositiveExit(t *testing.T) {
	cfg, pod, lines := journalFixture()
	statuses, ok, err := recoverJournalExitWith(context.Background(), cfg, pod, journalQuery(lines))
	if err != nil || !ok || len(statuses) != 1 {
		t.Fatalf("complete journal chain: statuses=%+v ok=%v error=%v", statuses, ok, err)
	}
	status := statuses[0].Status
	if status.Id != strings.Repeat("1", 64) || status.Metadata.Name != pod.ContainerName || status.Labels["io.kubernetes.pod.uid"] != pod.UID || status.Labels["io.kubernetes.pod.namespace"] != pod.Namespace || status.Labels["io.kubernetes.pod.name"] != pod.Name || status.State != cri.ContainerState_CONTAINER_EXITED || status.FinishedAt != 1600000000424252708 {
		t.Fatalf("identity or positive exit changed: %+v", status)
	}
	for _, value := range []string{cfg.NodeUID, journalTestMachine, journalTestBoot, "cursor-100", "cursor-200", "cursor-300"} {
		if !strings.Contains(statuses[0].Info["acc.runtime_journal"], value) {
			t.Fatalf("missing original journal provenance %q: %+v", value, statuses[0].Info)
		}
	}
}

func TestJournalRecoveryRejectsIncompleteOrConflictingFacts(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*journalRecoveryConfig, *biz.PodRef, *[][]byte)
	}{
		{"missing_sandbox", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) { *l = (*l)[1:] }},
		{"missing_create", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) { *l = append((*l)[:1], (*l)[2]) }},
		{"missing_exit", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) { *l = (*l)[:2] }},
		{"different_pod_uid", func(_ *journalRecoveryConfig, p *biz.PodRef, _ *[][]byte) {
			p.UID = "9a306469-bb48-47d6-9e50-4190e0602a49"
		}},
		{"different_namespace", func(_ *journalRecoveryConfig, p *biz.PodRef, _ *[][]byte) { p.Namespace = "another" }},
		{"different_name", func(_ *journalRecoveryConfig, p *biz.PodRef, _ *[][]byte) { p.Name = "replacement-0" }},
		{"different_container", func(_ *journalRecoveryConfig, p *biz.PodRef, _ *[][]byte) { p.ContainerName = "sidecar" }},
		{"wrong_machine", func(c *journalRecoveryConfig, _ *biz.PodRef, _ *[][]byte) { c.MachineID = strings.Repeat("a", 32) }},
		{"another_boot_exit", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = []byte(strings.ReplaceAll(string((*l)[2]), journalTestBoot, strings.Repeat("a", 32)))
		}},
		{"untrusted_process", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = []byte(strings.ReplaceAll(string((*l)[2]), `"_UID":"0"`, `"_UID":"1000"`))
		}},
		{"wrong_unit", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = []byte(strings.ReplaceAll(string((*l)[2]), "containerd.service", "application.service"))
		}},
		{"exit_event_id_conflict", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`received container exit event container_id:"`+strings.Repeat("1", 64)+`" id:"`+strings.Repeat("2", 64)+`" exited_at:{seconds:1600000000 nanos:424252708}`, 300)
		}},
		{"exit_before_create", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`received container exit event container_id:"`+strings.Repeat("1", 64)+`" id:"`+strings.Repeat("1", 64)+`" exited_at:{seconds:1600000000 nanos:424252708}`, 150)
		}},
		{"unknown_attempt", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			*l = append(*l, journalLine(`CreateContainer within sandbox "`+strings.Repeat("d", 64)+`" for name:"workspace" attempt:1 returns container id "`+strings.Repeat("2", 64)+`"`, 400))
		}},
		{"new_start_after_exit", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			*l = append(*l, journalLine(`StartContainer for "`+strings.Repeat("1", 64)+`"`, 400))
		}},
		{"no_finished_at", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`received container exit event container_id:"`+strings.Repeat("1", 64)+`" id:"`+strings.Repeat("1", 64)+`"`, 300)
		}},
		{"malformed_timestamp", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`received container exit event container_id:"`+strings.Repeat("1", 64)+`" id:"`+strings.Repeat("1", 64)+`" exited_at:{seconds:1600000000 nanos:1000000000}`, 300)
		}},
		{"not_found_only", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`ContainerStatus for "`+strings.Repeat("1", 64)+`" failed: NotFound`, 300)
		}},
		{"remove_only", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`RemoveContainer for "`+strings.Repeat("1", 64)+`" returns successfully`, 300)
		}},
		{"state_text_only", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) {
			(*l)[2] = journalLine(`Container to stop "`+strings.Repeat("1", 64)+`" must be in running or unknown state, current state "CONTAINER_EXITED"`, 300)
		}},
		{"invalid_json", func(_ *journalRecoveryConfig, _ *biz.PodRef, l *[][]byte) { (*l)[2] = []byte(`{"MESSAGE":`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, pod, lines := journalFixture()
			test.edit(&cfg, &pod, &lines)
			statuses, ok, _ := recoverJournalExitWith(context.Background(), cfg, pod, journalQuery(lines))
			if ok || len(statuses) != 0 {
				t.Fatalf("incomplete or conflicting original records released occupancy: %+v", statuses)
			}
		})
	}
}

func TestJournalRecoveryRequiresEveryAttemptAndKeepsIdentity(t *testing.T) {
	cfg, pod, lines := journalFixture()
	second := strings.Repeat("2", 64)
	lines = append(lines, journalLine(`CreateContainer within sandbox "`+strings.Repeat("d", 64)+`" for name:"workspace" attempt:1 returns container id "`+second+`"`, 400), journalLine(`received container exit event container_id:"`+second+`" id:"`+second+`" exit_status:137 exited_at:{seconds:1600000000 nanos:800000000}`, 500))
	statuses, ok, err := recoverJournalExitWith(context.Background(), cfg, pod, journalQuery(lines))
	if err != nil || !ok || len(statuses) != 2 || statuses[0].Status.Id != strings.Repeat("1", 64) || statuses[1].Status.Id != second {
		t.Fatalf("all attempts not preserved: %+v %v %v", statuses, ok, err)
	}
	if reflect.DeepEqual(statuses[0].Status, statuses[1].Status) {
		t.Fatal("attempt identity collapsed")
	}
}

func TestJournalRecoveryStopsOnQueryError(t *testing.T) {
	cfg, pod, _ := journalFixture()
	statuses, ok, err := recoverJournalExitWith(context.Background(), cfg, pod, func(context.Context, journalRecoveryConfig, string) ([]journalRecord, error) {
		return nil, errors.New("unreadable journal")
	})
	if err == nil || ok || len(statuses) != 0 {
		t.Fatalf("unavailable journal became positive: %+v %v %v", statuses, ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := recoverJournalExitWith(ctx, cfg, pod, journalQuery(nil)); ok || err == nil {
		t.Fatal("cancelled collection became positive")
	}
}

func TestJournalCommandArgumentsAndOutputAreBounded(t *testing.T) {
	cfg, _, _ := journalFixture()
	got := journalArguments(cfg, "a|b")
	want := []string{"--directory=" + cfg.Directory, "--unit=containerd.service", "--no-pager", "--output=json", "--all", "--quiet", "--grep=a|b", "_MACHINE_ID=" + cfg.MachineID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected command arguments: %#v", got)
	}
	buffer := journalBoundedBuffer{limit: 3}
	if n, err := buffer.Write([]byte("abcd")); err == nil || n != 3 || !buffer.overflow || buffer.String() != "abc" {
		t.Fatalf("journal output exceeded bounded buffer: %d %v %+v", n, err, buffer)
	}
	if _, err := decodeJournalRecords([]byte(strings.Repeat("x", journalMaxOutput+1))); err == nil {
		t.Fatal("oversized journal response accepted")
	}
	cfg.Timeout = time.Hour
	if journalTimeout(cfg) > 25*time.Second {
		t.Fatal("journal query timeout not bounded")
	}
}

// MESSAGE strings below are unchanged from the original node journal read
// inside the deployed ACC Pod; only unrelated trusted metadata was omitted.
func TestJournalDeployedRawMessageNeedsExactlyOneQuotedTokenDecode(t *testing.T) {
	raw := `{"_UID":"0","__CURSOR":"s=88615490b55c47eea6a6629edb6e75f6;i=2e767;b=6644a471edb642618328f5a5a02fc2f0;m=e647d0175;t=65e0053539903;x=e2cd7d7ec82e1298","_SYSTEMD_UNIT":"containerd.service","__MONOTONIC_TIMESTAMP":"61815456117","_TRANSPORT":"stdout","MESSAGE":"time=\"2026-10-17T10:49:11.260377427+08:00\" level=info msg=\"RunPodSandbox for name:\\\"instance-d6ae5e78-844a-4731-af16-a10b0e384ccd-0\\\"  uid:\\\"481c72aa-da10-4544-ba08-29f090cf6e4d\\\"  namespace:\\\"ani-tenant-92b09b38-ce59-4a4f-9293-f2b33148d799\\\"\"","__REALTIME_TIMESTAMP":"1792205351262467","_COMM":"containerd","_MACHINE_ID":"2e5cc7ce6b9c4666a1943a6fa65c1858","_BOOT_ID":"6644a471edb642618328f5a5a02fc2f0","_EXE":"/usr/local/bin/containerd"}
{"_TRANSPORT":"stdout","__REALTIME_TIMESTAMP":"1792205351879481","_MACHINE_ID":"2e5cc7ce6b9c4666a1943a6fa65c1858","__CURSOR":"s=88615490b55c47eea6a6629edb6e75f6;i=2e775;b=6644a471edb642618328f5a5a02fc2f0;m=e64866bac;t=65e00535d0339;x=3d913b9d4e37b882","MESSAGE":"time=\"2026-10-17T10:49:11.878947196+08:00\" level=info msg=\"RunPodSandbox for name:\\\"instance-d6ae5e78-844a-4731-af16-a10b0e384ccd-0\\\"  uid:\\\"481c72aa-da10-4544-ba08-29f090cf6e4d\\\"  namespace:\\\"ani-tenant-92b09b38-ce59-4a4f-9293-f2b33148d799\\\" returns sandbox id \\\"dd5a38fedde4838ab5afbb763954ff29fcee2c93e82b2b3d65544a6f01c14d36\\\"\"","_UID":"0","_BOOT_ID":"6644a471edb642618328f5a5a02fc2f0","_COMM":"containerd","_EXE":"/usr/local/bin/containerd","_SYSTEMD_UNIT":"containerd.service","__MONOTONIC_TIMESTAMP":"61816073132"}`
	records, err := decodeJournalRecords([]byte(raw))
	if err != nil || len(records) != 2 {
		t.Fatalf("deployed raw decode: records=%+v error=%v", records, err)
	}
	for _, r := range records {
		match := journalPod.FindStringSubmatch(r.Message)
		if strings.Contains(r.Message, `\`) || match == nil || match[1] != "instance-d6ae5e78-844a-4731-af16-a10b0e384ccd-0" || match[2] != "481c72aa-da10-4544-ba08-29f090cf6e4d" || match[3] != "ani-tenant-92b09b38-ce59-4a4f-9293-f2b33148d799" {
			t.Fatalf("actual quoted token decoding mismatch: %q", r.Message)
		}
	}
	if got := journalPod.FindStringSubmatch(records[1].Message)[5]; got != "dd5a38fedde4838ab5afbb763954ff29fcee2c93e82b2b3d65544a6f01c14d36" {
		t.Fatalf("sandbox identity changed: %q", got)
	}
}

func TestJournalRecoveryBudgetCoversDeployedScanWithinParentDeadline(t *testing.T) {
	if got := journalTimeout(journalRecoveryConfig{}); got < 20*time.Second || got >= 30*time.Second {
		t.Fatalf("default recovery budget %v cannot cover deployed 6.86s first scan and followup scans inside parent 30s", got)
	}
	if got := journalTimeout(journalRecoveryConfig{Timeout: time.Hour}); got != 25*time.Second {
		t.Fatalf("unbounded recovery timeout: %v", got)
	}
	cfg, pod, lines := journalFixture()
	expectedCursor := makeJournalFixtureCursor(journalTestBoot, 100, 1600000001000000)
	for i := range lines {
		var r map[string]string
		if err := json.Unmarshal(lines[i], &r); err != nil {
			t.Fatal(err)
		}
		r["__CURSOR"] = makeJournalFixtureCursor(r["_BOOT_ID"], int64((i+1)*100), 1600000001000000)
		lines[i], _ = json.Marshal(r)
	}
	calls := 0
	query := journalQuery(lines)
	statuses, ok, err := recoverJournalExitWith(context.Background(), cfg, pod, func(ctx context.Context, c journalRecoveryConfig, grep string) ([]journalRecord, error) {
		args := journalArguments(c, grep)
		cursor := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "--cursor=") {
				cursor = strings.TrimPrefix(arg, "--cursor=")
			}
		}
		if calls == 0 && cursor != "" {
			t.Fatal("initial UID query skipped original history")
		}
		if calls > 0 && cursor != expectedCursor {
			t.Fatalf("followup query did not seek exact earliest trusted cursor: %#v", args)
		}
		if deadline, exists := ctx.Deadline(); !exists || time.Until(deadline) < 19*time.Second || time.Until(deadline) > 20*time.Second {
			t.Fatal("query budget not preserved")
		}
		calls++
		return query(ctx, c, grep)
	})
	if err != nil || !ok || len(statuses) != 1 || calls != 3 {
		t.Fatalf("bounded cursor recovery failed: %+v ok=%v error=%v calls=%d", statuses, ok, err, calls)
	}
}

func makeJournalFixtureCursor(boot string, monotonic, realtime int64) string {
	return "s=88615490b55c47eea6a6629edb6e75f6;i=1;b=" + boot + ";m=" + strconv.FormatInt(monotonic, 16) + ";t=" + strconv.FormatInt(realtime, 16) + ";x=1"
}

func TestJournalRecoveryCursorUsesSameBootMonotonicOrder(t *testing.T) {
	first := journalRecord{Boot: journalTestBoot, Monotonic: 100, Realtime: 1600000001000000}
	first.Cursor = makeJournalFixtureCursor(first.Boot, first.Monotonic, first.Realtime)
	second := journalRecord{Boot: journalTestBoot, Monotonic: 200, Realtime: 1600000000000000}
	second.Cursor = makeJournalFixtureCursor(second.Boot, second.Monotonic, second.Realtime)
	if got := journalRecoveryCursor([]journalRecord{second, first}); got != first.Cursor {
		t.Fatalf("wall clock rollback changed earliest source location: %q", got)
	}
	second.Boot = strings.Repeat("a", 32)
	if got := journalRecoveryCursor([]journalRecord{first, second}); got != "" {
		t.Fatalf("cross-boot history was truncated: %q", got)
	}
	for _, cursor := range []string{"untrusted-cursor", makeJournalFixtureCursor(strings.Repeat("b", 32), 100, first.Realtime), makeJournalFixtureCursor(first.Boot, 101, first.Realtime), makeJournalFixtureCursor(first.Boot, 100, first.Realtime+1)} {
		first.Cursor = cursor
		if got := journalRecoveryCursor([]journalRecord{first}); got != "" {
			t.Fatalf("cursor conflicted with trusted source metadata: %q", got)
		}
	}
	if journalTimeout(journalRecoveryConfig{TimeoutSeconds: 60}) != 25*time.Second || journalTimeout(journalRecoveryConfig{TimeoutSeconds: 12}) != 12*time.Second {
		t.Fatal("operator timeout configuration is not bounded")
	}
	parent, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	cfg, pod, _ := journalFixture()
	_, _, err := recoverJournalExitWith(parent, cfg, pod, func(ctx context.Context, _ journalRecoveryConfig, _ string) ([]journalRecord, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("recovery exceeded parent deadline: %v", err)
	}
}
