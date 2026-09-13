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

const participantColumns = `id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at, deleted_at`

func scanParticipant(scanner interface{ Scan(dest ...any) error }) (models.Participant, error) {
	var p models.Participant
	err := scanner.Scan(&p.ID, &p.Name, &p.Address, &p.AddressName, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt)
	return p, err
}

func (r *participantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+participantColumns+`
		FROM participants
		WHERE deleted_at IS NULL AND ($1 = '' OR name ILIKE '%' || $1 || '%')
		ORDER BY name`, search)
	if err != nil {
		return nil, fmt.Errorf("failed to query participants: %w", err)
	}
	return collectParticipants(rows)
}

func (r *participantRepository) ListDeleted(ctx context.Context) ([]models.Participant, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+participantColumns+`
		FROM participants
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query deleted participants: %w", err)
	}
	return collectParticipants(rows)
}

func (r *participantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	p, err := scanParticipant(r.db.QueryRowContext(ctx, `SELECT `+participantColumns+` FROM participants WHERE id = $1 AND deleted_at IS NULL`, id))
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
	rows, err := r.db.QueryContext(ctx, `SELECT `+participantColumns+` FROM participants WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
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
		updateRow: func(ctx context.Context, tx *sql.Tx, p *models.Participant, now time.Time) (sql.Result, error) {
			return tx.ExecContext(ctx, `
				UPDATE participants
				SET name = $1, address = $2, address_name = NULLIF($3, ''), lat = $4, lng = $5, updated_at = $6
				WHERE id = $7 AND deleted_at IS NULL`,
				p.Name, p.Address, p.AddressName, p.Lat, p.Lng, now, p.ID)
		},
		importUpdate: func(ctx context.Context, tx *sql.Tx, id int64, p *models.Participant, now time.Time) (sql.Result, error) {
			return tx.ExecContext(ctx, `
				UPDATE participants
				SET address_name = COALESCE(NULLIF($1, ''), address_name), updated_at = $2
				WHERE id = $3 AND deleted_at IS NULL`,
				p.AddressName, now, id)
		},
		fields: func(p *models.Participant) rosterFields {
			return rosterFields{id: &p.ID, createdAt: &p.CreatedAt, updatedAt: &p.UpdatedAt}
		},
	}
}

func (r *participantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.CreateWithLabels(ctx, p, nil)
}

func (r *participantRepository) UpsertBatch(ctx context.Context, participants []*models.Participant) (database.BatchUpsertResult, error) {
	return r.writes().upsertBatch(ctx, participants)
}

func (r *participantRepository) CreateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	return r.writes().createWithLabels(ctx, p, labelIDs)
}

func (r *participantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.writes().updateWithLabels(ctx, p, nil, false)
}

func (r *participantRepository) UpdateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	return r.writes().updateWithLabels(ctx, p, labelIDs, true)
}

func (r *participantRepository) Delete(ctx context.Context, id int64) error {
	return r.writes().delete(ctx, id)
}

func (r *participantRepository) Restore(ctx context.Context, id int64) error {
	return r.writes().restore(ctx, id)
}
