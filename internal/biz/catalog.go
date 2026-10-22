package biz

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type Usecase struct {
	repo        Repository
	provider    Provider
	cursor      *CursorCodec
	connections map[string]bool
	maxReplicas uint32
	freshness   time.Duration
	now         func() time.Time
}

func NewUsecase(repo Repository, provider Provider, cursor *CursorCodec, connections []string, maxReplicas uint32, freshness time.Duration) (*Usecase, error) {
	if repo == nil || provider == nil || cursor == nil || maxReplicas == 0 || maxReplicas > 16 || freshness <= 0 || freshness > 5*time.Minute {
		return nil, Invalid("INVALID_RUNTIME_CONFIG")
	}
	u := &Usecase{repo: repo, provider: provider, cursor: cursor, maxReplicas: maxReplicas, freshness: freshness, connections: map[string]bool{}, now: time.Now}
	for _, ref := range connections {
		if !Ref(ref) || u.connections[ref] {
			return nil, Invalid("INVALID_CONNECTION_REF")
		}
		u.connections[ref] = true
	}
	return u, nil
}

// connectionRefs is derived exclusively from provider.json by composition.
func (u *Usecase) connectionRefs() []string {
	refs := make([]string, 0, len(u.connections))
	for ref := range u.connections {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
func mutate[T any](ctx context.Context, u *Usecase, m *AdminMutation, method string, input any, fn func(Transaction) (*T, error)) (*T, error) {
	if err := AuthorizeMutation(ctx, m, method); err != nil {
		return nil, err
	}
	d, err := Hash(input)
	if err != nil {
		return nil, err
	}
	actorHash, err := Hash(m.Context.Actor)
	if err != nil {
		return nil, err
	}
	var result *T
	err = u.repo.Transact(ctx, func(tx Transaction) error {
		if err := tx.Lock(ctx, "mutation:"+actorHash+":"+method+":"+m.IdempotencyKey); err != nil {
			return err
		}
		prior, err := tx.Replay(ctx, actorHash, method, m.IdempotencyKey)
		if err != nil {
			return err
		}
		if prior != nil {
			if prior.Digest != d {
				return Conflict("IDEMPOTENCY_CONFLICT")
			}
			result = new(T)
			if json.Unmarshal(prior.Result, result) != nil {
				return Precondition("STORED_RESULT_INVALID")
			}
			return u.authorizeMutationResult(ctx, tx, any(result))
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		if err = u.authorizeMutationResult(ctx, tx, any(result)); err != nil {
			return err
		}
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Remember(ctx, IdempotencyRecord{actorHash, method, m.IdempotencyKey, d, b})
	})
	return result, err
}

// prepareMutation releases the short idempotency/read transaction before any
// provider work. mutate repeats arbitration after that work, so a same-key winner
// is returned before checking a possibly stale prepared catalog snapshot.
func prepareMutation[T any](ctx context.Context, u *Usecase, m *AdminMutation, method string, input any, read func(Reader) error) (*T, error) {
	if err := AuthorizeMutation(ctx, m, method); err != nil {
		return nil, err
	}
	d, err := Hash(input)
	if err != nil {
		return nil, err
	}
	actor, err := Hash(m.Context.Actor)
	if err != nil {
		return nil, err
	}
	var result *T
	err = u.repo.Transact(ctx, func(tx Transaction) error {
		if err := tx.Lock(ctx, "mutation:"+actor+":"+method+":"+m.IdempotencyKey); err != nil {
			return err
		}
		prior, err := tx.Replay(ctx, actor, method, m.IdempotencyKey)
		if err != nil {
			return err
		}
		if prior == nil {
			return read(tx)
		}
		if prior.Digest != d {
			return Conflict("IDEMPOTENCY_CONFLICT")
		}
		result = new(T)
		if json.Unmarshal(prior.Result, result) != nil {
			return Precondition("STORED_RESULT_INVALID")
		}
		return u.authorizeMutationResult(ctx, tx, any(result))
	})
	return result, err
}

// Catalog entities are immutable or versioned. Compare their complete persisted
// snapshots after external verification; never apply evidence to a newer group.
func checkCatalogSnapshot(ctx context.Context, r Reader, c *Cluster, p *Pool, g *SupplyGroup, profile *Profile) error {
	compare := func(before, after any) error {
		a, err := Hash(before)
		if err != nil {
			return err
		}
		b, err := Hash(after)
		if err != nil {
			return err
		}
		if a != b {
			return Conflict("VERSION_CONFLICT")
		}
		return nil
	}
	if c != nil {
		v, err := r.Cluster(ctx, c.ClusterID)
		if err != nil {
			return err
		}
		if err = compare(c, v); err != nil {
			return err
		}
	}
	if p != nil {
		v, err := r.Pool(ctx, p.PoolID)
		if err != nil {
			return err
		}
		if err = compare(p, v); err != nil {
			return err
		}
	}
	if g != nil {
		v, err := r.Group(ctx, g.GroupID)
		if err != nil {
			return err
		}
		if err = compare(g, v); err != nil {
			return err
		}
	}
	if profile != nil {
		v, err := r.Profile(ctx, profile.ProfileID, profile.ProfileVersion)
		if err != nil {
			return err
		}
		if err = compare(profile, v); err != nil {
			return err
		}
	}
	return nil
}
func (u *Usecase) authorizeMutationResult(ctx context.Context, r Reader, value any) error {
	switch v := value.(type) {
	case *Cluster:
		return u.authorizeCluster(ctx, r, v.ClusterID)
	case *Pool:
		return u.authorizeCluster(ctx, r, v.ClusterID)
	case *SupplyGroup:
		return u.authorizeCluster(ctx, r, v.ClusterID)
	case *Profile:
		g, err := r.Group(ctx, v.Spec.GroupID)
		if err != nil {
			return err
		}
		return u.authorizeCluster(ctx, r, g.ClusterID)
	default:
		return Denied()
	}
}
func (u *Usecase) RegisterCluster(ctx context.Context, m *AdminMutation, name, connection string) (*Cluster, error) {
	if !Text(name, 64) || !u.connections[connection] {
		return nil, Invalid("INVALID_CONNECTION_REF")
	}
	return mutate(ctx, u, m, "RegisterCluster", []string{name, connection}, func(tx Transaction) (*Cluster, error) {
		c := &Cluster{NewID(), name, connection, 1}
		return c, tx.InsertCluster(ctx, *c)
	})
}
func (u *Usecase) CreatePool(ctx context.Context, m *AdminMutation, cluster, name string) (*Pool, error) {
	if !UUID(cluster) || !Text(name, 64) {
		return nil, Invalid("INVALID_POOL")
	}
	return mutate(ctx, u, m, "CreatePool", []string{cluster, name}, func(tx Transaction) (*Pool, error) {
		if err := u.authorizeCluster(ctx, tx, cluster); err != nil {
			return nil, err
		}
		p := &Pool{NewID(), cluster, name}
		return p, tx.InsertPool(ctx, *p)
	})
}

type AdoptInput struct {
	PoolID      string            `json:"pool_id"`
	DisplayName string            `json:"display_name"`
	Mode        SupplyMode        `json:"mode"`
	Nodes       []NodeRef         `json:"nodes"`
	ModelKey    string            `json:"model_key"`
	Baseline    *ProviderBaseline `json:"baseline"`
	QueueName   string            `json:"queue_name"`
}

func (u *Usecase) AdoptSupplyGroup(ctx context.Context, m *AdminMutation, a AdoptInput) (*SupplyGroup, error) {
	if !UUID(a.PoolID) || !Text(a.DisplayName, 64) || !Label(a.ModelKey) || !Name(a.QueueName) || len(a.Nodes) == 0 || len(a.Nodes) > 1024 {
		return nil, Invalid("INVALID_GROUP")
	}
	if err := ValidateBaseline(a.Baseline, a.Mode); err != nil {
		return nil, err
	}
	a.Nodes = append([]NodeRef{}, a.Nodes...)
	sort.Slice(a.Nodes, func(i, j int) bool { return a.Nodes[i].UID < a.Nodes[j].UID })
	names := map[string]bool{}
	for i, n := range a.Nodes {
		if !Ref(n.UID) || !Text(n.Name, 253) || names[n.Name] || (i > 0 && a.Nodes[i-1].UID == n.UID) {
			return nil, Invalid("INVALID_GROUP_NODES")
		}
		names[n.Name] = true
	}
	var p *Pool
	var c *Cluster
	prior, err := prepareMutation[SupplyGroup](ctx, u, m, "AdoptSupplyGroup", a, func(r Reader) error {
		var err error
		p, err = r.Pool(ctx, a.PoolID)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, p.ClusterID); err != nil {
			return err
		}
		c, err = r.Cluster(ctx, p.ClusterID)
		return err
	})
	if err != nil || prior != nil {
		return prior, err
	}
	b := *a.Baseline
	g := &SupplyGroup{GroupID: NewID(), PoolID: p.PoolID, ClusterID: p.ClusterID, DisplayName: a.DisplayName, Mode: a.Mode, Nodes: a.Nodes, ModelKey: a.ModelKey, Baseline: &b, Admission: Closed, QueueName: a.QueueName, Version: 1, AdoptedAt: u.now().UTC().Format(time.RFC3339Nano)}
	inspection, err := u.inspect(ctx, *c, *g, false)
	if err != nil {
		return nil, err
	}
	return mutate(ctx, u, m, "AdoptSupplyGroup", a, func(tx Transaction) (*SupplyGroup, error) {
		if err := checkCatalogSnapshot(ctx, tx, c, p, nil, nil); err != nil {
			return nil, err
		}
		if err := u.checkInspection(*g, false, inspection); err != nil {
			return nil, err
		}
		return g, tx.InsertGroup(ctx, *g)
	})
}
func (u *Usecase) inspect(ctx context.Context, c Cluster, g SupplyGroup, labels bool) (GroupInspection, error) {
	i, err := u.provider.Inspect(ctx, c, g)
	if err != nil {
		return i, err
	}
	return i, u.checkInspection(g, labels, i)
}
func (u *Usecase) checkInspection(g SupplyGroup, labels bool, i GroupInspection) error {
	if i.ObservedAt.IsZero() || u.now().Sub(i.ObservedAt) > u.freshness || i.ObservedAt.After(u.now().Add(5*time.Second)) {
		return Precondition("SOURCE_STALE")
	}
	if i.BaselineDigest != g.Baseline.BaselineDigest || i.QueueName != g.QueueName || len(i.Nodes) != len(g.Nodes) {
		return Precondition("CONFIG_DRIFT")
	}
	want := map[string]NodeRef{}
	for _, n := range g.Nodes {
		want[n.UID] = n
	}
	for _, n := range i.Nodes {
		ref, ok := want[n.Node.UID]
		if !ok || ref != n.Node || n.ModelKey != g.ModelKey || n.Factor != g.Baseline.MemoryBlockMiB || n.SplitCount != g.Baseline.SplitCount || n.ConfigDigest != g.Baseline.NodeConfigDigest {
			return Precondition("CONFIG_DRIFT")
		}
		delete(want, n.Node.UID)
		if labels && (n.Labels[GroupLabel] != g.GroupID || n.Labels[ModelLabel] != g.ModelKey || n.Labels[BaselineLabel] != g.Baseline.BaselineID) {
			return Precondition("NODE_LABEL_MISMATCH")
		}
	}
	return nil
}

type CreateProfileInput struct {
	DisplayName          string       `json:"display_name"`
	Spec                 *ProfileSpec `json:"spec"`
	ExpectedGroupVersion uint64       `json:"expected_group_version"`
}

func (u *Usecase) CreateProfile(ctx context.Context, m *AdminMutation, a CreateProfileInput) (*Profile, error) {
	if a.Spec == nil || !UUID(a.Spec.GroupID) || !Text(a.DisplayName, 128) || a.ExpectedGroupVersion == 0 {
		return nil, Invalid("INVALID_PROFILE")
	}
	var g *SupplyGroup
	var c *Cluster
	prior, err := prepareMutation[Profile](ctx, u, m, "CreateProfile", a, func(r Reader) error {
		var err error
		g, err = r.Group(ctx, a.Spec.GroupID)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		if g.Version != a.ExpectedGroupVersion {
			return Conflict("VERSION_CONFLICT")
		}
		if a.Spec.Mode != g.Mode || a.Spec.ModelKey != g.ModelKey {
			return Precondition("PROFILE_SCOPE_MISMATCH")
		}
		if err = ValidateSpec(a.Spec, g.Baseline); err != nil {
			return err
		}
		c, err = r.Cluster(ctx, g.ClusterID)
		return err
	})
	if err != nil || prior != nil {
		return prior, err
	}
	first, err := u.inspect(ctx, *c, *g, true)
	if err != nil {
		return nil, err
	}
	if err = checkDeviceBudgets(a.Spec, first); err != nil {
		return nil, err
	}
	return mutate(ctx, u, m, "CreateProfile", a, func(tx Transaction) (*Profile, error) {
		if err := tx.Lock(ctx, "group:"+g.GroupID); err != nil {
			return nil, err
		}
		if err := checkCatalogSnapshot(ctx, tx, c, nil, g, nil); err != nil {
			return nil, err
		}
		if err := u.checkInspection(*g, true, first); err != nil {
			return nil, err
		}
		d, err := SpecDigest(a.Spec, g.Baseline.BaselineID)
		if err != nil {
			return nil, err
		}
		p := &Profile{ProfileID: NewID(), ProfileVersion: 1, DisplayName: a.DisplayName, Spec: a.Spec, BaselineID: g.Baseline.BaselineID, SpecDigest: d, State: ProfileActive, Revision: 1}
		if err = tx.InsertProfile(ctx, *p, g.ClusterID); err != nil {
			return nil, err
		}
		prior := g.Version
		g.Version++
		if err = tx.UpdateGroup(ctx, *g, prior); err != nil {
			return nil, err
		}
		return p, nil
	})
}

type AdmissionInput struct {
	GroupID         string         `json:"group_id"`
	ExpectedVersion uint64         `json:"expected_version"`
	Desired         AdmissionState `json:"desired"`
	Reason          string         `json:"reason"`
}

func (u *Usecase) SetAdmission(ctx context.Context, m *AdminMutation, a AdmissionInput) (*SupplyGroup, error) {
	if !UUID(a.GroupID) || a.ExpectedVersion == 0 || !Text(a.Reason, 512) || (a.Desired != Open && a.Desired != Closed) {
		return nil, Invalid("INVALID_ADMISSION")
	}
	var g *SupplyGroup
	var c *Cluster
	var profiles []Profile
	prior, err := prepareMutation[SupplyGroup](ctx, u, m, "SetAdmission", a, func(r Reader) error {
		var err error
		g, err = r.Group(ctx, a.GroupID)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		if g.Version != a.ExpectedVersion {
			return Conflict("VERSION_CONFLICT")
		}
		if a.Desired == Open {
			ps, err := r.Profiles(ctx, g.ClusterID, g.Mode, "", 10000)
			if err != nil {
				return err
			}
			for _, p := range ps {
				if p.Spec.GroupID == g.GroupID && p.State == ProfileActive {
					profiles = append(profiles, p)
				}
			}
			if len(profiles) == 0 {
				return Precondition("PROFILE_NOT_ACTIVE")
			}
			c, err = r.Cluster(ctx, g.ClusterID)
			return err
		}
		return nil
	})
	if err != nil || prior != nil {
		return prior, err
	}
	var checks []GroupInspection
	for _, p := range profiles {
		i, err := u.checkActiveConfiguration(ctx, *c, *g, &p)
		if err != nil {
			return nil, err
		}
		checks = append(checks, i)
	}
	return mutate(ctx, u, m, "SetAdmission", a, func(tx Transaction) (*SupplyGroup, error) {
		if err := tx.Lock(ctx, "group:"+g.GroupID); err != nil {
			return nil, err
		}
		if err := checkCatalogSnapshot(ctx, tx, c, nil, g, nil); err != nil {
			return nil, err
		}
		for j, p := range profiles {
			if err := checkCatalogSnapshot(ctx, tx, nil, nil, nil, &p); err != nil {
				return nil, err
			}
			if err := u.checkInspection(*g, true, checks[j]); err != nil {
				return nil, err
			}
		}
		prior := g.Version
		g.Version++
		g.Admission = a.Desired
		return g, tx.UpdateGroup(ctx, *g, prior)
	})
}
func (u *Usecase) checkActiveConfiguration(ctx context.Context, c Cluster, g SupplyGroup, p *Profile) (GroupInspection, error) {
	if p.State != ProfileActive {
		return GroupInspection{}, Precondition("PROFILE_NOT_ACTIVE")
	}
	i, err := u.inspect(ctx, c, g, true)
	if err != nil {
		return i, err
	}
	return i, checkDeviceBudgets(p.Spec, i)
}
func checkDeviceBudgets(s *ProfileSpec, i GroupInspection) error {
	count := 0
	for _, n := range i.Nodes {
		for _, budget := range n.DeviceBudgetsMiB {
			count++
			if budget <= 0 || s.MemoryMiBPerDevice > budget || (s.Mode == Whole && s.MemoryMiBPerDevice != budget) {
				return Precondition("DEVICE_MEMORY_MISMATCH")
			}
		}
	}
	if count == 0 {
		return Precondition("DEVICE_CAPACITY_UNKNOWN")
	}
	return nil
}
func (u *Usecase) Resolve(ctx context.Context, a *TenantContext, gpu *GpuRequest) (*ResolvedGpuRequest, error) {
	if err := AuthorizeTenant(ctx, a, "ResolveGpuRequest"); err != nil {
		return nil, err
	}
	return u.resolve(ctx, gpu)
}
func (u *Usecase) resolve(ctx context.Context, gpu *GpuRequest) (*ResolvedGpuRequest, error) {
	if err := ValidateRequest(gpu, u.maxReplicas); err != nil {
		return nil, err
	}
	var plan *ResolvedGpuRequest
	var p *Profile
	var g *SupplyGroup
	var c *Cluster
	err := u.repo.View(ctx, func(r Reader) error {
		if err := u.authorizeCluster(ctx, r, gpu.ClusterID); err != nil {
			return err
		}
		var err error
		p, err = r.Profile(ctx, gpu.ProfileID, gpu.ProfileVersion)
		if err != nil {
			return err
		}
		g, err = r.Group(ctx, p.Spec.GroupID)
		if err != nil {
			return err
		}
		if g.Admission != Open {
			return Precondition("ADMISSION_CLOSED")
		}
		if err := u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		if gpu.ClusterID != g.ClusterID || gpu.PoolID != g.PoolID {
			return Precondition("PROFILE_SCOPE_MISMATCH")
		}
		c, err = r.Cluster(ctx, g.ClusterID)
		return err
	})
	if err != nil {
		return nil, err
	}
	inspection, err := u.checkActiveConfiguration(ctx, *c, *g, p)
	if err != nil {
		return nil, err
	}
	err = u.repo.View(ctx, func(r Reader) error {
		if err := checkCatalogSnapshot(ctx, r, c, nil, g, p); err != nil {
			return err
		}
		if err := u.checkInspection(*g, true, inspection); err != nil {
			return err
		}
		view, err := u.capacity(ctx, r, p, g, true)
		if err != nil {
			return err
		}
		for _, d := range view.Devices {
			for _, issue := range d.Issues {
				if issue == "CAPACITY_INVARIANT_VIOLATION" {
					return Precondition(issue)
				}
			}
		}
		plan, err = BuildResolution(gpu, p, g, u.maxReplicas)
		return err
	})
	return plan, err
}
func (u *Usecase) GetProfile(ctx context.Context, a *TenantContext, id string, version uint64) (*Profile, error) {
	if err := AuthorizeTenant(ctx, a, "GetProfile"); err != nil {
		return nil, err
	}
	if !UUID(id) || version == 0 {
		return nil, Invalid("INVALID_PROFILE")
	}
	if version != 1 {
		return nil, NotFound()
	}
	var p *Profile
	err := u.repo.View(ctx, func(r Reader) error {
		var err error
		p, err = r.Profile(ctx, id, version)
		if err != nil {
			return err
		}
		g, err := r.Group(ctx, p.Spec.GroupID)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		if p.State != ProfileActive {
			return NotFound()
		}
		return nil
	})
	return p, err
}
func IsNotFound(err error) bool { var e *Error; return errors.As(err, &e) && e.Kind == "not_found" }

// Mutable catalog state is separate from the immutable execution contract.
func (u *Usecase) SetProfileState(ctx context.Context, m *AdminMutation, id string, expected uint64, desired ProfileState) (*Profile, error) {
	if !UUID(id) || expected == 0 || (desired != ProfileActive && desired != ProfileDisabled && desired != ProfileDeleted) {
		return nil, Invalid("INVALID_PROFILE_STATE")
	}
	method := "SetProfileState"
	if desired == ProfileDeleted {
		method = "DeleteProfile"
	}
	return mutate(ctx, u, m, method, []any{id, expected, desired}, func(tx Transaction) (*Profile, error) {
		if err := tx.Lock(ctx, "profile:"+id); err != nil {
			return nil, err
		}
		p, err := tx.Profile(ctx, id, 1)
		if err != nil {
			return nil, err
		}
		if p.Revision != expected {
			return nil, Conflict("VERSION_CONFLICT")
		}
		if p.State == ProfileDeleted {
			return nil, Precondition("PROFILE_DELETED")
		}
		p.State = desired
		p.Revision++
		return p, tx.UpdateProfile(ctx, *p, expected)
	})
}

type ProfilePreview struct {
	Spec            *ProfileSpec `json:"spec"`
	UnusedMemoryMiB int64        `json:"unused_memory_mib"`
	UnusedCoreUnits uint32       `json:"unused_core_units"`
}

func (u *Usecase) PreviewProfile(ctx context.Context, a *AdminRead, id string, parts uint32) (*ProfilePreview, error) {
	if err := AuthorizeAdmin(ctx, a, "PreviewProfile"); err != nil {
		return nil, err
	}
	if !UUID(id) || (parts != 2 && parts != 4 && parts != 8) {
		return nil, Invalid("INVALID_PROFILE_PARTS")
	}
	var c *Cluster
	var g *SupplyGroup
	err := u.repo.View(ctx, func(r Reader) error {
		var err error
		g, err = r.Group(ctx, id)
		if err != nil {
			return err
		}
		if err = u.authorizeCluster(ctx, r, g.ClusterID); err != nil {
			return err
		}
		c, err = r.Cluster(ctx, g.ClusterID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if g.Mode != Shared {
		return nil, Invalid("SHAPE_UNSUPPORTED")
	}
	i, err := u.inspect(ctx, *c, *g, true)
	if err != nil {
		return nil, err
	}
	var usable int64
	for _, n := range i.Nodes {
		for _, b := range n.DeviceBudgetsMiB {
			if b <= 0 {
				return nil, Precondition("DEVICE_CAPACITY_UNKNOWN")
			}
			if usable == 0 {
				usable = b
			}
			if b != usable {
				return nil, Precondition("DEVICE_MEMORY_MISMATCH")
			}
		}
	}
	factor := int64(g.Baseline.MemoryBlockMiB)
	memory := usable / factor / int64(parts) * factor
	cores := 100 / parts
	if memory <= 0 || cores == 0 {
		return nil, Invalid("SHAPE_UNSUPPORTED")
	}
	return &ProfilePreview{Spec: &ProfileSpec{GroupID: id, Mode: Shared, ModelKey: g.ModelKey, MemoryMiBPerDevice: memory, CoreLimitPercent: cores, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE"}, UnusedMemoryMiB: usable - memory*int64(parts), UnusedCoreUnits: 100 - cores*parts}, nil
}
