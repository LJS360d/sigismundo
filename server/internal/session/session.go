// Package session holds the single global game session: who hosts it, who
// controls it, who is watching, and when it ends.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
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
	ErrForbidden = errors.New("you can't do that in this session")
	ErrBadCode   = errors.New("room codes are 4 letters")
)

// controllerGrace is how long the controller may be away from the Activity
// before control passes to the longest-present viewer.
const controllerGrace = time.Minute

type User struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// Viewer is one connected Activity client.
type Viewer struct {
	User
	GuildID   string
	ChannelID string
	// Notify is called (never concurrently for one viewer) when the state changes.
	Notify func()
	joined time.Time
}

// Starter launches the pipeline; tests swap it out.
type Starter func(runner.Spec, func(string)) (*runner.Run, error)

type Session struct {
	ID         string
	Pick       catalog.Pick
	Host       User
	Controller User
	GuildID    string
	ChannelID  string
	Started    time.Time
	Status     string
	RoomCode   string
	run        *runner.Run
	awaySince  time.Time
}

type Manager struct {
	mu       sync.Mutex
	Catalog  *catalog.Catalog
	start    Starter
	cur      *Session
	viewers  map[*Viewer]struct{}
	lastSeen time.Time
}

func NewManager(cat *catalog.Catalog) *Manager {
	return &Manager{
		Catalog:  cat,
		start:    runner.Start,
		viewers:  map[*Viewer]struct{}{},
		lastSeen: time.Now(),
	}
}

func (m *Manager) SetStarter(s Starter) { m.start = s }

// Start begins a session hosted by host in the given VC. The pipeline comes
// up in the background; viewers follow progress through Status.
func (m *Manager) Start(pick catalog.Pick, host User, guildID, channelID string) (View, error) {
	m.mu.Lock()
	if m.cur != nil {
		v := m.viewLocked(nil)
		m.mu.Unlock()
		return v, ErrBusy
	}
	s := &Session{
		ID:         newID(),
		Pick:       pick,
		Host:       host,
		Controller: host,
		GuildID:    guildID,
		ChannelID:  channelID,
		Started:    time.Now(),
		Status:     "Starting",
	}
	m.cur = s
	m.lastSeen = time.Now()
	v := m.viewLocked(nil)
	m.mu.Unlock()
	logger.Infof("session %s: %s hosting %s", s.ID, host.Name, pick.Key())
	m.broadcast()

	go m.launch(s)
	return v, nil
}

func (m *Manager) launch(s *Session) {
	spec := runner.Spec{
		Pick:       s.Pick,
		WinePrefix: config.WinePrefix,
		Display:    config.DisplayNum,
		HostAudio:  config.HostAudio,
	}
	run, err := m.start(spec, func(status string) { m.setStatus(s, status) })
	m.mu.Lock()
	if m.cur != s {
		m.mu.Unlock()
		if run != nil {
			run.Stop()
		}
		return
	}
	if err != nil {
		m.mu.Unlock()
		m.end(s, err)
		return
	}
	s.run = run
	s.Status = ""
	m.mu.Unlock()
	m.broadcast()
	<-run.Done
	m.end(s, run.Err)
}

func (m *Manager) setStatus(s *Session, status string) {
	m.mu.Lock()
	if m.cur != s {
		m.mu.Unlock()
		return
	}
	s.Status = status
	m.mu.Unlock()
	m.broadcast()
}

// end tears s down if it is still the current session.
func (m *Manager) end(s *Session, err error) {
	m.mu.Lock()
	if m.cur != s {
		m.mu.Unlock()
		return
	}
	m.cur = nil
	m.mu.Unlock()
	if s.run != nil {
		s.run.Stop()
	}
	if err != nil {
		logger.Warnf("session %s ended: %v", s.ID, err)
	} else {
		logger.Infof("session %s ended", s.ID)
	}
	m.broadcast()
}

// Stop ends the session on behalf of userID.
func (m *Manager) Stop(userID string) error {
	m.mu.Lock()
	s := m.cur
	if s == nil {
		m.mu.Unlock()
		return ErrNoSession
	}
	if !m.privilegedLocked(userID) {
		m.mu.Unlock()
		return ErrForbidden
	}
	m.mu.Unlock()
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

func (m *Manager) privilegedLocked(userID string) bool {
	s := m.cur
	return s.Host.ID == userID || s.Controller.ID == userID || config.IsOwner(userID)
}

// Handoff gives control to another connected viewer.
func (m *Manager) Handoff(by, to string) error {
	m.mu.Lock()
	s := m.cur
	if s == nil {
		m.mu.Unlock()
		return ErrNoSession
	}
	if !m.privilegedLocked(by) {
		m.mu.Unlock()
		return ErrForbidden
	}
	target, ok := m.viewerLocked(to)
	if !ok {
		m.mu.Unlock()
		return errors.New("that user isn't watching")
	}
	m.setControllerLocked(target)
	m.mu.Unlock()
	m.broadcast()
	return nil
}

// TakeControl lets the host or an owner reclaim control.
func (m *Manager) TakeControl(by User) error {
	m.mu.Lock()
	s := m.cur
	if s == nil {
		m.mu.Unlock()
		return ErrNoSession
	}
	if s.Host.ID != by.ID && !config.IsOwner(by.ID) {
		m.mu.Unlock()
		return ErrForbidden
	}
	m.setControllerLocked(by)
	m.mu.Unlock()
	m.broadcast()
	return nil
}

func (m *Manager) setControllerLocked(u User) {
	s := m.cur
	if s.Controller.ID == u.ID {
		return
	}
	logger.Infof("session %s: control %s -> %s", s.ID, s.Controller.Name, u.Name)
	s.Controller = u
	s.awaySince = time.Time{}
	if s.run != nil && s.run.Input != nil {
		s.run.Input.ReleaseAll()
	}
}

func (m *Manager) SetRoomCode(by, code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && (len(code) != 4 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return ErrBadCode
	}
	m.mu.Lock()
	s := m.cur
	if s == nil {
		m.mu.Unlock()
		return ErrNoSession
	}
	if !m.privilegedLocked(by) {
		m.mu.Unlock()
		return ErrForbidden
	}
	s.RoomCode = code
	m.mu.Unlock()
	m.broadcast()
	return nil
}

// Input runs fn against the display if userID controls the running session.
func (m *Manager) Input(userID string, fn func(*input.Injector) error) error {
	m.mu.Lock()
	s := m.cur
	if s == nil || s.Controller.ID != userID {
		m.mu.Unlock()
		return ErrForbidden
	}
	if s.run == nil || s.run.Input == nil {
		m.mu.Unlock()
		return nil
	}
	in := s.run.Input
	m.mu.Unlock()
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

func (m *Manager) Attach(v *Viewer) {
	m.mu.Lock()
	v.joined = time.Now()
	m.viewers[v] = struct{}{}
	m.mu.Unlock()
	m.broadcast()
}

func (m *Manager) Detach(v *Viewer) {
	m.mu.Lock()
	delete(m.viewers, v)
	m.lastSeen = time.Now()
	m.mu.Unlock()
	m.broadcast()
}

// Run enforces the idle timeout and the controller grace period until stop closes.
func (m *Manager) Run(stop <-chan struct{}) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		m.tick(time.Now())
	}
}

func (m *Manager) tick(now time.Time) {
	m.mu.Lock()
	s := m.cur
	if s == nil {
		m.mu.Unlock()
		return
	}
	if len(m.viewers) == 0 {
		idle := config.IdleTimeout
		if now.Sub(m.lastSeen) > idle && now.Sub(s.Started) > idle {
			m.mu.Unlock()
			logger.Infof("session %s: no viewers for %s", s.ID, idle)
			m.end(s, nil)
			return
		}
		m.mu.Unlock()
		return
	}
	changed := false
	if _, here := m.viewerLocked(s.Controller.ID); here {
		s.awaySince = time.Time{}
	} else if s.awaySince.IsZero() {
		s.awaySince = now
	} else if now.Sub(s.awaySince) > controllerGrace {
		if next, ok := m.oldestViewerLocked(s.Controller.ID); ok {
			m.setControllerLocked(next)
			changed = true
		}
	}
	m.mu.Unlock()
	if changed {
		m.broadcast()
	}
}

func (m *Manager) viewerLocked(id string) (User, bool) {
	for v := range m.viewers {
		if v.ID == id {
			return v.User, true
		}
	}
	return User{}, false
}

func (m *Manager) oldestViewerLocked(except string) (User, bool) {
	var best *Viewer
	for v := range m.viewers {
		if v.ID != except && (best == nil || v.joined.Before(best.joined)) {
			best = v
		}
	}
	if best == nil {
		return User{}, false
	}
	return best.User, true
}

func (m *Manager) broadcast() {
	m.mu.Lock()
	vs := make([]*Viewer, 0, len(m.viewers))
	for v := range m.viewers {
		vs = append(vs, v)
	}
	m.mu.Unlock()
	for _, v := range vs {
		if v.Notify != nil {
			v.Notify()
		}
	}
}

// View is the session as shown to one viewer (anonymous with nil).
type View struct {
	Session *SessionView `json:"session"`
	Viewers []User       `json:"viewers"`
	// Role of the asking viewer: "controller", "host", "viewer" or "" when idle.
	Role string `json:"role"`
	// CanControl is true when the viewer could take control back.
	CanReclaim bool `json:"can_reclaim"`
	Privileged bool `json:"privileged"`
}

type SessionView struct {
	ID         string    `json:"id"`
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	Host       User      `json:"host"`
	Controller User      `json:"controller"`
	GuildID    string    `json:"guild_id"`
	ChannelID  string    `json:"channel_id"`
	Started    time.Time `json:"started"`
	Status     string    `json:"status"`
	Running    bool      `json:"running"`
	RoomCode   string    `json:"room_code"`
	JoinURL    string    `json:"join_url"`
}

func (m *Manager) View(u *User) View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked(u)
}

func (m *Manager) viewLocked(u *User) View {
	v := View{Viewers: []User{}}
	seen := map[string]bool{}
	for vw := range m.viewers {
		if !seen[vw.ID] {
			seen[vw.ID] = true
			v.Viewers = append(v.Viewers, vw.User)
		}
	}
	slices.SortFunc(v.Viewers, func(a, b User) int { return strings.Compare(a.Name, b.Name) })
	s := m.cur
	if s == nil {
		return v
	}
	v.Session = &SessionView{
		ID:         s.ID,
		Key:        s.Pick.Key(),
		Name:       s.Pick.Name(),
		Host:       s.Host,
		Controller: s.Controller,
		GuildID:    s.GuildID,
		ChannelID:  s.ChannelID,
		Started:    s.Started,
		Status:     s.Status,
		Running:    s.run != nil,
		RoomCode:   s.RoomCode,
		JoinURL:    s.Pick.JoinURL(s.RoomCode),
	}
	if u != nil {
		switch {
		case s.Controller.ID == u.ID:
			v.Role = "controller"
		case s.Host.ID == u.ID:
			v.Role = "host"
		default:
			v.Role = "viewer"
		}
		v.Privileged = m.privilegedLocked(u.ID)
		v.CanReclaim = v.Role != "controller" && (s.Host.ID == u.ID || config.IsOwner(u.ID))
	}
	return v
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
