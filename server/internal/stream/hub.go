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

// Sub receives frames. A subscriber that falls behind loses frames; for
// video it then waits for the next keyframe so the decoder never sees a gap.
type Sub struct {
	C         chan Frame
	needKey   bool
	hub       *Hub
	closeOnce sync.Once
}

func (s *Sub) Close() {
	s.closeOnce.Do(func() { s.hub.remove(s) })
}

type Hub struct {
	mu    sync.Mutex
	subs  map[*Sub]struct{}
	gop   []Frame
	start time.Time
}

func NewHub() *Hub {
	return &Hub{subs: map[*Sub]struct{}{}, start: time.Now()}
}

// Since is the stream clock used for frame timestamps.
func (h *Hub) Since() time.Duration { return time.Since(h.start) }

// Subscribe replays the current GOP so the viewer can decode right away.
func (h *Hub) Subscribe() *Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Sub{C: make(chan Frame, max(256, len(h.gop)+64)), hub: h}
	for _, f := range h.gop {
		s.C <- f
	}
	s.needKey = len(h.gop) == 0
	h.subs[s] = struct{}{}
	return s
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
	h.gop = nil
}

func (h *Hub) Publish(f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f.Kind() == KindVideo {
		if f.Key() {
			h.gop = h.gop[:0:0]
		}
		if f.Key() || len(h.gop) > 0 {
			h.gop = append(h.gop, f)
		}
	}
	for s := range h.subs {
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
