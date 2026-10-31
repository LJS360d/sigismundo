// Package relay publishes the running session's stream to the relay Worker
// (relay/ at the repo root), which fans it out to viewers: the uplink then
// carries one copy of the stream however many watch.
//
// Each binary message is a bundle of frames, each prefixed by its length as
// u32 BE: the audio packets since the last video frame, then that frame. The
// Worker's free plan bills incoming messages, so one per video frame instead
// of one per packet roughly triples the hours of streaming a day it covers.
// A text message {"t":"reset"} tells the Worker a new stream begins.
package relay

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"time"

	"sigismundo/internal/config"
	"sigismundo/internal/logger"
	"sigismundo/internal/stream"

	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	// bundles hold at most this many audio packets (20ms each) when no video
	// frame comes to close them
	maxAudio = 5
	// sendBuffer caps the kernel's queue for the socket, as in httpapi
	sendBuffer = 32 << 10
)

var dialer = websocket.Dialer{
	HandshakeTimeout: 10 * time.Second,
	WriteBufferSize:  64 << 10,
	NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetWriteBuffer(sendBuffer)
		}
		return c, err
	},
}

// Hubs gives the running session's stream (session.Manager), nil when none.
type Hubs interface{ Hub() *stream.Hub }

// Run keeps publishing to config.RelayURL until stop closes. While it can't,
// viewers fall back to the stream of /api/ws.
func Run(sessions Hubs, stop <-chan struct{}) {
	backoff := time.Second
	for {
		err := publish(sessions, stop, &backoff)
		select {
		case <-stop:
			return
		default:
		}
		logger.Warnf("relay: %v, retrying in %s", err, backoff)
		select {
		case <-stop:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func publish(sessions Hubs, stop <-chan struct{}, backoff *time.Duration) error {
	conn, _, err := dialer.Dial(config.RelayURL+"/publish", http.Header{
		"Authorization": {"Bearer " + string(config.RelaySecret)},
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	*backoff = time.Second
	logger.Infof("relay: publishing to %s", config.RelayURL)

	// Reading handles pings and notices the close.
	dead := make(chan struct{})
	go func() {
		defer close(dead)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	write := func(typ int, b []byte) error {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(typ, b)
	}

	var sub *stream.Sub
	var hub *stream.Hub
	var pacer stream.Pacer
	var bundle []byte
	audio := 0
	defer func() {
		if sub != nil {
			sub.Close()
		}
	}()
	// The hub changes with sessions; a second's delay picking it up is fine.
	check := time.NewTicker(time.Second)
	defer check.Stop()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		var frames <-chan stream.Frame
		if sub != nil {
			frames = sub.C
		}
		select {
		case <-stop:
			write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
			return nil
		case <-dead:
			return errors.New("connection closed")
		case <-check.C:
			h := sessions.Hub()
			if h == hub {
				continue
			}
			if sub != nil {
				sub.Close()
				sub = nil
			}
			hub = h
			pacer.Reset()
			bundle, audio = bundle[:0], 0
			if h != nil {
				sub = h.Subscribe()
			}
			if err := write(websocket.TextMessage, []byte(`{"t":"reset"}`)); err != nil {
				return err
			}
		case f, ok := <-frames:
			if !ok {
				sub = nil
				continue
			}
			if !pacer.Keep(f, hub.Since()) {
				continue
			}
			bundle = binary.BigEndian.AppendUint32(bundle, uint32(len(f)))
			bundle = append(bundle, f...)
			if f.Kind() == stream.KindAudio {
				if audio++; audio < maxAudio {
					continue
				}
			}
			if err := write(websocket.BinaryMessage, bundle); err != nil {
				return err
			}
			bundle, audio = bundle[:0], 0
		case <-ping.C:
			if pacer.Dropped > 0 {
				logger.Debugf("relay: dropped %d late frames in 30s, the uplink can't keep up", pacer.Dropped)
				pacer.Dropped = 0
			}
			if err := write(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}
