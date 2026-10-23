// Package webapp serves a local dashboard over the Mastercard and Visa parsers.
package webapp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"mciipm/internal/baseii"
	"mciipm/internal/convert"
	"mciipm/internal/iso8583"
	"mciipm/internal/validate"
)

// Session holds one uploaded file's parsed data in memory.
// Scheme selects which network's results and summary are populated.
type Session struct {
	ID         string
	Filename   string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Scheme     Scheme

	// Parse options the file was read with.
	Encoding     iso8583.Encoding // Mastercard
	Blocked1014  bool             // Mastercard
	IncludeAll   bool             // both schemes
	VisaFormat   baseii.Format    // Visa
	VisaEncoding baseii.Encoding  // Visa

	// Mastercard IPM: results of the single parse pass; re-used for every
	// subsequent browse/validate/export action so the file is never re-read.
	Results []convert.RecordResult
	Role    validate.Role
	Summary validate.Summary

	// Visa BASE II: same idea, grouped transactions instead of records.
	VisaTransactions []baseii.Transaction
	VisaSummary      baseii.Summary

	// ScanAborted is set when the file could not be read to its end. The
	// records already parsed are still shown, but every count in the
	// dashboard then describes a prefix of the file rather than the whole
	// of it, so the UI has to say so.
	ScanAborted string

	// Rows caches the table view; rebuilt after parsing or a role change.
	Rows []RecordRow

	mu sync.Mutex
}

// Lock/Unlock guard mutation of cached validation state (Role/Summary) when
// the user switches role via the UI while concurrent requests may be
// reading it.
func (s *Session) Lock()   { s.mu.Lock() }
func (s *Session) Unlock() { s.mu.Unlock() }

// Store is a thread-safe in-memory session store with a simple idle-timeout
// eviction sweep, so long-running server processes don't accumulate memory
// from abandoned uploads indefinitely.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

// NewStore creates a session store. Sessions idle for longer than ttl are
// evicted by a background sweep.
func NewStore(ttl time.Duration) *Store {
	s := &Store{sessions: make(map[string]*Session), ttl: ttl}
	go s.sweepLoop()
	return s
}

func (s *Store) sweepLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.sweep()
	}
}

func (s *Store) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-s.ttl)
	for id, sess := range s.sessions {
		sess.mu.Lock()
		stale := sess.LastUsedAt.Before(cutoff)
		sess.mu.Unlock()
		if stale {
			delete(s.sessions, id)
		}
	}
}

// New creates and registers a new session, returning it and its ID.
func (s *Store) New() *Session {
	id := newSessionID()
	sess := &Session{ID: id, CreatedAt: time.Now(), LastUsedAt: time.Now()}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess
}

// Get retrieves a session by ID, touching its last-used time. Returns nil,
// false if the session doesn't exist (expired, or never existed - e.g. an
// old cookie from a previous server restart).
func (s *Store) Get(id string) (*Session, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	sess.mu.Lock()
	sess.LastUsedAt = time.Now()
	sess.mu.Unlock()
	return sess, true
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
