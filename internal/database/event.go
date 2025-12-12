package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"ride-home-router/internal/models"
)

// EventRepository handles event history persistence
type EventRepository interface {
	List(ctx context.Context, limit, offset int) ([]models.Event, int, error)
	GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error)
	Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error)
	Delete(ctx context.Context, id int64) error
}

type eventRepository struct {
	db *sql.DB
}

func (r *eventRepository) List(ctx context.Context, limit, offset int) ([]models.Event, int, error) {
	countQuery := `SELECT COUNT(*) FROM events`
	var total int
	if err := r.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	query := `
		SELECT e.id, e.event_date, e.notes, e.created_at
		FROM events e
		ORDER BY e.event_date DESC, e.created_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.ID, &e.EventDate, &e.Notes, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan event: %w", err)
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("row iteration error: %w", err)
	}

	return events, total, nil
}

func (r *eventRepository) GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error) {
	eventQuery := `SELECT id, event_date, notes, created_at FROM events WHERE id = ?`
	var event models.Event
	err := r.db.QueryRowContext(ctx, eventQuery, id).Scan(&event.ID, &event.EventDate, &event.Notes, &event.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event: %w", err)
	}

	assignmentsQuery := `
		SELECT id, event_id, driver_id, driver_name, driver_address, route_order,
		       participant_id, participant_name, participant_address, distance_from_prev, used_institute_vehicle
		FROM event_assignments
		WHERE event_id = ?
		ORDER BY driver_id, route_order
	`
	rows, err := r.db.QueryContext(ctx, assignmentsQuery, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query event assignments: %w", err)
	}
	defer rows.Close()

	var assignments []models.EventAssignment
	for rows.Next() {
		var a models.EventAssignment
		if err := rows.Scan(&a.ID, &a.EventID, &a.DriverID, &a.DriverName, &a.DriverAddress, &a.RouteOrder,
			&a.ParticipantID, &a.ParticipantName, &a.ParticipantAddress, &a.DistanceFromPrev, &a.UsedInstituteVehicle); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan assignment: %w", err)
		}
		assignments = append(assignments, a)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("row iteration error: %w", err)
	}

	summaryQuery := `
		SELECT event_id, total_participants, total_drivers, total_distance_meters,
		       used_institute_vehicle, institute_vehicle_driver_name
		FROM event_summaries
		WHERE event_id = ?
	`
	var summary models.EventSummary
	var instituteDriverName sql.NullString
	err = r.db.QueryRowContext(ctx, summaryQuery, id).Scan(&summary.EventID, &summary.TotalParticipants, &summary.TotalDrivers,
		&summary.TotalDistanceMeters, &summary.UsedInstituteVehicle, &instituteDriverName)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, nil, fmt.Errorf("failed to get event summary: %w", err)
	}
	if instituteDriverName.Valid {
		summary.InstituteVehicleDriverName = instituteDriverName.String
	}

	return &event, assignments, &summary, nil
}

func (r *eventRepository) Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[DB] Failed to begin event create transaction: err=%v", err)
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	eventQuery := `INSERT INTO events (event_date, notes) VALUES (?, ?) RETURNING id, created_at`
	err = tx.QueryRowContext(ctx, eventQuery, event.EventDate, event.Notes).Scan(&event.ID, &event.CreatedAt)
	if err != nil {
		log.Printf("[DB] Failed to create event: date=%s err=%v", event.EventDate.Format("2006-01-02"), err)
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	if len(assignments) > 0 {
		assignmentQuery := `
			INSERT INTO event_assignments (event_id, driver_id, driver_name, driver_address, route_order,
			                               participant_id, participant_name, participant_address, distance_from_prev, used_institute_vehicle)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		for _, a := range assignments {
			_, err := tx.ExecContext(ctx, assignmentQuery, event.ID, a.DriverID, a.DriverName, a.DriverAddress, a.RouteOrder,
				a.ParticipantID, a.ParticipantName, a.ParticipantAddress, a.DistanceFromPrev, a.UsedInstituteVehicle)
			if err != nil {
				log.Printf("[DB] Failed to create assignment: event_id=%d driver_id=%d err=%v", event.ID, a.DriverID, err)
				return nil, fmt.Errorf("failed to create assignment: %w", err)
			}
		}
	}

	summaryQuery := `
		INSERT INTO event_summaries (event_id, total_participants, total_drivers, total_distance_meters,
		                             used_institute_vehicle, institute_vehicle_driver_name)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	var instituteDriverName interface{}
	if summary.InstituteVehicleDriverName != "" {
		instituteDriverName = summary.InstituteVehicleDriverName
	}
	_, err = tx.ExecContext(ctx, summaryQuery, event.ID, summary.TotalParticipants, summary.TotalDrivers,
		summary.TotalDistanceMeters, summary.UsedInstituteVehicle, instituteDriverName)
	if err != nil {
		log.Printf("[DB] Failed to create event summary: event_id=%d err=%v", event.ID, err)
		return nil, fmt.Errorf("failed to create event summary: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[DB] Failed to commit event create transaction: err=%v", err)
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("[DB] Created event: id=%d date=%s assignments=%d", event.ID, event.EventDate.Format("2006-01-02"), len(assignments))
	return event, nil
}

func (r *eventRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM events WHERE id = ?`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		log.Printf("[DB] Failed to delete event: id=%d err=%v", id, err)
		return fmt.Errorf("failed to delete event: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		log.Printf("[DB] Failed to get rows affected for delete: id=%d err=%v", id, err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		log.Printf("[DB] Event not found for delete: id=%d", id)
		return sql.ErrNoRows
	}

	log.Printf("[DB] Deleted event: id=%d", id)
	return nil
}
