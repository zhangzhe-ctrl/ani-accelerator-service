package data

import (
	"context"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type initializationTestProvider struct {
	testProvider
	environment       biz.InitializationEnvironment
	labels            func(context.Context, biz.SupplyGroup) error
	beforeEnvironment func(context.Context) error
}

func (p *initializationTestProvider) InitializationEnvironment(ctx context.Context) (biz.InitializationEnvironment, error) {
	if p.beforeEnvironment != nil {
		if err := p.beforeEnvironment(ctx); err != nil {
			return p.environment, err
		}
	}
	return p.environment, nil
}
func (p *initializationTestProvider) EnsureInitializationLabels(ctx context.Context, _ biz.Cluster, group biz.SupplyGroup, guard func(context.Context) error) error {
	if err := guard(ctx); err != nil {
		return err
	}
	if p.labels != nil {
		if err := p.labels(ctx, group); err != nil {
			return err
		}
	}
	return guard(ctx)
}
func initializationPGFixture(t *testing.T) (*pgFixture, *initializationTestProvider, *biz.InitializationUsecase, biz.InitializationInput) {
	t.Helper()
	fixture := pgSetup(t)
	provider := &initializationTestProvider{environment: biz.InitializationEnvironment{ConnectionRef: fixture.cluster.ConnectionRef, ClusterDisplayName: "local", DiscoveryRevision: "discovery-1", Baselines: map[string]biz.InitializationBaseline{"baseline-1": {Mode: fixture.group.Mode, ModelKey: fixture.group.ModelKey, Baseline: fixture.group.Baseline}}}}
	codec, err := biz.NewCursorCodec([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := biz.NewUsecase(fixture.db, provider, codec, []string{fixture.cluster.ConnectionRef}, 16, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fixture.u = catalog
	usecase, err := biz.NewInitializationUsecase(catalog, fixture.db, provider)
	if err != nil {
		t.Fatal(err)
	}
	input := biz.InitializationInput{DiscoveryRevision: "discovery-1", Pools: []biz.InitializationPoolInput{{ClientKey: "pool", NewPool: &biz.InitializationNewPool{DisplayName: "initialization pool"}}}, Groups: []biz.InitializationGroupInput{{ClientKey: "group", NewGroup: &biz.InitializationNewGroup{PoolKey: "pool", DisplayName: "initialized group", Nodes: []biz.NodeRef{{UID: biz.NewID(), Name: "node-" + biz.NewID()}}, BaselineRef: "baseline-1", QueueName: "default"}}}, Profiles: []biz.InitializationProfileInput{{ClientKey: "profile", GroupKey: "group", DisplayName: "8GiB", MemoryMiBPerDevice: 8192, CoreLimitPercent: 25, MaxDevicesPerReplica: 1}}}
	selected := input.Groups[0].NewGroup.Nodes[0]
	provider.environment.Nodes = map[string]biz.InitializationNode{selected.UID: {Node: selected, ModelKey: fixture.group.ModelKey, BaselineRefs: []string{"baseline-1"}, Selectable: true}}
	return fixture, provider, usecase, input
}

func TestPostgresInitializationCompletesVersionChainAndReplays(t *testing.T) {
	fixture, provider, usecase, input := initializationPGFixture(t)
	input.Profiles = append(input.Profiles, biz.InitializationProfileInput{ClientKey: "profile-second", GroupKey: "group", DisplayName: "4GiB", MemoryMiBPerDevice: 4096, CoreLimitPercent: 20, MaxDevicesPerReplica: 1})
	mutation := adminMutation()
	operation, err := usecase.Create(adminContext("CreateInitialization"), mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	provider.environment.DiscoveryRevision = "changed-after-acceptance"
	replay, err := usecase.Create(adminContext("CreateInitialization"), mutation, input)
	if err != nil || replay.OperationID != operation.OperationID {
		t.Fatal("same-key replay consulted changed discovery")
	}
	provider.environment.DiscoveryRevision = input.DiscoveryRevision
	result, err := usecase.Advance(context.Background(), operation.OperationID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != biz.InitializationSucceeded || result.Groups["group"].RecordedGroupVersion != 4 || result.Groups["group"].AutoOpenResult != "OPENED" {
		t.Fatalf("initialization did not complete own version chain: state=%s reason=%s group=%+v", result.State, result.Reason, result.Groups["group"])
	}
	if err = fixture.db.View(context.Background(), func(reader biz.Reader) error {
		group, err := reader.Group(context.Background(), result.Groups["group"].GroupID)
		if err != nil {
			return err
		}
		if group.Admission != biz.Open || group.Version != 4 {
			t.Fatal("persisted group was not opened at own version")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for index, step := range result.Steps {
		if step.StepID != operation.Steps[index].StepID {
			t.Fatal("step identity changed during processing")
		}
	}
	input.Profiles[0].DisplayName = "different"
	if _, err = usecase.Create(adminContext("CreateInitialization"), mutation, input); err == nil {
		t.Fatal("same idempotency key accepted different input")
	}
}

func TestPostgresInitializationRetainsPartialSuccessAndRetriesOriginalSteps(t *testing.T) {
	fixture, provider, usecase, input := initializationPGFixture(t)
	failed := false
	provider.labels = func(context.Context, biz.SupplyGroup) error {
		if !failed {
			failed = true
			return biz.Unavailable()
		}
		return nil
	}
	operation, err := usecase.Create(adminContext("CreateInitialization"), adminMutation(), input)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := usecase.Advance(context.Background(), operation.OperationID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.State != biz.InitializationBlocked || !blocked.Retryable || !blocked.Groups["group"].Created {
		t.Fatalf("partial success missing: %s %s", blocked.State, blocked.Reason)
	}
	restarted, err := biz.NewInitializationUsecase(fixture.u, fixture.db, provider)
	if err != nil {
		t.Fatal(err)
	}
	mutation := adminMutation()
	accepted, err := restarted.Retry(adminContext("RetryInitializationOperation"), mutation, blocked.OperationID, blocked.Version)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := restarted.Advance(context.Background(), accepted.OperationID, "worker-2")
	if err != nil || completed.State != biz.InitializationSucceeded {
		t.Fatalf("retry failed: %v %+v", err, completed)
	}
	replay, err := restarted.Retry(adminContext("RetryInitializationOperation"), mutation, blocked.OperationID, blocked.Version)
	if err != nil || replay.Version != accepted.Version {
		t.Fatal("retry receipt replay used advanced version as a new precondition")
	}
	if completed.Groups["group"].GroupID != blocked.Groups["group"].GroupID {
		t.Fatal("retry recreated group")
	}
	for index, step := range completed.Steps {
		if step.StepID != blocked.Steps[index].StepID {
			t.Fatal("retry replaced stable step identity")
		}
	}
}

func TestPostgresInitializationHonorsExternalGroupAndProfileChanges(t *testing.T) {
	for _, intervention := range []string{"closed_to_closed", "profile_disabled_reenabled"} {
		t.Run(intervention, func(t *testing.T) {
			fixture, provider, usecase, input := initializationPGFixture(t)
			operation, err := usecase.Create(adminContext("CreateInitialization"), adminMutation(), input)
			if err != nil {
				t.Fatal(err)
			}
			if intervention == "closed_to_closed" {
				provider.labels = func(_ context.Context, group biz.SupplyGroup) error {
					_, err := fixture.u.SetAdmission(adminContext("SetAdmission"), adminMutation(), biz.AdmissionInput{GroupID: group.GroupID, ExpectedVersion: group.Version, Desired: biz.Closed, Reason: "manual pause"})
					return err
				}
			} else {
				injected := false
				provider.beforeEnvironment = func(ctx context.Context) error {
					if injected {
						return nil
					}
					var profile *biz.Profile
					err := fixture.db.View(ctx, func(reader biz.Reader) error {
						var err error
						profile, err = reader.Profile(ctx, operation.Profiles["profile"].ProfileID, 1)
						return err
					})
					if biz.IsNotFound(err) {
						return nil
					}
					if err != nil {
						return err
					}
					injected = true
					disabled, err := fixture.u.SetProfileState(adminContext("SetProfileState"), adminMutation(), profile.ProfileID, profile.Revision, biz.ProfileDisabled)
					if err != nil {
						return err
					}
					_, err = fixture.u.SetProfileState(adminContext("SetProfileState"), adminMutation(), profile.ProfileID, disabled.Revision, biz.ProfileActive)
					return err
				}
			}
			result, err := usecase.Advance(context.Background(), operation.OperationID, "worker")
			if err != nil {
				t.Fatal(err)
			}
			if result.State != biz.InitializationConflict || result.Retryable {
				t.Fatalf("external intervention was ignored: %s %s", result.State, result.Reason)
			}
			err = fixture.db.View(context.Background(), func(reader biz.Reader) error {
				group, err := reader.Group(context.Background(), result.Groups["group"].GroupID)
				if err != nil {
					return err
				}
				if group.Admission != biz.Closed {
					t.Fatal("operation overrode external pause/profile intent")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresInitializationKeepsExistingAndUnprofiledGroupsClosed(t *testing.T) {
	for _, mode := range []string{"existing_closed", "new_without_profile", "first_cluster"} {
		t.Run(mode, func(t *testing.T) {
			fixture, provider, usecase, input := initializationPGFixture(t)
			if mode == "existing_closed" {
				closed, err := fixture.u.SetAdmission(adminContext("SetAdmission"), adminMutation(), biz.AdmissionInput{GroupID: fixture.group.GroupID, ExpectedVersion: fixture.group.Version, Desired: biz.Closed, Reason: "existing paused supply"})
				if err != nil {
					t.Fatal(err)
				}
				input.Pools = nil
				input.Groups = []biz.InitializationGroupInput{{ClientKey: "group", ExistingGroup: &biz.InitializationExistingGroup{GroupID: closed.GroupID, ExpectedVersion: closed.Version}}}
			} else if mode == "new_without_profile" {
				input.Profiles = nil
			} else {
				connection := "first:" + biz.NewID()
				provider.environment.ConnectionRef = connection
				codec, err := biz.NewCursorCodec([]byte("01234567890123456789012345678901"))
				if err != nil {
					t.Fatal(err)
				}
				catalog, err := biz.NewUsecase(fixture.db, provider, codec, []string{connection}, 16, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				usecase, err = biz.NewInitializationUsecase(catalog, fixture.db, provider)
				if err != nil {
					t.Fatal(err)
				}
			}
			operation, err := usecase.Create(adminContext("CreateInitialization"), adminMutation(), input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := usecase.Advance(context.Background(), operation.OperationID, "worker")
			if err != nil || result.State != biz.InitializationSucceeded {
				t.Fatalf("initialization failed: %v %+v", err, result)
			}
			err = fixture.db.View(context.Background(), func(reader biz.Reader) error {
				group, err := reader.Group(context.Background(), result.Groups["group"].GroupID)
				if err != nil {
					return err
				}
				if mode == "first_cluster" {
					if group.Admission != biz.Open || !operation.Plan.CreateCluster {
						t.Fatal("first cluster initialization did not complete")
					}
				} else if group.Admission != biz.Closed {
					t.Fatal("existing/unprofiled group was automatically reopened")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresInitializationRejectsDiscoveredIdentityAndMembershipBeforeWrites(t *testing.T) {
	for _, mode := range []string{"changed_uid", "existing_membership", "not_selectable"} {
		t.Run(mode, func(t *testing.T) {
			fixture, provider, usecase, input := initializationPGFixture(t)
			selected := input.Groups[0].NewGroup.Nodes[0]
			discovered := provider.environment.Nodes[selected.UID]
			switch mode {
			case "changed_uid":
				discovered.Node.UID = biz.NewID()
			case "existing_membership":
				discovered.GroupID = fixture.group.GroupID
			case "not_selectable":
				discovered.Selectable = false
			}
			provider.environment.Nodes[selected.UID] = discovered
			if _, err := usecase.Create(adminContext("CreateInitialization"), adminMutation(), input); err == nil {
				t.Fatal("unselectable or changed discovered node accepted")
			}
			var pools []biz.Pool
			err := fixture.db.View(context.Background(), func(reader biz.Reader) error {
				var err error
				pools, err = reader.Pools(context.Background(), fixture.cluster.ClusterID, "", 100)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(pools) != 1 {
				t.Fatal("rejected request wrote catalog objects")
			}
		})
	}
}

func TestPostgresInitializationLeaseEpochFencesStaleCommit(t *testing.T) {
	fixture, _, usecase, input := initializationPGFixture(t)
	operation, err := usecase.Create(adminContext("CreateInitialization"), adminMutation(), input)
	if err != nil {
		t.Fatal(err)
	}
	var stale biz.InitializationOperation
	err = fixture.db.InitializationTransact(context.Background(), func(tx biz.InitializationTransaction) error {
		current, err := tx.InitializationOperation(context.Background(), operation.OperationID)
		if err != nil {
			return err
		}
		stale = *current
		current.LeaseEpoch++
		current.LeaseOwner = "replacement"
		current.LeaseExpiresAt = time.Now().Add(time.Minute)
		return tx.SaveInitializationOperation(context.Background(), *current, current.Version, stale.LeaseEpoch)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = fixture.db.InitializationTransact(context.Background(), func(tx biz.InitializationTransaction) error {
		stale.Version++
		return tx.SaveInitializationOperation(context.Background(), stale, operation.Version, stale.LeaseEpoch)
	})
	if err == nil {
		t.Fatal("stale worker committed with prior epoch")
	}
	persisted, err := fixture.db.InitializationOperation(context.Background(), operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Version != operation.Version || persisted.LeaseOwner != "replacement" {
		t.Fatal("lease heartbeat changed public version or stale save overwrote lease")
	}
}
