package biz

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func currentRetirementBatch() ObservationBatch {
	now := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	pod := PodRef{ClusterID: testID, Namespace: "test", Name: "retired", UID: "pod-old", ContainerName: "inference"}
	batch := ObservationBatch{
		ConnectionRef: "test:retirement",
		ClusterID:     testID,
		NodeRevisions: map[string]uint64{"node-1": 7},
		Releases: []ReleaseProof{{
			Pod: pod, DeviceID: testID, NodeUID: "node-1",
			ObservedAt: now, ReceivedAt: now, EvidenceRef: "current-state:retired",
			Retirement: RetirementCurrentState, AllocationAbsent: true, NodeOnline: true,
			CheckedNodeRevision: 7,
		}},
	}
	for _, source := range []string{"scheduler", "runtime", "allocation"} {
		batch.Claims = append(batch.Claims, Claim{
			Pod: pod, DeviceID: testID, NodeUID: "node-1", Source: source, State: Active,
			ObservedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-time.Minute),
			MemoryMiB: 6144, CoreLimitPercent: 25, MemoryBlockMiB: 1,
			BaselineDigest: strings.Repeat("a", 64), EvidenceRef: "old:" + source,
		})
	}
	return batch
}

func TestCurrentStateRetirementCoversHistoricalUnknownClaims(t *testing.T) {
	batch := currentRetirementBatch()
	for index := range batch.Claims {
		batch.Claims[index].State = AllocationUnknown
		// The observation boundary is inclusive, as for legacy exit proofs.
		batch.Claims[index].ObservedAt = batch.Releases[0].ObservedAt
		batch.Claims[index].ReceivedAt = batch.Releases[0].ReceivedAt
	}
	if err := ValidateObservationBatch(batch); err != nil {
		t.Fatal(err)
	}
	proof := batch.Releases[0]
	binding, err := MergeBinding(testID, batch.Claims, &proof, nil)
	if err != nil || binding.State != ReleaseConfirmed || !binding.Observation.Complete {
		t.Fatalf("current reconciliation failed to retire old unknown claims: binding=%+v error=%v", binding, err)
	}
	if binding.EvidenceRef != proof.EvidenceRef || binding.Observation.ObservedAt != proof.ObservedAt.Format(time.RFC3339Nano) {
		t.Fatal("retirement lost its current observation boundary")
	}
	if proof.RuntimeExited || proof.AdmissionRejected {
		t.Fatal("current reconciliation was represented as a legacy exit or rejection")
	}
}

func TestCurrentStateRetirementPreservesScopeAndNewerClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReleaseProof, []Claim)
	}{
		{"node replacement", func(p *ReleaseProof, _ []Claim) { p.NodeUID = "node-2" }},
		{"same name new pod UID", func(p *ReleaseProof, _ []Claim) { p.Pod.UID = "pod-new" }},
		{"other container", func(p *ReleaseProof, _ []Claim) { p.Pod.ContainerName = "sidecar" }},
		{"other device", func(p *ReleaseProof, _ []Claim) { p.DeviceID = "10000000-0000-4000-8000-000000000002" }},
		{"allocation remains", func(p *ReleaseProof, _ []Claim) { p.AllocationAbsent = false }},
		{"node offline", func(p *ReleaseProof, _ []Claim) { p.NodeOnline = false }},
		{"unknown retirement mode", func(p *ReleaseProof, _ []Claim) { p.Retirement = "UNSUPPORTED" }},
		{"ambiguous exit assertion", func(p *ReleaseProof, _ []Claim) { p.RuntimeExited = true }},
		{"new runtime claim", func(p *ReleaseProof, claims []Claim) {
			claims[1].ObservedAt = p.ObservedAt.Add(time.Nanosecond)
		}},
		{"new allocation claim", func(p *ReleaseProof, claims []Claim) {
			claims[2].ObservedAt = p.ObservedAt.Add(time.Nanosecond)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := currentRetirementBatch()
			proof := batch.Releases[0]
			test.mutate(&proof, batch.Claims)
			binding, err := MergeBinding(testID, batch.Claims, &proof, nil)
			if err != nil {
				t.Fatal(err)
			}
			if binding.State == ReleaseConfirmed || binding.MemoryMiB != 6144 {
				t.Fatalf("retirement erased an uncovered occupancy: %+v", binding)
			}
		})
	}
}

func TestCurrentStateRetirementRequiresMatchingNodeRevision(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ObservationBatch)
	}{
		{"missing connection", func(b *ObservationBatch) { b.ConnectionRef = "" }},
		{"zero checked revision", func(b *ObservationBatch) { b.Releases[0].CheckedNodeRevision = 0 }},
		{"missing node revisions", func(b *ObservationBatch) { b.NodeRevisions = nil }},
		{"other node revision", func(b *ObservationBatch) { b.NodeRevisions = map[string]uint64{"node-2": 7} }},
		{"different node revision", func(b *ObservationBatch) { b.NodeRevisions["node-1"]++ }},
		{"unknown mode", func(b *ObservationBatch) { b.Releases[0].Retirement = "UNSUPPORTED" }},
		{"current and exit", func(b *ObservationBatch) { b.Releases[0].RuntimeExited = true }},
		{"current and admission", func(b *ObservationBatch) { b.Releases[0].AdmissionRejected = true }},
		{"no retirement mode", func(b *ObservationBatch) { b.Releases[0].Retirement = "" }},
		{"both legacy modes", func(b *ObservationBatch) {
			b.Releases[0].Retirement = ""
			b.Releases[0].RuntimeExited = true
			b.Releases[0].AdmissionRejected = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := currentRetirementBatch()
			test.mutate(&batch)
			if err := ValidateObservationBatch(batch); err == nil || err.Error() != "INVALID_RELEASE_PROOF" {
				t.Fatalf("invalid retirement accepted: %v", err)
			}
		})
	}
}

func TestCurrentStateRetirementKeepsLegacyProofSemantics(t *testing.T) {
	for _, admission := range []bool{false, true} {
		name := "runtime exit"
		if admission {
			name = "admission rejection"
		}
		t.Run(name, func(t *testing.T) {
			batch := currentRetirementBatch()
			batch.ConnectionRef, batch.NodeRevisions = "", nil
			proof := &batch.Releases[0]
			proof.Retirement, proof.CheckedNodeRevision = "", 0
			proof.RuntimeExited, proof.AdmissionRejected = !admission, admission
			encoded, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["retirement"]; exists {
				t.Fatal("legacy proof encoding acquired a new mode field")
			}
			var decoded ReleaseProof
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			batch.Releases[0] = decoded
			if err := ValidateObservationBatch(batch); err != nil {
				t.Fatalf("persisted legacy proof now requires a node revision: %v", err)
			}
			binding, err := MergeBinding(testID, batch.Claims, &decoded, nil)
			want := ReleaseConfirmed
			if admission {
				want = Active
			}
			if err != nil || binding.State != want {
				t.Fatalf("legacy proof changed scope: binding=%+v error=%v", binding, err)
			}
			if admission {
				binding, err = MergeBinding(testID, batch.Claims[:1], &decoded, nil)
				if err != nil || binding.State != ReleaseConfirmed {
					t.Fatalf("legacy rejection no longer retires scheduler intent: binding=%+v error=%v", binding, err)
				}
			}
		})
	}
}
