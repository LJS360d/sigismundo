package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"sigismundo/internal/config"
	"sigismundo/internal/input"
	"sigismundo/internal/logger"
	"sigismundo/internal/session"
	"sigismundo/internal/stream"

	"github.com/gorilla/websocket"
)

const writeWait = 10 * time.Second

// Requests reach us through Discord's proxy (or same-origin in a browser),
// and the token is the credential, so Origin isn't checked.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 64 << 10,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// clientMsg is every message the Activity sends.
type clientMsg struct {
	T    string  `json:"t"`
	Key  string  `json:"key"`
	Code string  `json:"code"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	B    byte    `json:"b"`
	D    bool    `json:"d"`
}

type stateMsg struct {
	T     string       `json:"t"`
	Me    session.User `json:"me"`
	Owner bool         `json:"owner"`
	session.View
}

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	user, err := verifyToken(r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{
		srv:    s,
		conn:   conn,
		notify: make(chan struct{}, 1),
		out:    make(chan any, 16),
		viewer: &session.Viewer{User: user},
	}
	c.viewer.Notify = func() {
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
	go c.writeLoop()
	c.srv.sessions.Attach(c.viewer)
	c.readLoop()
}

type wsClient struct {
	srv    *Server
	conn   *websocket.Conn
	notify chan struct{}
	out    chan any
	viewer *session.Viewer
}

func (c *wsClient) readLoop() {
	defer func() {
		c.srv.sessions.Detach(c.viewer)
		close(c.out)
	}()
	c.conn.SetReadLimit(4096)
	for {
		var m clientMsg
		if err := c.conn.ReadJSON(&m); err != nil {
			return
		}
		if err := c.handle(m); err != nil {
			c.send(map[string]string{"t": "error", "message": err.Error()})
		}
	}
}

func (c *wsClient) send(v any) {
	select {
	case c.out <- v:
	default:
	}
}

func (c *wsClient) handle(m clientMsg) error {
	mgr := c.srv.sessions
	uid := c.viewer.ID
	switch m.T {
	case "start":
		e, ok := mgr.Catalog.Get(m.Key)
		if !ok {
			return errors.New("unknown game")
		}
		return mgr.Start(*e, c.viewer.User)
	case "stop":
		return mgr.Stop(uid)
	case "seize":
		return mgr.Seize(c.viewer.User)
	case "m":
		return quiet(mgr.Input(uid, func(in *input.Injector) error { return in.MoveRel(clamp01(m.X), clamp01(m.Y)) }))
	case "b":
		return quiet(mgr.Input(uid, func(in *input.Injector) error { return in.Button(m.B, m.D) }))
	case "k":
		name, ok := input.FromBrowserCode(m.Code)
		if !ok {
			return nil
		}
		return quiet(mgr.Input(uid, func(in *input.Injector) error { return in.Key(name, m.D) }))
	}
	return nil
}

// quiet drops input errors from non-controllers; the UI already disables input for them.
func quiet(err error) error {
	if errors.Is(err, session.ErrForbidden) {
		return nil
	}
	if err != nil {
		logger.Debugf("input: %v", err)
	}
	return nil
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}

func (c *wsClient) writeLoop() {
	var sub *stream.Sub
	var hub *stream.Hub
	defer func() {
		if sub != nil {
			sub.Close()
		}
		c.conn.Close()
	}()
	write := func(typ int, b []byte) bool {
		c.conn.SetWriteDeadline(time.Now().Add(writeWait))
		return c.conn.WriteMessage(typ, b) == nil
	}
	writeJSON := func(v any) bool {
		b, _ := json.Marshal(v)
		return write(websocket.TextMessage, b)
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		var frames <-chan stream.Frame
		if sub != nil {
			frames = sub.C
		}
		select {
		case v, ok := <-c.out:
			if !ok || !writeJSON(v) {
				return
			}
		case <-c.notify:
			me := c.viewer.User
			if !writeJSON(stateMsg{T: "state", Me: me, Owner: config.IsOwner(me.ID), View: c.srv.sessions.View()}) {
				return
			}
			if h := c.srv.sessions.Hub(); h != hub {
				if sub != nil {
					sub.Close()
					sub = nil
				}
				hub = h
				if h != nil {
					sub = h.Subscribe()
				}
			}
		case f, ok := <-frames:
			if !ok {
				sub, hub = nil, nil
				continue
			}
			if !write(websocket.BinaryMessage, f) {
				return
			}
		case <-ping.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}
