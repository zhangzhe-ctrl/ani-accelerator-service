package biz

import (
	"strings"
	"testing"
)

func TestProfileCatalogScopePreservesPlanDigest(t *testing.T) {
	plan := testPlan(t, Whole, 1024)
	group := &SupplyGroup{GroupID: testID, PoolID: testID, ClusterID: testID, Mode: Whole, ModelKey: plan.Profile.Spec.ModelKey, Baseline: baselineForTest(1024, Whole)}
	pool := &Pool{PoolID: testID, ClusterID: testID}
	scope, err := profileCatalogScope(*plan.Profile, group, pool, testID)
	if err != nil {
		t.Fatal(err)
	}
	if scope.ProfileID != plan.Profile.ProfileID || scope.ProfileVersion != plan.Profile.ProfileVersion || scope.SpecDigest != plan.Profile.SpecDigest || scope.ClusterID != testID || scope.PoolID != testID {
		t.Fatalf("incomplete catalog scope: %+v", scope)
	}
	digest, err := PlanDigest(plan)
	if err != nil || digest != plan.ResolutionDigest {
		t.Fatalf("catalog metadata changed the frozen plan: digest=%s error=%v", digest, err)
	}
}

func TestProfileCatalogScopeRejectsConflictingRelations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Profile, *SupplyGroup, *Pool)
		reason string
	}{
		{"missing-spec", func(p *Profile, _ *SupplyGroup, _ *Pool) { p.Spec = nil }, "PROFILE_SCOPE_MISMATCH"},
		{"missing-version", func(p *Profile, _ *SupplyGroup, _ *Pool) { p.ProfileVersion = 0 }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-group", func(_ *Profile, g *SupplyGroup, _ *Pool) { g.GroupID = NewID() }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-cluster", func(_ *Profile, g *SupplyGroup, _ *Pool) { g.ClusterID = NewID() }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-pool", func(_ *Profile, _ *SupplyGroup, p *Pool) { p.PoolID = NewID() }, "PROFILE_SCOPE_MISMATCH"},
		{"pool-cross-cluster", func(_ *Profile, _ *SupplyGroup, p *Pool) { p.ClusterID = NewID() }, "PROFILE_SCOPE_MISMATCH"},
		{"missing-baseline", func(_ *Profile, g *SupplyGroup, _ *Pool) { g.Baseline = nil }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-baseline", func(p *Profile, _ *SupplyGroup, _ *Pool) { p.BaselineID = NewID() }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-mode", func(_ *Profile, g *SupplyGroup, _ *Pool) { g.Mode = Shared }, "PROFILE_SCOPE_MISMATCH"},
		{"wrong-model", func(_ *Profile, g *SupplyGroup, _ *Pool) { g.ModelKey = "other-model" }, "PROFILE_SCOPE_MISMATCH"},
		{"forged-digest", func(p *Profile, _ *SupplyGroup, _ *Pool) { p.SpecDigest = strings.Repeat("0", 64) }, "SPEC_DIGEST_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := testPlan(t, Whole, 1024)
			group := &SupplyGroup{GroupID: testID, PoolID: testID, ClusterID: testID, Mode: Whole, ModelKey: plan.Profile.Spec.ModelKey, Baseline: baselineForTest(1024, Whole)}
			pool := &Pool{PoolID: testID, ClusterID: testID}
			tc.change(plan.Profile, group, pool)
			_, err := profileCatalogScope(*plan.Profile, group, pool, testID)
			requireReason(t, err, tc.reason)
		})
	}
	plan := testPlan(t, Whole, 1024)
	if _, err := profileCatalogScope(*plan.Profile, nil, nil, testID); err == nil {
		t.Fatal("unknown group accepted")
	}
}
