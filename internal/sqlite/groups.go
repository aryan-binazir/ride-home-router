package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type groupRepository struct {
	store *Store
}

func (r *groupRepository) List(ctx context.Context) ([]models.Group, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT g.id, g.name,
		       COALESCE(pg.participant_count, 0),
		       COALESCE(dg.driver_count, 0),
		       g.created_at, g.updated_at
		FROM groups g
		LEFT JOIN (
			SELECT group_id, COUNT(*) AS participant_count
			FROM participant_groups
			GROUP BY group_id
		) pg ON pg.group_id = g.id
		LEFT JOIN (
			SELECT group_id, COUNT(*) AS driver_count
			FROM driver_groups
			GROUP BY group_id
		) dg ON dg.group_id = g.id
		ORDER BY g.name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query groups: %w", err)
	}
	defer rows.Close()

	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.ParticipantCount, &g.DriverCount, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan group: %w", err)
		}
		groups = append(groups, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating groups: %w", err)
	}

	return groups, nil
}

func (r *groupRepository) GetByID(ctx context.Context, id int64) (*models.Group, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var g models.Group
	err := r.store.db.QueryRowContext(ctx, `
		SELECT g.id, g.name,
		       COALESCE((SELECT COUNT(*) FROM participant_groups WHERE group_id = g.id), 0),
		       COALESCE((SELECT COUNT(*) FROM driver_groups WHERE group_id = g.id), 0),
		       g.created_at, g.updated_at
		FROM groups g
		WHERE g.id = ?
	`, id).Scan(&g.ID, &g.Name, &g.ParticipantCount, &g.DriverCount, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get group: %w", err)
	}

	return &g, nil
}

func (r *groupRepository) Create(ctx context.Context, g *models.Group) (*models.Group, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	now := time.Now()
	g.CreatedAt = now
	g.UpdatedAt = now

	result, err := r.store.db.ExecContext(ctx, `
		INSERT INTO groups (name, created_at, updated_at)
		VALUES (?, ?, ?)
	`, g.Name, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	g.ID = id
	return g, nil
}

func (r *groupRepository) Update(ctx context.Context, g *models.Group) (*models.Group, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	g.UpdatedAt = time.Now()

	result, err := r.store.db.ExecContext(ctx, `
		UPDATE groups
		SET name = ?, updated_at = ?
		WHERE id = ?
	`, g.Name, g.UpdatedAt, g.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update group: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, database.ErrNotFound
	}

	return g, nil
}

func (r *groupRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	result, err := r.store.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
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

func (r *groupRepository) ListGroupsForParticipant(ctx context.Context, participantID int64) ([]models.Group, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.created_at, g.updated_at
		FROM groups g
		INNER JOIN participant_groups pg ON pg.group_id = g.id
		WHERE pg.participant_id = ?
		ORDER BY g.name
	`, participantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query participant groups: %w", err)
	}
	defer rows.Close()

	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan participant group: %w", err)
		}
		groups = append(groups, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating participant groups: %w", err)
	}

	return groups, nil
}

func (r *groupRepository) ListGroupsForDriver(ctx context.Context, driverID int64) ([]models.Group, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.created_at, g.updated_at
		FROM groups g
		INNER JOIN driver_groups dg ON dg.group_id = g.id
		WHERE dg.driver_id = ?
		ORDER BY g.name
	`, driverID)
	if err != nil {
		return nil, fmt.Errorf("failed to query driver groups: %w", err)
	}
	defer rows.Close()

	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan driver group: %w", err)
		}
		groups = append(groups, g)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating driver groups: %w", err)
	}

	return groups, nil
}

func (r *groupRepository) SetGroupsForParticipant(ctx context.Context, participantID int64, groupIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.replaceMemberships(ctx, "participant_groups", "participant_id", participantID, groupIDs)
}

func (r *groupRepository) SetGroupsForDriver(ctx context.Context, driverID int64, groupIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.replaceMemberships(ctx, "driver_groups", "driver_id", driverID, groupIDs)
}

func (r *groupRepository) AddGroupToParticipants(ctx context.Context, groupID int64, participantIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.addMemberships(ctx, "participant_groups", "participant_id", groupID, participantIDs)
}

func (r *groupRepository) RemoveGroupFromParticipants(ctx context.Context, groupID int64, participantIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.removeMemberships(ctx, "participant_groups", "participant_id", groupID, participantIDs)
}

func (r *groupRepository) AddGroupToDrivers(ctx context.Context, groupID int64, driverIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.addMemberships(ctx, "driver_groups", "driver_id", groupID, driverIDs)
}

func (r *groupRepository) RemoveGroupFromDrivers(ctx context.Context, groupID int64, driverIDs []int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	return r.removeMemberships(ctx, "driver_groups", "driver_id", groupID, driverIDs)
}

func (r *groupRepository) ListGroupIDsForParticipants(ctx context.Context) (map[int64][]int64, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT participant_id, group_id
		FROM participant_groups
		ORDER BY participant_id, group_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query participant group IDs: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]int64)
	for rows.Next() {
		var participantID, groupID int64
		if err := rows.Scan(&participantID, &groupID); err != nil {
			return nil, fmt.Errorf("failed to scan participant group ID: %w", err)
		}
		result[participantID] = append(result[participantID], groupID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating participant group IDs: %w", err)
	}

	return result, nil
}

func (r *groupRepository) ListGroupIDsForDrivers(ctx context.Context) (map[int64][]int64, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT driver_id, group_id
		FROM driver_groups
		ORDER BY driver_id, group_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query driver group IDs: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]int64)
	for rows.Next() {
		var driverID, groupID int64
		if err := rows.Scan(&driverID, &groupID); err != nil {
			return nil, fmt.Errorf("failed to scan driver group ID: %w", err)
		}
		result[driverID] = append(result[driverID], groupID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating driver group IDs: %w", err)
	}

	return result, nil
}

func (r *groupRepository) replaceMemberships(ctx context.Context, tableName, ownerColumn string, ownerID int64, groupIDs []int64) error {
	if err := validateMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin membership transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s = ?`, tableName, ownerColumn), ownerID); err != nil {
		return fmt.Errorf("failed to clear memberships: %w", err)
	}

	seen := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}

		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (group_id, %s)
			VALUES (?, ?)
		`, tableName, ownerColumn), groupID, ownerID); err != nil {
			return fmt.Errorf("failed to insert membership: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit membership transaction: %w", err)
	}

	return nil
}

func (r *groupRepository) addMemberships(ctx context.Context, tableName, ownerColumn string, groupID int64, ownerIDs []int64) error {
	if err := validateMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin add memberships transaction: %w", err)
	}
	defer tx.Rollback()

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
			INSERT OR IGNORE INTO %s (group_id, %s)
			VALUES (?, ?)
		`, tableName, ownerColumn), groupID, ownerID); err != nil {
			return fmt.Errorf("failed to add membership: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit add memberships transaction: %w", err)
	}

	return nil
}

func (r *groupRepository) removeMemberships(ctx context.Context, tableName, ownerColumn string, groupID int64, ownerIDs []int64) error {
	if err := validateMembershipTable(tableName, ownerColumn); err != nil {
		return err
	}

	if len(ownerIDs) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(ownerIDs))
	args := make([]any, 0, len(ownerIDs)+1)
	args = append(args, groupID)
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
		WHERE group_id = ? AND %s IN (%s)
	`, tableName, ownerColumn, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return fmt.Errorf("failed to remove memberships: %w", err)
	}

	return nil
}

func validateMembershipTable(tableName, ownerColumn string) error {
	switch {
	case tableName == "participant_groups" && ownerColumn == "participant_id":
		return nil
	case tableName == "driver_groups" && ownerColumn == "driver_id":
		return nil
	default:
		return fmt.Errorf("invalid membership target")
	}
}
