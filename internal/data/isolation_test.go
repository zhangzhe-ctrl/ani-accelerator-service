package data

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

func TestPostgresExplicitTenantSQLCIsolation(t *testing.T) {
	f := pgSetup(t)
	ctx := context.Background()
	var secure bool
	if e := f.db.pool.QueryRow(ctx, testSQL(t, "security_catalog.sql")).Scan(&secure); e != nil || !secure {
		t.Fatalf("runtime catalog security: %v %v", secure, e)
	}
	p := f.projection(1)
	syncCtx := biz.WithCaller(ctx, biz.Caller{Service: "ani-governance", Action: "SyncGpuUsage"})
	if _, e := f.u.SyncUsage(syncCtx, "tenant-original", p); e != nil {
		t.Fatal(e)
	}
	other := *p
	otherRef := *p.Ref
	otherRef.TenantID = biz.NewID()
	other.Ref = &otherRef
	other.PayloadDigest, _ = biz.ProjectionDigest(&other)
	// Identical business and idempotency identities are legal in different tenants.
	if _, e := f.u.SyncUsage(syncCtx, "tenant-same-business-key", &other); e != nil {
		t.Fatal(e)
	}
	if e := f.db.View(ctx, func(r biz.Reader) error {
		for _, want := range []*biz.UsageProjection{p, &other} {
			got, e := r.Usage(ctx, *want.Ref)
			if e != nil {
				return e
			}
			if *got.Ref != *want.Ref {
				t.Fatal("known cross-tenant resource selected")
			}
			list, e := r.Usages(ctx, want.Ref.TenantID, "", 0, "", 20)
			if e != nil {
				return e
			}
			if len(list) != 1 || *list[0].Ref != *want.Ref {
				t.Fatal("tenant list predicate lost", list)
			}
		}
		unknown := *p.Ref
		unknown.TenantID = biz.NewID()
		if _, e := r.Usage(ctx, unknown); !biz.IsNotFound(e) {
			t.Fatal("known foreign UUID exposed", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, path := range [][]string{{"ref"}, {"ref", "tenant_id"}, {"ref", "owner_service"}, {"ref", "resource_id"}, {"ref", "create_operation_id"}, {"profile_id"}, {"profile_version"}, {"execution_digest"}, {"revision"}, {"state"}, {"source_operation_created_at"}} {
		_, e := f.db.pool.Exec(ctx, testSQL(t, "corrupt_usage_json.sql"), p.Ref.TenantID, p.Ref.OwnerService, p.Ref.ResourceID, path)
		var constraint *pgconn.PgError
		if !errors.As(e, &constraint) || constraint.Code != "23514" {
			t.Fatalf("NULL bypass for %v: %v", path, e)
		}
	}
	foreign := biz.NewID()
	_, e := f.db.pool.Exec(ctx, testSQL(t, "relink_revision.sql"), foreign, p.Ref.OwnerService, p.Ref.ResourceID, p.Ref.CreateOperationID, p.PayloadDigest)
	var constraint *pgconn.PgError
	if !errors.As(e, &constraint) || constraint.Code != "23503" {
		t.Fatal("cross-tenant revision FK", e)
	}
	// Terminal updates and replay remain in the original tenant despite same keys.
	p.Revision = 2
	p.State = biz.Ended
	p.EndReason = biz.OwnerReleased
	p.SourceFactRef = "quota-operation:" + p.Ref.CreateOperationID + ":gpu-fully-released"
	p.PayloadDigest, _ = biz.ProjectionDigest(p)
	if _, e = f.u.SyncUsage(syncCtx, "terminal", p); e != nil {
		t.Fatal(e)
	}
	reopened := openPGTest(t)
	if e = reopened.View(ctx, func(r biz.Reader) error {
		got, e := r.Usage(ctx, *other.Ref)
		if e != nil {
			return e
		}
		if got.State != biz.Declared {
			t.Fatal("cross-tenant update or restart leakage")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	t.Log("RLS/SQLC/TENANT: restricted role, no policy/RLS, identical tenant business keys, read/list/update/restart separation, JSON NULL and composite FK negatives")
}
