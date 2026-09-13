package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"time"
)

type workflowRepository struct{ db *sql.DB }

func (s *Store) Workflows() database.WorkflowRepository { return &workflowRepository{db: s.db} }

func (r *workflowRepository) Create(ctx context.Context, kind, id string, data []byte, ttl time.Duration) error {
	if len(data) > 24<<20 {
		return database.ErrWorkflowPayloadTooLarge
	}
	limit := 256
	if kind == "import" {
		limit = 4
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':workflow-capacity:' || $1,0))`, kind); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_sessions WHERE kind=$1 AND NOT consumed AND expires_at>clock_timestamp()`, kind).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		if kind != "draft" && kind != "route" {
			return database.ErrWorkflowCapacity
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM workflow_sessions WHERE (kind,id) IN (SELECT kind,id FROM workflow_sessions WHERE kind=$1 AND NOT consumed AND expires_at>clock_timestamp() ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2)`, kind, count-limit+1)
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if removed < int64(count-limit+1) {
			return database.ErrWorkflowCapacity
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_sessions(kind,id,payload,expires_at) VALUES($1,$2,$3,clock_timestamp()+$4*interval '1 second')`, kind, id, string(data), ttl.Seconds())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *workflowRepository) Load(ctx context.Context, kind, id string, ttl time.Duration) (database.WorkflowRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var record database.WorkflowRecord
	err := r.db.QueryRowContext(ctx, `UPDATE workflow_sessions SET expires_at=clock_timestamp()+$3*interval '1 second' WHERE kind=$1 AND id=$2 AND expires_at>clock_timestamp() RETURNING payload,revision,consumed`, kind, id, ttl.Seconds()).Scan(&record.Data, &record.Revision, &record.Consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return record, database.ErrNotFound
	}
	return record, err
}

func (r *workflowRepository) Update(ctx context.Context, kind, id string, ttl time.Duration, update func(*database.WorkflowRecord) error) error {
	return r.Transact(ctx, kind, id, ttl, func(record *database.WorkflowRecord, _ database.WorkflowWrites) error { return update(record) })
}

func (r *workflowRepository) Transact(ctx context.Context, kind, id string, ttl time.Duration, update func(*database.WorkflowRecord, database.WorkflowWrites) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	var record database.WorkflowRecord
	err = tx.QueryRowContext(ctx, `SELECT payload,revision,consumed FROM workflow_sessions WHERE kind=$1 AND id=$2 AND expires_at>clock_timestamp() FOR UPDATE`, kind, id).Scan(&record.Data, &record.Revision, &record.Consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return database.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = update(&record, workflowWrites{db: r.db, tx: tx}); err != nil {
		return err
	}
	if len(record.Data) > 24<<20 {
		return database.ErrWorkflowPayloadTooLarge
	}
	_, err = tx.ExecContext(ctx, `UPDATE workflow_sessions SET payload=$3,revision=revision+1,consumed=$4,expires_at=clock_timestamp()+$5*interval '1 second' WHERE kind=$1 AND id=$2`, kind, id, string(record.Data), record.Consumed, ttl.Seconds())
	if err != nil {
		return fmt.Errorf("save workflow: %w", err)
	}
	return tx.Commit()
}

func (r *workflowRepository) CompareAndSwap(ctx context.Context, kind, id string, revision int64, data []byte, ttl time.Duration) error {
	return r.Update(ctx, kind, id, ttl, func(record *database.WorkflowRecord) error {
		if record.Revision != revision || record.Consumed {
			return database.ErrWorkflowConflict
		}
		record.Data = data
		return nil
	})
}

func (r *workflowRepository) Delete(ctx context.Context, kind, id string) error {
	if id == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM workflow_sessions WHERE kind=$1 AND id=$2 AND NOT consumed`, kind, id)
	return err
}

// CleanupWorkflows deletes a bounded batch; concurrent sweepers skip busy rows.
func (s *Store) CleanupWorkflows(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflow_sessions WHERE (kind,id) IN (SELECT kind,id FROM workflow_sessions WHERE expires_at<clock_timestamp() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT 100)`)
	if err != nil {
		return err
	}
	return s.cleanupDeletedRoster(ctx)
}
