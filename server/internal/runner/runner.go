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

// dxvkDir holds DXVK's x64 DLLs (the image). wined3d's GL path renders
// D3D11 games black on llvmpipe; DXVK on lavapipe works.
const dxvkDir = "/opt/dxvk"

const (
	// how long the game exe may take to show up as a process
	startGrace = 90 * time.Second
)

type Run struct {
	Hub   *stream.Hub
	Input *input.Injector
	// Done closes when the game exits or a process of the pipeline dies.
	Done chan struct{}
	Err  error

	display string
	sink    string
	runtime string
	env     []string
	unload  func()
	mu      sync.Mutex
	procs   []*proc
	once    sync.Once
}

// Start brings the whole pipeline up for entry; status reports progress for viewers.
func Start(entry catalog.Entry, status func(string)) (*Run, error) {
	runtime, err := os.MkdirTemp("", "sigismundo-run-")
	if err != nil {
		return nil, err
	}
	r := &Run{
		Hub:     stream.NewHub(),
		Done:    make(chan struct{}),
		display: fmt.Sprintf(":%d", config.DisplayNum),
		sink:    "game",
		runtime: runtime,
	}
	pulse := filepath.Join(runtime, "pulse.sock")
	if config.HostAudio {
		r.sink = "sigismundo_" + strconv.Itoa(os.Getpid())
	}
	overrides := "winemenubuilder.exe=d;mscoree=;mshtml="
	_, err = os.Stat(dxvkDir)
	dxvk := err == nil
	if dxvk {
		overrides += ";d3d11,dxgi=n"
	}
	r.env = append(os.Environ(),
		"DISPLAY="+r.display,
		"PULSE_SINK="+r.sink,
		"WINEPREFIX="+config.WinePrefix,
		"WINEDLLOVERRIDES="+overrides,
	)
	if os.Getenv("WINEDEBUG") == "" {
		r.env = append(r.env, "WINEDEBUG=-all")
	}
	// Rendering past the stream rate only burns CPU (or GPU).
	if os.Getenv("DXVK_FRAME_RATE") == "" {
		r.env = append(r.env, "DXVK_FRAME_RATE="+strconv.Itoa(config.FPS))
	}
	r.env = append(r.env, DetectGPU().Env...)
	if !config.HostAudio {
		r.env = append(r.env, "XDG_RUNTIME_DIR="+runtime, "PULSE_SERVER=unix:"+pulse)
	}
	ok := false
	defer func() {
		if !ok {
			r.Stop()
		}
	}()

	status("Starting display")
	os.Remove(fmt.Sprintf("/tmp/.X%d-lock", config.DisplayNum))
	if _, err := r.spawn("xvfb", "", nil, "Xvfb", r.display,
		"-screen", "0", fmt.Sprintf("%dx%dx24", config.Width, config.Height),
		"-nolisten", "tcp", "-noreset"); err != nil {
		return nil, err
	}
	if err := waitPath(fmt.Sprintf("/tmp/.X11-unix/X%d", config.DisplayNum), 10*time.Second); err != nil {
		return nil, fmt.Errorf("xvfb: %w", err)
	}

	status("Starting audio")
	if config.HostAudio {
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

	if _, err := os.Stat(filepath.Join(config.WinePrefix, "system.reg")); err != nil {
		status("Preparing Wine prefix (first run, can take a minute)")
		if err := os.MkdirAll(config.WinePrefix, 0o755); err != nil {
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
	if dxvk {
		if err := installDXVK(config.WinePrefix); err != nil {
			return nil, fmt.Errorf("dxvk: %w", err)
		}
	}

	r.Input, err = input.Open(r.display)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}

	status("Launching " + entry.Name)
	exe := entry.Exe
	args := append([]string{"explorer", fmt.Sprintf("/desktop=sigismundo,%dx%d", config.Width, config.Height), exe}, entry.Args...)
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
	ok = true
	return r, nil
}

func (r *Run) startVideo() error {
	args := []string{"-loglevel", "error", "-nostdin",
		"-f", "x11grab", "-draw_mouse", "1", "-framerate", strconv.Itoa(config.FPS),
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height), "-i", r.display}
	args = append(args, DetectGPU().Args...)
	args = append(args, "-flush_packets", "1", "-f", "h264", "-")
	out, err := r.spawnPipe("ffmpeg-video", "ffmpeg", args...)
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

// installDXVK copies DXVK's DLLs into the prefix each launch, so an image
// update reaches existing prefixes.
func installDXVK(prefix string) error {
	sys := filepath.Join(prefix, "drive_c", "windows", "system32")
	for _, dll := range []string{"d3d11.dll", "dxgi.dll"} {
		b, err := os.ReadFile(filepath.Join(dxvkDir, dll))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sys, dll), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
