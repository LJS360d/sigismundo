package session

import (
	"testing"
	"time"

	"sigismundo/internal/catalog"
	"sigismundo/internal/config"
	"sigismundo/internal/runner"
	"sigismundo/internal/stream"
)

func setup(t *testing.T) (*Manager, chan *runner.Run) {
	t.Helper()
	m := NewManager(&catalog.Catalog{Entries: []catalog.Entry{{ID: "jpp7", Name: "Pack 7"}}})
	runs := make(chan *runner.Run, 4)
	m.start = func(catalog.Entry, func(string)) (*runner.Run, error) {
		r := &runner.Run{Hub: stream.NewHub(), Done: make(chan struct{})}
		runs <- r
		return r, nil
	}
	return m, runs
}

func attach(m *Manager, u User) *Viewer {
	v := &Viewer{User: u, Notify: func() {}}
	m.Attach(v)
	time.Sleep(time.Millisecond) // distinct join times
	return v
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(what)
}

func controller(m *Manager) string {
	if c := m.View().Controller; c != nil {
		return c.ID
	}
	return ""
}

var (
	alice = User{ID: "1", Name: "alice"}
	bob   = User{ID: "2", Name: "bob"}
	eve   = User{ID: "3", Name: "eve"}
	pick  = catalog.Entry{ID: "jpp7", Name: "Pack 7"}
)

func TestLifecycleAndPermissions(t *testing.T) {
	m, _ := setup(t)
	attach(m, alice)
	attach(m, bob)
	if err := m.Start(pick, bob); err != ErrForbidden {
		t.Fatalf("non-controller start: %v", err)
	}
	if err := m.Start(pick, alice); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(pick, alice); err != ErrBusy {
		t.Fatalf("second start: %v", err)
	}
	waitFor(t, "never running", func() bool { return m.View().Session.Running })
	if err := m.Stop(bob.ID); err != ErrForbidden {
		t.Fatalf("bob stop: %v", err)
	}
	if err := m.Stop(alice.ID); err != nil {
		t.Fatal(err)
	}
	if m.View().Session != nil {
		t.Fatal("session still current")
	}
}

func TestGameExitEndsSession(t *testing.T) {
	m, runs := setup(t)
	attach(m, alice)
	m.Start(pick, alice)
	(<-runs).Stop()
	waitFor(t, "never ended", func() bool { return m.View().Session == nil })
}

func TestControllerAwayAndReset(t *testing.T) {
	m, _ := setup(t)
	a := attach(m, alice)
	attach(m, bob)
	attach(m, eve)
	m.Detach(a)

	now := time.Now()
	m.tick(now)
	m.tick(now.Add(controllerGrace / 2))
	if c := controller(m); c != alice.ID {
		t.Fatalf("transferred within grace to %s", c)
	}
	m.tick(now.Add(2 * controllerGrace))
	if c := controller(m); c != bob.ID {
		t.Fatalf("controller %s, want longest-present bob", c)
	}

	m2, _ := setup(t)
	v := attach(m2, alice)
	m2.Detach(v)
	attach(m2, bob)
	if c := controller(m2); c != bob.ID {
		t.Fatalf("empty Activity: controller %s, want next joiner bob", c)
	}
}

func TestSeize(t *testing.T) {
	config.OwnerIDs = []string{eve.ID}
	defer func() { config.OwnerIDs = nil }()
	m, _ := setup(t)
	attach(m, alice)
	if err := m.Seize(bob); err != ErrNotOwner {
		t.Fatalf("non-owner seize: %v", err)
	}
	if err := m.Seize(eve); err != nil || controller(m) != eve.ID {
		t.Fatalf("owner seize: %v, controller %s", err, controller(m))
	}
}

func TestIdleTimeout(t *testing.T) {
	m, _ := setup(t)
	m.controller = alice
	m.Start(pick, alice)
	waitFor(t, "never running", func() bool { return m.View().Session.Running })
	m.tick(time.Now().Add(time.Minute))
	if m.View().Session == nil {
		t.Fatal("ended too early")
	}
	m.tick(time.Now().Add(time.Hour))
	waitFor(t, "never ended", func() bool { return m.View().Session == nil })
}
