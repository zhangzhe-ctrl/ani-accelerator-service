package data

import (
	"bytes"
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
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
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
	q := accdb.New(p.pool)
	version, err := q.SchemaVersion(ctx)
	if err != nil {
		return biz.Precondition("MIGRATION_REQUIRED")
	}
	if version != 8 {
		return biz.Precondition("SCHEMA_VERSION_UNSUPPORTED")
	}
	restricted, err := q.RuntimeRestricted(ctx)
	if err != nil {
		return cleanError(err)
	}
	if !restricted {
		return biz.Precondition("RUNTIME_DDL_FORBIDDEN")
	}
	return nil
}

type pgStore struct{ q *accdb.Queries }

func (p *Postgres) View(ctx context.Context, fn func(biz.Reader) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return cleanError(err)
	}
	defer tx.Rollback(context.Background())
	if err = fn(&pgStore{accdb.New(tx)}); err != nil {
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
	if err = fn(&pgStore{accdb.New(tx)}); err != nil {
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
func jsonBytes(v any) ([]byte, error) { return json.Marshal(v) }
func decodeOne[T any](b []byte, err error) (*T, error) {
	if err != nil {
		return nil, cleanError(err)
	}
	v := new(T)
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(v) != nil {
		return nil, biz.Precondition("STORED_DATA_INVALID")
	}
	return v, nil
}
func decodeMany[T any](rows [][]byte, err error) ([]T, error) {
	if err != nil {
		return nil, cleanError(err)
	}
	result := make([]T, 0, len(rows))
	for _, b := range rows {
		var v T
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&v) != nil {
			return nil, biz.Precondition("STORED_DATA_INVALID")
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *pgStore) Lock(ctx context.Context, key string) error {
	return cleanError(s.q.LockWrite1(ctx, key))
}
func (s *pgStore) Replay(ctx context.Context, actor, method, key string) (*biz.IdempotencyRecord, error) {
	r := &biz.IdempotencyRecord{Actor: actor, Method: method, Key: key}
	row, err := s.q.ReplayRecord(ctx, actor, method, key)
	r.Digest, r.Result = row.PayloadDigest, row.Result
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, cleanError(err)
}
func (s *pgStore) Remember(ctx context.Context, r biz.IdempotencyRecord) error {
	return cleanError(s.q.RememberWrite1(ctx, r.Actor, r.Method, r.Key, r.Digest, r.Result))
}
func (s *pgStore) Cluster(ctx context.Context, id string) (*biz.Cluster, error) {
	return decodeOne[biz.Cluster](s.q.ClusterData1(ctx, id))
}
func (s *pgStore) Clusters(ctx context.Context, connections []string, after string, n int) ([]biz.Cluster, error) {
	return decodeMany[biz.Cluster](s.q.ClustersData1(ctx, connections, after, int32(n)))
}
func (s *pgStore) Pool(ctx context.Context, id string) (*biz.Pool, error) {
	return decodeOne[biz.Pool](s.q.PoolData1(ctx, id))
}
func (s *pgStore) Pools(ctx context.Context, cluster, after string, n int) ([]biz.Pool, error) {
	return decodeMany[biz.Pool](s.q.PoolsData1(ctx, cluster, after, int32(n)))
}
func (s *pgStore) Group(ctx context.Context, id string) (*biz.SupplyGroup, error) {
	return decodeOne[biz.SupplyGroup](s.q.GroupData1(ctx, id))
}
func (s *pgStore) Groups(ctx context.Context, pool, after string, n int) ([]biz.SupplyGroup, error) {
	return decodeMany[biz.SupplyGroup](s.q.GroupsData1(ctx, pool, after, int32(n)))
}
func (s *pgStore) Profile(ctx context.Context, id string, v uint64) (*biz.Profile, error) {
	return decodeOne[biz.Profile](s.q.ProfileData1(ctx, id, v))
}
func (s *pgStore) Profiles(ctx context.Context, cluster string, mode biz.SupplyMode, after string, n int) ([]biz.Profile, error) {
	return decodeMany[biz.Profile](s.q.ProfilesData1(ctx, cluster, int32(mode), after, int32(n)))
}
func (s *pgStore) InsertCluster(ctx context.Context, c biz.Cluster) error {
	b, e := jsonBytes(c)
	if e != nil {
		return e
	}
	return cleanError(s.q.InsertClusterWrite1(ctx, c.ClusterID, c.ConnectionRef, c.Version, b))
}
func (s *pgStore) InsertPool(ctx context.Context, p biz.Pool) error {
	b, e := jsonBytes(p)
	if e != nil {
		return e
	}
	return cleanError(s.q.InsertPoolWrite1(ctx, p.PoolID, p.ClusterID, b))
}
func (s *pgStore) InsertGroup(ctx context.Context, g biz.SupplyGroup) error {
	b, e := jsonBytes(g)
	if e != nil {
		return e
	}
	if e = cleanError(s.q.InsertGroupWrite1(ctx, g.GroupID, g.ClusterID, g.PoolID, int32(g.Mode), g.Version, g.Baseline.BaselineDigest, b)); e != nil {
		return e
	}
	for _, n := range g.Nodes {
		if e = cleanError(s.q.InsertGroupWrite2(ctx, g.ClusterID, n.UID, n.Name, g.GroupID)); e != nil {
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
	affected, e := s.q.UpdateGroupCAS(ctx, g.Version, b, g.GroupID, expected, g.Baseline.BaselineDigest)
	if e != nil {
		return cleanError(e)
	}
	if affected != 1 {
		return biz.Conflict("VERSION_CONFLICT")
	}
	return nil
}
func (s *pgStore) InsertProfile(ctx context.Context, p biz.Profile, cluster string) error {
	b, e := jsonBytes(p)
	if e != nil {
		return e
	}
	return cleanError(s.q.InsertProfileWrite1(ctx, p.ProfileID, p.ProfileVersion, cluster, p.Spec.GroupID, int32(p.Spec.Mode), b))
}
func (s *pgStore) UpdateProfile(ctx context.Context, p biz.Profile, expected uint64) error {
	b, e := jsonBytes(p)
	if e != nil {
		return e
	}
	count, e := s.q.UpdateProfileCAS(ctx, b, p.ProfileID, expected)
	if e != nil {
		return cleanError(e)
	}
	if count != 1 {
		return biz.Conflict("VERSION_CONFLICT")
	}
	return nil
}
