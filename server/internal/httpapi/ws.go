package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

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
	T         string  `json:"t"`
	GuildID   string  `json:"guild_id"`
	ChannelID string  `json:"channel_id"`
	Key       string  `json:"key"`
	To        string  `json:"to"`
	Code      string  `json:"code"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	B         byte    `json:"b"`
	D         bool    `json:"d"`
}

type stateMsg struct {
	T  string       `json:"t"`
	Me session.User `json:"me"`
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
	attached := false
	for {
		var m clientMsg
		if err := c.conn.ReadJSON(&m); err != nil {
			return
		}
		if !attached {
			if m.T != "hello" {
				return
			}
			c.viewer.GuildID, c.viewer.ChannelID = m.GuildID, m.ChannelID
			c.srv.sessions.Attach(c.viewer)
			attached = true
			continue
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
		pick, ok := mgr.Catalog.Resolve(m.Key)
		if !ok {
			return errors.New("unknown game")
		}
		_, err := mgr.Start(pick, c.viewer.User, c.viewer.GuildID, c.viewer.ChannelID)
		return err
	case "stop":
		return mgr.Stop(uid)
	case "handoff":
		return mgr.Handoff(uid, m.To)
	case "take":
		return mgr.TakeControl(c.viewer.User)
	case "room":
		return mgr.SetRoomCode(uid, m.Code)
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
			if !writeJSON(stateMsg{T: "state", Me: me, View: c.srv.sessions.View(&me)}) {
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
