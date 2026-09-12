package postgres

import (
	"context"
	"database/sql"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

// workflowWrites explicitly shares the transaction with workflow consumption.
type workflowWrites struct {
	db *sql.DB
	tx *sql.Tx
}

func (w workflowWrites) CreateEvent(ctx context.Context, e *models.Event, routes []models.EventRoute, summary *models.EventSummary) (*models.Event, error) {
	return createEventTx(ctx, w.tx, e, routes, summary)
}

func (w workflowWrites) UpsertParticipants(ctx context.Context, rows []*models.Participant) (database.BatchUpsertResult, error) {
	return (&participantRepository{db: w.db}).writes().upsertBatchTx(ctx, w.tx, rows)
}

func (w workflowWrites) UpsertDrivers(ctx context.Context, rows []*models.Driver) (database.BatchUpsertResult, error) {
	return (&driverRepository{db: w.db}).writes().upsertBatchTx(ctx, w.tx, rows)
}
