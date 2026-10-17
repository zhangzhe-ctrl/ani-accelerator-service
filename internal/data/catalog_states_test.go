package data

import (
	"context"
	"testing"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresMultipleProfilesAndAcceptedUsageSurviveCatalogChanges(t *testing.T) {
	f := pgSetup(t)
	for _, memory := range []int64{12288, 3072, 8193} {
		spec := *f.profile.Spec
		spec.MemoryMiBPerDevice = memory
		input := biz.CreateProfileInput{DisplayName: "additional immutable profile", Spec: &spec, ExpectedGroupVersion: f.group.Version}
		mutation := adminMutation()
		created, err := f.u.CreateProfile(adminContext("CreateProfile"), mutation, input)
		if err != nil {
			t.Fatal("same-group profile rejected", err)
		}
		replay, err := f.u.CreateProfile(adminContext("CreateProfile"), mutation, input)
		if err != nil || replay.ProfileID != created.ProfileID || replay.Spec.MemoryMiBPerDevice != memory {
			t.Fatal("same-key creation did not return immutable original", replay, err)
		}
		if err := f.db.View(context.Background(), func(r biz.Reader) error {
			var err error
			f.group, err = r.Group(context.Background(), f.group.GroupID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.View(context.Background(), func(r biz.Reader) error {
		profiles, err := r.Profiles(context.Background(), f.cluster.ClusterID, biz.Shared, "", 20)
		if err != nil {
			return err
		}
		if len(profiles) != 4 {
			t.Fatalf("same-group profiles did not coexist: %d", len(profiles))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	pendingUsage := f.projection(1)
	disabled, err := f.u.SetProfileState(adminContext("SetProfileState"), adminMutation(), f.profile.ProfileID, f.profile.Revision, biz.ProfileDisabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.u.SetProfileState(adminContext("SetProfileState"), adminMutation(), f.profile.ProfileID, f.profile.Revision, biz.ProfileActive); err == nil || err.Error() != "VERSION_CONFLICT" {
		t.Fatal("stale profile revision accepted", err)
	}
	tenant := &biz.TenantContext{RequestID: "catalog-state", TenantID: f.tenant, Actor: &adminActor}
	resolveContext := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, Action: "ResolveGpuRequest", TenantID: f.tenant, ClusterIDs: []string{f.cluster.ClusterID}})
	if _, err := f.u.Resolve(resolveContext, tenant, f.request); err == nil || err.Error() != "PROFILE_NOT_ACTIVE" {
		t.Fatal("disabled profile resolved", err)
	}
	restored, err := f.u.SetProfileState(adminContext("SetProfileState"), adminMutation(), f.profile.ProfileID, disabled.Revision, biz.ProfileActive)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.u.Resolve(resolveContext, tenant, f.request)
	if err != nil || resolved.Execution.ExecutionDigest != f.resolved.Execution.ExecutionDigest {
		t.Fatal("catalog state changed immutable execution", resolved, err)
	}
	if _, err := f.u.SetProfileState(adminContext("DeleteProfile"), adminMutation(), f.profile.ProfileID, restored.Revision, biz.ProfileDeleted); err != nil {
		t.Fatal(err)
	}
	if _, err := f.u.Resolve(resolveContext, tenant, f.request); err == nil || err.Error() != "PROFILE_NOT_ACTIVE" {
		t.Fatal("deleted profile resolved", err)
	}
	syncContext := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	if _, err := f.u.SyncUsage(syncContext, "already-accepted", pendingUsage); err != nil {
		t.Fatal("catalog deletion blocked accepted thin usage", err)
	}
}
