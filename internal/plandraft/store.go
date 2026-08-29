package plandraft

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"sync"
	"time"
)

const (
	defaultTTL             = 8 * time.Hour
	defaultCleanupInterval = 15 * time.Minute
)

// Draft is the server-side state shared by the mobile plan screens.
type Draft struct {
	LocationID       int64
	ParticipantIDs   []int64
	DriverIDs        []int64
	DriverVehicleIDs map[int64]int64
	RouteTime        string
	Mode             string
	RouteSessionID   string
	lastAccessedAt   time.Time
}

type Store struct {
	mu          sync.Mutex
	drafts      map[string]Draft
	ttl         time.Duration
	now         func() time.Time
	stopCleanup chan struct{}
	cleanupDone chan struct{}
	closeOnce   sync.Once
}

func NewStore() *Store {
	s := &Store{
		drafts: make(map[string]Draft), ttl: defaultTTL, now: time.Now,
		stopCleanup: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	go s.cleanupLoop(defaultCleanupInterval)
	return s
}

func (s *Store) NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("plandraft: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(bytes[:])
}

func (s *Store) Get(id string) Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	draft, ok := s.drafts[id]
	if !ok || now.Sub(draft.lastAccessedAt) > s.ttl {
		draft = Draft{RouteTime: "17:30", Mode: "dropoff", DriverVehicleIDs: map[int64]int64{}}
	}
	draft.lastAccessedAt = now
	s.drafts[id] = clone(draft)
	return clone(draft)
}

func (s *Store) Update(id string, update func(*Draft)) Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	draft, ok := s.drafts[id]
	if !ok || now.Sub(draft.lastAccessedAt) > s.ttl {
		draft = Draft{RouteTime: "17:30", Mode: "dropoff", DriverVehicleIDs: map[int64]int64{}}
	}
	update(&draft)
	draft.lastAccessedAt = now
	s.drafts[id] = clone(draft)
	return clone(draft)
}

func (s *Store) Close() { s.closeOnce.Do(func() { close(s.stopCleanup); <-s.cleanupDone }) }

func (s *Store) cleanupLoop(interval time.Duration) {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			s.mu.Lock()
			for id, draft := range s.drafts {
				if now.Sub(draft.lastAccessedAt) > s.ttl {
					delete(s.drafts, id)
				}
			}
			s.mu.Unlock()
		case <-s.stopCleanup:
			return
		}
	}
}

func clone(d Draft) Draft {
	d.ParticipantIDs = append([]int64(nil), d.ParticipantIDs...)
	d.DriverIDs = append([]int64(nil), d.DriverIDs...)
	assignments := d.DriverVehicleIDs
	d.DriverVehicleIDs = make(map[int64]int64, len(assignments))
	maps.Copy(d.DriverVehicleIDs, assignments)
	return d
}
