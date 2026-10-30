package stream

import (
	"bytes"
	"encoding/binary"
	"testing"
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

func TestHubGOPReplayAndKeyWait(t *testing.T) {
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
	if f := <-late.C; f[10] != 3 {
		t.Fatal("late sub should get the rest of the GOP")
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
		t.Fatal("resume should replay the GOP from its keyframe")
	}
	if f := <-s.C; f[10] != 2 {
		t.Fatal("resume should replay the whole GOP")
	}
	h.Publish(NewFrame(KindVideo, 0, 0, []byte{3}))
	if f := <-s.C; f[10] != 3 {
		t.Fatal("resumed sub should get live video")
	}
	h.Close()
}
