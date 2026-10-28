// Package runner owns the processes of one session: Xvfb, PulseAudio, the
// game under Wine and the two ffmpeg encoders feeding the stream hub.
package runner

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sigismundo/internal/catalog"
	"sigismundo/internal/config"
	"sigismundo/internal/input"
	"sigismundo/internal/logger"
	"sigismundo/internal/stream"
)

const (
	// how long the game exe may take to show up as a process
	startGrace = 90 * time.Second
	navDelay   = 15 * time.Second
	navStep    = 400 * time.Millisecond
)

type Spec struct {
	Pick       catalog.Pick
	WinePrefix string
	// Display is the X display number Xvfb takes.
	Display int
	// HostAudio uses the PulseAudio/PipeWire server already running (through
	// pactl) instead of spawning a private pulseaudio.
	HostAudio bool
}

type Run struct {
	Hub   *stream.Hub
	Input *input.Injector
	// Done closes when the game exits or a process of the pipeline dies.
	Done chan struct{}
	Err  error

	spec    Spec
	display string
	sink    string
	runtime string
	env     []string
	unload  func()
	mu      sync.Mutex
	procs   []*proc
	once    sync.Once
}

// Start brings the whole pipeline up; status reports progress for viewers.
func Start(spec Spec, status func(string)) (*Run, error) {
	runtime, err := os.MkdirTemp("", "sigismundo-run-")
	if err != nil {
		return nil, err
	}
	r := &Run{
		Hub:     stream.NewHub(),
		Done:    make(chan struct{}),
		spec:    spec,
		display: fmt.Sprintf(":%d", spec.Display),
		sink:    "game",
		runtime: runtime,
	}
	pulse := filepath.Join(runtime, "pulse.sock")
	if spec.HostAudio {
		r.sink = "sigismundo_" + strconv.Itoa(os.Getpid())
	}
	r.env = append(os.Environ(),
		"DISPLAY="+r.display,
		"PULSE_SINK="+r.sink,
		"WINEPREFIX="+spec.WinePrefix,
		"WINEDEBUG=-all",
		"WINEDLLOVERRIDES=winemenubuilder.exe=d;mscoree=;mshtml=",
	)
	if !spec.HostAudio {
		r.env = append(r.env, "XDG_RUNTIME_DIR="+runtime, "PULSE_SERVER=unix:"+pulse)
	}
	ok := false
	defer func() {
		if !ok {
			r.Stop()
		}
	}()

	status("Starting display")
	os.Remove(fmt.Sprintf("/tmp/.X%d-lock", spec.Display))
	if _, err := r.spawn("xvfb", "", nil, "Xvfb", r.display,
		"-screen", "0", fmt.Sprintf("%dx%dx24", config.Width, config.Height),
		"-nolisten", "tcp", "-noreset"); err != nil {
		return nil, err
	}
	if err := waitPath(fmt.Sprintf("/tmp/.X11-unix/X%d", spec.Display), 10*time.Second); err != nil {
		return nil, fmt.Errorf("xvfb: %w", err)
	}

	status("Starting audio")
	if spec.HostAudio {
		if err := r.hostSink(); err != nil {
			return nil, err
		}
	} else {
		if _, err := r.spawn("pulse", "", nil, "pulseaudio", "-n", "--daemonize=no", "--exit-idle-time=-1",
			"--disallow-exit", "--disable-shm",
			"--load=module-native-protocol-unix auth-anonymous=1 socket="+pulse,
			"--load=module-null-sink sink_name="+r.sink+" rate=48000 channels=2",
		); err != nil {
			return nil, err
		}
		if err := waitPath(pulse, 10*time.Second); err != nil {
			return nil, fmt.Errorf("pulseaudio: %w", err)
		}
	}

	if _, err := os.Stat(filepath.Join(spec.WinePrefix, "system.reg")); err != nil {
		status("Preparing Wine prefix (first run, can take a minute)")
		if err := os.MkdirAll(spec.WinePrefix, 0o755); err != nil {
			return nil, err
		}
		boot, err := r.spawn("wineboot", "", nil, "wineboot", "--init")
		if err != nil {
			return nil, err
		}
		<-boot.exited
		if boot.err != nil {
			return nil, fmt.Errorf("wineboot: %w", boot.err)
		}
		r.wineserver("-w")
	}

	r.Input, err = input.Open(r.display)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}

	status("Launching " + spec.Pick.Name())
	exe := spec.Pick.Entry.Exe
	args := append([]string{"explorer", fmt.Sprintf("/desktop=sigismundo,%dx%d", config.Width, config.Height), exe}, spec.Pick.Entry.Args...)
	if _, err := r.spawn("wine", filepath.Dir(exe), nil, "wine", args...); err != nil {
		return nil, err
	}

	if err := r.startVideo(); err != nil {
		return nil, err
	}
	if err := r.startAudio(); err != nil {
		return nil, err
	}

	go r.watchGame(filepath.Base(exe))
	if spec.Pick.Game != nil && len(spec.Pick.Game.Nav) > 0 {
		go r.navigate(status)
	}
	ok = true
	return r, nil
}

func (r *Run) startVideo() error {
	kbps := strconv.Itoa(config.VideoBitrate) + "k"
	out, err := r.spawnPipe("ffmpeg-video", "ffmpeg", "-loglevel", "error", "-nostdin",
		"-f", "x11grab", "-draw_mouse", "1", "-framerate", strconv.Itoa(config.FPS),
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height), "-i", r.display,
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-pix_fmt", "yuv420p",
		"-b:v", kbps, "-maxrate", kbps, "-bufsize", strconv.Itoa(config.VideoBitrate/2)+"k",
		"-g", strconv.Itoa(config.FPS*2), "-bf", "0", "-x264-params", "repeat-headers=1",
		"-bsf:v", "h264_metadata=aud=insert",
		"-flush_packets", "1", "-f", "h264", "-")
	if err != nil {
		return err
	}
	go func() {
		defer out.Close()
		err := stream.ReadAccessUnits(out, func(au stream.AccessUnit) {
			var flags byte
			if au.Key {
				flags = stream.FlagKey
			}
			r.Hub.Publish(stream.NewFrame(stream.KindVideo, flags, r.Hub.Since(), au.Data))
		})
		r.finish(fmt.Errorf("video encoder stopped: %w", err))
	}()
	return nil
}

// hostSink adds a null sink to the host's sound server for this run, so the
// game is captured without playing on the host's speakers.
func (r *Run) hostSink() error {
	out, err := exec.Command("pactl", "load-module", "module-null-sink",
		"sink_name="+r.sink, "rate=48000", "channels=2").Output()
	if err != nil {
		return fmt.Errorf("pactl load-module: %w", err)
	}
	module := strings.TrimSpace(string(out))
	r.unload = func() {
		if err := exec.Command("pactl", "unload-module", module).Run(); err != nil {
			logger.Warnf("pactl unload-module %s: %v", module, err)
		}
	}
	return nil
}

func (r *Run) startAudio() error {
	out, err := r.spawnPipe("ffmpeg-audio", "ffmpeg", "-loglevel", "error", "-nostdin",
		"-f", "pulse", "-fragment_size", "1920", "-i", r.sink+".monitor",
		"-ac", "2", "-ar", "48000",
		"-c:a", "libopus", "-b:a", strconv.Itoa(config.AudioBitrate)+"k", "-application", "lowdelay", "-frame_duration", "20",
		"-page_duration", "20000", "-flush_packets", "1", "-f", "ogg", "-")
	if err != nil {
		return err
	}
	go func() {
		defer out.Close()
		err := stream.ReadOpusPackets(out, func(p []byte) {
			r.Hub.Publish(stream.NewFrame(stream.KindAudio, 0, r.Hub.Since(), p))
		})
		r.finish(fmt.Errorf("audio encoder stopped: %w", err))
	}()
	return nil
}

// navigate types the game's nav macro once the pack menu had time to load.
// A "wait:<duration>" step pauses instead of pressing a key.
func (r *Run) navigate(status func(string)) {
	delay := r.spec.Pick.Entry.NavDelay
	if delay == 0 {
		delay = navDelay
	}
	select {
	case <-time.After(delay):
	case <-r.Done:
		return
	}
	status("Opening " + r.spec.Pick.Game.Name)
	for _, step := range r.spec.Pick.Game.Nav {
		if d, ok := strings.CutPrefix(step, "wait:"); ok {
			if wait, err := time.ParseDuration(d); err == nil {
				time.Sleep(wait)
			}
			continue
		}
		if err := r.Input.Tap(step); err != nil {
			logger.Warnf("nav %s: %v", r.spec.Pick.Key(), err)
		}
		time.Sleep(navStep)
	}
	status("")
}

// watchGame ends the run once the game process has come and gone. Wine
// launchers often hand off to a child process, so it looks for the exe name
// among all processes instead of waiting on the wine command.
func (r *Run) watchGame(exe string) {
	exe = strings.ToLower(exe)
	started := time.Now()
	seen := false
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Done:
			return
		case <-t.C:
		}
		alive := processRunning(exe)
		switch {
		case alive:
			seen = true
		case seen:
			r.finish(nil)
			return
		case time.Since(started) > startGrace:
			r.finish(errors.New("the game never started"))
			return
		}
	}
}

func processRunning(exe string) bool {
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err != nil || len(b) == 0 {
			continue
		}
		argv0, _, _ := strings.Cut(string(b), "\x00")
		argv0 = strings.ToLower(strings.ReplaceAll(argv0, `\`, "/"))
		if filepath.Base(argv0) == exe {
			return true
		}
	}
	return false
}

func (r *Run) finish(err error) {
	r.once.Do(func() {
		r.Err = err
		close(r.Done)
	})
}

// Stop kills the whole pipeline and waits for it. Safe to call repeatedly.
func (r *Run) Stop() {
	r.finish(nil)
	r.mu.Lock()
	procs, unload := r.procs, r.unload
	r.procs, r.unload = nil, nil
	r.mu.Unlock()
	if unload != nil {
		defer unload()
	}
	if len(procs) == 0 {
		return
	}
	if r.Input != nil {
		r.Input.Close()
	}
	r.wineserver("-k")
	for i := len(procs) - 1; i >= 0; i-- {
		procs[i].kill()
	}
	r.Hub.Close()
	os.RemoveAll(r.runtime)
}

func (r *Run) wineserver(flag string) {
	cmd := exec.Command("wineserver", flag)
	cmd.Env = r.env
	done := make(chan struct{})
	go func() {
		cmd.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
}

type proc struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error
}

func (p *proc) kill() {
	pgid := p.cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(3 * time.Second):
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-p.exited
	}
}

// spawn starts bin in its own process group, logging its stderr (and its
// stdout unless one is given).
func (r *Run) spawn(name, dir string, stdout *os.File, bin string, args ...string) (*proc, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = r.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = cmd.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	p := &proc{cmd: cmd, exited: make(chan struct{})}
	go func() {
		logLines(name, stderr)
		p.err = cmd.Wait()
		close(p.exited)
	}()
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
	return p, nil
}

// spawnPipe returns the process stdout, which hits EOF when it exits.
func (r *Run) spawnPipe(name, bin string, args ...string) (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	_, err = r.spawn(name, "", pw, bin, args...)
	pw.Close()
	if err != nil {
		pr.Close()
		return nil, err
	}
	return pr, nil
}

func logLines(name string, rd io.Reader) {
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		logger.Debugf("[%s] %s", name, sc.Text())
	}
}

func waitPath(p string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear within %s", p, timeout)
}
