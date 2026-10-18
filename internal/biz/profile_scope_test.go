package biz

import (
	"strings"
	"testing"
)

func TestProfileCatalogScopePreservesExecutionDigest(t *testing.T) {
	profile, group := testProfile(Whole)
	pool := &Pool{PoolID: testID, ClusterID: testID}
	scope, err := profileCatalogScope(*profile, group, pool, testID)
	if err != nil {
		t.Fatal(err)
	}
	if scope.ProfileID != profile.ProfileID || scope.ProfileVersion != profile.ProfileVersion || scope.SpecDigest != profile.SpecDigest || scope.ClusterID != testID || scope.PoolID != testID {
		t.Fatalf("incomplete catalog scope: %+v", scope)
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
			profile, group := testProfile(Whole)
			pool := &Pool{PoolID: testID, ClusterID: testID}
			tc.change(profile, group, pool)
			_, err := profileCatalogScope(*profile, group, pool, testID)
			requireReason(t, err, tc.reason)
		})
	}
	profile, _ := testProfile(Whole)
	if _, err := profileCatalogScope(*profile, nil, nil, testID); err == nil {
		t.Fatal("unknown group accepted")
	}
}
