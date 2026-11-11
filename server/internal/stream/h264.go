package stream

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

const (
	nalIDR = 5
	nalSPS = 7
	nalPPS = 8
	nalAUD = 9
)

// AccessUnit is one encoded frame in Annex B form, starting at its AUD.
type AccessUnit struct {
	Data []byte
	Key  bool
}

// ReadAccessUnits splits an Annex B stream into access units, cut at each
// access unit delimiter (the encoder puts one before every frame: -aud 1, or
// h264_metadata=aud=insert for libx264). It returns when r fails.
func ReadAccessUnits(r io.Reader, emit func(AccessUnit)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	var au bytes.Buffer
	key := false
	flush := func() {
		if au.Len() > 0 {
			emit(AccessUnit{Data: bytes.Clone(au.Bytes()), Key: key})
		}
		au.Reset()
		key = false
	}
	err := splitNALs(br, func(nal []byte) {
		if len(nal) == 0 {
			return
		}
		switch nal[0] & 0x1f {
		case nalAUD:
			flush()
		case nalIDR:
			key = true
		}
		au.Write([]byte{0, 0, 0, 1})
		au.Write(nal)
	})
	flush()
	return err
}

// CheckJoinable reports whether an encoded stream holds at least two
// keyframes, each with the SPS/PPS a viewer joining there needs, and a
// delimiter before every frame.
func CheckJoinable(data []byte) error {
	var aus []AccessUnit
	ReadAccessUnits(bytes.NewReader(data), func(au AccessUnit) { aus = append(aus, au) })
	keys := 0
	for i, au := range aus {
		var types [32]bool
		splitNALs(bufio.NewReader(bytes.NewReader(au.Data)), func(nal []byte) {
			if len(nal) > 0 {
				types[nal[0]&0x1f] = true
			}
		})
		// ReadAccessUnits writes each NAL behind a 4-byte start code
		if len(au.Data) < 5 || au.Data[4]&0x1f != nalAUD {
			return fmt.Errorf("frame %d doesn't start with an access unit delimiter", i)
		}
		if au.Key {
			if !types[nalSPS] || !types[nalPPS] {
				return fmt.Errorf("keyframe %d has no SPS/PPS", i)
			}
			keys++
		}
	}
	if keys < 2 {
		return fmt.Errorf("%d keyframes in %d frames, want 2", keys, len(aus))
	}
	return nil
}

// splitNALs calls fn with every NAL unit payload (without start code).
func splitNALs(br *bufio.Reader, fn func([]byte)) error {
	var nal []byte
	started := false
	zeros := 0
	for {
		b, err := br.ReadByte()
		if err != nil {
			if started {
				fn(trimZeros(nal, zeros))
			}
			return err
		}
		if b == 1 && zeros >= 2 {
			if started {
				fn(nal[:len(nal)-zeros])
			}
			started = true
			nal = nal[:0:0]
			zeros = 0
			continue
		}
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
		if started {
			nal = append(nal, b)
		}
	}
}

func trimZeros(nal []byte, zeros int) []byte {
	if zeros > len(nal) {
		zeros = len(nal)
	}
	return nal[:len(nal)-zeros]
}
