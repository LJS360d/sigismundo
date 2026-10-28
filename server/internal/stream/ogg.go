package stream

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

var errBadPage = errors.New("ogg: bad page")

// ReadOpusPackets demuxes a single-stream Ogg Opus file and calls emit with
// each audio packet, skipping the OpusHead and OpusTags headers.
func ReadOpusPackets(r io.Reader, emit func([]byte)) error {
	br := bufio.NewReader(r)
	var hdr [27]byte
	var packet []byte
	headers := 2
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return err
		}
		if !bytes.Equal(hdr[:4], []byte("OggS")) {
			return errBadPage
		}
		segs := make([]byte, hdr[26])
		if _, err := io.ReadFull(br, segs); err != nil {
			return err
		}
		total := 0
		for _, s := range segs {
			total += int(s)
		}
		body := make([]byte, total)
		if _, err := io.ReadFull(br, body); err != nil {
			return err
		}
		off := 0
		for _, s := range segs {
			packet = append(packet, body[off:off+int(s)]...)
			off += int(s)
			if s == 255 {
				continue
			}
			if headers > 0 {
				headers--
			} else if len(packet) > 0 {
				emit(packet)
			}
			packet = nil
		}
	}
}
