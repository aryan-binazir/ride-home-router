package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"ride-home-router/internal/models"
)

// ParticipantRepository handles participant persistence
type ParticipantRepository interface {
	List(ctx context.Context, search string) ([]models.Participant, error)
	GetByID(ctx context.Context, id int64) (*models.Participant, error)
	GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error)
	Create(ctx context.Context, p *models.Participant) (*models.Participant, error)
	Update(ctx context.Context, p *models.Participant) (*models.Participant, error)
	Delete(ctx context.Context, id int64) error
}

type participantRepository struct {
	db *sql.DB
}

func (r *participantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	query := `SELECT id, name, address, lat, lng, created_at, updated_at FROM participants`
	args := []interface{}{}

	if search != "" {
		query += ` WHERE name LIKE ?`
		args = append(args, "%"+search+"%")
	}

	query += ` ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
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
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return participants, nil
}

func (r *participantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	query := `SELECT id, name, address, lat, lng, created_at, updated_at FROM participants WHERE id = ?`

	var p models.Participant
	err := r.db.QueryRowContext(ctx, query, id).Scan(&p.ID, &p.Name, &p.Address, &p.Lat, &p.Lng, &p.CreatedAt, &p.UpdatedAt)
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

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`SELECT id, name, address, lat, lng, created_at, updated_at FROM participants WHERE id IN (%s) ORDER BY name ASC`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return participants, nil
}

func (r *participantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	query := `INSERT INTO participants (name, address, lat, lng) VALUES (?, ?, ?, ?) RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query, p.Name, p.Address, p.Lat, p.Lng).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create participant: %w", err)
	}

	return p, nil
}

func (r *participantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	query := `UPDATE participants SET name = ?, address = ?, lat = ?, lng = ? WHERE id = ? RETURNING updated_at`

	err := r.db.QueryRowContext(ctx, query, p.Name, p.Address, p.Lat, p.Lng, p.ID).Scan(&p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update participant: %w", err)
	}

	return p, nil
}

func (r *participantRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM participants WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete participant: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}
