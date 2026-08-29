package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"ride-home-router/internal/models"
)

type routeFeedbackRepository struct {
	db *sql.DB
}

func (r *routeFeedbackRepository) Create(ctx context.Context, record *models.RouteFeedbackRecord) error {
	input, err := json.Marshal(record.Input)
	if err != nil {
		return fmt.Errorf("failed to marshal route feedback input: %w", err)
	}
	proposed, err := json.Marshal(record.Proposed)
	if err != nil {
		return fmt.Errorf("failed to marshal proposed route feedback: %w", err)
	}
	final, err := json.Marshal(record.Final)
	if err != nil {
		return fmt.Errorf("failed to marshal final route feedback: %w", err)
	}
	if _, err := r.db.ExecContext(
		ctx, `
		INSERT INTO route_feedback (
			event_id, session_id, sme_email, schema_version, mode, input, proposed, final
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (session_id) DO NOTHING`,
		record.EventID, record.SessionID, record.SMEEmail, record.SchemaVersion, record.Mode,
		input, proposed, final,
	); err != nil {
		return fmt.Errorf("failed to create route feedback: %w", err)
	}
	return nil
}
