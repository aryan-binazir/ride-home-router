package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ride-home-router/internal/models"
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
		query := `SELECT id, name, address, lat, lng, created_at, updated_at
		          FROM participants
		          WHERE name LIKE ?
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query, "%"+search+"%")
	} else {
		query := `SELECT id, name, address, lat, lng, created_at, updated_at
		          FROM participants
		          ORDER BY name`
		rows, err = r.store.db.QueryContext(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query participants: %w", err)
	}
	defer rows.Close()

	var participants []models.Participant
	for rows.Next() {
		var p models.Participant
		if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

	query := `SELECT id, name, address, lat, lng, created_at, updated_at
	          FROM participants WHERE id = ?`

	var p models.Participant
	err := r.store.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Name, &p.Address, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
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
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, name, address, lat, lng, created_at, updated_at
		 FROM participants WHERE id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := r.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query participants by IDs: %w", err)
	}
	defer rows.Close()

	var participants []models.Participant
	for rows.Next() {
		var p models.Participant
		if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

	query := `INSERT INTO participants (name, address, lat, lng, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?)`

	result, err := r.store.db.ExecContext(ctx, query,
		p.Name, p.Address, p.Lat, p.Lng, p.CreatedAt, p.UpdatedAt,
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

func (r *participantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	p.UpdatedAt = time.Now()

	query := `UPDATE participants
	          SET name = ?, address = ?, lat = ?, lng = ?, updated_at = ?
	          WHERE id = ?`

	result, err := r.store.db.ExecContext(ctx, query,
		p.Name, p.Address, p.Lat, p.Lng, p.UpdatedAt, p.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("participant not found")
	}

	return p, nil
}

func (r *participantRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	query := `DELETE FROM participants WHERE id = ?`
	result, err := r.store.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("participant not found")
	}

	return nil
}
