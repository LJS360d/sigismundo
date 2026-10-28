package runner

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"sigismundo/internal/config"
	"sigismundo/internal/logger"
)

// GPU is what the machine offers for rendering games and encoding video,
// resolved once from GPU_DEVICE, GPU_RENDER and VIDEO_ENCODER.
type GPU struct {
	Device string // render node, empty for none
	Driver string // kernel driver of Device: i915, xe, amdgpu, nvidia...
	Render bool   // games render on Device instead of lavapipe
	// Env is added to each session's environment.
	Env []string
	// Encoder holds ffmpeg's video encoder args, for bgr0 input from x11grab.
	Encoder string
	Args    []string
}

var (
	gpuOnce sync.Once
	gpu     GPU
)

// DetectGPU resolves the setup on first use and logs it.
func DetectGPU() *GPU {
	gpuOnce.Do(func() {
		gpu.Device = findDevice()
		if gpu.Device != "" {
			gpu.Driver = sysfs(gpu.Device, "device/driver")
		}
		gpu.Render = renderOnGPU(gpu.Device, gpu.Driver)
		gpu.Env = vulkanEnv(gpu.Device, gpu.Render)
		gpu.Encoder, gpu.Args = pickEncoder(gpu.Device, gpu.Driver)
		dev := cmp.Or(gpu.Device, "none")
		if gpu.Driver != "" {
			dev += " (" + gpu.Driver + ")"
		}
		logger.Infof("gpu: device %s, render on %s, encoder %s", dev,
			map[bool]string{true: "gpu", false: "cpu (lavapipe)"}[gpu.Render], gpu.Encoder)
		logger.Debugf("gpu: env %v, encoder args %v", gpu.Env, gpu.Args)
	})
	return &gpu
}

func findDevice() string {
	switch d := config.GPUDevice; d {
	case "none", "off", "false":
		return ""
	case "auto", "":
		nodes, _ := filepath.Glob("/dev/dri/renderD*")
		for _, n := range nodes {
			if accessible(n) {
				return n
			}
		}
		if len(nodes) > 0 {
			logger.Warnf("gpu: %s not accessible by uid %d, add its group to the container (stat -c %%g %s)",
				strings.Join(nodes, ", "), os.Getuid(), nodes[0])
		}
		return ""
	default:
		if !accessible(d) {
			logger.Warnf("gpu: GPU_DEVICE %s missing or not accessible by uid %d", d, os.Getuid())
			return ""
		}
		return d
	}
}

func accessible(node string) bool {
	f, err := os.OpenFile(node, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// sysfs reads attr of a DRM node; a symlink resolves to its base name.
func sysfs(node, attr string) string {
	p := filepath.Join("/sys/class/drm", filepath.Base(node), attr)
	if l, err := os.Readlink(p); err == nil {
		return filepath.Base(l)
	}
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

// renderOnGPU: Mesa drivers present to Xvfb through the sw WSI path; the
// proprietary NVIDIA driver can't, so auto leaves it on lavapipe.
func renderOnGPU(device, driver string) bool {
	if device == "" {
		return false
	}
	switch config.GPURender {
	case "true", "1", "on":
		return true
	case "false", "0", "off":
		return false
	}
	return driver != "nvidia"
}

// vulkanEnv steers DXVK's Vulkan device choice. Variables already set in
// the server's environment win.
func vulkanEnv(device string, render bool) []string {
	var env []string
	set := func(k, v string) {
		if _, ok := os.LookupEnv(k); !ok && v != "" {
			env = append(env, k+"="+v)
		}
	}
	if !render {
		lvp, _ := filepath.Glob("/usr/share/vulkan/icd.d/lvp_icd*.json")
		set("VK_DRIVER_FILES", strings.Join(lvp, ":"))
		return env
	}
	// Xvfb has no DRI3, so Mesa won't present from a GPU and DXVK falls
	// back to lavapipe. The sw WSI path renders on the GPU and copies
	// finished frames to the X server instead.
	set("MESA_VK_WSI_DEBUG", "sw")
	// That path never completes present ids (Mesa only does on DRI3), yet
	// hardware drivers still offer VK_KHR_present_wait, so DXVK's wait on
	// vsync'd (FIFO) presents never returns and the game freezes. Sync
	// interval 0 keeps DXVK on immediate presents, which it doesn't wait on;
	// DXVK_FRAME_RATE still paces frames.
	set("DXVK_CONFIG", "dxgi.syncInterval = 0")
	// Pin the device on multi-GPU hosts (Mesa's device-select layer).
	vendor, id := sysfs(device, "device/vendor"), sysfs(device, "device/device")
	if vendor != "" && id != "" {
		set("MESA_VK_DEVICE_SELECT", strings.TrimPrefix(vendor, "0x")+":"+strings.TrimPrefix(id, "0x"))
	}
	return env
}

type encoder struct {
	name     string
	variants [][]string // tried in order, the first that encodes wins
}

func encoders(device string) []encoder {
	kbps := strconv.Itoa(config.VideoBitrate) + "k"
	rate := []string{"-b:v", kbps, "-maxrate", kbps, "-bufsize", strconv.Itoa(config.VideoBitrate/2) + "k",
		"-g", strconv.Itoa(config.FPS * 2), "-bf", "0"}
	with := func(args ...string) []string { return append(args, rate...) }
	var vaapi [][]string
	// Plain, then low-power (all some Intel drivers offer); color conversion
	// on the GPU, then on the CPU. async_depth 1 keeps one frame in flight
	// instead of two, a frame less of latency.
	for _, lp := range []string{"0", "1"} {
		for _, vf := range []string{"hwupload,scale_vaapi=format=nv12", "format=nv12,hwupload"} {
			vaapi = append(vaapi, with("-vaapi_device", device, "-vf", vf, "-c:v", "h264_vaapi", "-low_power", lp,
				"-async_depth", "1", "-profile:v", "constrained_baseline", "-rc_mode", "CBR", "-aud", "1"))
		}
	}
	return []encoder{
		{"h264_nvenc", [][]string{with("-c:v", "h264_nvenc", "-preset", "p1", "-tune", "ull", "-zerolatency", "1",
			"-profile:v", "baseline", "-rc", "cbr", "-aud", "1")}},
		{"h264_vaapi", vaapi},
		{"libx264", [][]string{with("-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
			"-profile:v", "baseline", "-pix_fmt", "yuv420p", "-x264-params", "repeat-headers=1",
			"-bsf:v", "h264_metadata=aud=insert")}},
	}
}

// pickEncoder probes VIDEO_ENCODER, or under auto NVENC (NVIDIA only) then
// VAAPI, falling back to libx264.
func pickEncoder(device, driver string) (string, []string) {
	all := encoders(device)
	fallback := all[len(all)-1]
	want := config.VideoEncoder
	_, nvctl := os.Stat("/dev/nvidiactl")
	for _, e := range all[:len(all)-1] {
		switch {
		case want == e.name:
		case want != "auto" && want != "":
			continue
		case e.name == "h264_nvenc" && driver != "nvidia" && nvctl != nil:
			continue
		case e.name == "h264_vaapi" && device == "":
			continue
		}
		for _, args := range e.variants {
			if err := probe(args); err != nil {
				logger.Debugf("gpu: %s %v: %v", e.name, args, err)
				continue
			}
			return e.name, args
		}
		logger.Warnf("gpu: %s failed a test encode (details at debug level)", e.name)
	}
	if want != "auto" && want != "" && want != fallback.name {
		logger.Warnf("gpu: VIDEO_ENCODER %s unusable, using %s", want, fallback.name)
	}
	return fallback.name, fallback.variants[0]
}

// probe encodes a few frames shaped like x11grab's output.
func probe(enc []string) error {
	args := append([]string{"-loglevel", "error", "-nostdin", "-f", "lavfi",
		"-i", fmt.Sprintf("color=s=%dx%d:r=%d:d=0.2,format=bgr0", config.Width, config.Height, config.FPS)}, enc...)
	out, err := exec.Command("ffmpeg", append(args, "-f", "null", "-")...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
