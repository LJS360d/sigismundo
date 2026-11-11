package runner

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Every encoder setup the machine can run must give a stream viewers can
// join; libx264, the fallback, must run everywhere.
func TestEncodersJoinable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	device := ""
	if len(nodes) > 0 {
		device = nodes[0]
	}
	for _, e := range encoders(device) {
		ok := 0
		for _, args := range e.variants {
			if err := probe(args); err != nil {
				t.Logf("%s %v: %v", e.name, args, err)
				continue
			}
			ok++
		}
		t.Logf("%s: %d of %d setups work here", e.name, ok, len(e.variants))
		if e.name == "libx264" && ok == 0 {
			t.Error("libx264 failed")
		}
	}
}
