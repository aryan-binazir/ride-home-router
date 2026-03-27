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

type eventRepository struct {
	store *Store
}

func (r *eventRepository) List(ctx context.Context, limit, offset int) ([]models.Event, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var total int
	if err := r.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	rows, err := r.store.db.QueryContext(ctx, `
		SELECT id, event_date, notes, mode, created_at
		FROM events
		ORDER BY event_date DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var event models.Event
		var notes sql.NullString
		var mode string
		if err := rows.Scan(&event.ID, &event.EventDate, &notes, &mode, &event.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("failed to scan event: %w", err)
		}
		if notes.Valid {
			event.Notes = notes.String
		}
		event.Mode, err = models.ParseRouteMode(mode)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid route mode for event %d: %w", event.ID, err)
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating events: %w", err)
	}

	return events, total, nil
}

func (r *eventRepository) GetSummariesByEventIDs(ctx context.Context, eventIDs []int64) (map[int64]*models.EventSummary, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	summaries := make(map[int64]*models.EventSummary, len(eventIDs))
	if len(eventIDs) == 0 {
		return summaries, nil
	}

	placeholders := make([]string, len(eventIDs))
	args := make([]any, len(eventIDs))
	for i, eventID := range eventIDs {
		placeholders[i] = "?"
		args[i] = eventID
	}

	rows, err := r.store.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT event_id, total_participants, total_drivers, total_distance_meters,
		       org_vehicles_used, mode
		FROM event_summaries
		WHERE event_id IN (%s)
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query event summaries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var summary models.EventSummary
		var mode string
		if err := rows.Scan(
			&summary.EventID, &summary.TotalParticipants, &summary.TotalDrivers,
			&summary.TotalDistanceMeters, &summary.OrgVehiclesUsed, &mode,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event summary: %w", err)
		}
		summary.Mode, err = models.ParseRouteMode(mode)
		if err != nil {
			return nil, fmt.Errorf("invalid route mode for event summary %d: %w", summary.EventID, err)
		}
		summaryCopy := summary
		summaries[summary.EventID] = &summaryCopy
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event summaries: %w", err)
	}

	return summaries, nil
}

func (r *eventRepository) GetByID(ctx context.Context, id int64) (*models.Event, []models.EventRoute, *models.EventSummary, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var event models.Event
	var notes sql.NullString
	var mode string
	err := r.store.db.QueryRowContext(ctx, `
		SELECT id, event_date, notes, mode, created_at
		FROM events
		WHERE id = ?
	`, id).Scan(&event.ID, &event.EventDate, &notes, &mode, &event.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event: %w", err)
	}
	if notes.Valid {
		event.Notes = notes.String
	}
	event.Mode, err = models.ParseRouteMode(mode)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid route mode for event %d: %w", event.ID, err)
	}

	routeRows, err := r.store.db.QueryContext(ctx, `
		SELECT id, event_id, route_order, driver_id, driver_name, driver_address,
		       effective_capacity, org_vehicle_id, org_vehicle_name,
		       total_dropoff_distance_meters, distance_to_driver_home_meters,
		       total_distance_meters, baseline_duration_secs, route_duration_secs,
		       detour_secs, mode, snapshot_version, metrics_complete
		FROM event_routes
		WHERE event_id = ?
		ORDER BY route_order, id
	`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query event routes: %w", err)
	}
	defer routeRows.Close()

	var routes []models.EventRoute
	routeIDs := make([]int64, 0)
	routeIndexByID := make(map[int64]int)
	for routeRows.Next() {
		var route models.EventRoute
		var orgVehicleID sql.NullInt64
		var orgVehicleName sql.NullString
		var mode string
		var metricsComplete int
		if err := routeRows.Scan(
			&route.ID, &route.EventID, &route.RouteOrder, &route.DriverID, &route.DriverName, &route.DriverAddress,
			&route.EffectiveCapacity, &orgVehicleID, &orgVehicleName,
			&route.TotalDropoffDistanceMeters, &route.DistanceToDriverHomeMeters,
			&route.TotalDistanceMeters, &route.BaselineDurationSecs, &route.RouteDurationSecs,
			&route.DetourSecs, &mode, &route.SnapshotVersion, &metricsComplete,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan event route: %w", err)
		}
		if orgVehicleID.Valid {
			route.OrgVehicleID = orgVehicleID.Int64
		}
		if orgVehicleName.Valid {
			route.OrgVehicleName = orgVehicleName.String
		}
		route.Mode, err = models.ParseRouteMode(mode)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid route mode for event route in event %d: %w", id, err)
		}
		route.MetricsComplete = metricsComplete == 1
		route.Stops = []models.EventRouteStop{}
		routeIndexByID[route.ID] = len(routes)
		routeIDs = append(routeIDs, route.ID)
		routes = append(routes, route)
	}
	if err := routeRows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("error iterating event routes: %w", err)
	}

	if len(routeIDs) > 0 {
		placeholders := make([]string, len(routeIDs))
		args := make([]any, len(routeIDs))
		for i, routeID := range routeIDs {
			placeholders[i] = "?"
			args[i] = routeID
		}

		stopRows, err := r.store.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT id, event_route_id, route_order, participant_id, participant_name,
			       participant_address, distance_from_prev_meters, cumulative_distance_meters,
			       duration_from_prev_secs, cumulative_duration_secs
			FROM event_route_stops
			WHERE event_route_id IN (%s)
			ORDER BY event_route_id, route_order
		`, strings.Join(placeholders, ",")), args...)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to query event route stops: %w", err)
		}
		defer stopRows.Close()

		for stopRows.Next() {
			var stop models.EventRouteStop
			if err := stopRows.Scan(
				&stop.ID, &stop.EventRouteID, &stop.Order, &stop.ParticipantID, &stop.ParticipantName,
				&stop.ParticipantAddress, &stop.DistanceFromPrevMeters, &stop.CumulativeDistanceMeters,
				&stop.DurationFromPrevSecs, &stop.CumulativeDurationSecs,
			); err != nil {
				return nil, nil, nil, fmt.Errorf("failed to scan event route stop: %w", err)
			}

			routeIndex, ok := routeIndexByID[stop.EventRouteID]
			if !ok {
				continue
			}
			routes[routeIndex].Stops = append(routes[routeIndex].Stops, stop)
		}
		if err := stopRows.Err(); err != nil {
			return nil, nil, nil, fmt.Errorf("error iterating event route stops: %w", err)
		}
	}

	var summary models.EventSummary
	var summaryMode string
	err = r.store.db.QueryRowContext(ctx, `
		SELECT event_id, total_participants, total_drivers, total_distance_meters,
		       org_vehicles_used, mode
		FROM event_summaries
		WHERE event_id = ?
	`, id).Scan(
		&summary.EventID, &summary.TotalParticipants, &summary.TotalDrivers,
		&summary.TotalDistanceMeters, &summary.OrgVehiclesUsed, &summaryMode,
	)
	if err == sql.ErrNoRows {
		return &event, routes, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event summary: %w", err)
	}
	summary.Mode, err = models.ParseRouteMode(summaryMode)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid route mode for event summary %d: %w", id, err)
	}

	return &event, routes, &summary, nil
}

func (r *eventRepository) Create(ctx context.Context, event *models.Event, routes []models.EventRoute, summary *models.EventSummary) (*models.Event, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	event.CreatedAt = time.Now()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO events (event_date, notes, mode, created_at)
		VALUES (?, ?, ?, ?)
	`, event.EventDate, event.Notes, string(event.Mode), event.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	eventID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get event id: %w", err)
	}
	event.ID = eventID

	routeInsert := `
		INSERT INTO event_routes (
			event_id, route_order, driver_id, driver_name, driver_address,
			effective_capacity, org_vehicle_id, org_vehicle_name,
			total_dropoff_distance_meters, distance_to_driver_home_meters,
			total_distance_meters, baseline_duration_secs, route_duration_secs,
			detour_secs, mode, snapshot_version, metrics_complete
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stopInsert := `
		INSERT INTO event_route_stops (
			event_route_id, route_order, participant_id, participant_name,
			participant_address, distance_from_prev_meters, cumulative_distance_meters,
			duration_from_prev_secs, cumulative_duration_secs
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	for _, route := range routes {
		var orgVehicleID *int64
		var orgVehicleName *string
		snapshotVersion := route.SnapshotVersion
		if snapshotVersion == 0 {
			snapshotVersion = 2
		}
		metricsComplete := route.MetricsComplete
		if snapshotVersion >= 2 && !metricsComplete {
			metricsComplete = true
		}
		if route.OrgVehicleID != 0 {
			orgVehicleID = &route.OrgVehicleID
			orgVehicleName = &route.OrgVehicleName
		}

		routeResult, err := tx.ExecContext(ctx, routeInsert,
			eventID, route.RouteOrder, route.DriverID, route.DriverName, route.DriverAddress,
			route.EffectiveCapacity, orgVehicleID, orgVehicleName,
			route.TotalDropoffDistanceMeters, route.DistanceToDriverHomeMeters,
			route.TotalDistanceMeters, route.BaselineDurationSecs, route.RouteDurationSecs,
			route.DetourSecs, string(route.Mode), snapshotVersion, boolToSQLiteInt(metricsComplete),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create event route: %w", err)
		}

		eventRouteID, err := routeResult.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("failed to get event route id: %w", err)
		}

		for _, stop := range route.Stops {
			if _, err := tx.ExecContext(ctx, stopInsert,
				eventRouteID, stop.Order, stop.ParticipantID, stop.ParticipantName,
				stop.ParticipantAddress, stop.DistanceFromPrevMeters, stop.CumulativeDistanceMeters,
				stop.DurationFromPrevSecs, stop.CumulativeDurationSecs,
			); err != nil {
				return nil, fmt.Errorf("failed to create event route stop: %w", err)
			}
		}
	}

	if summary != nil {
		summary.EventID = eventID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_summaries (
				event_id, total_participants, total_drivers, total_distance_meters,
				org_vehicles_used, mode
			) VALUES (?, ?, ?, ?, ?, ?)
		`, summary.EventID, summary.TotalParticipants, summary.TotalDrivers,
			summary.TotalDistanceMeters, summary.OrgVehiclesUsed, string(summary.Mode),
		); err != nil {
			return nil, fmt.Errorf("failed to create event summary: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return event, nil
}

func (r *eventRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	result, err := r.store.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id)
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

func (r *eventRepository) HasLegacyArchive(ctx context.Context) (bool, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var exists int
	if err := r.store.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sqlite_master
			WHERE type = 'table' AND name = 'events_legacy'
		)
	`).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check legacy archive: %w", err)
	}

	return exists == 1, nil
}

func boolToSQLiteInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
