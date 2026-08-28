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

type participantRepository struct {
	db *sql.DB
}

const participantColumns = `id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at`

func scanParticipant(scanner interface{ Scan(dest ...any) error }) (models.Participant, error) {
	var p models.Participant
	err := scanner.Scan(&p.ID, &p.Name, &p.Address, &p.AddressName, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (r *participantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+participantColumns+`
		FROM participants
		WHERE $1 = '' OR name ILIKE '%' || $1 || '%'
		ORDER BY name`, search)
	if err != nil {
		return nil, fmt.Errorf("failed to query participants: %w", err)
	}
	return collectParticipants(rows)
}

func (r *participantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	p, err := scanParticipant(r.db.QueryRowContext(ctx, `SELECT `+participantColumns+` FROM participants WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get participant: %w", err)
	}
	return &p, nil
}

func (r *participantRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error) {
	if len(ids) == 0 {
		return []models.Participant{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+participantColumns+` FROM participants WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to query participants by IDs: %w", err)
	}
	return collectParticipants(rows)
}

func collectParticipants(rows *sql.Rows) ([]models.Participant, error) {
	defer func() { _ = rows.Close() }()
	var participants []models.Participant
	for rows.Next() {
		p, err := scanParticipant(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan participant: %w", err)
		}
		participants = append(participants, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating participants: %w", err)
	}
	return participants, nil
}

const insertParticipant = `
	INSERT INTO participants (name, address, address_name, lat, lng, created_at, updated_at)
	VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)
	RETURNING id`

func (r *participantRepository) writes() rosterWriteCore[models.Participant] {
	return rosterWriteCore[models.Participant]{
		db:     r.db,
		noun:   "participant",
		table:  "participants",
		labels: participantLabels,
		key: func(p *models.Participant) string {
			return models.RosterKey(p.Name, p.Address)
		},
		insert: func(ctx context.Context, tx *sql.Tx, p *models.Participant, now time.Time) (int64, error) {
			var id int64
			err := tx.QueryRowContext(ctx, insertParticipant,
				p.Name, p.Address, p.AddressName, p.Lat, p.Lng, now, now,
			).Scan(&id)
			return id, err
		},
		createdFields: func(p *models.Participant) rosterCreatedFields {
			return rosterCreatedFields{id: &p.ID, createdAt: &p.CreatedAt, updatedAt: &p.UpdatedAt}
		},
	}
}

func (r *participantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.CreateWithLabels(ctx, p, nil)
}

func (r *participantRepository) CreateBatch(ctx context.Context, participants []*models.Participant, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	return r.writes().createBatch(ctx, participants, allowExistingDuplicate)
}

func (r *participantRepository) CreateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	return r.writes().createWithLabels(ctx, p, labelIDs)
}

func (r *participantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.update(ctx, p, nil, false)
}

func (r *participantRepository) UpdateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	return r.update(ctx, p, labelIDs, true)
}

func (r *participantRepository) update(ctx context.Context, p *models.Participant, labelIDs []int64, replaceLabels bool) (*models.Participant, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin participant transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	p.UpdatedAt = time.Now()
	result, err := tx.ExecContext(ctx, `
		UPDATE participants
		SET name = $1, address = $2, address_name = NULLIF($3, ''), lat = $4, lng = $5, updated_at = $6
		WHERE id = $7`,
		p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.UpdatedAt, p.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update participant: %w", err)
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	if replaceLabels {
		if err := replaceLabelMemberships(ctx, tx, participantLabels, p.ID, labelIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant transaction: %w", err)
	}
	return p, nil
}

func (r *participantRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM participants WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete participant: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
