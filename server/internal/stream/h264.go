package stream

import (
	"bufio"
	"bytes"
	"io"
)

const (
	nalIDR = 5
	nalAUD = 9
)

// AccessUnit is one encoded frame in Annex B form, starting at its AUD.
type AccessUnit struct {
	Data []byte
	Key  bool
}

// ReadAccessUnits splits an Annex B stream into access units, cut at each
// access unit delimiter (ffmpeg's h264_metadata=aud=insert puts one before
// every frame). It returns when r fails.
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
