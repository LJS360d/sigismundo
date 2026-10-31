package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"sigismundo/internal/config"
	"sigismundo/internal/input"
	"sigismundo/internal/logger"
	"sigismundo/internal/session"
	"sigismundo/internal/stream"

	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	// sendBuffer caps the kernel's queue for the socket: anything queued there
	// is out of reach of stream.MaxLag.
	sendBuffer = 32 << 10
)

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
	// Relay is a token for the relay's /view, set when one is configured.
	Relay string `json:"relay,omitempty"`
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
	if tc, ok := conn.NetConn().(*net.TCPConn); ok {
		tc.SetWriteBuffer(sendBuffer)
	}
	c := &wsClient{
		srv:    s,
		conn:   conn,
		notify: make(chan struct{}, 1),
		media:  make(chan struct{}, 1),
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
	// hidden: the Activity isn't on screen, so it gets no video. relayed: it
	// gets the stream from the relay, so none from here. media wakes
	// writeLoop, which owns the subscription, to apply them.
	hidden  atomic.Bool
	relayed atomic.Bool
	media   chan struct{}
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
	case "vis", "relay":
		if m.T == "vis" {
			c.hidden.Store(!m.D)
		} else {
			c.relayed.Store(m.D)
		}
		select {
		case c.media <- struct{}{}:
		default:
		}
		return nil
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
	var pacer stream.Pacer
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
	// resubscribe follows the session's hub, unless the relay streams instead.
	resubscribe := func() {
		h := c.srv.sessions.Hub()
		if c.relayed.Load() {
			h = nil
		}
		if h != hub {
			if sub != nil {
				sub.Close()
				sub = nil
			}
			hub = h
			pacer.Reset()
			if h != nil {
				sub = h.Subscribe()
			}
		}
		if sub != nil {
			sub.Pause(c.hidden.Load())
		}
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
			msg := stateMsg{T: "state", Me: me, Owner: config.IsOwner(me.ID), View: c.srv.sessions.View()}
			if config.RelayURL != "" {
				msg.Relay = relayToken()
			}
			if !writeJSON(msg) {
				return
			}
			resubscribe()
		case <-c.media:
			resubscribe()
		case f, ok := <-frames:
			if !ok {
				sub, hub = nil, nil
				continue
			}
			if !pacer.Keep(f, hub.Since()) {
				continue
			}
			if !write(websocket.BinaryMessage, f) {
				return
			}
		case <-ping.C:
			if pacer.Dropped > 0 {
				logger.Debugf("ws %s: dropped %d late frames in 30s, the uplink can't keep up", c.viewer.ID, pacer.Dropped)
				pacer.Dropped = 0
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}
