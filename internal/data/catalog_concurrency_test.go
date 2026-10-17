package data

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

// The provider observations run against the actual restricted PG adapter.
// Before delaying, assert its pool has no acquired connection; this catches
// read-only transactions too, not just advisory-lock contention.
type delayedCatalogPorts struct {
	db      *Postgres
	phase   string
	entered chan error
	release chan struct{}
	once    sync.Once
}

func (p *delayedCatalogPorts) pause(ctx context.Context, phase string) error {
	if p.phase != phase {
		return nil
	}
	var err error
	p.once.Do(func() {
		if n := p.db.pool.Stat().AcquiredConns(); n != 0 {
			err = fmt.Errorf("external %s holds %d PostgreSQL connections", phase, n)
		}
		p.entered <- err
		if err == nil {
			select {
			case <-p.release:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
	})
	return err
}
func (p *delayedCatalogPorts) Inspect(ctx context.Context, c biz.Cluster, g biz.SupplyGroup) (biz.GroupInspection, error) {
	if err := p.pause(ctx, "inspect"); err != nil {
		return biz.GroupInspection{}, err
	}
	return testProvider{}.Inspect(ctx, c, g)
}
func (p *delayedCatalogPorts) Collect(ctx context.Context, c biz.Cluster, g []biz.SupplyGroup) (biz.ObservationBatch, error) {
	return testProvider{}.Collect(ctx, c, g)
}
func delayedCatalogUsecase(t *testing.T, f *pgFixture, phase string) (*biz.Usecase, *delayedCatalogPorts) {
	t.Helper()
	p := &delayedCatalogPorts{db: f.db, phase: phase, entered: make(chan error, 1), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	codec, e := biz.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if e != nil {
		t.Fatal(e)
	}
	u, e := biz.NewUsecase(f.db, p, codec, []string{f.cluster.ConnectionRef}, 16, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	return u, p
}
func awaitCatalogGate(t *testing.T, p *delayedCatalogPorts) {
	t.Helper()
	select {
	case e := <-p.entered:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("external gate not reached")
	}
}
func timedCatalogContext(method string) (context.Context, context.CancelFunc) {
	return context.WithTimeout(adminContext(method), 2*time.Second)
}

func TestPostgresCatalogProviderOutsideTransactions(t *testing.T) {
	t.Run("adopt_same_key_winner", func(t *testing.T) {
		f := pgSetup(t)
		slow, p := delayedCatalogUsecase(t, f, "inspect")
		baseline := *f.group.Baseline
		in := biz.AdoptInput{PoolID: f.pool.PoolID, DisplayName: "concurrent", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: "node-" + biz.NewID(), Name: "concurrent-node"}}, ModelKey: f.group.ModelKey, Baseline: &baseline, QueueName: f.group.QueueName}
		m := adminMutation()
		done := make(chan error, 1)
		var got *biz.SupplyGroup
		go func() {
			var e error
			got, e = slow.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), m, in)
			done <- e
		}()
		awaitCatalogGate(t, p)
		ctx, cancel := timedCatalogContext("AdoptSupplyGroup")
		defer cancel()
		winner, e := f.u.AdoptSupplyGroup(ctx, m, in)
		if e != nil {
			t.Fatal("same-key winner blocked by provider", e)
		}
		close(p.release)
		if e = <-done; e != nil {
			t.Fatal(e)
		}
		if got.GroupID != winner.GroupID {
			t.Fatal("prepared loser did not replay original winner")
		}
	})
	for _, scenario := range []string{"create_inspect_change", "create_same_key", "open_change", "resolve_change"} {
		t.Run(scenario, func(t *testing.T) {
			f := pgSetup(t)
			if scenario == "create_same_key" {
				baseline := *f.group.Baseline
				g, err := f.u.AdoptSupplyGroup(adminContext("AdoptSupplyGroup"), adminMutation(), biz.AdoptInput{PoolID: f.pool.PoolID, DisplayName: "unpublished", Mode: biz.Shared, Nodes: []biz.NodeRef{{UID: "node-" + biz.NewID(), Name: "unpublished-node"}}, ModelKey: f.group.ModelKey, Baseline: &baseline, QueueName: f.group.QueueName})
				if err != nil {
					t.Fatal(err)
				}
				f.group = g
				spec := *f.profile.Spec
				spec.GroupID = g.GroupID
				profile := *f.profile
				profile.Spec = &spec
				f.profile = &profile
			}
			phase := "inspect"
			slow, p := delayedCatalogUsecase(t, f, phase)
			m := adminMutation()
			in := biz.CreateProfileInput{DisplayName: "concurrent profile", Spec: f.profile.Spec, ExpectedGroupVersion: f.group.Version}
			done := make(chan error, 1)
			var got *biz.Profile
			go func() {
				var e error
				switch scenario {
				case "open_change":
					_, e = slow.SetAdmission(adminContext("SetAdmission"), m, biz.AdmissionInput{GroupID: f.group.GroupID, ExpectedVersion: f.group.Version, Desired: biz.Open, Reason: "delayed open"})
				case "resolve_change":
					actor := adminActor
					ctx := biz.WithCaller(context.Background(), biz.Caller{Service: "ani-governance", TenantID: f.tenant, Actor: actor, Action: "ResolveGpuRequest", ClusterIDs: []string{f.cluster.ClusterID}})
					_, e = slow.Resolve(ctx, &biz.TenantContext{RequestID: "resolve-race", TenantID: f.tenant, Actor: &actor}, f.request)
				default:
					got, e = slow.CreateProfile(adminContext("CreateProfile"), m, in)
				}
				done <- e
			}()
			awaitCatalogGate(t, p)
			if scenario == "create_same_key" {
				ctx, cancel := timedCatalogContext("CreateProfile")
				defer cancel()
				winner, e := f.u.CreateProfile(ctx, m, in)
				if e != nil {
					t.Fatal("same-key creation blocked by external I/O", e)
				}
				close(p.release)
				if e = <-done; e != nil {
					t.Fatal(e)
				}
				if got.ProfileID != winner.ProfileID {
					t.Fatal("same-key loser replaced winning snapshot")
				}
			} else {
				ctx, cancel := timedCatalogContext("SetAdmission")
				defer cancel()
				closed, e := f.u.SetAdmission(ctx, adminMutation(), biz.AdmissionInput{GroupID: f.group.GroupID, ExpectedVersion: f.group.Version, Desired: biz.Closed, Reason: "concurrent policy change"})
				if e != nil {
					t.Fatal("group writer blocked by external I/O", e)
				}
				close(p.release)
				e = <-done
				if e == nil || !strings.Contains(e.Error(), "VERSION_CONFLICT") {
					t.Fatal("stale prepared snapshot accepted", e)
				}
				if e = f.db.View(context.Background(), func(r biz.Reader) error {
					g, e := r.Group(context.Background(), f.group.GroupID)
					if e != nil {
						return e
					}
					if g.Version != closed.Version || g.Admission != biz.Closed {
						return fmt.Errorf("concurrent CLOSED state overwritten")
					}
					ps, e := r.Profiles(context.Background(), f.cluster.ClusterID, biz.Shared, "", 20)
					if e != nil {
						return e
					}
					if len(ps) != 1 {
						return fmt.Errorf("stale creation left profile")
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
	if !t.Failed() {
		t.Log("SQLC-02 PASS: actual restricted PG, delayed Inspect with zero acquired connections; same-key winners replayed and changed snapshots rejected across Adopt/Create/OPEN/Resolve")
	}
}
