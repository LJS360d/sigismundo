// Package stream turns the encoder output into frames and fans them out to
// WebSocket viewers.
package stream

import (
	"encoding/binary"
	"sync"
	"time"
)

const (
	KindVideo byte = 1
	KindAudio byte = 2

	FlagKey byte = 1
)

// Frame is the wire format of one binary WebSocket message:
// kind u8 | flags u8 | timestamp u64 BE (µs since stream start) | payload.
type Frame []byte

func NewFrame(kind, flags byte, ts time.Duration, payload []byte) Frame {
	f := make([]byte, 10+len(payload))
	f[0] = kind
	f[1] = flags
	binary.BigEndian.PutUint64(f[2:10], uint64(ts.Microseconds()))
	copy(f[10:], payload)
	return f
}

func (f Frame) Kind() byte { return f[0] }
func (f Frame) Key() bool  { return f[1]&FlagKey != 0 }

// TS is the frame's stream-clock timestamp.
func (f Frame) TS() time.Duration {
	return time.Duration(binary.BigEndian.Uint64(f[2:10])) * time.Microsecond
}

// Sub receives frames. A subscriber that falls behind loses frames; for
// video it then waits for the next keyframe so the decoder never sees a gap.
type Sub struct {
	C         chan Frame
	needKey   bool
	paused    bool
	hub       *Hub
	closeOnce sync.Once
}

func (s *Sub) Close() {
	s.closeOnce.Do(func() { s.hub.remove(s) })
}

// Pause stops video to the subscriber while audio keeps flowing; resuming
// sends the last keyframe so a picture is back at once.
func (s *Sub) Pause(p bool) {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; !ok || s.paused == p {
		return
	}
	s.paused = p
	if !p {
		h.sendKey(s)
	}
}

type Hub struct {
	mu    sync.Mutex
	subs  map[*Sub]struct{}
	key   Frame
	start time.Time
}

func NewHub() *Hub {
	return &Hub{subs: map[*Sub]struct{}{}, start: time.Now()}
}

// Since is the stream clock used for frame timestamps.
func (h *Hub) Since() time.Duration { return time.Since(h.start) }

// Subscribe sends the last keyframe so the viewer has a picture right away.
func (h *Hub) Subscribe() *Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Sub{C: make(chan Frame, 256), hub: h}
	h.sendKey(s)
	h.subs[s] = struct{}{}
	return s
}

// sendKey gives s the last keyframe alone: replaying its whole GOP would
// burst seconds of video onto an uplink that may have no room for it. Video
// then resumes at the next keyframe, the deltas in between being useless
// without the frames they reference.
func (h *Hub) sendKey(s *Sub) {
	s.needKey = true
	if h.key != nil {
		select {
		case s.C <- h.key:
		default:
		}
	}
}

func (h *Hub) remove(s *Sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.C)
	}
}

// Close disconnects every subscriber.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		delete(h.subs, s)
		close(s.C)
	}
	h.key = nil
}

func (h *Hub) Publish(f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f.Kind() == KindVideo && f.Key() {
		h.key = f
	}
	for s := range h.subs {
		if f.Kind() == KindVideo && s.paused {
			continue
		}
		if f.Kind() == KindVideo && s.needKey {
			if !f.Key() {
				continue
			}
			s.needKey = false
		}
		select {
		case s.C <- f:
		default:
			if f.Kind() == KindVideo {
				s.needKey = true
			}
		}
	}
}
