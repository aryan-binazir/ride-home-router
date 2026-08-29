package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"time"
)

type rosterFields struct {
	id        *int64
	createdAt *time.Time
	updatedAt *time.Time
}

// rosterWriteCore owns the transaction sequences shared by participant and
// driver writes. Entity-specific SQL stays in each repository.
type rosterWriteCore[T any] struct {
	db        *sql.DB
	noun      string
	table     string
	labels    membershipTable
	key       func(*T) string
	insert    func(context.Context, *sql.Tx, *T, time.Time) (int64, error)
	updateRow func(context.Context, *sql.Tx, *T, time.Time) (sql.Result, error)
	fields    func(*T) rosterFields
}

func (w rosterWriteCore[T]) createBatch(ctx context.Context, entities []*T, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to begin %s batch transaction: %w", w.noun, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, w.table); err != nil {
		return database.BatchCreateResult{}, err
	}
	existing, err := rosterKeys(ctx, tx, w.table)
	if err != nil {
		return database.BatchCreateResult{}, err
	}

	type createdRow struct {
		entity    *T
		id        int64
		createdAt time.Time
	}
	createdRows := make([]createdRow, 0, len(entities))
	batchResult := database.BatchCreateResult{}
	for i, entity := range entities {
		if entity == nil {
			return database.BatchCreateResult{}, errors.New(w.noun + " batch contains a nil " + w.noun)
		}
		key := w.key(entity)
		_, duplicate := existing[key]
		allowDuplicate := i < len(allowExistingDuplicate) && allowExistingDuplicate[i]
		if key != "" && duplicate && !allowDuplicate {
			batchResult.SkippedDuplicate++
			continue
		}

		// Do not add inserted keys to existing. Duplicate handling intentionally
		// applies only to rows that existed before this batch began.
		now := time.Now()
		id, err := w.insert(ctx, tx, entity, now)
		if err != nil {
			return database.BatchCreateResult{}, fmt.Errorf("failed to create %s in batch: %w", w.noun, err)
		}
		createdRows = append(createdRows, createdRow{entity: entity, id: id, createdAt: now})
		batchResult.Created++
	}

	if err := tx.Commit(); err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to commit %s batch transaction: %w", w.noun, err)
	}
	for _, row := range createdRows {
		fields := w.fields(row.entity)
		*fields.id = row.id
		*fields.createdAt = row.createdAt
		*fields.updatedAt = row.createdAt
	}
	return batchResult, nil
}

func (w rosterWriteCore[T]) createWithLabels(ctx context.Context, entity *T, labelIDs []int64) (*T, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin %s transaction: %w", w.noun, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, w.table); err != nil {
		return nil, err
	}
	now := time.Now()
	fields := w.fields(entity)
	*fields.createdAt = now
	*fields.updatedAt = now
	id, err := w.insert(ctx, tx, entity, now)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", w.noun, err)
	}
	*fields.id = id
	if err := insertLabelMemberships(ctx, tx, w.labels, id, labelIDs); err != nil {
		return nil, fmt.Errorf("failed to insert %s label memberships: %w", w.noun, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit %s transaction: %w", w.noun, err)
	}
	return entity, nil
}

func (w rosterWriteCore[T]) updateWithLabels(ctx context.Context, entity *T, labelIDs []int64, replaceLabels bool) (*T, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin %s transaction: %w", w.noun, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	fields := w.fields(entity)
	*fields.updatedAt = now
	result, err := w.updateRow(ctx, tx, entity, now)
	if err != nil {
		return nil, fmt.Errorf("failed to update %s: %w", w.noun, err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	if replaceLabels {
		if err := replaceLabelMemberships(ctx, tx, w.labels, *fields.id, labelIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit %s transaction: %w", w.noun, err)
	}
	return entity, nil
}

func (w rosterWriteCore[T]) delete(ctx context.Context, id int64) error {
	result, err := w.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, w.table), id)
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", w.noun, err)
	}
	return rowsAffectedOrNotFound(result)
}

func (w rosterWriteCore[T]) restore(ctx context.Context, id int64) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin %s restore transaction: %w", w.noun, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, w.table); err != nil {
		return err
	}
	key, err := archivedRosterKey(ctx, tx, w.table, id)
	if err != nil {
		return err
	}
	existing, err := rosterKeys(ctx, tx, w.table)
	if err != nil {
		return err
	}
	if _, duplicate := existing[key]; key != "" && duplicate {
		return database.ErrDuplicate
	}

	result, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`, w.table), id)
	if err != nil {
		return fmt.Errorf("failed to restore %s: %w", w.noun, err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s restore transaction: %w", w.noun, err)
	}
	return nil
}

// lockRoster keeps duplicate checks and roster writes in one serial order.
func lockRoster(ctx context.Context, tx *sql.Tx, table string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "ride-home-router:"+table); err != nil {
		return fmt.Errorf("failed to lock %s for writing: %w", table, err)
	}
	return nil
}

// rosterKeys loads normalized name and address keys from a roster table.
func rosterKeys(ctx context.Context, tx *sql.Tx, table string) (map[string]struct{}, error) {
	var query string
	switch table {
	case "participants":
		query = `SELECT name, address FROM participants WHERE deleted_at IS NULL`
	case "drivers":
		query = `SELECT name, address FROM drivers WHERE deleted_at IS NULL`
	default:
		return nil, fmt.Errorf("invalid roster table %q", table)
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s duplicates: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	keys := make(map[string]struct{})
	for rows.Next() {
		var name, address string
		if err := rows.Scan(&name, &address); err != nil {
			return nil, fmt.Errorf("failed to scan %s duplicate: %w", table, err)
		}
		if key := models.RosterKey(name, address); key != "" {
			keys[key] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate %s duplicates: %w", table, err)
	}
	return keys, nil
}

func archivedRosterKey(ctx context.Context, tx *sql.Tx, table string, id int64) (string, error) {
	var query string
	switch table {
	case "participants":
		query = `SELECT name, address FROM participants WHERE id = $1 AND deleted_at IS NOT NULL`
	case "drivers":
		query = `SELECT name, address FROM drivers WHERE id = $1 AND deleted_at IS NOT NULL`
	default:
		return "", fmt.Errorf("invalid roster table %q", table)
	}

	var name, address string
	if err := tx.QueryRowContext(ctx, query, id).Scan(&name, &address); errors.Is(err, sql.ErrNoRows) {
		return "", database.ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("failed to query archived %s: %w", table, err)
	}
	return models.RosterKey(name, address), nil
}
