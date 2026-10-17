package data

import (
	"context"
	"embed"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// These adapters exist only in test compilation and isolated PostgreSQL data.
type testProvider struct{}

func (testProvider) Inspect(_ context.Context, _ biz.Cluster, g biz.SupplyGroup) (biz.GroupInspection, error) {
	i := biz.GroupInspection{BaselineDigest: g.Baseline.BaselineDigest, QueueName: g.QueueName, ObservedAt: time.Now()}
	for _, n := range g.Nodes {
		i.Nodes = append(i.Nodes, biz.InspectedNode{Node: n, ModelKey: g.ModelKey, Factor: g.Baseline.MemoryBlockMiB, SplitCount: g.Baseline.SplitCount, ConfigDigest: g.Baseline.NodeConfigDigest, Labels: map[string]string{biz.GroupLabel: g.GroupID, biz.ModelLabel: g.ModelKey, biz.BaselineLabel: g.Baseline.BaselineID}, DeviceBudgetsMiB: []int64{24576}})
	}
	return i, nil
}
func (testProvider) Collect(_ context.Context, c biz.Cluster, _ []biz.SupplyGroup) (biz.ObservationBatch, error) {
	return biz.ObservationBatch{ClusterID: c.ClusterID}, nil
}

type pgFixture struct {
	db       *Postgres
	u        *biz.Usecase
	cluster  *biz.Cluster
	pool     *biz.Pool
	group    *biz.SupplyGroup
	profile  *biz.Profile
	resolved *biz.ResolvedGpuRequest
	request  *biz.GpuRequest
	tenant   string
}

var adminActor = biz.Actor{Type: "user", ID: "acc-test-operator"}

func adminContext(action string) context.Context {
	return biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, Action: action, ClusterIDs: []string{"*"}})
}
func adminMutation() *biz.AdminMutation {
	return &biz.AdminMutation{Context: &biz.AdminRead{RequestID: "test", Actor: &adminActor}, IdempotencyKey: biz.NewID()}
}
func openPGTest(t *testing.T) *Postgres {
	t.Helper()
	path := os.Getenv("ACC_TEST_DSN_FILE")
	if path == "" {
		t.Skip("requires isolated PostgreSQL: ACC_TEST_DSN_FILE")
	}
	db, err := OpenPostgres(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}
func pgSetup(t *testing.T) *pgFixture {
	t.Helper()
	db := openPGTest(t)
	connection := "test:" + biz.NewID()
	codec, err := biz.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	u, err := biz.NewUsecase(db, testProvider{}, codec, []string{connection}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, err := u.RegisterCluster(adminContext("RegisterCluster"), adminMutation(), "test", connection)
	if err != nil {
		t.Fatal(err)
	}
	p, err := u.CreatePool(adminContext("CreatePool"), adminMutation(), c.ClusterID, "pool")
	if err != nil {
		t.Fatal(err)
	}
	b := &biz.ProviderBaseline{BaselineID: biz.NewID(), AdapterID: "volcano-hami-mib-v1", VolcanoVersion: "v1.12.0", VolcanoImageDigest: "sha256:" + strings.Repeat("a", 64), DevicePluginVersion: "v1.12.0", DevicePluginImageDigest: "sha256:" + strings.Repeat("b", 64), HamiCoreRevision: "test", MemoryBlockMiB: 1, MemoryEncoding: "MIB", SplitCount: 4, MemoryScalePercent: 100, CoreScalePercent: 100, CoreBudgetAdditive: true, NodeConfigDigest: strings.Repeat("c", 64), SchedulerConfigDigest: strings.Repeat("d", 64), RecipeVersion: "volcano-hami-mib-v1", RuntimeClassName: "nvidia"}
	b.BaselineDigest, _ = biz.BaselineDigest(b)
	g, err := u.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), adminMutation(), biz.AdoptInput{PoolID: p.PoolID, DisplayName: "group", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: "node-" + biz.NewID(), Name: "node"}}, ModelKey: "nvidia-test", Baseline: b, QueueName: "gpu"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Admission != biz.Closed {
		t.Fatal("adopt did not default closed")
	}
	profile, err := u.CreateProfile(adminContext("CreateProfile"), adminMutation(), biz.CreateProfileInput{DisplayName: "6 GiB", Spec: &biz.ProfileSpec{GroupID: g.GroupID, Mode: biz.Shared, ModelKey: g.ModelKey, MemoryMiBPerDevice: 6144, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, ExpectedGroupVersion: g.Version})
	if err != nil {
		t.Fatal(err)
	}
	g, err = u.SetAdmission(adminContext("SetAdmission"), adminMutation(), biz.AdmissionInput{GroupID: g.GroupID, ExpectedVersion: 2, Desired: biz.Open, Reason: "isolated test only"})
	if err != nil {
		t.Fatal(err)
	}
	r := &biz.GpuRequest{ClusterID: c.ClusterID, PoolID: p.PoolID, ProfileID: profile.ProfileID, ProfileVersion: 1, Replicas: 2, DevicesPerReplica: 1, ContainerName: "inference"}
	resolved, err := biz.BuildResolution(r, profile, g, 16)
	if err != nil {
		t.Fatal(err)
	}
	return &pgFixture{db, u, c, p, g, profile, resolved, r, biz.NewID()}
}
func (f *pgFixture) projection(revision uint64) *biz.UsageProjection {
	p := &biz.UsageProjection{Ref: &biz.UsageRef{TenantID: f.tenant, OwnerService: "ani-inference", ResourceID: biz.NewID(), CreateOperationID: biz.NewID()}, Revision: revision, State: biz.UsageState(revision), ProfileID: f.profile.ProfileID, ProfileVersion: f.profile.ProfileVersion, GroupID: f.group.GroupID, BaselineID: f.group.Baseline.BaselineID, ClusterID: f.cluster.ClusterID, ContainerName: f.request.ContainerName, ExecutionDigest: f.resolved.Execution.ExecutionDigest, SourceOperationCreatedAt: "2026-09-23T00:00:00Z"}
	suffix := "accepted"
	if revision == 2 {
		p.EndReason = biz.OwnerReleased
		suffix = "gpu-fully-released"
	}
	p.SourceFactRef = "quota-operation:" + p.Ref.CreateOperationID + ":" + suffix
	p.PayloadDigest, _ = biz.ProjectionDigest(p)
	return p
}
func TestPostgresRuntimeRoleAndAtomicIdempotency(t *testing.T) {
	f := pgSetup(t)
	if _, err := f.db.pool.Exec(context.Background(), testSQL(t, "legacy_check_5.sql")); err == nil {
		t.Fatal("runtime role can DDL")
	}
	if _, err := f.db.pool.Exec(context.Background(), testSQL(t, "legacy_check_4.sql")); err == nil {
		t.Fatal("runtime role can temporary DDL")
	}
	m := adminMutation()
	ctx := adminContext("CreatePool")
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			p, e := f.u.CreatePool(ctx, m, f.cluster.ClusterID, "concurrent")
			if e != nil {
				errs <- e
			} else {
				ids <- p.PoolID
			}
		})
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		t.Fatal(e)
	}
	id := ""
	for x := range ids {
		if id != "" && id != x {
			t.Fatal("idempotency produced two pools")
		}
		id = x
	}
	if _, err := f.u.CreatePool(ctx, m, f.cluster.ClusterID, "changed"); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
		t.Fatal(err)
	}
	err := f.db.Transact(context.Background(), func(tx biz.Transaction) error {
		if err := tx.InsertPool(context.Background(), biz.Pool{PoolID: biz.NewID(), ClusterID: f.cluster.ClusterID, DisplayName: "must-rollback"}); err != nil {
			return err
		}
		return biz.Invalid("INJECTED_BEFORE_COMMIT")
	})
	if err == nil {
		t.Fatal("failure injection accepted")
	}
	var n int
	if err = f.db.pool.QueryRow(context.Background(), testSQL(t, "legacy_check_3.sql"), f.cluster.ClusterID).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	t.Log("DB-01/03/05: runtime DDL denied; 12 concurrent writes converge; failed transaction leaves no pool")
}
func TestPostgresProjectionOrderConflictAndRestart(t *testing.T) {
	f := pgSetup(t)
	p := f.projection(2)
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := f.u.SyncUsage(ctx, "test", p); errs <- err })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	old := *p
	old.Revision = 1
	old.State = biz.Declared
	old.EndReason = 0
	old.SourceFactRef = "quota-operation:" + p.Ref.CreateOperationID + ":accepted"
	old.PayloadDigest, _ = biz.ProjectionDigest(&old)
	res, err := f.u.SyncUsage(ctx, "test", &old)
	if err != nil || res.State != biz.Ended || !res.Replayed {
		t.Fatal(res, err)
	}
	conflict := *p
	conflict.EndReason = biz.CanceledUnsent
	conflict.SourceFactRef = "quota-operation:" + p.Ref.CreateOperationID + ":canceled-unsent"
	conflict.PayloadDigest, _ = biz.ProjectionDigest(&conflict)
	if _, err = f.u.SyncUsage(ctx, "test", &conflict); err == nil || err.Error() != "USAGE_PROJECTION_CONFLICT" {
		t.Fatal(err)
	}
	ref := *p.Ref
	ref.CreateOperationID = biz.NewID()
	conflict = old
	conflict.Ref = &ref
	conflict.SourceFactRef = "quota-operation:" + ref.CreateOperationID + ":accepted"
	conflict.PayloadDigest, _ = biz.ProjectionDigest(&conflict)
	if _, err = f.u.SyncUsage(ctx, "test", &conflict); err == nil || err.Error() != "USAGE_PROJECTION_CONFLICT" {
		t.Fatal(err)
	}
	second := openPGTest(t)
	err = second.View(context.Background(), func(r biz.Reader) error {
		got, e := r.Usage(context.Background(), *p.Ref)
		if e != nil {
			return e
		}
		if got.State != biz.Ended {
			t.Fatal("terminal state lost")
		}
		other := *p.Ref
		other.TenantID = biz.NewID()
		if _, e = r.Usage(context.Background(), other); !biz.IsNotFound(e) {
			t.Fatal("cross-tenant read", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("SYNC-RX-01/02/03: parallel terminal-first delivery, stale revision and create-operation resurrection; DB persistence across connections")
}
func TestPostgresBindingsBeforeProjectionAndScopedRelease(t *testing.T) {
	for _, owner := range []string{"ani-inference", "ani-resource-service"} {
		t.Run(owner, func(t *testing.T) { testOwnerBindingsBeforeProjectionAndScopedRelease(t, owner) })
	}
}
func testOwnerBindingsBeforeProjectionAndScopedRelease(t *testing.T, ownerService string) {
	f := pgSetup(t)
	p := f.projection(1)
	p.Ref.OwnerService = ownerService
	if ownerService == "ani-resource-service" {
		p.ContainerName = "workspace"
	}
	p.PayloadDigest, _ = biz.ProjectionDigest(p)
	now := time.Now().UTC()
	device := biz.NewID()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "pod", UID: "pod-" + biz.NewID(), ContainerName: p.ContainerName}
	d := biz.DeviceRecord{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &f.group.Nodes[0], VendorDeviceID: "GPU-test", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{Complete: true, ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano)}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}
	batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{d}}
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		batch.Claims = append(batch.Claims, biz.Claim{Pod: pod, DeviceID: device, NodeUID: f.group.Nodes[0].UID, Source: source, Cursor: "1", ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "test:" + source, OwnerRef: p.Ref, ExecutionDigest: p.ExecutionDigest})
	}
	for _, source := range []string{"kubernetes", "podresources", "cri"} {
		batch.Watermarks = append(batch.Watermarks, biz.SourceWatermark{ClusterID: f.cluster.ClusterID, NodeUID: f.group.Nodes[0].UID, Source: source, Cursor: "1", ObservedAt: now, ReceivedAt: now, Complete: true})
	}
	store := func(b biz.ObservationBatch) {
		t.Helper()
		if err := f.db.Transact(context.Background(), func(tx biz.Transaction) error { return tx.SaveObservation(context.Background(), b) }); err != nil {
			t.Fatal(err)
		}
	}
	store(batch)
	check := func(state biz.AllocationState, linked bool) {
		t.Helper()
		err := f.db.View(context.Background(), func(r biz.Reader) error {
			bs, e := r.DeviceBindings(context.Background(), device)
			if e != nil {
				return e
			}
			if len(bs) != 1 || bs[0].State != state || (bs[0].VerifiedUsageRef != nil) != linked {
				t.Fatalf("bindings=%+v", bs)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(biz.Active, false)
	capacity, err := f.u.AdminGetCapacity(adminContext("AdminGetCapacity"), adminMutation().Context, f.profile.ProfileID, 1, true)
	if err != nil || capacity.KnownCommittedUnits != 1 || capacity.KnownFreeUnits != 3 {
		t.Fatal(capacity, err)
	}
	owner := biz.WithCaller(context.Background(), biz.Caller{Service: ownerService, Action: "ObserveRelease", TenantID: p.Ref.TenantID})
	obs, err := f.u.ObserveRelease(owner, "test", p.Ref, biz.NewID(), []biz.PodRef{pod})
	if err != nil || obs.Finding != biz.StillPresent {
		t.Fatal(obs, err)
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	if _, err = f.u.SyncUsage(ctx, "test", p); err != nil {
		t.Fatal(err)
	}
	check(biz.Active, true)
	p.Revision = 2
	p.State = biz.Ended
	p.EndReason = biz.OwnerReleased
	p.SourceFactRef = "quota-operation:" + p.Ref.CreateOperationID + ":gpu-fully-released"
	p.PayloadDigest, _ = biz.ProjectionDigest(p)
	if _, err = f.u.SyncUsage(ctx, "test", p); err != nil {
		t.Fatal(err)
	}
	check(biz.Active, true)
	store(biz.ObservationBatch{ClusterID: batch.ClusterID})
	check(biz.Active, true)
	later := now.Add(time.Millisecond)
	proof := biz.ReleaseProof{Pod: pod, DeviceID: device, NodeUID: f.group.Nodes[0].UID, ObservedAt: later, ReceivedAt: later, EvidenceRef: "test:positive-release", RuntimeExited: true, AllocationAbsent: true, NodeOnline: true, OwnerRef: p.Ref, ExecutionDigest: p.ExecutionDigest}
	store(biz.ObservationBatch{ClusterID: batch.ClusterID, Releases: []biz.ReleaseProof{proof}})
	check(biz.ReleaseConfirmed, true)
	obs, err = f.u.ObserveRelease(owner, "test", p.Ref, biz.NewID(), []biz.PodRef{pod})
	if err != nil || obs.Finding != biz.AllocationReleased {
		t.Fatal(obs, err)
	}
	capacity, err = f.u.AdminGetCapacity(adminContext("AdminGetCapacity"), adminMutation().Context, f.profile.ProfileID, 1, true)
	if err != nil || capacity.KnownFreeUnits != 4 {
		t.Fatal(capacity, err)
	}
	t.Log("CAP-01/03/04, REL-01/02: three sources count once; ENDED and missing poll preserve allocation; positive exact-scope proof retires older evidence")
}

func TestPostgresConstraintsVersionRaceAndConcurrentAssociation(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	copyGroup := *f.group
	copyGroup.GroupID = biz.NewID()
	err := f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.InsertGroup(ctx, copyGroup) })
	if err == nil {
		t.Fatal("same Node UID admitted to two groups")
	}
	if err = f.db.View(ctx, func(r biz.Reader) error {
		_, e := r.Group(ctx, copyGroup.GroupID)
		if !biz.IsNotFound(e) {
			t.Fatal("failed group write not atomic", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := *f.profile
	changedSpec := *changed.Spec
	changedSpec.MemoryMiBPerDevice++
	changed.Spec = &changedSpec
	changed.SpecDigest, _ = biz.SpecDigest(changed.Spec, changed.BaselineID)
	changed.Revision++
	if err = f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.UpdateProfile(ctx, changed, f.profile.Revision) }); err == nil {
		t.Fatal("runtime mutated immutable profile resources")
	}
	for _, path := range [][]string{{"state"}, {"revision"}, {"spec"}} {
		_, err := f.db.pool.Exec(ctx, testSQL(t, "corrupt_profile_catalog_json.sql"), f.profile.ProfileID, path)
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatal("missing profile field bypassed catalog protection", path, err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := f.u.SetAdmission(adminContext("SetAdmission"), adminMutation(), biz.AdmissionInput{GroupID: f.group.GroupID, ExpectedVersion: f.group.Version, Desired: biz.Closed, Reason: "version race"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if err.Error() == "VERSION_CONFLICT" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("version race", success, conflict)
	}
	// CLOSED prevents new resolutions but an already accepted execution snapshot still syncs.
	p := f.projection(1)
	device := biz.NewID()
	now := time.Now().UTC()
	pod := biz.PodRef{ClusterID: f.cluster.ClusterID, Namespace: "test", Name: "race", UID: biz.NewID(), ContainerName: "inference"}
	batch := biz.ObservationBatch{ClusterID: f.cluster.ClusterID, Devices: []biz.DeviceRecord{{Device: biz.PhysicalDevice{DeviceID: device, ClusterID: f.cluster.ClusterID, GroupID: f.group.GroupID, Node: &f.group.Nodes[0], VendorDeviceID: "GPU-race", ModelKey: f.group.ModelKey, PhysicalMemoryBytes: 24576 << 20, Health: "HEALTHY", Observation: &biz.Observation{ObservedAt: now.Format(time.RFC3339Nano), ReceivedAt: now.Format(time.RFC3339Nano), Complete: true}}, ProviderMemoryBlocks: 24576, MemoryBlockMiB: 1, SafeMemoryBudgetMiB: 24576, BaselineDigest: f.group.Baseline.BaselineDigest}}}
	for _, source := range []string{"scheduler", "allocation", "runtime"} {
		batch.Claims = append(batch.Claims, biz.Claim{Pod: pod, DeviceID: device, NodeUID: f.group.Nodes[0].UID, Source: source, ObservedAt: now, ReceivedAt: now, State: biz.Active, MemoryMiB: 6144, CoreLimitPercent: 25, BaselineDigest: f.group.Baseline.BaselineDigest, MemoryBlockMiB: 1, EvidenceRef: "race:" + source, OwnerRef: p.Ref, ExecutionDigest: p.ExecutionDigest})
	}
	syncCtx := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	concurrentErrors := make(chan error, 10)
	for range 5 {
		wg.Go(func() { _, e := f.u.SyncUsage(syncCtx, "concurrent", p); concurrentErrors <- e })
		wg.Go(func() {
			concurrentErrors <- f.db.Transact(ctx, func(tx biz.Transaction) error { return tx.SaveObservation(ctx, batch) })
		})
	}
	wg.Wait()
	close(concurrentErrors)
	for err := range concurrentErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	err = f.db.View(ctx, func(r biz.Reader) error {
		bs, e := r.DeviceBindings(ctx, device)
		if e != nil {
			return e
		}
		if len(bs) != 1 || bs[0].VerifiedUsageRef == nil || *bs[0].VerifiedUsageRef != *p.Ref {
			t.Fatal("concurrent association did not converge", bs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A binding may never reference another tenant's otherwise valid resource.
	_, err = f.db.pool.Exec(ctx, testSQL(t, "legacy_check_1.sql"), biz.NewID(), device)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23503" {
		t.Fatal("tenant-preserving foreign key absent", err)
	}
	t.Log("DB-04/05 RUN-03: unique node and immutable profile, atomic failed group, CAS race, concurrent sync/binding and cross-tenant FK")
}

func TestPostgresCatalogDenialsAndStablePaging(t *testing.T) {
	f := pgSetup(t)
	for _, name := range []string{"second", "third"} {
		if _, err := f.u.CreatePool(adminContext("CreatePool"), adminMutation(), f.cluster.ClusterID, name); err != nil {
			t.Fatal(err)
		}
	}
	page := &biz.Page{Size: 1}
	seen := map[string]bool{}
	for {
		result, err := f.u.ListPools(adminContext("ListPools"), adminMutation().Context, f.cluster.ClusterID, page)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || seen[result.Items[0].PoolID] {
			t.Fatal("unstable page", result)
		}
		seen[result.Items[0].PoolID] = true
		if result.NextToken == "" {
			break
		}
		page.Token = result.NextToken
	}
	if len(seen) != 3 {
		t.Fatal("incomplete pages", seen)
	}
	tenant := &biz.TenantContext{RequestID: "test", TenantID: f.tenant, Actor: &adminActor}
	caller := biz.Caller{Service: "ani-governance", Actor: adminActor, Action: "CheckGpuFit", TenantID: f.tenant, ClusterIDs: []string{f.cluster.ClusterID}}
	fit, err := f.u.CheckFit(biz.WithCaller(context.Background(), caller), tenant, f.request)
	if err != nil || fit.Status != biz.FitUnknown || fit.EstimateKnown {
		t.Fatal("missing inventory not UNKNOWN", fit, err)
	}
	caller.Action = "GetProfile"
	if _, err = f.u.GetProfile(biz.WithCaller(context.Background(), caller), tenant, f.profile.ProfileID, 2); !biz.IsNotFound(err) {
		t.Fatal("nonexistent version visible", err)
	}
	if _, err = f.u.GetProfile(biz.WithCaller(context.Background(), caller), tenant, f.profile.ProfileID, ^uint64(0)); !biz.IsNotFound(err) {
		t.Fatal("version overflow reached database", err)
	}
	caller.ClusterIDs = []string{biz.NewID()}
	if _, err = f.u.GetProfile(biz.WithCaller(context.Background(), caller), tenant, f.profile.ProfileID, 1); !biz.IsNotFound(err) {
		t.Fatal("out-of-scope profile visible", err)
	}
	m := adminMutation()
	closed, err := f.u.SetAdmission(adminContext("SetAdmission"), m, biz.AdmissionInput{GroupID: f.group.GroupID, ExpectedVersion: f.group.Version, Desired: biz.Closed, Reason: "close test"})
	if err != nil {
		t.Fatal(err)
	}
	caller.Action = "ResolveGpuRequest"
	caller.ClusterIDs = []string{f.cluster.ClusterID}
	if _, err = f.u.Resolve(biz.WithCaller(context.Background(), caller), tenant, f.request); err == nil || err.Error() != "ADMISSION_CLOSED" {
		t.Fatal("closed resolves", err)
	}
	restricted := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", Actor: adminActor, Action: "SetAdmission", ClusterIDs: []string{biz.NewID()}})
	if _, err = f.u.SetAdmission(restricted, m, biz.AdmissionInput{GroupID: f.group.GroupID, ExpectedVersion: f.group.Version, Desired: biz.Closed, Reason: "close test"}); !biz.IsNotFound(err) {
		t.Fatal("idempotency bypassed changed scope", err)
	}
	if closed.Version != f.group.Version+1 {
		t.Fatal(closed)
	}
	badBaseline := *f.group.Baseline
	badBaseline.MemoryBlockMiB = 256
	badBaseline.BaselineDigest, _ = biz.BaselineDigest(&badBaseline)
	if _, err = f.u.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), adminMutation(), biz.AdoptInput{PoolID: f.pool.PoolID, DisplayName: "bad", Mode: biz.Shared, Nodes: f.group.Nodes, ModelKey: f.group.ModelKey, Baseline: &badBaseline, QueueName: "gpu"}); err == nil || err.Error() != "MEMORY_UNIT_MISMATCH" {
		t.Fatal("non-MiB configuration accepted", err)
	}
	t.Log("CAT-05/06 AUTH-05/06: PG keyset pages, missing version/scope, unknown capacity, CLOSED/execution snapshot and current-scope idempotency checks")
}

//go:embed testdata/*.sql
var testQueries embed.FS

func testSQL(t *testing.T, name string) string {
	t.Helper()
	b, e := testQueries.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
