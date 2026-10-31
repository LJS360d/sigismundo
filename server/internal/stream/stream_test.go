package stream

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestReadAccessUnits(t *testing.T) {
	var in bytes.Buffer
	nal := func(typ byte, body ...byte) {
		in.Write([]byte{0, 0, 0, 1, typ})
		in.Write(body)
	}
	nal(nalAUD, 0xf0)
	nal(7, 1, 2) // SPS
	nal(nalIDR, 0, 0, 3, 4)
	nal(nalAUD, 0xf0)
	nal(1, 5, 6, 0)

	var aus []AccessUnit
	if err := ReadAccessUnits(&in, func(a AccessUnit) { aus = append(aus, a) }); err == nil {
		t.Fatal("expected EOF")
	}
	if len(aus) != 2 || !aus[0].Key || aus[1].Key {
		t.Fatalf("got %d aus: %+v", len(aus), aus)
	}
	want := []byte{0, 0, 0, 1, nalAUD, 0xf0, 0, 0, 0, 1, 7, 1, 2, 0, 0, 0, 1, nalIDR, 0, 0, 3, 4}
	if !bytes.Equal(aus[0].Data, want) {
		t.Fatalf("au0 %x", aus[0].Data)
	}
	if !bytes.Equal(aus[1].Data, []byte{0, 0, 0, 1, nalAUD, 0xf0, 0, 0, 0, 1, 1, 5, 6}) {
		t.Fatalf("au1 %x", aus[1].Data)
	}
}

func oggPage(packets ...[]byte) []byte {
	var segs, body []byte
	for _, p := range packets {
		n := len(p)
		for n >= 255 {
			segs = append(segs, 255)
			n -= 255
		}
		segs = append(segs, byte(n))
		body = append(body, p...)
	}
	hdr := make([]byte, 27)
	copy(hdr, "OggS")
	binary.LittleEndian.PutUint64(hdr[6:], 0)
	hdr[26] = byte(len(segs))
	return append(append(hdr, segs...), body...)
}

func TestReadOpusPackets(t *testing.T) {
	big := bytes.Repeat([]byte{7}, 300)
	var in bytes.Buffer
	in.Write(oggPage([]byte("OpusHead....")))
	in.Write(oggPage([]byte("OpusTags....")))
	in.Write(oggPage([]byte{1, 2}, big))
	var got [][]byte
	_ = ReadOpusPackets(&in, func(p []byte) { got = append(got, p) })
	if len(got) != 2 || !bytes.Equal(got[0], []byte{1, 2}) || !bytes.Equal(got[1], big) {
		t.Fatalf("got %d packets", len(got))
	}
}

func TestHubKeyReplayAndKeyWait(t *testing.T) {
	h := NewHub()
	early := h.Subscribe()
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{1}))
	h.Publish(NewFrame(KindVideo, FlagKey, 0, []byte{2}))
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{3}))
	h.Publish(NewFrame(KindAudio, 0, 0, []byte{9}))

	if f := <-early.C; f[10] != 2 {
		t.Fatalf("early sub should skip to keyframe, got %d", f[10])
	}
	late := h.Subscribe()
	if f := <-late.C; f[10] != 2 || !f.Key() {
		t.Fatal("late sub should get the cached keyframe first")
	}
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{4}))
	h.Publish(NewFrame(KindVideo, FlagKey, 0, []byte{5}))
	if f := <-late.C; f[10] != 5 {
		t.Fatalf("late sub should skip deltas until the next keyframe, got %d", f[10])
	}
	late.Close()
	late.Close()
	h.Close()
	if _, ok := <-late.C; ok {
		t.Fatal("closed")
	}
}

func TestHubPause(t *testing.T) {
	h := NewHub()
	s := h.Subscribe()
	s.Pause(true)
	h.Publish(NewFrame(KindVideo, FlagKey, 0, []byte{1}))
	h.Publish(NewFrame(KindAudio, 0, 0, []byte{9}))
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{2}))
	if f := <-s.C; f.Kind() != KindAudio {
		t.Fatal("paused sub should only get audio")
	}
	s.Pause(false)
	if f := <-s.C; f[10] != 1 || !f.Key() {
		t.Fatal("resume should send the last keyframe")
	}
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{3}))
	h.Publish(NewFrame(KindVideo, FlagKey, 0, []byte{4}))
	if f := <-s.C; f[10] != 4 {
		t.Fatal("resumed sub should pick up at the next keyframe")
	}
	h.Close()
}

func TestFrameTS(t *testing.T) {
	if ts := NewFrame(KindAudio, 0, 1500*time.Millisecond, nil).TS(); ts != 1500*time.Millisecond {
		t.Fatalf("got %v", ts)
	}
}

func TestPacer(t *testing.T) {
	var p Pacer
	now := time.Second
	fresh, stale := now-MaxLag/2, now-2*MaxLag
	if !p.Keep(NewFrame(KindVideo, 0, fresh, nil), now) {
		t.Fatal("fresh delta should pass")
	}
	if p.Keep(NewFrame(KindVideo, 0, stale, nil), now) {
		t.Fatal("stale delta should drop")
	}
	if p.Keep(NewFrame(KindVideo, 0, fresh, nil), now) {
		t.Fatal("deltas after a drop should wait for a keyframe")
	}
	if !p.Keep(NewFrame(KindAudio, 0, fresh, nil), now) || p.Keep(NewFrame(KindAudio, 0, stale, nil), now) {
		t.Fatal("audio should pass unless stale")
	}
	if !p.Keep(NewFrame(KindVideo, FlagKey, stale, nil), now) || !p.Keep(NewFrame(KindVideo, 0, fresh, nil), now) {
		t.Fatal("a keyframe, even stale, should pass and resume deltas")
	}
	if p.Dropped != 3 {
		t.Fatalf("dropped %d", p.Dropped)
	}
}
