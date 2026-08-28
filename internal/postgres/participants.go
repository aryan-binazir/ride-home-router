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

func (r *participantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.CreateWithLabels(ctx, p, nil)
}

func (r *participantRepository) CreateBatch(ctx context.Context, participants []*models.Participant, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to begin participant batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, "participants"); err != nil {
		return database.BatchCreateResult{}, err
	}
	existing, err := rosterKeys(ctx, tx, "participants")
	if err != nil {
		return database.BatchCreateResult{}, err
	}
	batchResult := database.BatchCreateResult{}

	type created struct {
		participant *models.Participant
		id          int64
		createdAt   time.Time
	}
	var createdRows []created
	for i, participant := range participants {
		if participant == nil {
			return database.BatchCreateResult{}, errors.New("participant batch contains a nil participant")
		}
		key := models.RosterKey(participant.Name, participant.Address)
		_, duplicate := existing[key]
		allowDuplicate := i < len(allowExistingDuplicate) && allowExistingDuplicate[i]
		if key != "" && duplicate && !allowDuplicate {
			batchResult.SkippedDuplicate++
			continue
		}
		now := time.Now()
		var id int64
		if err := tx.QueryRowContext(ctx, insertParticipant,
			participant.Name, participant.Address, participant.AddressName, participant.Lat, participant.Lng, now, now,
		).Scan(&id); err != nil {
			return database.BatchCreateResult{}, fmt.Errorf("failed to create participant in batch: %w", err)
		}
		createdRows = append(createdRows, created{participant: participant, id: id, createdAt: now})
		batchResult.Created++
	}

	if err := tx.Commit(); err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to commit participant batch transaction: %w", err)
	}
	for _, item := range createdRows {
		item.participant.ID = item.id
		item.participant.CreatedAt = item.createdAt
		item.participant.UpdatedAt = item.createdAt
	}
	return batchResult, nil
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
		query = `SELECT name, address FROM participants`
	case "drivers":
		query = `SELECT name, address FROM drivers`
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

func (r *participantRepository) CreateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin participant transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockRoster(ctx, tx, "participants"); err != nil {
		return nil, err
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	if err := tx.QueryRowContext(ctx, insertParticipant,
		p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.CreatedAt, p.UpdatedAt,
	).Scan(&p.ID); err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}
	if err := insertLabelMemberships(ctx, tx, participantLabels, p.ID, labelIDs); err != nil {
		return nil, fmt.Errorf("failed to insert participant label memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant transaction: %w", err)
	}
	return p, nil
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
