package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"strings"
	"time"
)

type participantRepository struct {
	store *Store
}

func (r *participantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var rows *sql.Rows
	var err error

	if search != "" {
		query := `SELECT id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at
		          FROM participants
		          WHERE name LIKE ?
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query, "%"+search+"%")
	} else {
		query := `SELECT id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at
		          FROM participants
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query participants: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var participants []models.Participant
	for rows.Next() {
		var p models.Participant
		if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.AddressName, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan participant: %w", err)
		}
		participants = append(participants, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating participants: %w", err)
	}

	return participants, nil
}

func (r *participantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	query := `SELECT id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at
	          FROM participants WHERE id = ?`

	var p models.Participant
	err := r.store.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Name, &p.Address, &p.AddressName, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt,
	)

	if err == sql.ErrNoRows {
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

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	// Build placeholders
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf( //nolint:gosec // G201: placeholder list is "?" only; values are bound args.
		`SELECT id, name, address, COALESCE(address_name, ''), lat, lng, created_at, updated_at
		 FROM participants WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query participants by IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var participants []models.Participant
	for rows.Next() {
		var p models.Participant
		if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.AddressName, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan participant: %w", err)
		}
		participants = append(participants, p)
	}

	return participants, rows.Err()
}

func (r *participantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	query := `INSERT INTO participants (name, address, address_name, lat, lng, created_at, updated_at)
	          VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)`

	result, err := r.store.db.ExecContext(ctx, query,
		p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	p.ID = id

	return p, nil
}

func (r *participantRepository) CreateBatch(ctx context.Context, participants []*models.Participant, allowExistingDuplicate []bool) (database.BatchCreateResult, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to begin participant batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := participantDuplicateKeys(ctx, tx)
	if err != nil {
		return database.BatchCreateResult{}, err
	}
	batchResult := database.BatchCreateResult{}

	type createdParticipant struct {
		participant *models.Participant
		id          int64
		createdAt   time.Time
	}
	created := make([]createdParticipant, 0, len(participants))
	for i, participant := range participants {
		if participant == nil {
			return database.BatchCreateResult{}, errors.New("participant batch contains a nil participant")
		}
		key := importer.DuplicateKey(participant.Name, participant.Address)
		_, duplicate := existing[key]
		allowDuplicate := i < len(allowExistingDuplicate) && allowExistingDuplicate[i]
		if key != "" && duplicate && !allowDuplicate {
			batchResult.SkippedDuplicate++
			continue
		}
		now := time.Now()
		result, err := tx.ExecContext(ctx, `
			INSERT INTO participants (name, address, address_name, lat, lng, created_at, updated_at)
			VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)
		`, participant.Name, participant.Address, participant.AddressName, participant.Lat, participant.Lng, now, now)
		if err != nil {
			return database.BatchCreateResult{}, fmt.Errorf("failed to create participant in batch: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return database.BatchCreateResult{}, fmt.Errorf("failed to get participant batch insert id: %w", err)
		}
		created = append(created, createdParticipant{participant: participant, id: id, createdAt: now})
		batchResult.Created++
	}

	if err := tx.Commit(); err != nil {
		return database.BatchCreateResult{}, fmt.Errorf("failed to commit participant batch transaction: %w", err)
	}
	for _, item := range created {
		item.participant.ID = item.id
		item.participant.CreatedAt = item.createdAt
		item.participant.UpdatedAt = item.createdAt
	}
	return batchResult, nil
}

func participantDuplicateKeys(ctx context.Context, tx *sql.Tx) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name, address FROM participants`)
	if err != nil {
		return nil, fmt.Errorf("failed to query participant duplicates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make(map[string]struct{})
	for rows.Next() {
		var name, address string
		if err := rows.Scan(&name, &address); err != nil {
			return nil, fmt.Errorf("failed to scan participant duplicate: %w", err)
		}
		if key := importer.DuplicateKey(name, address); key != "" {
			keys[key] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate participant duplicates: %w", err)
	}
	return keys, nil
}

func (r *participantRepository) CreateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin participant label transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	result, err := tx.ExecContext(ctx, `
		INSERT INTO participants (name, address, address_name, lat, lng, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?)
	`, p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	p.ID = id

	if err := insertLabelMemberships(ctx, tx, "participant_labels", "participant_id", p.ID, labelIDs); err != nil {
		return nil, fmt.Errorf("failed to insert participant label memberships: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant label transaction: %w", err)
	}

	return p, nil
}

func (r *participantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	p.UpdatedAt = time.Now()

	query := `UPDATE participants
	          SET name = ?, address = ?, address_name = NULLIF(?, ''), lat = ?, lng = ?, updated_at = ?
	          WHERE id = ?`

	result, err := r.store.db.ExecContext(ctx, query,
		p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.UpdatedAt, p.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return nil, database.ErrNotFound
	}

	return p, nil
}

func (r *participantRepository) UpdateWithLabels(ctx context.Context, p *models.Participant, labelIDs []int64) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin participant label transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	p.UpdatedAt = time.Now()

	result, err := tx.ExecContext(ctx, `
		UPDATE participants
		SET name = ?, address = ?, address_name = NULLIF(?, ''), lat = ?, lng = ?, updated_at = ?
		WHERE id = ?
	`, p.Name, p.Address, p.AddressName, p.Lat, p.Lng, p.UpdatedAt, p.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return nil, database.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM participant_labels WHERE participant_id = ?`, p.ID); err != nil {
		return nil, fmt.Errorf("failed to clear participant label memberships: %w", err)
	}

	if err := insertLabelMemberships(ctx, tx, "participant_labels", "participant_id", p.ID, labelIDs); err != nil {
		return nil, fmt.Errorf("failed to insert participant label memberships: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant label transaction: %w", err)
	}

	return p, nil
}

func (r *participantRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	result, err := r.store.db.ExecContext(ctx, `DELETE FROM participants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}

	return nil
}
