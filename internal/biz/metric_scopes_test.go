package biz

import (
	"testing"
	"time"
)

func TestMetricScopeConflictsPrecedeTenantFilterAndNeverExpire(t *testing.T) {
	now := time.Now().UTC()
	a := &UsageRef{TenantID: "tenant-a", OwnerService: "ani-resource-service", ResourceID: "r1", CreateOperationID: "c1"}
	b := &UsageRef{TenantID: "tenant-b", OwnerService: "ani-resource-service", ResourceID: "r2", CreateOperationID: "c2"}
	event := func(id, uid string, ref *UsageRef, at time.Time, rev uint64) MetricScopeEvent {
		return MetricScopeEvent{Revision: rev, RecordedAt: at, SourceKey: "same-source", Active: true, Scope: MetricScope{ScopeID: id, BindingID: id, PodUID: uid, Ref: ref, CandidateComplete: true, OwnershipState: "VERIFIED"}}
	}
	events := []MetricScopeEvent{event("one", "uid-a", a, now.Add(-40*24*time.Hour), 1), event("two", "uid-b", b, now.Add(-time.Hour), 2)}
	got := ResolveMetricScopes(events, MetricScopeFilter{TenantID: b.TenantID}, now.Add(-2*time.Hour), now)
	if len(got) != 1 || got[0].Ref.TenantID != b.TenantID || got[0].OwnershipState != "OWNERSHIP_AMBIGUOUS" || got[0].CandidateComplete {
		t.Fatalf("cross-tenant/retired candidate lost: %+v", got)
	}
	events[1].Scope.Ref = a
	got = ResolveMetricScopes(events, MetricScopeFilter{TenantID: a.TenantID}, now.Add(-2*time.Hour), now)
	if len(got) != 2 || got[0].OwnershipState != "VERIFIED" || got[1].OwnershipState != "VERIFIED" {
		t.Fatalf("same lifecycle UID replacement rejected: %+v", got)
	}
	events[1].Scope.Ref = &UsageRef{TenantID: a.TenantID, OwnerService: a.OwnerService, ResourceID: a.ResourceID, CreateOperationID: "different-create"}
	got = ResolveMetricScopes(events, MetricScopeFilter{TenantID: a.TenantID}, now.Add(-2*time.Hour), now)
	if got[0].OwnershipState != "OWNERSHIP_AMBIGUOUS" {
		t.Fatal("create lifecycle collision ignored")
	}
}

func TestMetricScopeNilCurrentDoesNotEraseKnownPast(t *testing.T) {
	now := time.Now().UTC()
	ref := &UsageRef{TenantID: "a", ResourceID: "r"}
	events := []MetricScopeEvent{{Revision: 1, RecordedAt: now.Add(-time.Hour), SourceKey: "source", Active: true, Scope: MetricScope{ScopeID: "past", BindingID: "binding", PodUID: "uid", Ref: ref}}, {Revision: 2, RecordedAt: now.Add(-time.Minute), SourceKey: "source", Active: true, Scope: MetricScope{ScopeID: "unknown", BindingID: "binding", PodUID: "uid"}}}
	got := ResolveMetricScopes(events, MetricScopeFilter{TenantID: "a"}, now.Add(-2*time.Hour), now)
	if len(got) != 1 || got[0].ValidTo != events[1].RecordedAt.Format(time.RFC3339Nano) {
		t.Fatalf("known interval overwritten: %+v", got)
	}
	if latest := ResolveMetricScopes(events, MetricScopeFilter{TenantID: "a"}, now.Add(-30*time.Second), now); len(latest) != 0 {
		t.Fatal("past owner was extended into unknown current interval")
	}
}

func TestMetricScopeNodeIdentityCannotBeReconstructedFromBareUUID(t *testing.T) {
	now := time.Now().UTC()
	ref := &UsageRef{TenantID: "a", ResourceID: "r"}
	events := []MetricScopeEvent{{Revision: 1, RecordedAt: now.Add(-time.Hour), SourceKey: "same-node-name-and-gpu", Active: true, Scope: MetricScope{ScopeID: "a", BindingID: "a", PodUID: "p1", Node: &NodeRef{UID: "old-node", Name: "node"}, DeviceID: "old-device", Ref: ref}}, {Revision: 2, RecordedAt: now.Add(-time.Minute), SourceKey: "same-node-name-and-gpu", Active: true, Scope: MetricScope{ScopeID: "b", BindingID: "b", PodUID: "p2", Node: &NodeRef{UID: "new-node", Name: "node"}, DeviceID: "new-device", Ref: ref}}}
	got := ResolveMetricScopes(events, MetricScopeFilter{TenantID: "a"}, now.Add(-2*time.Hour), now)
	for _, scope := range got {
		if scope.CandidateComplete || scope.OwnershipState != "OWNERSHIP_UNKNOWN" {
			t.Fatalf("node recreation silently merged: %+v", got)
		}
	}
}
