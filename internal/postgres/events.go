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

type eventRepository struct {
	db *sql.DB
}

func (r *eventRepository) List(ctx context.Context, limit, offset int) ([]models.Event, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_date, notes, mode, created_at
		FROM events
		ORDER BY event_date DESC, id DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []models.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating events: %w", err)
	}
	return events, total, nil
}

func scanEvent(scanner interface{ Scan(dest ...any) error }) (models.Event, error) {
	var event models.Event
	var notes sql.NullString
	var mode string
	if err := scanner.Scan(&event.ID, &event.EventDate, &notes, &mode, &event.CreatedAt); err != nil {
		return models.Event{}, err
	}
	event.Notes = notes.String
	var err error
	event.Mode, err = models.ParseRouteMode(mode)
	if err != nil {
		return models.Event{}, fmt.Errorf("invalid route mode for event %d: %w", event.ID, err)
	}
	return event, nil
}

const summaryColumns = `event_id, total_participants, total_drivers, total_distance_meters, org_vehicles_used, mode`

func scanSummary(scanner interface{ Scan(dest ...any) error }) (models.EventSummary, error) {
	var summary models.EventSummary
	var mode string
	if err := scanner.Scan(&summary.EventID, &summary.TotalParticipants, &summary.TotalDrivers,
		&summary.TotalDistanceMeters, &summary.OrgVehiclesUsed, &mode); err != nil {
		return models.EventSummary{}, err
	}
	var err error
	summary.Mode, err = models.ParseRouteMode(mode)
	if err != nil {
		return models.EventSummary{}, fmt.Errorf("invalid route mode for event summary %d: %w", summary.EventID, err)
	}
	return summary, nil
}

func (r *eventRepository) GetSummariesByEventIDs(ctx context.Context, eventIDs []int64) (map[int64]*models.EventSummary, error) {
	summaries := make(map[int64]*models.EventSummary, len(eventIDs))
	if len(eventIDs) == 0 {
		return summaries, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM event_summaries WHERE event_id = ANY($1)`, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query event summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan event summary: %w", err)
		}
		summaries[summary.EventID] = &summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event summaries: %w", err)
	}
	return summaries, nil
}

func (r *eventRepository) GetByID(ctx context.Context, id int64) (*models.Event, []models.EventRoute, *models.EventSummary, error) {
	event, err := scanEvent(r.db.QueryRowContext(ctx, `SELECT id, event_date, notes, mode, created_at FROM events WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, database.ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event: %w", err)
	}

	routes, err := r.routesForEvent(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}

	summary, err := scanSummary(r.db.QueryRowContext(ctx, `SELECT `+summaryColumns+` FROM event_summaries WHERE event_id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return &event, routes, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get event summary: %w", err)
	}
	return &event, routes, &summary, nil
}

func (r *eventRepository) routesForEvent(ctx context.Context, eventID int64) ([]models.EventRoute, error) {
	routeRows, err := r.db.QueryContext(ctx, `
		SELECT id, event_id, route_order, driver_id, driver_name, driver_address,
		       COALESCE(driver_address_name, ''),
		       effective_capacity, org_vehicle_id, org_vehicle_name,
		       total_dropoff_distance_meters, distance_to_driver_home_meters,
		       total_distance_meters, baseline_duration_secs, route_duration_secs,
		       detour_secs, mode, snapshot_version, metrics_complete
		FROM event_routes
		WHERE event_id = $1
		ORDER BY route_order, id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to query event routes: %w", err)
	}
	defer func() { _ = routeRows.Close() }()

	var routes []models.EventRoute
	routeIDs := make([]int64, 0)
	routeIndexByID := make(map[int64]int)
	for routeRows.Next() {
		var route models.EventRoute
		var orgVehicleID sql.NullInt64
		var orgVehicleName sql.NullString
		var mode string
		if err := routeRows.Scan(
			&route.ID, &route.EventID, &route.RouteOrder, &route.DriverID, &route.DriverName, &route.DriverAddress,
			&route.DriverAddressName,
			&route.EffectiveCapacity, &orgVehicleID, &orgVehicleName,
			&route.TotalDropoffDistanceMeters, &route.DistanceToDriverHomeMeters,
			&route.TotalDistanceMeters, &route.BaselineDurationSecs, &route.RouteDurationSecs,
			&route.DetourSecs, &mode, &route.SnapshotVersion, &route.MetricsComplete,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event route: %w", err)
		}
		route.OrgVehicleID = orgVehicleID.Int64
		route.OrgVehicleName = orgVehicleName.String
		route.Mode, err = models.ParseRouteMode(mode)
		if err != nil {
			return nil, fmt.Errorf("invalid route mode for event route in event %d: %w", eventID, err)
		}
		route.Stops = []models.EventRouteStop{}
		routeIndexByID[route.ID] = len(routes)
		routeIDs = append(routeIDs, route.ID)
		routes = append(routes, route)
	}
	if err := routeRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event routes: %w", err)
	}
	if len(routeIDs) == 0 {
		return routes, nil
	}

	stopRows, err := r.db.QueryContext(ctx, `
		SELECT id, event_route_id, route_order, participant_id, participant_name,
		       participant_address, COALESCE(participant_address_name, ''),
		       distance_from_prev_meters, cumulative_distance_meters,
		       duration_from_prev_secs, cumulative_duration_secs
		FROM event_route_stops
		WHERE event_route_id = ANY($1)
		ORDER BY event_route_id, route_order`, routeIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to query event route stops: %w", err)
	}
	defer func() { _ = stopRows.Close() }()

	for stopRows.Next() {
		var stop models.EventRouteStop
		if err := stopRows.Scan(
			&stop.ID, &stop.EventRouteID, &stop.Order, &stop.ParticipantID, &stop.ParticipantName,
			&stop.ParticipantAddress, &stop.ParticipantAddressName,
			&stop.DistanceFromPrevMeters, &stop.CumulativeDistanceMeters,
			&stop.DurationFromPrevSecs, &stop.CumulativeDurationSecs,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event route stop: %w", err)
		}
		if routeIndex, ok := routeIndexByID[stop.EventRouteID]; ok {
			routes[routeIndex].Stops = append(routes[routeIndex].Stops, stop)
		}
	}
	if err := stopRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event route stops: %w", err)
	}
	return routes, nil
}

func (r *eventRepository) Create(ctx context.Context, event *models.Event, routes []models.EventRoute, summary *models.EventSummary) (*models.Event, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	event.CreatedAt = time.Now()
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO events (event_date, notes, mode, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		event.EventDate, event.Notes, string(event.Mode), event.CreatedAt).Scan(&event.ID); err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	for _, route := range routes {
		var orgVehicleID *int64
		var orgVehicleName *string
		snapshotVersion := route.SnapshotVersion
		if snapshotVersion == 0 {
			snapshotVersion = 2
		}
		metricsComplete := route.MetricsComplete || snapshotVersion >= 2
		if route.OrgVehicleID != 0 {
			orgVehicleID = &route.OrgVehicleID
			orgVehicleName = &route.OrgVehicleName
		}

		var eventRouteID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO event_routes (
				event_id, route_order, driver_id, driver_name, driver_address, driver_address_name,
				effective_capacity, org_vehicle_id, org_vehicle_name,
				total_dropoff_distance_meters, distance_to_driver_home_meters,
				total_distance_meters, baseline_duration_secs, route_duration_secs,
				detour_secs, mode, snapshot_version, metrics_complete
			) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
			RETURNING id`,
			event.ID, route.RouteOrder, route.DriverID, route.DriverName, route.DriverAddress, route.DriverAddressName,
			route.EffectiveCapacity, orgVehicleID, orgVehicleName,
			route.TotalDropoffDistanceMeters, route.DistanceToDriverHomeMeters,
			route.TotalDistanceMeters, route.BaselineDurationSecs, route.RouteDurationSecs,
			route.DetourSecs, string(route.Mode), snapshotVersion, metricsComplete,
		).Scan(&eventRouteID); err != nil {
			return nil, fmt.Errorf("failed to create event route: %w", err)
		}

		for _, stop := range route.Stops {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO event_route_stops (
					event_route_id, route_order, participant_id, participant_name,
					participant_address, participant_address_name,
					distance_from_prev_meters, cumulative_distance_meters,
					duration_from_prev_secs, cumulative_duration_secs
				) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10)`,
				eventRouteID, stop.Order, stop.ParticipantID, stop.ParticipantName,
				stop.ParticipantAddress, stop.ParticipantAddressName,
				stop.DistanceFromPrevMeters, stop.CumulativeDistanceMeters,
				stop.DurationFromPrevSecs, stop.CumulativeDurationSecs,
			); err != nil {
				return nil, fmt.Errorf("failed to create event route stop: %w", err)
			}
		}
	}

	if summary != nil {
		summary.EventID = event.ID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_summaries (`+summaryColumns+`) VALUES ($1, $2, $3, $4, $5, $6)`,
			summary.EventID, summary.TotalParticipants, summary.TotalDrivers,
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
	result, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
	}
	return rowsAffectedOrNotFound(result)
}
