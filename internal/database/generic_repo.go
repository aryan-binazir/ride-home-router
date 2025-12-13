package database

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
)

// genericRepository provides common CRUD operations for entity types
type genericRepository[T any] struct {
	store       *JSONStore
	getSlice    func(*JSONData) *[]T
	getNextID   func(*JSONData) *int64
	entityName  string
	getID       func(*T) int64
	setID       func(*T, int64)
	getName     func(*T) string
	withTimestamps bool
	getCreatedAt func(*T) time.Time
	setCreatedAt func(*T, time.Time)
	setUpdatedAt func(*T, time.Time)
}

// list returns all entities, optionally filtered by search term
func (r *genericRepository[T]) list(ctx context.Context, search string) ([]T, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	slice := r.getSlice(r.store.data)
	var result []T

	for _, item := range *slice {
		if search == "" || strings.Contains(strings.ToLower(r.getName(&item)), strings.ToLower(search)) {
			result = append(result, item)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return r.getName(&result[i]) < r.getName(&result[j])
	})

	return result, nil
}

// getByID returns a single entity by ID
func (r *genericRepository[T]) getByID(ctx context.Context, id int64) (*T, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	slice := r.getSlice(r.store.data)
	for _, item := range *slice {
		if r.getID(&item) == id {
			return &item, nil
		}
	}
	return nil, nil
}

// getByIDs returns multiple entities by their IDs
func (r *genericRepository[T]) getByIDs(ctx context.Context, ids []int64) ([]T, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	idSet := make(map[int64]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	slice := r.getSlice(r.store.data)
	var result []T
	for _, item := range *slice {
		if idSet[r.getID(&item)] {
			result = append(result, item)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return r.getName(&result[i]) < r.getName(&result[j])
	})

	return result, nil
}

// create creates a new entity
func (r *genericRepository[T]) create(ctx context.Context, entity *T) (*T, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	nextID := r.getNextID(r.store.data)
	r.setID(entity, *nextID)
	*nextID++

	if r.withTimestamps {
		now := time.Now()
		r.setCreatedAt(entity, now)
		r.setUpdatedAt(entity, now)
	}

	slice := r.getSlice(r.store.data)
	*slice = append(*slice, *entity)

	if err := r.store.saveUnlocked(); err != nil {
		return nil, err
	}

	log.Printf("[JSON] Created %s: id=%d", r.entityName, r.getID(entity))
	return entity, nil
}

// update updates an existing entity
func (r *genericRepository[T]) update(ctx context.Context, entity *T) (*T, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	slice := r.getSlice(r.store.data)
	id := r.getID(entity)

	for i, existing := range *slice {
		if r.getID(&existing) == id {
			if r.withTimestamps {
				r.setCreatedAt(entity, r.getCreatedAt(&existing))
				r.setUpdatedAt(entity, time.Now())
			}
			(*slice)[i] = *entity

			if err := r.store.saveUnlocked(); err != nil {
				return nil, err
			}

			log.Printf("[JSON] Updated %s: id=%d", r.entityName, id)
			return entity, nil
		}
	}

	return nil, ErrNotFound
}

// delete removes an entity by ID
func (r *genericRepository[T]) delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	slice := r.getSlice(r.store.data)
	for i, item := range *slice {
		if r.getID(&item) == id {
			*slice = append((*slice)[:i], (*slice)[i+1:]...)

			if err := r.store.saveUnlocked(); err != nil {
				return err
			}

			log.Printf("[JSON] Deleted %s: id=%d", r.entityName, id)
			return nil
		}
	}

	return ErrNotFound
}
