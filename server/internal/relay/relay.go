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
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

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
	// maxLag is how late a frame may still go out. Higher than the direct
	// stream's: a stall on the uplink (a lost packet waiting for its
	// retransmit) is over once the backlog flushes, and the player skips
	// ahead through it. Dropping instead would freeze video until the next
	// keyframe on top of the stall.
	maxLag = time.Second
	// statsEvery: how often the uplink's health is logged
	statsEvery    = 30 * time.Second
	tcpCongestion = 13 // TCP_CONGESTION
)

// dialer keeps the TCP connection in *tcp: under TLS (wss) the WebSocket's
// own NetConn is the TLS wrapper, which has no kernel stats to read.
func dialer(tcp **net.TCPConn) *websocket.Dialer {
	return &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		WriteBufferSize:  64 << 10,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if tc, ok := c.(*net.TCPConn); ok {
				tc.SetWriteBuffer(sendBuffer)
				useBBR(tc)
				*tcp = tc
			}
			return c, err
		},
	}
}

// useBBR switches the socket to BBR congestion control when the kernel has
// it: CUBIC halves its rate on every lost packet, which on a lossy home
// uplink means stalls; BBR paces by measured bandwidth instead.
func useBBR(tc *net.TCPConn) {
	raw, err := tc.SyscallConn()
	if err != nil {
		return
	}
	var serr error
	raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptString(int(fd), syscall.IPPROTO_TCP, tcpCongestion, "bbr")
	})
	if serr != nil {
		logger.Infof("relay: BBR unavailable (%v), staying on the default congestion control; `modprobe tcp_bbr` on the host enables it", serr)
	} else {
		logger.Infof("relay: using BBR congestion control")
	}
}

// tcpInfo reads the kernel's view of the connection: round trip and
// retransmits tell a lossy uplink from a slow one.
func tcpInfo(tc *net.TCPConn) (*syscall.TCPInfo, bool) {
	if tc == nil {
		return nil, false
	}
	raw, err := tc.SyscallConn()
	if err != nil {
		return nil, false
	}
	var info syscall.TCPInfo
	var errno syscall.Errno
	raw.Control(func(fd uintptr) {
		n := uint32(unsafe.Sizeof(info))
		_, _, errno = syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.IPPROTO_TCP, syscall.TCP_INFO,
			uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&n)), 0)
	})
	return &info, errno == 0
}

// load is the host's 1-minute load average, as a string; a box with fewer
// cores than that is CPU starved, which delays everything, frames included.
func load() string {
	b, _ := os.ReadFile("/proc/loadavg")
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "?"
	}
	return f[0]
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
	var tcp *net.TCPConn
	conn, _, err := dialer(&tcp).Dial(config.RelayURL+"/publish", http.Header{
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
	// Since the last stats: the longest a write blocked (a stalled uplink),
	// the latest a frame was by the time its write began (delay inside the
	// server: a starved CPU, a slow encoder) and the biggest message.
	var slowest, lateBefore time.Duration
	biggest := 0
	write := func(typ int, b []byte) error {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		t := time.Now()
		err := conn.WriteMessage(typ, b)
		slowest = max(slowest, time.Since(t))
		biggest = max(biggest, len(b))
		return err
	}
	var lastRetrans uint32
	stats := time.NewTicker(statsEvery)
	defer stats.Stop()

	var sub *stream.Sub
	var hub *stream.Hub
	pacer := stream.Pacer{Max: maxLag}
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
			lateBefore = max(lateBefore, hub.Since()-f.TS())
			if err := write(websocket.BinaryMessage, bundle); err != nil {
				return err
			}
			bundle, audio = bundle[:0], 0
		case <-stats.C:
			if hub != nil {
				link := "tcp stats unavailable"
				if info, ok := tcpInfo(tcp); ok {
					link = fmt.Sprintf("rtt %dms ±%d, retransmits +%d", info.Rtt/1000, info.Rttvar/1000, info.Total_retrans-lastRetrans)
					lastRetrans = info.Total_retrans
				}
				logger.Infof("relay uplink: %s, longest write %s, latest frame before writing %s, biggest message %dKB, dropped %d late frames, load %s",
					link, slowest.Round(time.Millisecond), lateBefore.Round(time.Millisecond), biggest>>10, pacer.Dropped, load())
			}
			slowest, lateBefore, biggest, pacer.Dropped = 0, 0, 0, 0
		case <-ping.C:
			if err := write(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}
