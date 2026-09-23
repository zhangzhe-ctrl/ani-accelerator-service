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
	evidence    ConsumptionEvidence
	cursor      *CursorCodec
	connections map[string]bool
	maxReplicas uint32
	freshness   time.Duration
	now         func() time.Time
}

func NewUsecase(repo Repository, provider Provider, evidence ConsumptionEvidence, cursor *CursorCodec, connections []string, maxReplicas uint32, freshness time.Duration) (*Usecase, error) {
	if repo == nil || provider == nil || evidence == nil || cursor == nil || maxReplicas == 0 || maxReplicas > 16 || freshness <= 0 || freshness > 5*time.Minute {
		return nil, Invalid("INVALID_RUNTIME_CONFIG")
	}
	u := &Usecase{repo: repo, provider: provider, evidence: evidence, cursor: cursor, maxReplicas: maxReplicas, freshness: freshness, connections: map[string]bool{}, now: time.Now}
	for _, ref := range connections {
		if !Ref(ref) || u.connections[ref] {
			return nil, Invalid("INVALID_CONNECTION_REF")
		}
		u.connections[ref] = true
	}
	return u, nil
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
			return authorizeMutationResult(ctx, tx, any(result))
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		if err = authorizeMutationResult(ctx, tx, any(result)); err != nil {
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
func authorizeMutationResult(ctx context.Context, r Reader, value any) error {
	switch v := value.(type) {
	case *Cluster:
		return AuthorizeCluster(ctx, v.ClusterID)
	case *Pool:
		return AuthorizeCluster(ctx, v.ClusterID)
	case *SupplyGroup:
		return AuthorizeCluster(ctx, v.ClusterID)
	case *Profile:
		g, err := r.Group(ctx, v.Spec.GroupID)
		if err != nil {
			return err
		}
		return AuthorizeCluster(ctx, g.ClusterID)
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
	if err := AuthorizeCluster(ctx, cluster); err != nil {
		return nil, err
	}
	return mutate(ctx, u, m, "CreatePool", []string{cluster, name}, func(tx Transaction) (*Pool, error) {
		if _, err := tx.Cluster(ctx, cluster); err != nil {
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
	if a.Baseline.Verification != nil && (a.Baseline.Verification.State != 0 && a.Baseline.Verification.State != NotVerified || a.Baseline.Verification.EvidenceRef != "" || a.Baseline.Verification.VerifiedAt != "" || a.Baseline.Verification.BaselineDigest != "") {
		return nil, Invalid("VERIFICATION_READ_ONLY")
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
	return mutate(ctx, u, m, "AdoptSupplyGroup", a, func(tx Transaction) (*SupplyGroup, error) {
		p, err := tx.Pool(ctx, a.PoolID)
		if err != nil {
			return nil, err
		}
		if err = AuthorizeCluster(ctx, p.ClusterID); err != nil {
			return nil, err
		}
		c, err := tx.Cluster(ctx, p.ClusterID)
		if err != nil {
			return nil, err
		}
		b := *a.Baseline
		b.Verification = &Verification{State: NotVerified}
		g := &SupplyGroup{GroupID: NewID(), PoolID: p.PoolID, ClusterID: p.ClusterID, DisplayName: a.DisplayName, Mode: a.Mode, Nodes: a.Nodes, ModelKey: a.ModelKey, Baseline: &b, Admission: Closed, QueueName: a.QueueName, Version: 1, AdoptedAt: u.now().UTC().Format(time.RFC3339Nano)}
		if err = u.inspect(ctx, *c, *g, false); err != nil {
			return nil, err
		}
		return g, tx.InsertGroup(ctx, *g)
	})
}
func (u *Usecase) inspect(ctx context.Context, c Cluster, g SupplyGroup, labels bool) error {
	i, err := u.provider.Inspect(ctx, c, g)
	if err != nil {
		return err
	}
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

type PublishInput struct {
	DisplayName          string       `json:"display_name"`
	Spec                 *ProfileSpec `json:"spec"`
	ExpectedGroupVersion uint64       `json:"expected_group_version"`
	VerificationRef      string       `json:"verification_ref"`
}

func (u *Usecase) PublishProfile(ctx context.Context, m *AdminMutation, a PublishInput) (*Profile, error) {
	if a.Spec == nil || !UUID(a.Spec.GroupID) || !Text(a.DisplayName, 128) || a.ExpectedGroupVersion == 0 || !Ref(a.VerificationRef) {
		return nil, Invalid("INVALID_PROFILE")
	}
	return mutate(ctx, u, m, "PublishProfile", a, func(tx Transaction) (*Profile, error) {
		if err := tx.Lock(ctx, "group:"+a.Spec.GroupID); err != nil {
			return nil, err
		}
		g, err := tx.Group(ctx, a.Spec.GroupID)
		if err != nil {
			return nil, err
		}
		if err = AuthorizeCluster(ctx, g.ClusterID); err != nil {
			return nil, err
		}
		if g.Version != a.ExpectedGroupVersion {
			return nil, Conflict("VERSION_CONFLICT")
		}
		if a.Spec.Mode != g.Mode || a.Spec.ModelKey != g.ModelKey {
			return nil, Precondition("PROFILE_SCOPE_MISMATCH")
		}
		if err = ValidateSpec(a.Spec, g.Baseline); err != nil {
			return nil, err
		}
		c, err := tx.Cluster(ctx, g.ClusterID)
		if err != nil {
			return nil, err
		}
		if err = u.inspect(ctx, *c, *g, true); err != nil {
			return nil, err
		}
		v, err := u.evidence.Verify(ctx, *g, a.Spec, a.VerificationRef)
		if err != nil {
			return nil, err
		}
		if err = checkVerification(v, g.Baseline.BaselineDigest, a.VerificationRef); err != nil {
			return nil, err
		}
		g.Baseline.Verification = v
		if err = u.inspect(ctx, *c, *g, true); err != nil {
			return nil, err
		}
		if err = tx.Lock(ctx, "shared-vector:"+g.ClusterID); err != nil {
			return nil, err
		}
		if g.Mode == Shared {
			prior, err := tx.SharedVector(ctx, g.ClusterID)
			if err != nil {
				return nil, err
			}
			if prior != nil && (prior.SharedMemoryMiB != a.Spec.SharedMemoryMiB || prior.CoreLimitPercent != a.Spec.CoreLimitPercent || prior.IsolationClass != a.Spec.IsolationClass) {
				return nil, Conflict("SHARED_VECTOR_CONFLICT")
			}
		}
		d, err := SpecDigest(a.Spec, g.Baseline.BaselineID)
		if err != nil {
			return nil, err
		}
		p := &Profile{NewID(), 1, a.DisplayName, a.Spec, g.Baseline.BaselineID, d, true}
		if err = tx.InsertProfile(ctx, *p, g.ClusterID); err != nil {
			return nil, err
		}
		prior := g.Version
		g.Version++
		g.Baseline.Verification = v
		if err = tx.UpdateGroup(ctx, *g, prior); err != nil {
			return nil, err
		}
		return p, nil
	})
}
func checkVerification(v *Verification, digest, ref string) error {
	if v == nil || v.State != Verified || v.BaselineDigest != digest || v.EvidenceRef != ref || !ValidTime(v.VerifiedAt) {
		return Precondition("CONSUMPTION_NOT_VERIFIED")
	}
	return nil
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
	return mutate(ctx, u, m, "SetAdmission", a, func(tx Transaction) (*SupplyGroup, error) {
		if err := tx.Lock(ctx, "group:"+a.GroupID); err != nil {
			return nil, err
		}
		g, err := tx.Group(ctx, a.GroupID)
		if err != nil {
			return nil, err
		}
		if err = AuthorizeCluster(ctx, g.ClusterID); err != nil {
			return nil, err
		}
		if g.Version != a.ExpectedVersion {
			return nil, Conflict("VERSION_CONFLICT")
		}
		if a.Desired == Open {
			ps, err := tx.Profiles(ctx, g.ClusterID, g.Mode, "", 10000)
			if err != nil {
				return nil, err
			}
			found := false
			for _, p := range ps {
				if p.Spec.GroupID == g.GroupID {
					found = true
					if err = u.checkActiveConfiguration(ctx, tx, *g, &p); err != nil {
						return nil, err
					}
				}
			}
			if !found {
				return nil, Precondition("PROFILE_NOT_ACTIVE")
			}
		}
		prior := g.Version
		g.Version++
		g.Admission = a.Desired
		return g, tx.UpdateGroup(ctx, *g, prior)
	})
}
func (u *Usecase) checkActiveConfiguration(ctx context.Context, r Reader, g SupplyGroup, p *Profile) error {
	if !p.Published || g.Baseline.Verification == nil {
		return Precondition("PROFILE_NOT_ACTIVE")
	}
	c, err := r.Cluster(ctx, g.ClusterID)
	if err != nil {
		return err
	}
	if err = u.inspect(ctx, *c, g, true); err != nil {
		return err
	}
	v, err := u.evidence.Verify(ctx, g, p.Spec, g.Baseline.Verification.EvidenceRef)
	if err != nil {
		return err
	}
	return checkVerification(v, g.Baseline.BaselineDigest, g.Baseline.Verification.EvidenceRef)
}
func (u *Usecase) Resolve(ctx context.Context, a *TenantContext, gpu *GpuRequest) (*ResolvedPlan, error) {
	if err := AuthorizeTenant(ctx, a, "ResolveGpuRequest"); err != nil {
		return nil, err
	}
	return u.resolve(ctx, gpu)
}
func (u *Usecase) resolve(ctx context.Context, gpu *GpuRequest) (*ResolvedPlan, error) {
	if err := ValidateRequest(gpu, u.maxReplicas); err != nil {
		return nil, err
	}
	if err := AuthorizeCluster(ctx, gpu.ClusterID); err != nil {
		return nil, err
	}
	var plan *ResolvedPlan
	err := u.repo.View(ctx, func(r Reader) error {
		p, err := r.Profile(ctx, gpu.ProfileID, gpu.ProfileVersion)
		if err != nil {
			return err
		}
		g, err := r.Group(ctx, p.Spec.GroupID)
		if err != nil {
			return err
		}
		if g.Admission != Open {
			return Precondition("ADMISSION_CLOSED")
		}
		if AuthorizeCluster(ctx, g.ClusterID) != nil {
			return NotFound()
		}
		if gpu.ClusterID != g.ClusterID || gpu.PoolID != g.PoolID {
			return Precondition("PROFILE_SCOPE_MISMATCH")
		}
		if err = u.checkActiveConfiguration(ctx, r, *g, p); err != nil {
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
		plan, err = BuildPlan(gpu, p, g, u.maxReplicas)
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
		if err = AuthorizeCluster(ctx, g.ClusterID); err != nil {
			return NotFound()
		}
		if !p.Published {
			return NotFound()
		}
		return nil
	})
	return p, err
}
func IsNotFound(err error) bool { var e *Error; return errors.As(err, &e) && e.Kind == "not_found" }
