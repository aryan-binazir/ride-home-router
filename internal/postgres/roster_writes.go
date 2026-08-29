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
	// importUpdate applies the import-mutable fields of entity to the existing
	// row id. Name, address, and coordinates are never overwritten by imports.
	// Zero rows affected means the row was deleted since the key snapshot.
	importUpdate func(context.Context, *sql.Tx, int64, *T, time.Time) (sql.Result, error)
	fields       func(*T) rosterFields
}

func (w rosterWriteCore[T]) upsertBatch(ctx context.Context, entities []*T) (database.BatchUpsertResult, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return database.BatchUpsertResult{}, fmt.Errorf("failed to begin %s batch transaction: %w", w.noun, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, w.table); err != nil {
		return database.BatchUpsertResult{}, err
	}
	existing, err := rosterKeys(ctx, tx, w.table)
	if err != nil {
		return database.BatchUpsertResult{}, err
	}

	type writtenRow struct {
		entity  *T
		id      int64
		at      time.Time
		created bool
	}
	written := make([]writtenRow, 0, len(entities))
	result := database.BatchUpsertResult{}
	for _, entity := range entities {
		if entity == nil {
			return database.BatchUpsertResult{}, errors.New(w.noun + " batch contains a nil " + w.noun)
		}
		now := time.Now()
		key := w.key(entity)
		if id, ok := existing[key]; ok && key != "" {
			updated, err := w.importUpdate(ctx, tx, id, entity, now)
			if err != nil {
				return database.BatchUpsertResult{}, fmt.Errorf("failed to update %s in batch: %w", w.noun, err)
			}
			if n, err := updated.RowsAffected(); err != nil {
				return database.BatchUpsertResult{}, fmt.Errorf("failed to update %s in batch: %w", w.noun, err)
			} else if n > 0 {
				written = append(written, writtenRow{entity: entity, id: id, at: now})
				result.Updated++
				continue
			}
			// The matched row was deleted concurrently; fall through and insert.
		}
		id, err := w.insert(ctx, tx, entity, now)
		if err != nil {
			return database.BatchUpsertResult{}, fmt.Errorf("failed to create %s in batch: %w", w.noun, err)
		}
		// Later rows with the same key update this row instead of inserting again.
		if key != "" {
			existing[key] = id
		}
		written = append(written, writtenRow{entity: entity, id: id, at: now, created: true})
		result.Created++
	}

	if err := tx.Commit(); err != nil {
		return database.BatchUpsertResult{}, fmt.Errorf("failed to commit %s batch transaction: %w", w.noun, err)
	}
	for _, row := range written {
		fields := w.fields(row.entity)
		*fields.id = row.id
		*fields.updatedAt = row.at
		if row.created {
			*fields.createdAt = row.at
		}
	}
	return result, nil
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

	// Identity edits serialize with import upserts, which snapshot roster keys.
	if err := lockRoster(ctx, tx, w.table); err != nil {
		return nil, err
	}
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
	result, err := w.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`, w.table), id)
	if err != nil {
		return fmt.Errorf("failed to restore %s: %w", w.noun, err)
	}
	return rowsAffectedOrNotFound(result)
}

// lockRoster keeps duplicate checks and roster writes in one serial order.
func lockRoster(ctx context.Context, tx *sql.Tx, table string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "ride-home-router:"+table); err != nil {
		return fmt.Errorf("failed to lock %s for writing: %w", table, err)
	}
	return nil
}

// rosterKeys maps each normalized name+address key in a roster table to its row ID.
func rosterKeys(ctx context.Context, tx *sql.Tx, table string) (map[string]int64, error) {
	var query string
	switch table {
	case "participants":
		query = `SELECT id, name, address FROM participants WHERE deleted_at IS NULL ORDER BY id`
	case "drivers":
		query = `SELECT id, name, address FROM drivers WHERE deleted_at IS NULL ORDER BY id`
	default:
		return nil, fmt.Errorf("invalid roster table %q", table)
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s duplicates: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	keys := make(map[string]int64)
	for rows.Next() {
		var id int64
		var name, address string
		if err := rows.Scan(&id, &name, &address); err != nil {
			return nil, fmt.Errorf("failed to scan %s duplicate: %w", table, err)
		}
		// Pre-existing duplicates resolve to the oldest row.
		if key := models.RosterKey(name, address); key != "" {
			if _, seen := keys[key]; !seen {
				keys[key] = id
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate %s duplicates: %w", table, err)
	}
	return keys, nil
}
