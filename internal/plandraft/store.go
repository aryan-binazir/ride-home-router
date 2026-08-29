package plandraft

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"ride-home-router/internal/models"
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
	cleanup     time.Duration
	now         func() time.Time
	stopCleanup chan struct{}
	cleanupDone chan struct{}
	closeOnce   sync.Once
}

func NewStore() *Store {
	return newStore(defaultTTL, defaultCleanupInterval, time.Now)
}

func newStore(ttl, cleanup time.Duration, now func() time.Time) *Store {
	s := &Store{
		drafts: make(map[string]Draft), ttl: ttl, cleanup: cleanup, now: now,
		stopCleanup: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

func (s *Store) NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("plandraft: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(bytes[:])
}

func (s *Store) Get(id string) (Draft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	draft, ok := s.drafts[id]
	if !ok {
		return Draft{}, false
	}
	if now.Sub(draft.lastAccessedAt) > s.ttl {
		delete(s.drafts, id)
		return Draft{}, false
	}
	draft.lastAccessedAt = now
	s.drafts[id] = clone(draft)
	return clone(draft), true
}

func (s *Store) Update(id string, update func(*Draft)) Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	draft, ok := s.drafts[id]
	if !ok || now.Sub(draft.lastAccessedAt) > s.ttl {
		draft = defaultDraft(now)
	}
	update(&draft)
	draft.lastAccessedAt = now
	s.drafts[id] = clone(draft)
	return clone(draft)
}

func (s *Store) Close() { s.closeOnce.Do(func() { close(s.stopCleanup); <-s.cleanupDone }) }

func (s *Store) cleanupLoop() {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(s.cleanup)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := s.now()
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

func defaultDraft(now time.Time) Draft {
	roundedMinutes := ((now.Minute() + 14) / 15) * 15
	defaultTime := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location()).Add(time.Duration(roundedMinutes) * time.Minute)
	return Draft{
		RouteTime:        defaultTime.Format("15:04"),
		Mode:             string(models.RouteModeDropoff),
		DriverVehicleIDs: map[int64]int64{},
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
