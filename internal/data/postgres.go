package data

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type Postgres struct{ pool *pgxpool.Pool }

// OpenPostgres loads a secret reference, verifies an explicitly migrated
// schema and refuses a DDL-capable runtime role. It never creates anything.
func OpenPostgres(ctx context.Context, dsnFile string) (*Postgres, error) {
	b, err := os.ReadFile(dsnFile)
	if err != nil {
		return nil, biz.Invalid("DATABASE_SECRET_UNAVAILABLE")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, biz.Invalid("DATABASE_CONFIG_INVALID")
	}
	cfg.MaxConns = 12
	cfg.MinConns = 1
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, biz.Unavailable()
	}
	p := &Postgres{pool}
	if err = p.CheckRuntime(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}
func (p *Postgres) Close()                         { p.pool.Close() }
func (p *Postgres) Ping(ctx context.Context) error { return cleanError(p.pool.Ping(ctx)) }
func (p *Postgres) CheckRuntime(ctx context.Context) error {
	var dangerous bool
	err := p.pool.QueryRow(ctx, `SELECT r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR has_database_privilege(current_user,current_database(),'CREATE') OR has_database_privilege(current_user,current_database(),'TEMPORARY') OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname NOT LIKE 'pg_%' AND has_schema_privilege(current_user,n.oid,'CREATE')) OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator' AND pg_has_role(current_user,c.relowner,'USAGE')) FROM pg_roles r WHERE r.rolname=current_user`).Scan(&dangerous)
	if err != nil {
		return cleanError(err)
	}
	if dangerous {
		return biz.Precondition("RUNTIME_DDL_FORBIDDEN")
	}
	var version int
	if err = p.pool.QueryRow(ctx, `SELECT max(version) FROM accelerator.schema_migrations`).Scan(&version); err != nil {
		return biz.Precondition("MIGRATION_REQUIRED")
	}
	if version != 1 {
		return biz.Precondition("SCHEMA_VERSION_UNSUPPORTED")
	}
	return nil
}

type pgStore struct{ tx pgx.Tx }

func (p *Postgres) View(ctx context.Context, fn func(biz.Reader) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return cleanError(err)
	}
	defer tx.Rollback(context.Background())
	if err = fn(&pgStore{tx}); err != nil {
		return err
	}
	return cleanError(tx.Commit(ctx))
}
func (p *Postgres) Transact(ctx context.Context, fn func(biz.Transaction) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return cleanError(err)
	}
	defer tx.Rollback(context.Background())
	if err = fn(&pgStore{tx}); err != nil {
		return err
	}
	return cleanError(tx.Commit(ctx))
}
func cleanError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.NotFound()
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return biz.Conflict("UNIQUE_CONFLICT")
		case "23503", "23514", "23502", "22P02":
			return biz.Invalid("PERSISTENCE_CONSTRAINT")
		case "40001", "40P01":
			return biz.Conflict("TRANSACTION_RETRY")
		}
	}
	return biz.Unavailable()
}
func (s *pgStore) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.tx.Exec(ctx, query, args...)
	return cleanError(err)
}
func jsonBytes(v any) ([]byte, error) { return json.Marshal(v) }
func one[T any](ctx context.Context, s *pgStore, query string, args ...any) (*T, error) {
	var b []byte
	if err := s.tx.QueryRow(ctx, query, args...).Scan(&b); err != nil {
		return nil, cleanError(err)
	}
	v := new(T)
	if json.Unmarshal(b, v) != nil {
		return nil, biz.Precondition("STORED_DATA_INVALID")
	}
	return v, nil
}
func many[T any](ctx context.Context, s *pgStore, query string, args ...any) ([]T, error) {
	rows, err := s.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, cleanError(err)
	}
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, cleanError(err)
		}
		var v T
		if json.Unmarshal(b, &v) != nil {
			return nil, biz.Precondition("STORED_DATA_INVALID")
		}
		result = append(result, v)
	}
	return result, cleanError(rows.Err())
}
func (s *pgStore) Lock(ctx context.Context, key string) error {
	return s.exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731903231))`, key)
}
func (s *pgStore) Replay(ctx context.Context, actor, method, key string) (*biz.IdempotencyRecord, error) {
	r := &biz.IdempotencyRecord{Actor: actor, Method: method, Key: key}
	err := s.tx.QueryRow(ctx, `SELECT payload_digest,result FROM accelerator.admin_idempotency WHERE actor_digest=$1 AND method=$2 AND idempotency_key=$3`, actor, method, key).Scan(&r.Digest, &r.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, cleanError(err)
}
func (s *pgStore) Remember(ctx context.Context, r biz.IdempotencyRecord) error {
	return s.exec(ctx, `INSERT INTO accelerator.admin_idempotency(actor_digest,method,idempotency_key,payload_digest,result) VALUES($1,$2,$3,$4,$5)`, r.Actor, r.Method, r.Key, r.Digest, r.Result)
}
func (s *pgStore) Cluster(ctx context.Context, id string) (*biz.Cluster, error) {
	return one[biz.Cluster](ctx, s, `SELECT payload FROM accelerator.clusters WHERE cluster_id=$1`, id)
}
func (s *pgStore) Clusters(ctx context.Context, allowed []string, after string, n int) ([]biz.Cluster, error) {
	all := false
	for _, v := range allowed {
		if v == "*" {
			all = true
		}
	}
	return many[biz.Cluster](ctx, s, `SELECT payload FROM accelerator.clusters WHERE ($1 OR cluster_id::text=ANY($2::text[])) AND cluster_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY cluster_id LIMIT $4`, all, allowed, after, n)
}
func (s *pgStore) Pool(ctx context.Context, id string) (*biz.Pool, error) {
	return one[biz.Pool](ctx, s, `SELECT payload FROM accelerator.pools WHERE pool_id=$1`, id)
}
func (s *pgStore) Pools(ctx context.Context, cluster, after string, n int) ([]biz.Pool, error) {
	return many[biz.Pool](ctx, s, `SELECT payload FROM accelerator.pools WHERE cluster_id=$1 AND pool_id>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY pool_id LIMIT $3`, cluster, after, n)
}
func (s *pgStore) Group(ctx context.Context, id string) (*biz.SupplyGroup, error) {
	return one[biz.SupplyGroup](ctx, s, `SELECT payload FROM accelerator.supply_groups WHERE group_id=$1`, id)
}
func (s *pgStore) Groups(ctx context.Context, pool, after string, n int) ([]biz.SupplyGroup, error) {
	return many[biz.SupplyGroup](ctx, s, `SELECT payload FROM accelerator.supply_groups WHERE pool_id=$1 AND group_id>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY group_id LIMIT $3`, pool, after, n)
}
func (s *pgStore) Profile(ctx context.Context, id string, v uint64) (*biz.Profile, error) {
	return one[biz.Profile](ctx, s, `SELECT payload FROM accelerator.profiles WHERE profile_id=$1 AND profile_version=$2`, id, v)
}
func (s *pgStore) Profiles(ctx context.Context, cluster string, mode biz.SupplyMode, after string, n int) ([]biz.Profile, error) {
	return many[biz.Profile](ctx, s, `SELECT payload FROM accelerator.profiles WHERE cluster_id=$1 AND ($2=0 OR mode=$2) AND profile_id>COALESCE(NULLIF($3,'')::uuid,'00000000-0000-0000-0000-000000000000') ORDER BY profile_id LIMIT $4`, cluster, int32(mode), after, n)
}
func (s *pgStore) InsertCluster(ctx context.Context, c biz.Cluster) error {
	b, e := jsonBytes(c)
	if e != nil {
		return e
	}
	return s.exec(ctx, `INSERT INTO accelerator.clusters(cluster_id,connection_ref,version,payload) VALUES($1,$2,$3,$4)`, c.ClusterID, c.ConnectionRef, c.Version, b)
}
func (s *pgStore) InsertPool(ctx context.Context, p biz.Pool) error {
	b, e := jsonBytes(p)
	if e != nil {
		return e
	}
	return s.exec(ctx, `INSERT INTO accelerator.pools(pool_id,cluster_id,payload) VALUES($1,$2,$3)`, p.PoolID, p.ClusterID, b)
}
func (s *pgStore) InsertGroup(ctx context.Context, g biz.SupplyGroup) error {
	b, e := jsonBytes(g)
	if e != nil {
		return e
	}
	if e = s.exec(ctx, `INSERT INTO accelerator.supply_groups(group_id,cluster_id,pool_id,mode,version,baseline_digest,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, g.GroupID, g.ClusterID, g.PoolID, int32(g.Mode), g.Version, g.Baseline.BaselineDigest, b); e != nil {
		return e
	}
	for _, n := range g.Nodes {
		if e = s.exec(ctx, `INSERT INTO accelerator.group_nodes(cluster_id,node_uid,node_name,group_id) VALUES($1,$2,$3,$4)`, g.ClusterID, n.UID, n.Name, g.GroupID); e != nil {
			return e
		}
	}
	return nil
}
func (s *pgStore) UpdateGroup(ctx context.Context, g biz.SupplyGroup, expected uint64) error {
	b, e := jsonBytes(g)
	if e != nil {
		return e
	}
	tag, e := s.tx.Exec(ctx, `UPDATE accelerator.supply_groups SET version=$1,payload=$2 WHERE group_id=$3 AND version=$4 AND baseline_digest=$5`, g.Version, b, g.GroupID, expected, g.Baseline.BaselineDigest)
	if e != nil {
		return cleanError(e)
	}
	if tag.RowsAffected() != 1 {
		return biz.Conflict("VERSION_CONFLICT")
	}
	return nil
}
func (s *pgStore) InsertProfile(ctx context.Context, p biz.Profile, cluster string) error {
	b, e := jsonBytes(p)
	if e != nil {
		return e
	}
	return s.exec(ctx, `INSERT INTO accelerator.profiles(profile_id,profile_version,cluster_id,group_id,mode,payload) VALUES($1,$2,$3,$4,$5,$6)`, p.ProfileID, p.ProfileVersion, cluster, p.Spec.GroupID, int32(p.Spec.Mode), b)
}
func (s *pgStore) SharedVector(ctx context.Context, cluster string) (*biz.ProfileSpec, error) {
	p, e := one[biz.Profile](ctx, s, `SELECT payload FROM accelerator.profiles WHERE cluster_id=$1 AND mode=2 ORDER BY profile_id LIMIT 1`, cluster)
	if biz.IsNotFound(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return p.Spec, nil
}
