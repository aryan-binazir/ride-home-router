package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type eventRepository struct {
	store *Store
}

func (r *eventRepository) List(ctx context.Context, limit, offset int) ([]models.Event, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) FROM events`
	if err := r.store.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	// Get paginated events
	query := `SELECT id, event_date, notes, mode, created_at
	          FROM events
	          ORDER BY event_date DESC
	          LIMIT ? OFFSET ?`

	rows, err := r.store.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var e models.Event
		var notes sql.NullString
		if err := rows.Scan(&e.ID, &e.EventDate, &notes, &e.Mode, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan event: %w", err)
		}
		if notes.Valid {
			e.Notes = notes.String
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating events: %w", err)
	}

	return events, total, nil
}

func (r *eventRepository) GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	// Get event
	eventQuery := `SELECT id, event_date, notes, mode, created_at FROM events WHERE id = ?`
	var event models.Event
	var notes sql.NullString
	err := r.store.db.QueryRowContext(ctx, eventQuery, id).Scan(
		&event.ID, &event.EventDate, &notes, &event.Mode, &event.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event: %w", err)
	}
	if notes.Valid {
		event.Notes = notes.String
	}

	// Get assignments
	assignQuery := `SELECT id, event_id, driver_id, driver_name, driver_address,
	                       route_order, participant_id, participant_name, participant_address,
	                       distance_from_prev_meters, org_vehicle_id, org_vehicle_name
	                FROM event_assignments
	                WHERE event_id = ?
	                ORDER BY driver_id, route_order`

	assignRows, err := r.store.db.QueryContext(ctx, assignQuery, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query event assignments: %w", err)
	}
	defer assignRows.Close()

	var assignments []models.EventAssignment
	for assignRows.Next() {
		var a models.EventAssignment
		var orgVehicleID sql.NullInt64
		var orgVehicleName sql.NullString
		if err := assignRows.Scan(
			&a.ID, &a.EventID, &a.DriverID, &a.DriverName, &a.DriverAddress,
			&a.RouteOrder, &a.ParticipantID, &a.ParticipantName, &a.ParticipantAddress,
			&a.DistanceFromPrev, &orgVehicleID, &orgVehicleName,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan event assignment: %w", err)
		}
		if orgVehicleID.Valid {
			a.OrgVehicleID = orgVehicleID.Int64
		}
		if orgVehicleName.Valid {
			a.OrgVehicleName = orgVehicleName.String
		}
		assignments = append(assignments, a)
	}

	// Get summary
	summaryQuery := `SELECT event_id, total_participants, total_drivers, total_distance_meters,
	                        org_vehicles_used, mode
	                 FROM event_summaries WHERE event_id = ?`

	var summary models.EventSummary
	err = r.store.db.QueryRowContext(ctx, summaryQuery, id).Scan(
		&summary.EventID, &summary.TotalParticipants, &summary.TotalDrivers,
		&summary.TotalDistanceMeters, &summary.OrgVehiclesUsed, &summary.Mode,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, nil, fmt.Errorf("failed to get event summary: %w", err)
	}

	return &event, assignments, &summary, nil
}

func (r *eventRepository) Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert event
	event.CreatedAt = time.Now()
	eventQuery := `INSERT INTO events (event_date, notes, mode, created_at) VALUES (?, ?, ?, ?)`
	result, err := tx.ExecContext(ctx, eventQuery, event.EventDate, event.Notes, event.Mode, event.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	eventID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get event id: %w", err)
	}
	event.ID = eventID

	// Insert assignments
	assignQuery := `INSERT INTO event_assignments
	                (event_id, driver_id, driver_name, driver_address, route_order,
	                 participant_id, participant_name, participant_address,
	                 distance_from_prev_meters, org_vehicle_id, org_vehicle_name)
	                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for _, a := range assignments {
		var orgVehicleID *int64
		var orgVehicleName *string
		if a.OrgVehicleID != 0 {
			orgVehicleID = &a.OrgVehicleID
			orgVehicleName = &a.OrgVehicleName
		}

		_, err := tx.ExecContext(ctx, assignQuery,
			eventID, a.DriverID, a.DriverName, a.DriverAddress, a.RouteOrder,
			a.ParticipantID, a.ParticipantName, a.ParticipantAddress,
			a.DistanceFromPrev, orgVehicleID, orgVehicleName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create event assignment: %w", err)
		}
	}

	// Insert summary
	summary.EventID = eventID
	summaryQuery := `INSERT INTO event_summaries
	                 (event_id, total_participants, total_drivers, total_distance_meters,
	                  org_vehicles_used, mode)
	                 VALUES (?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, summaryQuery,
		summary.EventID, summary.TotalParticipants, summary.TotalDrivers,
		summary.TotalDistanceMeters, summary.OrgVehiclesUsed, summary.Mode,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create event summary: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return event, nil
}

func (r *eventRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	// Foreign key cascade will delete assignments and summary
	query := `DELETE FROM events WHERE id = ?`
	result, err := r.store.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
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
