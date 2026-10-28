package session

import (
	"testing"
	"time"

	"sigismundo/internal/catalog"
	"sigismundo/internal/runner"
	"sigismundo/internal/stream"
)

func setup(t *testing.T) (*Manager, chan *runner.Run) {
	t.Helper()
	cat := &catalog.Catalog{Entries: []catalog.Entry{{ID: "jpp7", Name: "Pack 7", Games: []catalog.Game{{ID: "q3", Name: "Quiplash 3"}}}}}
	m := NewManager(cat)
	runs := make(chan *runner.Run, 4)
	m.SetStarter(func(runner.Spec, func(string)) (*runner.Run, error) {
		r := &runner.Run{Hub: stream.NewHub(), Done: make(chan struct{})}
		runs <- r
		return r, nil
	})
	return m, runs
}

func waitEnded(t *testing.T, m *Manager) {
	t.Helper()
	for range 200 {
		if m.View(nil).Session == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session never ended")
}

func waitRunning(t *testing.T, m *Manager) {
	t.Helper()
	for range 100 {
		if v := m.View(nil); v.Session != nil && v.Session.Running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session never running")
}

var (
	alice = User{ID: "1", Name: "alice"}
	bob   = User{ID: "2", Name: "bob"}
	eve   = User{ID: "3", Name: "eve"}
)

func TestLifecycleAndPermissions(t *testing.T) {
	m, _ := setup(t)
	pick, _ := m.Catalog.Resolve("jpp7/q3")
	if _, err := m.Start(pick, alice, "g", "vc"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(pick, bob, "g", "vc"); err != ErrBusy {
		t.Fatalf("second start: %v", err)
	}
	waitRunning(t, m)

	if err := m.SetRoomCode("1", "abcd"); err != nil || m.View(nil).Session.RoomCode != "ABCD" {
		t.Fatalf("room code: %v", err)
	}
	if err := m.SetRoomCode("1", "AB1D"); err != ErrBadCode {
		t.Fatalf("bad code: %v", err)
	}
	if err := m.Stop("3"); err != ErrForbidden {
		t.Fatalf("eve stop: %v", err)
	}

	vb := &Viewer{User: bob}
	m.Attach(vb)
	if err := m.Handoff("1", "2"); err != nil {
		t.Fatal(err)
	}
	if v := m.View(&bob); v.Role != "controller" {
		t.Fatalf("bob role %q", v.Role)
	}
	if v := m.View(&alice); v.Role != "host" || !v.CanReclaim {
		t.Fatalf("alice view %+v", v)
	}
	if err := m.TakeControl(eve); err != ErrForbidden {
		t.Fatalf("eve take: %v", err)
	}
	if err := m.TakeControl(alice); err != nil {
		t.Fatal(err)
	}

	if err := m.Stop("1"); err != nil {
		t.Fatal(err)
	}
	if m.View(nil).Session != nil {
		t.Fatal("session still current")
	}
}

func TestGameExitEndsSession(t *testing.T) {
	m, runs := setup(t)
	pick, _ := m.Catalog.Resolve("jpp7")
	m.Start(pick, alice, "g", "vc")
	r := <-runs
	waitRunning(t, m)
	r.Stop()
	waitEnded(t, m)
}

func TestControllerAway(t *testing.T) {
	m, _ := setup(t)
	pick, _ := m.Catalog.Resolve("jpp7")
	m.Start(pick, alice, "g", "vc")
	waitRunning(t, m)
	m.Attach(&Viewer{User: bob})
	time.Sleep(time.Millisecond)
	m.Attach(&Viewer{User: eve})

	now := time.Now()
	m.tick(now)
	m.tick(now.Add(controllerGrace / 2))
	if m.View(nil).Session.Controller.ID != "1" {
		t.Fatal("transferred within grace")
	}
	m.tick(now.Add(2 * controllerGrace))
	if c := m.View(nil).Session.Controller.ID; c != "2" {
		t.Fatalf("controller %s, want longest-present bob", c)
	}
}

func TestIdleTimeout(t *testing.T) {
	m, _ := setup(t)
	pick, _ := m.Catalog.Resolve("jpp7")
	m.Start(pick, alice, "g", "vc")
	waitRunning(t, m)
	m.tick(time.Now().Add(time.Minute))
	if m.View(nil).Session == nil {
		t.Fatal("ended too early")
	}
	m.tick(time.Now().Add(time.Hour))
	waitEnded(t, m)
}
