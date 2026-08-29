package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strings"
	"time"
)

type labelRepository struct {
	db *sql.DB
}

// membershipTable limits interpolated SQL names to two fixed values.
type membershipTable struct {
	table, ownerColumn, ownerTable string
}

var (
	participantLabels = membershipTable{table: "participant_labels", ownerColumn: "participant_id", ownerTable: "participants"}
	driverLabels      = membershipTable{table: "driver_labels", ownerColumn: "driver_id", ownerTable: "drivers"}
)

const labelWithCounts = `
	SELECT l.id, l.name,
	       COALESCE(pl.participant_count, 0),
	       COALESCE(dl.driver_count, 0),
	       l.created_at, l.updated_at
	FROM labels l
	LEFT JOIN (
		SELECT membership.label_id, COUNT(*) AS participant_count
		FROM participant_labels membership
		INNER JOIN participants participant ON participant.id = membership.participant_id
		WHERE participant.deleted_at IS NULL
		GROUP BY membership.label_id
	) pl ON pl.label_id = l.id
	LEFT JOIN (
		SELECT membership.label_id, COUNT(*) AS driver_count
		FROM driver_labels membership
		INNER JOIN drivers driver ON driver.id = membership.driver_id
		WHERE driver.deleted_at IS NULL
		GROUP BY membership.label_id
	) dl ON dl.label_id = l.id`

func (r *labelRepository) List(ctx context.Context) ([]models.Label, error) {
	rows, err := r.db.QueryContext(ctx, labelWithCounts+` ORDER BY l.name`)
	if err != nil {
		return nil, fmt.Errorf("failed to query labels: %w", err)
	}
	return collectLabels(rows, true)
}

func (r *labelRepository) GetByID(ctx context.Context, id int64) (*models.Label, error) {
	var label models.Label
	err := r.db.QueryRowContext(ctx, labelWithCounts+` WHERE l.id = $1`, id).
		Scan(&label.ID, &label.Name, &label.ParticipantCount, &label.DriverCount, &label.CreatedAt, &label.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
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
	rows, err := r.db.QueryContext(ctx, labelWithCounts+` WHERE l.id = ANY($1) ORDER BY l.name`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to query labels by IDs: %w", err)
	}
	return collectLabels(rows, true)
}

func collectLabels(rows *sql.Rows, withCounts bool) ([]models.Label, error) {
	defer func() { _ = rows.Close() }()
	var labels []models.Label
	for rows.Next() {
		var label models.Label
		var err error
		if withCounts {
			err = rows.Scan(&label.ID, &label.Name, &label.ParticipantCount, &label.DriverCount, &label.CreatedAt, &label.UpdatedAt)
		} else {
			err = rows.Scan(&label.ID, &label.Name, &label.CreatedAt, &label.UpdatedAt)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to scan label: %w", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating labels: %w", err)
	}
	return labels, nil
}

func (r *labelRepository) Create(ctx context.Context, label *models.Label) (*models.Label, error) {
	label.Name = strings.TrimSpace(label.Name)
	now := time.Now()
	label.CreatedAt = now
	label.UpdatedAt = now
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO labels (name, created_at, updated_at) VALUES ($1, $2, $3) RETURNING id`,
		label.Name, label.CreatedAt, label.UpdatedAt).Scan(&label.ID); err != nil {
		return nil, fmt.Errorf("failed to create label: %w", mapUniqueViolation(err))
	}
	return label, nil
}

func (r *labelRepository) Update(ctx context.Context, label *models.Label) (*models.Label, error) {
	label.Name = strings.TrimSpace(label.Name)
	label.UpdatedAt = time.Now()
	result, err := r.db.ExecContext(ctx, `UPDATE labels SET name = $1, updated_at = $2 WHERE id = $3`,
		label.Name, label.UpdatedAt, label.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update label: %w", mapUniqueViolation(err))
	}
	if err := rowsAffectedOrNotFound(result); err != nil {
		return nil, err
	}
	return label, nil
}

func (r *labelRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM labels WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete label: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}

func (r *labelRepository) ListLabelsForParticipant(ctx context.Context, participantID int64) ([]models.Label, error) {
	return r.listLabelsForOwner(ctx, participantLabels, participantID)
}

func (r *labelRepository) ListLabelsForDriver(ctx context.Context, driverID int64) ([]models.Label, error) {
	return r.listLabelsForOwner(ctx, driverLabels, driverID)
}

func (r *labelRepository) SetLabelsForParticipant(ctx context.Context, participantID int64, labelIDs []int64) error {
	return r.setMemberships(ctx, participantLabels, participantID, labelIDs)
}

func (r *labelRepository) SetLabelsForDriver(ctx context.Context, driverID int64, labelIDs []int64) error {
	return r.setMemberships(ctx, driverLabels, driverID, labelIDs)
}

func (r *labelRepository) AddLabelToParticipants(ctx context.Context, labelID int64, participantIDs []int64) error {
	return r.addMemberships(ctx, participantLabels, labelID, participantIDs)
}

func (r *labelRepository) RemoveLabelFromParticipants(ctx context.Context, labelID int64, participantIDs []int64) error {
	return r.removeMemberships(ctx, participantLabels, labelID, participantIDs)
}

func (r *labelRepository) AddLabelToDrivers(ctx context.Context, labelID int64, driverIDs []int64) error {
	return r.addMemberships(ctx, driverLabels, labelID, driverIDs)
}

func (r *labelRepository) RemoveLabelFromDrivers(ctx context.Context, labelID int64, driverIDs []int64) error {
	return r.removeMemberships(ctx, driverLabels, labelID, driverIDs)
}

func (r *labelRepository) ListLabelIDsForParticipants(ctx context.Context) (map[int64][]int64, error) {
	return r.listLabelIDsForOwners(ctx, participantLabels)
}

func (r *labelRepository) ListLabelIDsForDrivers(ctx context.Context) (map[int64][]int64, error) {
	return r.listLabelIDsForOwners(ctx, driverLabels)
}

func (r *labelRepository) listLabelsForOwner(ctx context.Context, m membershipTable, ownerID int64) ([]models.Label, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT l.id, l.name, l.created_at, l.updated_at
		FROM labels l
		INNER JOIN %s membership ON membership.label_id = l.id
		WHERE membership.%s = $1
		ORDER BY l.name`, m.table, m.ownerColumn), ownerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query owner labels: %w", err)
	}
	return collectLabels(rows, false)
}

func (r *labelRepository) setMemberships(ctx context.Context, m membershipTable, ownerID int64, labelIDs []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin label membership transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := replaceLabelMemberships(ctx, tx, m, ownerID, labelIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit label membership transaction: %w", err)
	}
	return nil
}

func replaceLabelMemberships(ctx context.Context, tx *sql.Tx, m membershipTable, ownerID int64, labelIDs []int64) error {
	if err := validateLiveOwners(ctx, tx, m, []int64{ownerID}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, m.table, m.ownerColumn), ownerID); err != nil {
		return fmt.Errorf("failed to clear label memberships: %w", err)
	}
	return insertLabelMemberships(ctx, tx, m, ownerID, labelIDs)
}

func insertLabelMemberships(ctx context.Context, tx *sql.Tx, m membershipTable, ownerID int64, labelIDs []int64) error {
	seen := make(map[int64]struct{}, len(labelIDs))
	for _, labelID := range labelIDs {
		if labelID <= 0 {
			return fmt.Errorf("invalid label ID: %d", labelID)
		}
		if _, exists := seen[labelID]; exists {
			continue
		}
		seen[labelID] = struct{}{}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (label_id, %s) VALUES ($1, $2)`, m.table, m.ownerColumn), labelID, ownerID); err != nil {
			return fmt.Errorf("failed to insert label membership: %w", err)
		}
	}
	return nil
}

func (r *labelRepository) addMemberships(ctx context.Context, m membershipTable, labelID int64, ownerIDs []int64) error {
	if labelID <= 0 {
		return nil
	}
	ids := uniquePositive(ownerIDs)
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin label membership transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateLiveOwners(ctx, tx, m, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (label_id, %s)
		SELECT $1, owner FROM unnest($2::bigint[]) AS owner
		ON CONFLICT DO NOTHING`, m.table, m.ownerColumn), labelID, ids); err != nil {
		return fmt.Errorf("failed to add label memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit label memberships: %w", err)
	}
	return nil
}

func (r *labelRepository) removeMemberships(ctx context.Context, m membershipTable, labelID int64, ownerIDs []int64) error {
	if labelID <= 0 {
		return nil
	}
	ids := uniquePositive(ownerIDs)
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin label membership transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateLiveOwners(ctx, tx, m, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE label_id = $1 AND %s = ANY($2)`, m.table, m.ownerColumn), labelID, ids); err != nil {
		return fmt.Errorf("failed to remove label memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit label memberships: %w", err)
	}
	return nil
}

func validateLiveOwners(ctx context.Context, tx *sql.Tx, m membershipTable, ownerIDs []int64) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ANY($1) AND deleted_at IS NULL FOR SHARE`, m.ownerTable), ownerIDs)
	if err != nil {
		return fmt.Errorf("failed to validate label membership owners: %w", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate label membership owners: %w", err)
	}
	if count != len(ownerIDs) {
		return database.ErrNotFound
	}
	return nil
}

func (r *labelRepository) listLabelIDsForOwners(ctx context.Context, m membershipTable) (map[int64][]int64, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s, label_id FROM %s ORDER BY %s, label_id`, m.ownerColumn, m.table, m.ownerColumn))
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

func uniquePositive(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
