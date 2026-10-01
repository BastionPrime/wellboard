package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// SessionTTL is how long a WellBoard login stays valid. The router's
// ubus session timeout does not apply here: WellBoard issues its own
// cookie and re-asks for the password once it expires.
const SessionTTL = 12 * time.Hour

// Session is one authenticated browser session.
type Session struct {
	Token    string
	CSRF     string
	Username string
	Created  time.Time
	Expires  time.Time
}

// Expired reports whether the session is past its TTL.
func (s *Session) Expired(now time.Time) bool { return !now.Before(s.Expires) }

// Manager keeps the live sessions in memory. A restart drops them all,
// which is the desired behaviour for a single-admin appliance: the
// password is re-checked against ubus after every restart.
type Manager struct {
	ttl time.Duration

	mu       sync.Mutex
	sessions map[string]*Session

	// now and rand are injectable for tests.
	now  func() time.Time
	rand func([]byte) error
}

// NewManager builds a Manager; ttl <= 0 means SessionTTL.
func NewManager(ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	return &Manager{
		ttl:      ttl,
		sessions: make(map[string]*Session),
		now:      time.Now,
		rand:     func(b []byte) error { _, err := rand.Read(b); return err },
	}
}

// Create mints a session for username (the ubus identity). It returns
// the session including its CSRF token.
func (m *Manager) Create(username string) (*Session, error) {
	token, err := m.token()
	if err != nil {
		return nil, err
	}
	csrf, err := m.token()
	if err != nil {
		return nil, err
	}
	now := m.now()
	s := &Session{
		Token:    token,
		CSRF:     csrf,
		Username: username,
		Created:  now,
		Expires:  now.Add(m.ttl),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(now)
	m.sessions[token] = s
	return s, nil
}

// Get returns a live session by cookie token.
func (m *Manager) Get(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok {
		return nil, false
	}
	if s.Expired(now) {
		delete(m.sessions, token)
		return nil, false
	}
	return s, true
}

// Delete drops a session (logout). Unknown tokens are ignored.
func (m *Manager) Delete(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
}

// Count reports the number of live sessions (diagnostics/tests).
func (m *Manager) Count() int {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked(now)
	return len(m.sessions)
}

func (m *Manager) sweepLocked(now time.Time) {
	for token, s := range m.sessions {
		if s.Expired(now) {
			delete(m.sessions, token)
		}
	}
}

func (m *Manager) token() (string, error) {
	var b [32]byte
	if err := m.rand(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// secureEqual compares secrets without leaking their length or content
// through timing.
func secureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
