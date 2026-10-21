package data

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/data/accdb"
)

type initializationStore struct {
	*pgStore
	queries *accdb.Queries
}

func (p *Postgres) InitializationTransact(ctx context.Context, fn func(biz.InitializationTransaction) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return cleanError(err)
	}
	defer tx.Rollback(context.Background())
	queries := accdb.New(tx)
	if err = fn(&initializationStore{pgStore: &pgStore{q: queries}, queries: queries}); err != nil {
		return err
	}
	return cleanError(tx.Commit(ctx))
}

func decodeInitializationOperation(payload []byte, owner string, epoch uint64, expires time.Time) (*biz.InitializationOperation, error) {
	operation, err := decodeOne[biz.InitializationOperation](payload, nil)
	if err != nil {
		return nil, err
	}
	operation.LeaseOwner = owner
	operation.LeaseEpoch = epoch
	operation.LeaseExpiresAt = expires
	return operation, nil
}

func (p *Postgres) InitializationOperation(ctx context.Context, id string) (*biz.InitializationOperation, error) {
	row, err := accdb.New(p.pool).InitializationOperationData(ctx, id)
	if err != nil {
		return nil, cleanError(err)
	}
	return decodeInitializationOperation(row.Payload, row.LeaseOwner, row.LeaseEpoch, row.LeaseExpiresAt)
}

func (p *Postgres) LatestInitializationOperation(ctx context.Context, connection string) (*biz.InitializationOperation, error) {
	row, err := accdb.New(p.pool).LatestInitializationOperationData(ctx, connection)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, cleanError(err)
	}
	return decodeInitializationOperation(row.Payload, row.LeaseOwner, row.LeaseEpoch, row.LeaseExpiresAt)
}

func (p *Postgres) InitializationWork(ctx context.Context, connections []string, limit int) ([]string, error) {
	ids, err := accdb.New(p.pool).InitializationWorkData(ctx, connections, int32(limit))
	return ids, cleanError(err)
}

func (s *initializationStore) InitializationOperation(ctx context.Context, id string) (*biz.InitializationOperation, error) {
	row, err := s.queries.LockInitializationOperationData(ctx, id)
	if err != nil {
		return nil, cleanError(err)
	}
	return decodeInitializationOperation(row.Payload, row.LeaseOwner, row.LeaseEpoch, row.LeaseExpiresAt)
}

func (s *initializationStore) InsertInitializationOperation(ctx context.Context, operation biz.InitializationOperation) error {
	payload, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	if err = s.queries.InsertInitializationOperation(ctx, operation.OperationID, operation.ConnectionRef, operation.Version, string(operation.State), operation.LeaseOwner, operation.LeaseEpoch, operation.LeaseExpiresAt, operation.CreatedAt, payload); err != nil {
		return cleanError(err)
	}
	return s.saveInitializationSteps(ctx, operation)
}

func (s *initializationStore) SaveInitializationOperation(ctx context.Context, operation biz.InitializationOperation, version, epoch uint64) error {
	payload, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	affected, err := s.queries.SaveInitializationOperationCAS(ctx, operation.Version, string(operation.State), operation.LeaseOwner, operation.LeaseEpoch, operation.LeaseExpiresAt, payload, operation.OperationID, version, epoch)
	if err != nil {
		return cleanError(err)
	}
	if affected != 1 {
		return biz.Conflict("INITIALIZATION_VERSION_CONFLICT")
	}
	return s.saveInitializationSteps(ctx, operation)
}

func (s *initializationStore) saveInitializationSteps(ctx context.Context, operation biz.InitializationOperation) error {
	for index, step := range operation.Steps {
		payload, err := json.Marshal(step)
		if err != nil {
			return err
		}
		if err = s.queries.SaveInitializationStep(ctx, step.StepID, operation.OperationID, step.ObjectKey, step.Kind, int32(index), payload); err != nil {
			return cleanError(err)
		}
	}
	return nil
}

var _ biz.InitializationRepository = (*Postgres)(nil)
var _ biz.InitializationSummaryRepository = (*Postgres)(nil)
