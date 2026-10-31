package stream

import "time"

// MaxLag: frames older than this when their turn to be written comes are
// dropped instead, so a slow link costs frames rather than piling up delay.
const MaxLag = 250 * time.Millisecond

// Pacer decides, frame by frame, what a writer still sends. Keyframes always
// go out: they are what recovers from a drop. After a dropped video frame the
// deltas are useless until one. Late audio goes too, since the player times
// video by it.
type Pacer struct {
	late    bool
	Dropped int
}

// Keep reports whether f is still worth writing at stream time now.
func (p *Pacer) Keep(f Frame, now time.Duration) bool {
	stale := now-f.TS() > MaxLag
	switch {
	case f.Kind() == KindVideo && f.Key():
		p.late = false
	case f.Kind() == KindVideo && (p.late || stale):
		p.late = true
		p.Dropped++
		return false
	case stale:
		p.Dropped++
		return false
	}
	return true
}

// Reset starts over for a new stream.
func (p *Pacer) Reset() { p.late = false }
