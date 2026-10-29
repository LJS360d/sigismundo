// Package session holds the global state behind the Activity: who controls
// it, who is watching, and the single game session with its lifecycle.
package session

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"sigismundo/internal/catalog"
	"sigismundo/internal/config"
	"sigismundo/internal/input"
	"sigismundo/internal/logger"
	"sigismundo/internal/runner"
	"sigismundo/internal/stream"
)

var (
	ErrBusy      = errors.New("a session is already running")
	ErrNoSession = errors.New("no session is running")
	ErrForbidden = errors.New("only the controller can do that")
	ErrNotOwner  = errors.New("only owners can seize control")
)

type User struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// Viewer is one connected Activity client.
type Viewer struct {
	User
	// Notify is called (never concurrently for one viewer) when the state changes.
	Notify func()
	joined time.Time
}

type Session struct {
	ID     string
	Entry  catalog.Entry
	Status string
	run    *runner.Run
}

type Manager struct {
	Catalog *catalog.Catalog
	// start launches the pipeline; tests swap it out.
	start func(catalog.Entry, func(string)) (*runner.Run, error)

	mu         sync.Mutex
	cur        *Session
	viewers    map[*Viewer]struct{}
	controller User
	awaySince  time.Time
	lastSeen   time.Time
}

func NewManager(cat *catalog.Catalog) *Manager {
	return &Manager{Catalog: cat, start: runner.Start, viewers: map[*Viewer]struct{}{}, lastSeen: time.Now()}
}

// Start begins a session on behalf of u. The pipeline comes up in the
// background; viewers follow progress through Status.
func (m *Manager) Start(entry catalog.Entry, u User) error {
	m.mu.Lock()
	if !m.privilegedLocked(u.ID) {
		m.mu.Unlock()
		return ErrForbidden
	}
	if m.cur != nil {
		m.mu.Unlock()
		return ErrBusy
	}
	s := &Session{ID: rand.Text()[:8], Entry: entry, Status: "Starting"}
	m.cur = s
	m.lastSeen = time.Now()
	m.mu.Unlock()
	logger.Infof("session %s: %s started %s", s.ID, u.Name, entry.ID)
	m.broadcast()
	go m.launch(s)
	return nil
}

func (m *Manager) launch(s *Session) {
	run, err := m.start(s.Entry, func(status string) { m.update(s, func() { s.Status = status }) })
	if err != nil {
		m.end(s, err)
		return
	}
	if !m.update(s, func() { s.run, s.Status = run, "" }) {
		run.Stop()
		return
	}
	<-run.Done
	m.end(s, run.Err)
}

// update applies fn and broadcasts if s is still the current session.
func (m *Manager) update(s *Session, fn func()) bool {
	m.mu.Lock()
	ok := m.cur == s
	if ok {
		fn()
	}
	m.mu.Unlock()
	if ok {
		m.broadcast()
	}
	return ok
}

// end tears s down if it is still the current session.
func (m *Manager) end(s *Session, err error) {
	if !m.update(s, func() { m.cur = nil }) {
		return
	}
	if s.run != nil {
		s.run.Stop()
	}
	if err != nil {
		logger.Warnf("session %s ended: %v", s.ID, err)
	} else {
		logger.Infof("session %s ended", s.ID)
	}
}

// Stop ends the session on behalf of userID.
func (m *Manager) Stop(userID string) error {
	m.mu.Lock()
	s := m.cur
	ok := m.privilegedLocked(userID)
	m.mu.Unlock()
	switch {
	case s == nil:
		return ErrNoSession
	case !ok:
		return ErrForbidden
	}
	m.end(s, nil)
	return nil
}

// Shutdown ends any session, for process exit.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	s := m.cur
	m.mu.Unlock()
	if s != nil {
		m.end(s, nil)
	}
}

// Seize makes owner u the controller.
func (m *Manager) Seize(u User) error {
	if !config.IsOwner(u.ID) {
		return ErrNotOwner
	}
	m.mu.Lock()
	m.setControllerLocked(u)
	m.mu.Unlock()
	m.broadcast()
	return nil
}

func (m *Manager) privilegedLocked(userID string) bool {
	return m.controller.ID == userID || config.IsOwner(userID)
}

func (m *Manager) setControllerLocked(u User) {
	m.awaySince = time.Time{}
	if m.controller.ID == u.ID {
		return
	}
	logger.Infof("control %s -> %s", m.controller.Name, u.Name)
	m.controller = u
	if s := m.cur; s != nil && s.run != nil && s.run.Input != nil {
		s.run.Input.ReleaseAll()
	}
}

// Input runs fn against the display if userID controls the running session.
func (m *Manager) Input(userID string, fn func(*input.Injector) error) error {
	m.mu.Lock()
	if m.controller.ID != userID {
		m.mu.Unlock()
		return ErrForbidden
	}
	var in *input.Injector
	if m.cur != nil && m.cur.run != nil {
		in = m.cur.run.Input
	}
	m.mu.Unlock()
	if in == nil {
		return nil
	}
	return fn(in)
}

// Hub is the running session's stream, nil while starting or idle.
func (m *Manager) Hub() *stream.Hub {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil || m.cur.run == nil {
		return nil
	}
	return m.cur.run.Hub
}

// Attach adds v; the first viewer of an empty Activity takes control.
func (m *Manager) Attach(v *Viewer) {
	m.mu.Lock()
	v.joined = time.Now()
	m.viewers[v] = struct{}{}
	if m.controller.ID == "" || m.controller.ID == v.ID {
		m.setControllerLocked(v.User)
	}
	m.mu.Unlock()
	m.broadcast()
}

// Detach removes v; once nobody is left, control goes to the next to join.
func (m *Manager) Detach(v *Viewer) {
	m.mu.Lock()
	delete(m.viewers, v)
	m.lastSeen = time.Now()
	if len(m.viewers) == 0 {
		m.controller = User{}
		m.awaySince = time.Time{}
	} else if m.awaySince.IsZero() && !m.presentLocked(m.controller.ID) {
		m.awaySince = m.lastSeen
	}
	m.mu.Unlock()
	m.broadcast()
}

// Run enforces the idle timeout and the controller grace period until stop closes.
func (m *Manager) Run(stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			m.tick(now)
		}
	}
}

func (m *Manager) tick(now time.Time) {
	m.mu.Lock()
	if len(m.viewers) == 0 {
		s, idle := m.cur, config.IdleTimeout
		expired := s != nil && now.Sub(m.lastSeen) > idle
		m.mu.Unlock()
		if expired {
			logger.Infof("session %s: no viewers for %s", s.ID, idle)
			m.end(s, nil)
		}
		return
	}
	changed := false
	switch {
	case m.presentLocked(m.controller.ID):
		m.awaySince = time.Time{}
	case m.awaySince.IsZero():
		m.awaySince = now
	case now.Sub(m.awaySince) >= config.ControlGrace:
		m.setControllerLocked(m.oldestViewerLocked())
		changed = true
	}
	m.mu.Unlock()
	if changed {
		m.broadcast()
	}
}

func (m *Manager) presentLocked(id string) bool {
	for v := range m.viewers {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (m *Manager) oldestViewerLocked() User {
	var best *Viewer
	for v := range m.viewers {
		if best == nil || v.joined.Before(best.joined) {
			best = v
		}
	}
	return best.User
}

func (m *Manager) broadcast() {
	m.mu.Lock()
	vs := make([]*Viewer, 0, len(m.viewers))
	for v := range m.viewers {
		vs = append(vs, v)
	}
	m.mu.Unlock()
	for _, v := range vs {
		v.Notify()
	}
}

// View is the state every viewer sees.
type View struct {
	Session    *SessionView `json:"session"`
	Controller *User        `json:"controller"`
	// Away is set while the controller has left and control is about to pass on.
	Away *Away `json:"away"`
}

// Away times the handover, in milliseconds: In left out of Grace.
type Away struct {
	In    int64 `json:"in"`
	Grace int64 `json:"grace"`
}

type SessionView struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Running bool   `json:"running"`
}

func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	var v View
	if m.controller.ID != "" {
		c := m.controller
		v.Controller = &c
	}
	if !m.awaySince.IsZero() {
		left := max(config.ControlGrace-time.Since(m.awaySince), 0)
		v.Away = &Away{In: left.Milliseconds(), Grace: config.ControlGrace.Milliseconds()}
	}
	if s := m.cur; s != nil {
		v.Session = &SessionView{ID: s.ID, Key: s.Entry.ID, Name: s.Entry.Name, Status: s.Status, Running: s.run != nil}
	}
	return v
}
