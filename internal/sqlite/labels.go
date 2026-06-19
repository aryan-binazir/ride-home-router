package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strings"
	"time"
)

type labelRepository struct {
	store *Store
}

func (r *labelRepository) List(ctx context.Context) ([]models.Label, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT l.id, l.name,
		       COALESCE(pl.participant_count, 0),
		       COALESCE(dl.driver_count, 0),
		       l.created_at, l.updated_at
		FROM labels l
		LEFT JOIN (
			SELECT label_id, COUNT(*) AS participant_count
			FROM participant_labels
			GROUP BY label_id
		) pl ON pl.label_id = l.id
		LEFT JOIN (
			SELECT label_id, COUNT(*) AS driver_count
			FROM driver_labels
			GROUP BY label_id
		) dl ON dl.label_id = l.id
		ORDER BY l.name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query labels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var labels []models.Label
	for rows.Next() {
		var label models.Label
		if err := rows.Scan(&label.ID, &label.Name, &label.ParticipantCount, &label.DriverCount, &label.CreatedAt, &label.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan label: %w", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating labels: %w", err)
	}

	return labels, nil
}

func (r *labelRepository) GetByID(ctx context.Context, id int64) (*models.Label, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var label models.Label
	err := r.store.db.QueryRowContext(ctx, `
		SELECT l.id, l.name,
		       COALESCE((SELECT COUNT(*) FROM participant_labels WHERE label_id = l.id), 0),
		       COALESCE((SELECT COUNT(*) FROM driver_labels WHERE label_id = l.id), 0),
		       l.created_at, l.updated_at
		FROM labels l
		WHERE l.id = ?
	`, id).Scan(&label.ID, &label.Name, &label.ParticipantCount, &label.DriverCount, &label.CreatedAt, &label.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get label: %w", err)
	}

	return &label, nil
}

func (r *labelRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Label, error) {
	if len(ids) == 0 {
		return []models.Label{}, nil
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := r.store.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.name,
		       COALESCE(pl.participant_count, 0),
		       COALESCE(dl.driver_count, 0),
		       l.created_at, l.updated_at
		FROM labels l
		LEFT JOIN (
			SELECT label_id, COUNT(*) AS participant_count
			FROM participant_labels
			GROUP BY label_id
		) pl ON pl.label_id = l.id
		LEFT JOIN (
			SELECT label_id, COUNT(*) AS driver_count
			FROM driver_labels
			GROUP BY label_id
		) dl ON dl.label_id = l.id
		WHERE l.id IN (%s)
		ORDER BY l.name
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query labels by IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var labels []models.Label
	for rows.Next() {
		var label models.Label
		if err := rows.Scan(&label.ID, &label.Name, &label.ParticipantCount, &label.DriverCount, &label.CreatedAt, &label.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan label: %w", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating labels by IDs: %w", err)
	}

	return labels, nil
}

func (r *labelRepository) Create(ctx context.Context, label *models.Label) (*models.Label, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	label.Name = strings.TrimSpace(label.Name)
	now := time.Now()
	label.CreatedAt = now
	label.UpdatedAt = now

	result, err := r.store.db.ExecContext(ctx, `
		INSERT INTO labels (name, created_at, updated_at)
		VALUES (?, ?, ?)
	`, label.Name, label.CreatedAt, label.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create label: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}
	label.ID = id

	return label, nil
}

func (r *labelRepository) Update(ctx context.Context, label *models.Label) (*models.Label, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	label.Name = strings.TrimSpace(label.Name)
	label.UpdatedAt = time.Now()

	result, err := r.store.db.ExecContext(ctx, `
		UPDATE labels
		SET name = ?, updated_at = ?
		WHERE id = ?
	`, label.Name, label.UpdatedAt, label.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update label: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, database.ErrNotFound
	}

	return label, nil
}

func (r *labelRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	result, err := r.store.db.ExecContext(ctx, `DELETE FROM labels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete label: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return database.ErrNotFound
	}

	return nil
}

func (r *labelRepository) ListLabelsForParticipant(ctx context.Context, participantID int64) ([]models.Label, error) {
	return r.listLabelsForOwner(ctx, "participant_labels", "participant_id", participantID)
}

func (r *labelRepository) ListLabelsForDriver(ctx context.Context, driverID int64) ([]models.Label, error) {
	return r.listLabelsForOwner(ctx, "driver_labels", "driver_id", driverID)
}

func (r *labelRepository) SetLabelsForParticipant(ctx context.Context, participantID int64, labelIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.replaceMemberships(ctx, "participant_labels", "participant_id", participantID, labelIDs)
}

func (r *labelRepository) SetLabelsForDriver(ctx context.Context, driverID int64, labelIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.replaceMemberships(ctx, "driver_labels", "driver_id", driverID, labelIDs)
}

func (r *labelRepository) AddLabelToParticipants(ctx context.Context, labelID int64, participantIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.addMemberships(ctx, "participant_labels", "participant_id", labelID, participantIDs)
}

func (r *labelRepository) RemoveLabelFromParticipants(ctx context.Context, labelID int64, participantIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.removeMemberships(ctx, "participant_labels", "participant_id", labelID, participantIDs)
}

func (r *labelRepository) AddLabelToDrivers(ctx context.Context, labelID int64, driverIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.addMemberships(ctx, "driver_labels", "driver_id", labelID, driverIDs)
}

func (r *labelRepository) RemoveLabelFromDrivers(ctx context.Context, labelID int64, driverIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.removeMemberships(ctx, "driver_labels", "driver_id", labelID, driverIDs)
}

func (r *labelRepository) ListLabelIDsForParticipants(ctx context.Context) (map[int64][]int64, error) {
	return r.listLabelIDsForOwners(ctx, "participant_labels", "participant_id")
}

func (r *labelRepository) ListLabelIDsForDrivers(ctx context.Context) (map[int64][]int64, error) {
	return r.listLabelIDsForOwners(ctx, "driver_labels", "driver_id")
}

func (r *labelRepository) listLabelsForOwner(ctx context.Context, tableName, ownerColumn string, ownerID int64) ([]models.Label, error) {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return nil, err
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.name, l.created_at, l.updated_at
		FROM labels l
		INNER JOIN %s membership ON membership.label_id = l.id
		WHERE membership.%s = ?
		ORDER BY l.name
	`, tableName, ownerColumn), ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query owner labels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var labels []models.Label
	for rows.Next() {
		var label models.Label
		if err := rows.Scan(&label.ID, &label.Name, &label.CreatedAt, &label.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan owner label: %w", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating owner labels: %w", err)
	}

	return labels, nil
}

func insertLabelMemberships(ctx context.Context, tx *sql.Tx, tableName, ownerColumn string, ownerID int64, labelIDs []int64) error {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}

	seen := make(map[int64]struct{}, len(labelIDs))
	for _, labelID := range labelIDs {
		if labelID <= 0 {
			return fmt.Errorf("invalid label ID: %d", labelID)
		}
		if _, exists := seen[labelID]; exists {
			continue
		}
		seen[labelID] = struct{}{}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (label_id, %s)
			VALUES (?, ?)
		`, tableName, ownerColumn), labelID, ownerID); err != nil {
			return fmt.Errorf("failed to insert label membership: %w", err)
		}
	}
	return nil
}

func (r *labelRepository) replaceMemberships(ctx context.Context, tableName, ownerColumn string, ownerID int64, labelIDs []int64) error {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin label membership transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s = ?`, tableName, ownerColumn), ownerID); err != nil {
		return fmt.Errorf("failed to clear label memberships: %w", err)
	}

	if err := insertLabelMemberships(ctx, tx, tableName, ownerColumn, ownerID, labelIDs); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit label membership transaction: %w", err)
	}

	return nil
}

func (r *labelRepository) addMemberships(ctx context.Context, tableName, ownerColumn string, labelID int64, ownerIDs []int64) error {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}
	if labelID <= 0 {
		return nil
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin add label memberships transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	seen := make(map[int64]struct{}, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		if ownerID <= 0 {
			continue
		}
		if _, exists := seen[ownerID]; exists {
			continue
		}
		seen[ownerID] = struct{}{}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT OR IGNORE INTO %s (label_id, %s)
			VALUES (?, ?)
		`, tableName, ownerColumn), labelID, ownerID); err != nil {
			return fmt.Errorf("failed to add label membership: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit add label memberships transaction: %w", err)
	}

	return nil
}

func (r *labelRepository) removeMemberships(ctx context.Context, tableName, ownerColumn string, labelID int64, ownerIDs []int64) error {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}
	if labelID <= 0 || len(ownerIDs) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(ownerIDs))
	args := make([]any, 0, len(ownerIDs)+1)
	args = append(args, labelID)
	seen := make(map[int64]struct{}, len(ownerIDs))
	for _, ownerID := range ownerIDs {
		if ownerID <= 0 {
			continue
		}
		if _, exists := seen[ownerID]; exists {
			continue
		}
		seen[ownerID] = struct{}{}
		placeholders = append(placeholders, "?")
		args = append(args, ownerID)
	}
	if len(placeholders) == 0 {
		return nil
	}

	_, err := r.store.db.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM %s
		WHERE label_id = ? AND %s IN (%s)
	`, tableName, ownerColumn, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return fmt.Errorf("failed to remove label memberships: %w", err)
	}

	return nil
}

func (r *labelRepository) listLabelIDsForOwners(ctx context.Context, tableName, ownerColumn string) (map[int64][]int64, error) {
	if err := validateLabelMembershipTable(tableName, ownerColumn); err != nil {
		return nil, err
	}

	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s, label_id
		FROM %s
		ORDER BY %s, label_id
	`, ownerColumn, tableName, ownerColumn))
	if err != nil {
		return nil, fmt.Errorf("failed to query label IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]int64)
	for rows.Next() {
		var ownerID, labelID int64
		if err := rows.Scan(&ownerID, &labelID); err != nil {
			return nil, fmt.Errorf("failed to scan label ID: %w", err)
		}
		result[ownerID] = append(result[ownerID], labelID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating label IDs: %w", err)
	}

	return result, nil
}

func validateLabelMembershipTable(tableName, ownerColumn string) error {
	switch {
	case tableName == "participant_labels" && ownerColumn == "participant_id":
		return nil
	case tableName == "driver_labels" && ownerColumn == "driver_id":
		return nil
	default:
		return fmt.Errorf("invalid label membership target")
	}
}
