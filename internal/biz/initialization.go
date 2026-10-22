package biz

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
)

type InitializationState string

const (
	InitializationAccepted  InitializationState = "ACCEPTED"
	InitializationRunning   InitializationState = "RUNNING"
	InitializationSucceeded InitializationState = "SUCCEEDED"
	InitializationBlocked   InitializationState = "BLOCKED"
	InitializationConflict  InitializationState = "CONFLICT"
	InitializationFailed    InitializationState = "FAILED"
)

type InitializationPoolInput struct {
	ClientKey      string                 `json:"client_key"`
	ExistingPoolID string                 `json:"existing_pool_id,omitempty"`
	NewPool        *InitializationNewPool `json:"new_pool,omitempty"`
}
type InitializationNewPool struct {
	DisplayName string `json:"display_name"`
}
type InitializationGroupInput struct {
	ClientKey     string                       `json:"client_key"`
	ExistingGroup *InitializationExistingGroup `json:"existing_group,omitempty"`
	NewGroup      *InitializationNewGroup      `json:"new_group,omitempty"`
}
type InitializationExistingGroup struct {
	GroupID         string `json:"group_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}
type InitializationNewGroup struct {
	PoolKey     string    `json:"pool_key"`
	DisplayName string    `json:"display_name"`
	Nodes       []NodeRef `json:"nodes"`
	BaselineRef string    `json:"baseline_ref"`
	QueueName   string    `json:"queue_name"`
}
type InitializationProfileInput struct {
	ClientKey            string `json:"client_key"`
	GroupKey             string `json:"group_key"`
	DisplayName          string `json:"display_name"`
	MemoryMiBPerDevice   int64  `json:"memory_mib_per_device"`
	CoreLimitPercent     uint32 `json:"core_limit_percent"`
	MaxDevicesPerReplica uint32 `json:"max_devices_per_replica"`
}
type InitializationInput struct {
	DiscoveryRevision string                       `json:"discovery_revision"`
	Pools             []InitializationPoolInput    `json:"pools"`
	Groups            []InitializationGroupInput   `json:"groups"`
	Profiles          []InitializationProfileInput `json:"profiles"`
}
type InitializationBaseline struct {
	Mode     SupplyMode        `json:"mode"`
	ModelKey string            `json:"model_key"`
	Baseline *ProviderBaseline `json:"baseline"`
}
type InitializationEnvironment struct {
	ConnectionRef      string
	ClusterDisplayName string
	DiscoveryRevision  string
	Baselines          map[string]InitializationBaseline
	Nodes              map[string]InitializationNode
}
type InitializationNode struct {
	Node          NodeRef
	ModelKey      string
	GroupID       string
	BaselineRefs  []string
	Selectable    bool
	BlockedReason string
}
type InitializationProvider interface {
	InitializationEnvironment(context.Context) (InitializationEnvironment, error)
	EnsureInitializationLabels(context.Context, Cluster, SupplyGroup, func(context.Context) error) error
}
type InitializationStep struct {
	StepID             string `json:"step_id"`
	ObjectKey          string `json:"object_key"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	Reason             string `json:"reason,omitempty"`
	ResourceID         string `json:"resource_id,omitempty"`
	InputGroupVersion  uint64 `json:"input_group_version,omitempty"`
	OutputGroupVersion uint64 `json:"output_group_version,omitempty"`
}
type InitializationGroupResult struct {
	GroupID              string `json:"group_id"`
	RecordedGroupVersion uint64 `json:"recorded_group_version"`
	Created              bool   `json:"created"`
	AutoOpenResult       string `json:"auto_open_result"`
}
type InitializationProfileResult struct {
	ProfileID      string       `json:"profile_id"`
	ProfileVersion uint64       `json:"profile_version"`
	Revision       uint64       `json:"revision"`
	State          ProfileState `json:"state"`
}
type InitializationPlan struct {
	Cluster       Cluster                `json:"cluster"`
	CreateCluster bool                   `json:"create_cluster"`
	Pools         map[string]Pool        `json:"pools"`
	NewPools      map[string]bool        `json:"new_pools"`
	Groups        map[string]SupplyGroup `json:"groups"`
	NewGroups     map[string]bool        `json:"new_groups"`
	BaselineRefs  map[string]string      `json:"baseline_refs"`
	Profiles      map[string]Profile     `json:"profiles"`
	ProfileGroups map[string]string      `json:"profile_groups"`
}
type InitializationOperation struct {
	OperationID     string                                 `json:"operation_id"`
	Version         uint64                                 `json:"version"`
	State           InitializationState                    `json:"state"`
	CreatedAt       time.Time                              `json:"created_at"`
	UpdatedAt       time.Time                              `json:"updated_at"`
	CurrentStep     string                                 `json:"current_step"`
	Retryable       bool                                   `json:"retryable"`
	Reason          string                                 `json:"reason,omitempty"`
	RequiredActions []string                               `json:"required_actions"`
	Actor           Actor                                  `json:"actor"`
	RetryActor      *Actor                                 `json:"retry_actor,omitempty"`
	ConnectionRef   string                                 `json:"connection_ref"`
	ClusterID       string                                 `json:"cluster_id"`
	PoolIDs         map[string]string                      `json:"pool_ids"`
	Groups          map[string]InitializationGroupResult   `json:"groups"`
	Profiles        map[string]InitializationProfileResult `json:"profiles"`
	Steps           []InitializationStep                   `json:"steps"`
	Input           InitializationInput                    `json:"input"`
	Plan            InitializationPlan                     `json:"plan"`
	LeaseOwner      string                                 `json:"-"`
	LeaseEpoch      uint64                                 `json:"-"`
	LeaseExpiresAt  time.Time                              `json:"-"`
}
type InitializationTransaction interface {
	Transaction
	InitializationOperation(context.Context, string) (*InitializationOperation, error)
	InsertInitializationOperation(context.Context, InitializationOperation) error
	SaveInitializationOperation(context.Context, InitializationOperation, uint64, uint64) error
}
type InitializationRepository interface {
	InitializationTransact(context.Context, func(InitializationTransaction) error) error
	InitializationOperation(context.Context, string) (*InitializationOperation, error)
	InitializationWork(context.Context, []string, int) ([]string, error)
}
type InitializationUsecase struct {
	catalog  *Usecase
	repo     InitializationRepository
	provider InitializationProvider
}

func NewInitializationUsecase(catalog *Usecase, repo InitializationRepository, provider InitializationProvider) (*InitializationUsecase, error) {
	if catalog == nil || repo == nil || provider == nil {
		return nil, Invalid("INITIALIZATION_CONFIG_INVALID")
	}
	return &InitializationUsecase{catalog: catalog, repo: repo, provider: provider}, nil
}

var initializationClientKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func NormalizeInitializationInput(input InitializationInput) (InitializationInput, error) {
	if !Text(input.DiscoveryRevision, 256) || len(input.Pools) > 32 || len(input.Groups) > 32 || len(input.Profiles) > 128 || len(input.Pools)+len(input.Groups)+len(input.Profiles) == 0 {
		return input, Invalid("INVALID_INITIALIZATION")
	}
	selectedNodes := 0
	for _, group := range input.Groups {
		if group.NewGroup != nil {
			selectedNodes += len(group.NewGroup.Nodes)
			if selectedNodes > 1024 {
				return input, Invalid("INITIALIZATION_NODE_LIMIT")
			}
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return input, Invalid("INVALID_INITIALIZATION")
	}
	var normalized InitializationInput
	if json.Unmarshal(raw, &normalized) != nil {
		return input, Invalid("INVALID_INITIALIZATION")
	}
	normalized.Pools = append([]InitializationPoolInput{}, normalized.Pools...)
	normalized.Groups = append([]InitializationGroupInput{}, normalized.Groups...)
	normalized.Profiles = append([]InitializationProfileInput{}, normalized.Profiles...)
	poolKeys, groupKeys, profileKeys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	poolIDs, groupIDs, nodes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pool := range normalized.Pools {
		if !initializationClientKey.MatchString(pool.ClientKey) || poolKeys[pool.ClientKey] || (pool.ExistingPoolID == "") == (pool.NewPool == nil) {
			return input, Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		poolKeys[pool.ClientKey] = true
		if pool.NewPool != nil {
			if !Text(pool.NewPool.DisplayName, 64) {
				return input, Invalid("INVALID_POOL")
			}
		} else {
			if !UUID(pool.ExistingPoolID) || poolIDs[pool.ExistingPoolID] {
				return input, Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			poolIDs[pool.ExistingPoolID] = true
		}
	}
	for index := range normalized.Groups {
		group := &normalized.Groups[index]
		if !initializationClientKey.MatchString(group.ClientKey) || groupKeys[group.ClientKey] || (group.ExistingGroup == nil) == (group.NewGroup == nil) {
			return input, Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		groupKeys[group.ClientKey] = true
		if group.ExistingGroup != nil {
			if !UUID(group.ExistingGroup.GroupID) || group.ExistingGroup.ExpectedVersion == 0 || groupIDs[group.ExistingGroup.GroupID] {
				return input, Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			groupIDs[group.ExistingGroup.GroupID] = true
		} else {
			fresh := group.NewGroup
			if !poolKeys[fresh.PoolKey] || !Text(fresh.DisplayName, 64) || !Text(fresh.BaselineRef, 256) || !Name(fresh.QueueName) || len(fresh.Nodes) == 0 {
				return input, Invalid("INVALID_INITIALIZATION_GRAPH")
			}
			names := map[string]bool{}
			for _, node := range fresh.Nodes {
				if !Ref(node.UID) || !Text(node.Name, 253) || nodes[node.UID] || names[node.Name] {
					return input, Invalid("INVALID_INITIALIZATION_GRAPH")
				}
				nodes[node.UID] = true
				names[node.Name] = true
			}
			sort.Slice(fresh.Nodes, func(left, right int) bool { return fresh.Nodes[left].UID < fresh.Nodes[right].UID })
		}
	}
	if len(nodes) > 1024 {
		return input, Invalid("INITIALIZATION_NODE_LIMIT")
	}
	for _, profile := range normalized.Profiles {
		if !initializationClientKey.MatchString(profile.ClientKey) || profileKeys[profile.ClientKey] || !groupKeys[profile.GroupKey] || !Text(profile.DisplayName, 128) || profile.MemoryMiBPerDevice <= 0 || profile.CoreLimitPercent == 0 || profile.CoreLimitPercent > 100 || profile.MaxDevicesPerReplica == 0 || profile.MaxDevicesPerReplica > 16 {
			return input, Invalid("INVALID_INITIALIZATION_GRAPH")
		}
		profileKeys[profile.ClientKey] = true
	}
	sort.Slice(normalized.Pools, func(left, right int) bool {
		return normalized.Pools[left].ClientKey < normalized.Pools[right].ClientKey
	})
	sort.Slice(normalized.Groups, func(left, right int) bool {
		return normalized.Groups[left].ClientKey < normalized.Groups[right].ClientKey
	})
	sort.Slice(normalized.Profiles, func(left, right int) bool {
		return normalized.Profiles[left].ClientKey < normalized.Profiles[right].ClientKey
	})
	return normalized, nil
}

func (u *InitializationUsecase) replay(ctx context.Context, tx InitializationTransaction, mutation *AdminMutation, method, digest string) (*InitializationOperation, error) {
	actorDigest, err := Hash(mutation.Context.Actor)
	if err != nil {
		return nil, err
	}
	if err = tx.Lock(ctx, "mutation:"+actorDigest+":"+method+":"+mutation.IdempotencyKey); err != nil {
		return nil, err
	}
	prior, err := tx.Replay(ctx, actorDigest, method, mutation.IdempotencyKey)
	if err != nil || prior == nil {
		return nil, err
	}
	if prior.Digest != digest {
		return nil, Conflict("IDEMPOTENCY_CONFLICT")
	}
	var operation InitializationOperation
	if json.Unmarshal(prior.Result, &operation) != nil {
		return nil, Precondition("STORED_RESULT_INVALID")
	}
	if !u.catalog.connections[operation.ConnectionRef] {
		return nil, NotFound()
	}
	return &operation, nil
}
func (u *InitializationUsecase) remember(ctx context.Context, tx InitializationTransaction, mutation *AdminMutation, method, digest string, operation *InitializationOperation) error {
	actorDigest, err := Hash(mutation.Context.Actor)
	if err != nil {
		return err
	}
	result, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	return tx.Remember(ctx, IdempotencyRecord{Actor: actorDigest, Method: method, Key: mutation.IdempotencyKey, Digest: digest, Result: result})
}
func (u *InitializationUsecase) Create(ctx context.Context, mutation *AdminMutation, input InitializationInput) (*InitializationOperation, error) {
	if err := AuthorizeMutation(ctx, mutation, "CreateInitialization"); err != nil {
		return nil, err
	}
	input, err := NormalizeInitializationInput(input)
	if err != nil {
		return nil, err
	}
	digest, err := Hash(input)
	if err != nil {
		return nil, err
	}
	var result *InitializationOperation
	err = u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
		var err error
		result, err = u.replay(ctx, tx, mutation, "CreateInitialization", digest)
		return err
	})
	if err != nil || result != nil {
		return result, err
	}
	environment, err := u.provider.InitializationEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	if !u.catalog.connections[environment.ConnectionRef] || !Text(environment.ClusterDisplayName, 64) {
		return nil, Precondition("CONNECTION_UNAVAILABLE")
	}
	if environment.DiscoveryRevision != input.DiscoveryRevision {
		return nil, Conflict("DISCOVERY_REVISION_CONFLICT")
	}
	err = u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
		var err error
		result, err = u.replay(ctx, tx, mutation, "CreateInitialization", digest)
		if err != nil || result != nil {
			return err
		}
		if err = tx.Lock(ctx, "initialization:connection:"+environment.ConnectionRef); err != nil {
			return err
		}
		result, err = u.freeze(ctx, tx, *mutation.Context.Actor, input, environment)
		if err != nil {
			return err
		}
		if err = tx.InsertInitializationOperation(ctx, *result); err != nil {
			return err
		}
		return u.remember(ctx, tx, mutation, "CreateInitialization", digest, result)
	})
	return result, err
}

func (u *InitializationUsecase) freeze(ctx context.Context, tx InitializationTransaction, actor Actor, input InitializationInput, environment InitializationEnvironment) (*InitializationOperation, error) {
	now := u.catalog.now().UTC()
	operation := &InitializationOperation{OperationID: NewID(), Version: 1, State: InitializationAccepted, CreatedAt: now, UpdatedAt: now, CurrentStep: "VALIDATE", Actor: actor, ConnectionRef: environment.ConnectionRef, Input: input, PoolIDs: map[string]string{}, Groups: map[string]InitializationGroupResult{}, Profiles: map[string]InitializationProfileResult{}}
	operation.Plan = InitializationPlan{Pools: map[string]Pool{}, NewPools: map[string]bool{}, Groups: map[string]SupplyGroup{}, NewGroups: map[string]bool{}, BaselineRefs: map[string]string{}, Profiles: map[string]Profile{}, ProfileGroups: map[string]string{}}
	plan := &operation.Plan
	clusters, err := tx.Clusters(ctx, []string{environment.ConnectionRef}, "", 2)
	if err != nil {
		return nil, err
	}
	if len(clusters) > 1 {
		return nil, Precondition("INITIALIZATION_SCOPE_INVALID")
	}
	if len(clusters) == 0 {
		plan.Cluster = Cluster{ClusterID: NewID(), DisplayName: environment.ClusterDisplayName, ConnectionRef: environment.ConnectionRef, Version: 1}
		plan.CreateCluster = true
	} else {
		plan.Cluster = clusters[0]
	}
	operation.ClusterID = plan.Cluster.ClusterID
	actions := map[string]bool{}
	if plan.CreateCluster {
		actions["accelerator:cluster:register"] = true
	}
	for _, inputPool := range input.Pools {
		var pool Pool
		if inputPool.NewPool != nil {
			pool = Pool{PoolID: NewID(), ClusterID: operation.ClusterID, DisplayName: inputPool.NewPool.DisplayName}
			plan.NewPools[inputPool.ClientKey] = true
			actions["accelerator:pool:create"] = true
		} else {
			existing, err := tx.Pool(ctx, inputPool.ExistingPoolID)
			if err != nil {
				return nil, err
			}
			if existing.ClusterID != operation.ClusterID {
				return nil, NotFound()
			}
			pool = *existing
		}
		plan.Pools[inputPool.ClientKey] = pool
		operation.PoolIDs[inputPool.ClientKey] = pool.PoolID
	}
	for _, inputGroup := range input.Groups {
		var group SupplyGroup
		if inputGroup.NewGroup != nil {
			fresh := inputGroup.NewGroup
			baseline, ok := environment.Baselines[fresh.BaselineRef]
			if !ok {
				return nil, Precondition("BASELINE_UNAVAILABLE")
			}
			if err := ValidateBaseline(baseline.Baseline, baseline.Mode); err != nil {
				return nil, err
			}
			modelKey := ""
			for _, selected := range fresh.Nodes {
				discovered, ok := environment.Nodes[selected.UID]
				if !ok || discovered.Node != selected {
					return nil, Conflict("NODE_IDENTITY_CONFLICT")
				}
				if discovered.GroupID != "" {
					return nil, Conflict("NODE_GROUP_CONFLICT")
				}
				if !discovered.Selectable {
					return nil, Precondition("NODE_NOT_SELECTABLE")
				}
				available := false
				for _, reference := range discovered.BaselineRefs {
					if reference == fresh.BaselineRef {
						available = true
					}
				}
				if !available {
					return nil, Precondition("BASELINE_UNAVAILABLE")
				}
				if !Label(discovered.ModelKey) || (modelKey != "" && modelKey != discovered.ModelKey) {
					return nil, Precondition("NODE_MODEL_MISMATCH")
				}
				modelKey = discovered.ModelKey
			}
			if baseline.ModelKey != "" && baseline.ModelKey != modelKey {
				return nil, Precondition("NODE_MODEL_MISMATCH")
			}
			baselineCopy := *baseline.Baseline
			group = SupplyGroup{GroupID: NewID(), ClusterID: operation.ClusterID, PoolID: plan.Pools[fresh.PoolKey].PoolID, DisplayName: fresh.DisplayName, Mode: baseline.Mode, ModelKey: modelKey, Baseline: &baselineCopy, QueueName: fresh.QueueName, Nodes: fresh.Nodes, Admission: Closed, Version: 1, AdoptedAt: now.Format(time.RFC3339Nano)}
			plan.NewGroups[inputGroup.ClientKey] = true
			plan.BaselineRefs[inputGroup.ClientKey] = fresh.BaselineRef
		} else {
			existing, err := tx.Group(ctx, inputGroup.ExistingGroup.GroupID)
			if err != nil {
				return nil, err
			}
			if existing.ClusterID != operation.ClusterID {
				return nil, NotFound()
			}
			if existing.Version != inputGroup.ExistingGroup.ExpectedVersion {
				return nil, Conflict("VERSION_CONFLICT")
			}
			group = *existing
		}
		plan.Groups[inputGroup.ClientKey] = group
		recordedVersion := group.Version
		if plan.NewGroups[inputGroup.ClientKey] {
			recordedVersion = 0
		}
		operation.Groups[inputGroup.ClientKey] = InitializationGroupResult{GroupID: group.GroupID, RecordedGroupVersion: recordedVersion, AutoOpenResult: "PENDING"}
		actions["accelerator:supply-group:adopt"] = true
	}
	for _, inputProfile := range input.Profiles {
		group := plan.Groups[inputProfile.GroupKey]
		isolation := "SOFTWARE_COOPERATIVE"
		if group.Mode == Whole {
			isolation = "WHOLE_DEVICE_EXCLUSIVE"
		}
		spec := &ProfileSpec{GroupID: group.GroupID, Mode: group.Mode, ModelKey: group.ModelKey, MemoryMiBPerDevice: inputProfile.MemoryMiBPerDevice, CoreLimitPercent: inputProfile.CoreLimitPercent, MaxDevicesPerReplica: inputProfile.MaxDevicesPerReplica, IsolationClass: isolation}
		if err := ValidateSpec(spec, group.Baseline); err != nil {
			return nil, err
		}
		digest, err := SpecDigest(spec, group.Baseline.BaselineID)
		if err != nil {
			return nil, err
		}
		plan.Profiles[inputProfile.ClientKey] = Profile{ProfileID: NewID(), ProfileVersion: 1, Revision: 1, State: ProfileActive, DisplayName: inputProfile.DisplayName, Spec: spec, BaselineID: group.Baseline.BaselineID, SpecDigest: digest}
		operation.Profiles[inputProfile.ClientKey] = InitializationProfileResult{ProfileID: plan.Profiles[inputProfile.ClientKey].ProfileID, ProfileVersion: 1}
		plan.ProfileGroups[inputProfile.ClientKey] = inputProfile.GroupKey
		actions["accelerator:profile:publish"] = true
		if plan.NewGroups[inputProfile.GroupKey] {
			actions["accelerator:supply-group:set-admission"] = true
		}
	}
	for action := range actions {
		operation.RequiredActions = append(operation.RequiredActions, action)
	}
	sort.Strings(operation.RequiredActions)
	add := func(kind, key string) {
		operation.Steps = append(operation.Steps, InitializationStep{StepID: NewID(), Kind: kind, ObjectKey: key, State: "PENDING"})
	}
	add("VALIDATE", "")
	add("ENSURE_CLUSTER", "")
	for _, pool := range input.Pools {
		add("ENSURE_POOL", pool.ClientKey)
	}
	for _, group := range input.Groups {
		add("ADOPT_GROUP", group.ClientKey)
	}
	for _, group := range input.Groups {
		add("ENSURE_LABELS", group.ClientKey)
	}
	for _, profile := range input.Profiles {
		add("CREATE_PROFILE", profile.ClientKey)
	}
	for _, group := range input.Groups {
		add("VERIFY_OPEN", group.ClientKey)
	}
	for _, group := range input.Groups {
		add("OPEN_GROUP", group.ClientKey)
	}
	add("COMPLETE", "")
	return operation, nil
}

func (u *InitializationUsecase) Get(ctx context.Context, read *AdminRead, id string) (*InitializationOperation, error) {
	if err := AuthorizeAdmin(ctx, read, "GetInitializationOperation"); err != nil {
		return nil, err
	}
	if !UUID(id) {
		return nil, Invalid("INVALID_OPERATION_ID")
	}
	operation, err := u.repo.InitializationOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.catalog.connections[operation.ConnectionRef] {
		return nil, NotFound()
	}
	return operation, nil
}
func (u *InitializationUsecase) Retry(ctx context.Context, mutation *AdminMutation, id string, expectedVersion uint64) (*InitializationOperation, error) {
	if err := AuthorizeMutation(ctx, mutation, "RetryInitializationOperation"); err != nil {
		return nil, err
	}
	if !UUID(id) || expectedVersion == 0 {
		return nil, Invalid("INVALID_OPERATION_VERSION")
	}
	digest, err := Hash([]any{id, expectedVersion})
	if err != nil {
		return nil, err
	}
	var operation *InitializationOperation
	err = u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
		var err error
		operation, err = u.replay(ctx, tx, mutation, "RetryInitializationOperation", digest)
		if err != nil || operation != nil {
			return err
		}
		operation, err = tx.InitializationOperation(ctx, id)
		if err != nil {
			return err
		}
		if !u.catalog.connections[operation.ConnectionRef] {
			return NotFound()
		}
		if operation.Version != expectedVersion {
			return Conflict("VERSION_CONFLICT")
		}
		if operation.State != InitializationBlocked || !operation.Retryable {
			return Precondition("INITIALIZATION_NOT_RETRYABLE")
		}
		version, epoch := operation.Version, operation.LeaseEpoch
		operation.Version++
		operation.State = InitializationAccepted
		operation.Reason = ""
		operation.Retryable = false
		operation.RetryActor = mutation.Context.Actor
		operation.LeaseOwner = ""
		operation.LeaseExpiresAt = time.Time{}
		operation.UpdatedAt = u.catalog.now().UTC()
		for index := range operation.Steps {
			if operation.Steps[index].State == "BLOCKED" {
				operation.Steps[index].State = "PENDING"
				operation.Steps[index].Reason = ""
			}
		}
		if err = tx.SaveInitializationOperation(ctx, *operation, version, epoch); err != nil {
			return err
		}
		return u.remember(ctx, tx, mutation, "RetryInitializationOperation", digest, operation)
	})
	return operation, err
}

func initializationErrorState(err error) (InitializationState, string, bool) {
	var domainError *Error
	if !errors.As(err, &domainError) {
		return InitializationBlocked, "DEPENDENCY_UNAVAILABLE", true
	}
	if domainError.Kind == "conflict" {
		return InitializationConflict, domainError.Reason, false
	}
	if domainError.Kind == "precondition" || domainError.Kind == "unavailable" {
		return InitializationBlocked, domainError.Reason, true
	}
	return InitializationFailed, domainError.Reason, false
}

func initializationPending(operation *InitializationOperation) int {
	for index, step := range operation.Steps {
		if step.State != "SUCCEEDED" && step.State != "SKIPPED" {
			return index
		}
	}
	return -1
}

func (u *InitializationUsecase) Work(ctx context.Context, workerID string, limit int) error {
	if !Ref(workerID) || limit < 1 || limit > 64 {
		return Invalid("INVALID_INITIALIZATION_WORKER")
	}
	ids, err := u.repo.InitializationWork(ctx, u.catalog.connectionRefs(), limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = u.Advance(ctx, id, workerID); err != nil {
			var domainError *Error
			if errors.As(err, &domainError) && domainError.Reason == "INITIALIZATION_LEASE_HELD" {
				continue
			}
			return err
		}
	}
	return nil
}

func (u *InitializationUsecase) Advance(ctx context.Context, id, workerID string) (*InitializationOperation, error) {
	if !UUID(id) || !Ref(workerID) {
		return nil, Invalid("INVALID_INITIALIZATION_WORKER")
	}
	var operation *InitializationOperation
	err := u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
		var err error
		operation, err = tx.InitializationOperation(ctx, id)
		if err != nil {
			return err
		}
		if !u.catalog.connections[operation.ConnectionRef] {
			return NotFound()
		}
		if operation.State != InitializationAccepted && operation.State != InitializationRunning {
			return nil
		}
		now := u.catalog.now().UTC()
		if operation.LeaseOwner != "" && operation.LeaseExpiresAt.After(now) {
			return Conflict("INITIALIZATION_LEASE_HELD")
		}
		version, epoch := operation.Version, operation.LeaseEpoch
		operation.LeaseEpoch++
		operation.LeaseOwner = workerID
		operation.LeaseExpiresAt = now.Add(30 * time.Second)
		if operation.State != InitializationRunning {
			operation.State = InitializationRunning
			operation.Version++
			operation.UpdatedAt = now
		}
		return tx.SaveInitializationOperation(ctx, *operation, version, epoch)
	})
	if err != nil {
		return nil, err
	}
	if operation.State != InitializationRunning {
		return operation, nil
	}
	epoch := operation.LeaseEpoch
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workContext.Done():
				return
			case <-ticker.C:
				if u.guard(workContext, id, workerID, epoch, "") != nil {
					cancel()
					return
				}
			}
		}
	}()
	ctx = workContext
	for {
		index := initializationPending(operation)
		if index < 0 {
			return operation, Precondition("INITIALIZATION_STEPS_INVALID")
		}
		step := operation.Steps[index]
		prepared, skip, stepError := u.prepareStep(ctx, operation, step, workerID, epoch)
		if stepError == nil {
			stepError = u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
				current, err := u.leasedOperation(ctx, tx, id, workerID, epoch)
				if err != nil {
					return err
				}
				currentIndex := initializationPending(current)
				if currentIndex < 0 || current.Steps[currentIndex].StepID != step.StepID {
					return Conflict("INITIALIZATION_STEP_CONFLICT")
				}
				version := current.Version
				if skip && (step.Kind == "ADOPT_GROUP" || step.Kind == "VERIFY_OPEN") {
					result := current.Groups[step.ObjectKey]
					current.Steps[currentIndex].ResourceID = result.GroupID
					current.Steps[currentIndex].InputGroupVersion = result.RecordedGroupVersion
					current.Steps[currentIndex].OutputGroupVersion = result.RecordedGroupVersion
				}
				if !skip {
					if err = u.commitStep(ctx, tx, current, &current.Steps[currentIndex], prepared); err != nil {
						return err
					}
				} else if step.Kind == "OPEN_GROUP" {
					current.Steps[currentIndex].ResourceID = current.Groups[step.ObjectKey].GroupID
					current.Steps[currentIndex].InputGroupVersion = current.Groups[step.ObjectKey].RecordedGroupVersion
					current.Steps[currentIndex].OutputGroupVersion = current.Groups[step.ObjectKey].RecordedGroupVersion
					result := current.Groups[step.ObjectKey]
					if current.Plan.NewGroups[step.ObjectKey] {
						result.AutoOpenResult = "SKIPPED_NO_PROFILE"
					} else {
						result.AutoOpenResult = "SKIPPED_EXISTING"
					}
					current.Groups[step.ObjectKey] = result
				}
				current.Steps[currentIndex].State = "SUCCEEDED"
				if skip {
					current.Steps[currentIndex].State = "SKIPPED"
				}
				current.Steps[currentIndex].Reason = ""
				current.Version++
				current.UpdatedAt = u.catalog.now().UTC()
				current.LeaseExpiresAt = current.UpdatedAt.Add(30 * time.Second)
				if step.Kind == "COMPLETE" {
					current.State = InitializationSucceeded
					current.CurrentStep = "COMPLETE"
					current.LeaseOwner = ""
					current.LeaseExpiresAt = time.Time{}
				} else if next := initializationPending(current); next >= 0 {
					current.CurrentStep = current.Steps[next].Kind
				}
				if err = tx.SaveInitializationOperation(ctx, *current, version, epoch); err != nil {
					return err
				}
				operation = current
				return nil
			})
		}
		if stepError != nil {
			var saveError error
			saveError = u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
				current, err := u.leasedOperation(ctx, tx, id, workerID, epoch)
				if err != nil {
					return err
				}
				version := current.Version
				current.State, current.Reason, current.Retryable = initializationErrorState(stepError)
				current.Version++
				current.UpdatedAt = u.catalog.now().UTC()
				current.LeaseOwner = ""
				current.LeaseExpiresAt = time.Time{}
				if pending := initializationPending(current); pending >= 0 {
					current.Steps[pending].State = string(current.State)
					current.Steps[pending].Reason = current.Reason
				}
				if err = tx.SaveInitializationOperation(ctx, *current, version, epoch); err != nil {
					return err
				}
				operation = current
				return nil
			})
			if saveError != nil {
				return operation, saveError
			}
			return operation, nil
		}
		if operation.State == InitializationSucceeded {
			return operation, nil
		}
	}
}

func (u *InitializationUsecase) leasedOperation(ctx context.Context, tx InitializationTransaction, id, worker string, epoch uint64) (*InitializationOperation, error) {
	operation, err := tx.InitializationOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.catalog.connections[operation.ConnectionRef] {
		return nil, NotFound()
	}
	if operation.State != InitializationRunning || operation.LeaseOwner != worker || operation.LeaseEpoch != epoch || !operation.LeaseExpiresAt.After(u.catalog.now()) {
		return nil, Conflict("INITIALIZATION_LEASE_LOST")
	}
	return operation, nil
}

func (u *InitializationUsecase) frozenGroup(ctx context.Context, tx InitializationTransaction, operation *InitializationOperation, key string) (*SupplyGroup, error) {
	result, ok := operation.Groups[key]
	if !ok || result.RecordedGroupVersion == 0 {
		return nil, Precondition("INITIALIZATION_GROUP_NOT_CREATED")
	}
	if err := tx.Lock(ctx, "group:"+result.GroupID); err != nil {
		return nil, err
	}
	group, err := tx.Group(ctx, result.GroupID)
	if err != nil {
		return nil, err
	}
	if group.ClusterID != operation.ClusterID || group.Version != result.RecordedGroupVersion {
		return nil, Conflict("VERSION_CONFLICT")
	}
	initial := operation.Plan.Groups[key]
	if group.PoolID != initial.PoolID || group.Baseline == nil || initial.Baseline == nil || group.Baseline.BaselineDigest != initial.Baseline.BaselineDigest {
		return nil, Conflict("INITIALIZATION_SCOPE_CONFLICT")
	}
	return group, nil
}

type initializationPreparedStep struct {
	group      *SupplyGroup
	inspection GroupInspection
	profiles   []Profile
}

func (u *InitializationUsecase) guard(ctx context.Context, id, worker string, epoch uint64, groupKey string) error {
	return u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
		operation, err := u.leasedOperation(ctx, tx, id, worker, epoch)
		if err != nil {
			return err
		}
		if groupKey != "" {
			if _, err = u.frozenGroup(ctx, tx, operation, groupKey); err != nil {
				return err
			}
		}
		version := operation.Version
		operation.LeaseExpiresAt = u.catalog.now().UTC().Add(30 * time.Second)
		return tx.SaveInitializationOperation(ctx, *operation, version, epoch)
	})
}

func (u *InitializationUsecase) inspectFrozenBaseline(ctx context.Context, operation *InitializationOperation, key string, group SupplyGroup, labels bool) (GroupInspection, error) {
	environment, err := u.provider.InitializationEnvironment(ctx)
	if err != nil {
		return GroupInspection{}, err
	}
	if environment.ConnectionRef != operation.ConnectionRef {
		return GroupInspection{}, Conflict("INITIALIZATION_SCOPE_CONFLICT")
	}
	if reference := operation.Plan.BaselineRefs[key]; reference != "" {
		baseline, ok := environment.Baselines[reference]
		if !ok || baseline.Baseline == nil || group.Baseline == nil || baseline.Mode != group.Mode || (baseline.ModelKey != "" && baseline.ModelKey != group.ModelKey) || baseline.Baseline.BaselineDigest != group.Baseline.BaselineDigest {
			return GroupInspection{}, Precondition("CONFIG_DRIFT")
		}
	}
	return u.catalog.inspect(ctx, operation.Plan.Cluster, group, labels)
}

func (u *InitializationUsecase) prepareStep(ctx context.Context, operation *InitializationOperation, step InitializationStep, worker string, epoch uint64) (initializationPreparedStep, bool, error) {
	var prepared initializationPreparedStep
	if err := u.guard(ctx, operation.OperationID, worker, epoch, ""); err != nil {
		return prepared, false, err
	}
	switch step.Kind {
	case "VALIDATE":
		environment, err := u.provider.InitializationEnvironment(ctx)
		if err != nil {
			return prepared, false, err
		}
		if environment.ConnectionRef != operation.ConnectionRef || environment.DiscoveryRevision != operation.Input.DiscoveryRevision {
			return prepared, false, Conflict("DISCOVERY_REVISION_CONFLICT")
		}
		for _, input := range operation.Input.Groups {
			group := operation.Plan.Groups[input.ClientKey]
			if _, err = u.inspectFrozenBaseline(ctx, operation, input.ClientKey, group, false); err != nil {
				return prepared, false, err
			}
		}
	case "ADOPT_GROUP":
		if !operation.Plan.NewGroups[step.ObjectKey] {
			return prepared, true, nil
		}
		group := operation.Plan.Groups[step.ObjectKey]
		inspection, err := u.inspectFrozenBaseline(ctx, operation, step.ObjectKey, group, false)
		prepared.group = &group
		prepared.inspection = inspection
		return prepared, false, err
	case "ENSURE_LABELS", "CREATE_PROFILE", "VERIFY_OPEN", "OPEN_GROUP":
		key := step.ObjectKey
		if step.Kind == "CREATE_PROFILE" {
			key = operation.Plan.ProfileGroups[step.ObjectKey]
		}
		if step.Kind == "VERIFY_OPEN" || step.Kind == "OPEN_GROUP" {
			if !operation.Plan.NewGroups[key] || !operationHasProfiles(operation, key) {
				return prepared, true, nil
			}
		}
		err := u.repo.InitializationTransact(ctx, func(tx InitializationTransaction) error {
			current, err := u.leasedOperation(ctx, tx, operation.OperationID, worker, epoch)
			if err != nil {
				return err
			}
			prepared.group, err = u.frozenGroup(ctx, tx, current, key)
			if err != nil {
				return err
			}
			if step.Kind == "VERIFY_OPEN" || step.Kind == "OPEN_GROUP" {
				prepared.profiles, err = u.openProfiles(ctx, tx, current, key, false)
			}
			return err
		})
		if err != nil {
			return prepared, false, err
		}
		if step.Kind == "ENSURE_LABELS" {
			guard := func(callContext context.Context) error {
				return u.guard(callContext, operation.OperationID, worker, epoch, key)
			}
			if err = u.provider.EnsureInitializationLabels(ctx, operation.Plan.Cluster, *prepared.group, guard); err != nil {
				return prepared, false, err
			}
		}
		prepared.inspection, err = u.inspectFrozenBaseline(ctx, operation, key, *prepared.group, true)
		if err != nil {
			return prepared, false, err
		}
		if step.Kind == "CREATE_PROFILE" {
			profile := operation.Plan.Profiles[step.ObjectKey]
			err = checkDeviceBudgets(profile.Spec, prepared.inspection)
		}
		for _, profile := range prepared.profiles {
			if err = checkDeviceBudgets(profile.Spec, prepared.inspection); err != nil {
				break
			}
		}
		return prepared, false, err
	}
	return prepared, false, nil
}

func operationHasProfiles(operation *InitializationOperation, key string) bool {
	for _, groupKey := range operation.Plan.ProfileGroups {
		if groupKey == key {
			return true
		}
	}
	return false
}

func (u *InitializationUsecase) openProfiles(ctx context.Context, tx InitializationTransaction, operation *InitializationOperation, key string, lock bool) ([]Profile, error) {
	ids := []string{}
	for profileKey, groupKey := range operation.Plan.ProfileGroups {
		if groupKey == key {
			ids = append(ids, profileKey)
		}
	}
	sort.Slice(ids, func(left, right int) bool {
		return operation.Plan.Profiles[ids[left]].ProfileID < operation.Plan.Profiles[ids[right]].ProfileID
	})
	for _, profileKey := range ids {
		recorded := operation.Profiles[profileKey]
		if recorded.Revision == 0 {
			return nil, Conflict("INITIALIZATION_PROFILE_CONFLICT")
		}
		if lock {
			if err := tx.Lock(ctx, "profile:"+recorded.ProfileID); err != nil {
				return nil, err
			}
		}
		profile, err := tx.Profile(ctx, recorded.ProfileID, recorded.ProfileVersion)
		if err != nil {
			return nil, err
		}
		if profile.Revision != recorded.Revision || profile.State != recorded.State || profile.State != ProfileActive {
			return nil, Conflict("INITIALIZATION_PROFILE_CONFLICT")
		}
	}
	group := operation.Plan.Groups[key]
	profiles, err := tx.Profiles(ctx, operation.ClusterID, group.Mode, "", 10000)
	if err != nil {
		return nil, err
	}
	active := []Profile{}
	for _, profile := range profiles {
		if profile.Spec != nil && profile.Spec.GroupID == group.GroupID && profile.State == ProfileActive {
			active = append(active, profile)
		}
	}
	if len(active) == 0 {
		return nil, Precondition("PROFILE_NOT_ACTIVE")
	}
	return active, nil
}

func (u *InitializationUsecase) commitStep(ctx context.Context, tx InitializationTransaction, operation *InitializationOperation, step *InitializationStep, prepared initializationPreparedStep) error {
	switch step.Kind {
	case "VALIDATE", "COMPLETE":
		return nil
	case "ENSURE_CLUSTER":
		step.ResourceID = operation.ClusterID
		if err := tx.Lock(ctx, "cluster:connection:"+operation.ConnectionRef); err != nil {
			return err
		}
		if !operation.Plan.CreateCluster {
			return checkCatalogSnapshot(ctx, tx, &operation.Plan.Cluster, nil, nil, nil)
		}
		existing, err := tx.Clusters(ctx, []string{operation.ConnectionRef}, "", 2)
		if err != nil {
			return err
		}
		if len(existing) != 0 {
			return Conflict("INITIALIZATION_CLUSTER_CONFLICT")
		}
		return tx.InsertCluster(ctx, operation.Plan.Cluster)
	case "ENSURE_POOL":
		pool := operation.Plan.Pools[step.ObjectKey]
		step.ResourceID = pool.PoolID
		if err := checkCatalogSnapshot(ctx, tx, &operation.Plan.Cluster, nil, nil, nil); err != nil {
			return err
		}
		if operation.Plan.NewPools[step.ObjectKey] {
			return tx.InsertPool(ctx, pool)
		}
		return checkCatalogSnapshot(ctx, tx, nil, &pool, nil, nil)
	case "ADOPT_GROUP":
		if prepared.group == nil {
			return Precondition("INITIALIZATION_PREPARATION_INVALID")
		}
		pool, err := tx.Pool(ctx, prepared.group.PoolID)
		if err != nil {
			return err
		}
		if pool.ClusterID != operation.ClusterID {
			return Conflict("INITIALIZATION_SCOPE_CONFLICT")
		}
		if err = u.catalog.checkInspection(*prepared.group, false, prepared.inspection); err != nil {
			return err
		}
		if err = tx.InsertGroup(ctx, *prepared.group); err != nil {
			return err
		}
		step.ResourceID = prepared.group.GroupID
		step.OutputGroupVersion = prepared.group.Version
		result := operation.Groups[step.ObjectKey]
		result.Created = true
		result.RecordedGroupVersion = prepared.group.Version
		operation.Groups[step.ObjectKey] = result
	case "ENSURE_LABELS", "CREATE_PROFILE", "VERIFY_OPEN", "OPEN_GROUP":
		key := step.ObjectKey
		if step.Kind == "CREATE_PROFILE" {
			key = operation.Plan.ProfileGroups[key]
		}
		group, err := u.frozenGroup(ctx, tx, operation, key)
		if err != nil {
			return err
		}
		step.ResourceID = group.GroupID
		step.InputGroupVersion = group.Version
		step.OutputGroupVersion = group.Version
		if prepared.group == nil {
			return Precondition("INITIALIZATION_PREPARATION_INVALID")
		}
		if err = checkCatalogSnapshot(ctx, tx, nil, nil, prepared.group, nil); err != nil {
			return err
		}
		if err = u.catalog.checkInspection(*group, true, prepared.inspection); err != nil {
			return err
		}
		if step.Kind == "CREATE_PROFILE" {
			profile := operation.Plan.Profiles[step.ObjectKey]
			step.ResourceID = profile.ProfileID
			if err = ValidateSpec(profile.Spec, group.Baseline); err != nil {
				return err
			}
			if err = checkDeviceBudgets(profile.Spec, prepared.inspection); err != nil {
				return err
			}
			if err = tx.InsertProfile(ctx, profile, operation.ClusterID); err != nil {
				return err
			}
			prior := group.Version
			group.Version++
			if err = tx.UpdateGroup(ctx, *group, prior); err != nil {
				return err
			}
			operation.Profiles[step.ObjectKey] = InitializationProfileResult{ProfileID: profile.ProfileID, ProfileVersion: profile.ProfileVersion, Revision: profile.Revision, State: profile.State}
			result := operation.Groups[key]
			result.RecordedGroupVersion = group.Version
			operation.Groups[key] = result
			step.OutputGroupVersion = group.Version
		} else if step.Kind == "VERIFY_OPEN" || step.Kind == "OPEN_GROUP" {
			if _, err = u.openProfiles(ctx, tx, operation, key, true); err != nil {
				return err
			}
			for _, profile := range prepared.profiles {
				if err = checkCatalogSnapshot(ctx, tx, nil, nil, nil, &profile); err != nil {
					return err
				}
				if err = checkDeviceBudgets(profile.Spec, prepared.inspection); err != nil {
					return err
				}
			}
			if step.Kind == "OPEN_GROUP" {
				prior := group.Version
				group.Version++
				group.Admission = Open
				if err = tx.UpdateGroup(ctx, *group, prior); err != nil {
					return err
				}
				result := operation.Groups[key]
				result.RecordedGroupVersion = group.Version
				result.AutoOpenResult = "OPENED"
				operation.Groups[key] = result
				step.OutputGroupVersion = group.Version
			}
		}
	default:
		return Precondition("INITIALIZATION_STEP_INVALID")
	}
	return nil
}
