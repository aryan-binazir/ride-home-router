package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"ride-home-router/internal/database"
	"time"
)

type importJobRepository struct{ db *sql.DB }

func (s *Store) ImportJobs() database.ImportJobRepository { return &importJobRepository{db: s.db} }

type queryRows interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func importRows(ctx context.Context, q queryRows, id string) ([]database.ImportRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT row_index,payload,selected FROM import_rows WHERE session_id=$1 ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []database.ImportRow{}
	for rows.Next() {
		var row database.ImportRow
		if err = rows.Scan(&row.Index, &row.Data, &row.Selected); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *importJobRepository) Rows(ctx context.Context, id string) ([]database.ImportRow, error) {
	return importRows(ctx, r.db, id)
}

func (w workflowWrites) ImportRows(ctx context.Context, id string) ([]database.ImportRow, error) {
	return importRows(ctx, w.tx, id)
}

func (w workflowWrites) StageImport(ctx context.Context, id string, rows []database.ImportRow, jobs []database.ImportJob) error {
	data := make([]json.RawMessage, len(rows))
	selected := make([]bool, len(rows))
	for i, row := range rows {
		data[i] = row.Data
		selected[i] = row.Selected
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(ctx, `INSERT INTO import_rows(session_id,row_index,payload,selected) SELECT $1,ordinality-1,value,($3::boolean[])[ordinality] FROM json_array_elements($2::json) WITH ORDINALITY`, id, string(encoded), selected)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err = w.tx.ExecContext(ctx, `INSERT INTO import_jobs(session_id,job_index,address,row_indices) VALUES($1,$2,$3,$4)`, id, job.Index, job.Address, job.Rows); err != nil {
			return err
		}
	}
	return nil
}

func (w workflowWrites) SelectImportRows(ctx context.Context, id string, selected []bool) error {
	var count int
	if err := w.tx.QueryRowContext(ctx, `SELECT count(*) FROM import_rows WHERE session_id=$1`, id).Scan(&count); err != nil {
		return err
	}
	if count != len(selected) {
		return database.ErrInvalidWorkflowSelection
	}
	_, err := w.tx.ExecContext(ctx, `UPDATE import_rows SET selected=($2::boolean[])[row_index+1] WHERE session_id=$1`, id, selected)
	return err
}

func (w workflowWrites) ClearImportRows(ctx context.Context, id string) error {
	if _, err := w.tx.ExecContext(ctx, `DELETE FROM import_jobs WHERE session_id=$1`, id); err != nil {
		return err
	}
	_, err := w.tx.ExecContext(ctx, `DELETE FROM import_rows WHERE session_id=$1`, id)
	return err
}

func (r *importJobRepository) Progress(ctx context.Context, id string) (int, int, error) {
	var done, total int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE done),count(*) FROM import_jobs WHERE session_id=$1`, id).Scan(&done, &total)
	return done, total, err
}

func (r *importJobRepository) Claim(ctx context.Context, token string, ttl time.Duration) (database.ImportJob, bool, error) {
	var job database.ImportJob
	var indices []byte
	err := r.db.QueryRowContext(ctx, `UPDATE import_jobs SET claim_token=$1,claimed_until=clock_timestamp()+$2*interval '1 second' WHERE (session_id,job_index) = (
 SELECT j.session_id,j.job_index FROM import_jobs j JOIN workflow_sessions s ON s.kind='import' AND s.id=j.session_id
 WHERE (SELECT next_at FROM provider_throttles WHERE name=$3)<=clock_timestamp() AND NOT j.done AND (j.claimed_until IS NULL OR j.claimed_until<clock_timestamp()) AND NOT s.consumed AND s.expires_at>clock_timestamp()
 ORDER BY j.claimed_until NULLS FIRST,j.session_id,j.job_index FOR UPDATE OF j SKIP LOCKED LIMIT 1)
 RETURNING session_id,job_index,address,array_to_json(row_indices),claim_token`, token, ttl.Seconds(), geocodingThrottle).Scan(&job.SessionID, &job.Index, &job.Address, &indices, &job.Token)
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	if err == nil {
		err = json.Unmarshal(indices, &job.Rows)
	}
	return job, err == nil, err
}

func (r *importJobRepository) Release(ctx context.Context, job database.ImportJob) error {
	_, err := r.db.ExecContext(ctx, `UPDATE import_jobs SET claim_token=NULL,claimed_until=GREATEST(clock_timestamp()+interval '30 seconds',COALESCE((SELECT next_at FROM provider_throttles WHERE name=$4),clock_timestamp())) WHERE session_id=$1 AND job_index=$2 AND claim_token=$3 AND NOT done`, job.SessionID, job.Index, job.Token, geocodingThrottle)
	return err
}

func (r *importJobRepository) Finish(ctx context.Context, job database.ImportJob, rows []database.ImportRow, failed bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	var consumed bool
	err = tx.QueryRowContext(ctx, `SELECT consumed FROM workflow_sessions WHERE kind='import' AND id=$1 AND expires_at>clock_timestamp() FOR UPDATE`, job.SessionID).Scan(&consumed)
	if errors.Is(err, sql.ErrNoRows) || consumed {
		return database.ErrNotFound
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE import_jobs SET done=true,claim_token=NULL,claimed_until=GREATEST(clock_timestamp()+interval '30 seconds',COALESCE((SELECT next_at FROM provider_throttles WHERE name=$4),clock_timestamp())) WHERE session_id=$1 AND job_index=$2 AND claim_token=$3 AND NOT done AND claimed_until>clock_timestamp()`, job.SessionID, job.Index, job.Token, geocodingThrottle)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return database.ErrWorkflowConflict
	}
	for _, row := range rows {
		_, err = tx.ExecContext(ctx, `UPDATE import_rows SET payload=$3,selected=CASE WHEN $4 THEN false ELSE selected END WHERE session_id=$1 AND row_index=$2`, job.SessionID, row.Index, string(row.Data), failed)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
