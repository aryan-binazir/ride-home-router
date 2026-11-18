// Package workflowtest supplies a fake repository for database-free tests.
package workflowtest

import (
	"bytes"
	"context"
	"ride-home-router/internal/database"
	"sync"
	"time"
)

type key struct{ kind, id string }

type repository struct {
	database.WorkflowRepository
	mu      sync.Mutex
	records map[key]database.WorkflowRecord
}

func New() database.WorkflowRepository {
	return &repository{records: make(map[key]database.WorkflowRecord)}
}

func (r *repository) Create(_ context.Context, kind, id string, data []byte, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[key{kind, id}] = database.WorkflowRecord{Data: bytes.Clone(data), Revision: 1}
	return nil
}

func (r *repository) Load(_ context.Context, kind, id string, _ time.Duration) (database.WorkflowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[key{kind, id}]
	if !ok {
		return database.WorkflowRecord{}, database.ErrNotFound
	}
	record.Data = bytes.Clone(record.Data)
	return record, nil
}

func (r *repository) CompareAndSwap(ctx context.Context, kind, id string, revision int64, data []byte, ttl time.Duration) error {
	return r.Transact(ctx, kind, id, ttl, func(record *database.WorkflowRecord, _ database.WorkflowWrites) error {
		if record.Revision != revision || record.Consumed {
			return database.ErrWorkflowConflict
		}
		record.Data = data
		return nil
	})
}

type unsupportedWrites struct{ database.WorkflowWrites }

func (r *repository) Transact(_ context.Context, kind, id string, _ time.Duration, update func(*database.WorkflowRecord, database.WorkflowWrites) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{kind, id}
	record, ok := r.records[k]
	if !ok {
		return database.ErrNotFound
	}
	record.Data = bytes.Clone(record.Data)
	if err := update(&record, unsupportedWrites{}); err != nil {
		return err
	}
	record.Data = bytes.Clone(record.Data)
	record.Revision++
	r.records[k] = record
	return nil
}

func (r *repository) Delete(_ context.Context, kind, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key{kind, id}
	if !r.records[k].Consumed {
		delete(r.records, k)
	}
	return nil
}
