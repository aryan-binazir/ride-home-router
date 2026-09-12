package database

import (
	"context"
	"errors"
	"ride-home-router/internal/models"
	"time"
)

//nolint:staticcheck // This shared capacity sentinel is intentionally user-facing copy.
var ErrWorkflowCapacity = errors.New("Too many active plans. Finish or cancel an existing plan and try again.")

var ErrWorkflowPayloadTooLarge = errors.New("import data exceeds the 24 MiB storage limit")

var ErrInvalidWorkflowSelection = errors.New("invalid import selection")

var ErrWorkflowConflict = errors.New("workflow changed; reload and try again")

// WorkflowRecord is a versioned, expiring snapshot. Revision protects work
// calculated outside a transaction from overwriting a newer result.
type WorkflowRecord struct {
	Data     []byte
	Revision int64
	Consumed bool
}

// WorkflowRepository coordinates short state changes across application instances.
// Update callbacks must not perform network calls or access another workflow.
type WorkflowWrites interface {
	StageImport(context.Context, string, []ImportRow, []ImportJob) error
	ImportRows(context.Context, string) ([]ImportRow, error)
	SelectImportRows(context.Context, string, []bool) error
	ClearImportRows(context.Context, string) error
	CreateEvent(context.Context, *models.Event, []models.EventRoute, *models.EventSummary) (*models.Event, error)
	UpsertParticipants(context.Context, []*models.Participant) (BatchUpsertResult, error)
	UpsertDrivers(context.Context, []*models.Driver) (BatchUpsertResult, error)
}

type WorkflowRepository interface {
	Transact(context.Context, string, string, time.Duration, func(*WorkflowRecord, WorkflowWrites) error) error
	Create(context.Context, string, string, []byte, time.Duration) error
	Load(context.Context, string, string, time.Duration) (WorkflowRecord, error)
	Update(context.Context, string, string, time.Duration, func(*WorkflowRecord) error) error
	CompareAndSwap(context.Context, string, string, int64, []byte, time.Duration) error
	Delete(context.Context, string, string) error
}

// Import rows and geocoding jobs are stored separately from the session header.
type ImportRow struct {
	Index    int
	Data     []byte
	Selected bool
}
type ImportJob struct {
	SessionID string
	Index     int
	Address   string
	Rows      []int
	Token     string
}
type ImportJobRepository interface {
	Rows(context.Context, string) ([]ImportRow, error)
	Progress(context.Context, string) (int, int, error)
	Claim(context.Context, string, time.Duration) (ImportJob, bool, error)
	Finish(context.Context, ImportJob, []ImportRow, bool) error
	Release(context.Context, ImportJob) error
}
