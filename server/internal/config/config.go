package config

import (
	"cmp"
	"crypto/rand"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	ClientID      string
	ClientSecret  string
	OwnerIDs      []string
	Env           string
	DataDir       string
	GamesDir      string
	CatalogPath   string
	WinePrefix    string
	DisplayNum    int
	HostAudio     bool
	ServerAddress string
	SessionSecret []byte
	DevAuth       bool

	// Stream/session settings.
	Width        int
	Height       int
	FPS          int
	VideoBitrate int
	AudioBitrate int
	IdleTimeout  time.Duration
)

func init() {
	log.Println("Initializing config...")
	Env = envOr("GO_ENV", "production")

	ClientID = os.Getenv("CLIENT_ID")
	ClientSecret = os.Getenv("CLIENT_SECRET")
	if ClientSecret == "" {
		log.Println("CLIENT_SECRET not set in the environment, the Activity can't authenticate users")
	}
	for id := range strings.SplitSeq(os.Getenv("OWNER_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			OwnerIDs = append(OwnerIDs, id)
		}
	}
	// Absolute, since Wine runs games from their own directory.
	DataDir = abs(envOr("DATA_DIR", "data"))
	GamesDir = abs(envOr("GAMES_DIR", "/games"))
	CatalogPath = abs(envOr("CATALOG_PATH", filepath.Join(GamesDir, "catalog.yaml")))
	WinePrefix = abs(envOr("WINEPREFIX", filepath.Join(DataDir, "wine")))
	DisplayNum = envInt("DISPLAY_NUM", 99)
	switch os.Getenv("AUDIO_MODE") {
	case "host":
		HostAudio = true
	case "spawn":
	default:
		// No pulseaudio binary usually means a desktop on PipeWire: use its pulse server.
		_, err := exec.LookPath("pulseaudio")
		HostAudio = err != nil
	}
	ServerAddress = envOr("SERVER_ADDRESS", "127.0.0.1:8080")
	if s := os.Getenv("SESSION_SECRET"); s != "" {
		SessionSecret = []byte(s)
	} else {
		SessionSecret = make([]byte, 32)
		rand.Read(SessionSecret)
	}
	DevAuth = Env == "development" && envBool("DEV_AUTH", false)

	Width = envInt("STREAM_WIDTH", 1280)
	Height = envInt("STREAM_HEIGHT", 720)
	FPS = envInt("STREAM_FPS", 30)
	VideoBitrate = envInt("STREAM_VIDEO_KBPS", 2500)
	AudioBitrate = envInt("STREAM_AUDIO_KBPS", 96)
	IdleTimeout = envDuration("IDLE_TIMEOUT", 10*time.Minute)

	log.Println("Config initialized")
}

func IsOwner(id string) bool { return slices.Contains(OwnerIDs, id) }

func abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		log.Fatalf("path %s: %v", p, err)
	}
	return a
}

func envOr(key, def string) string { return cmp.Or(os.Getenv(key), def) }

func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "":
		return def
	case "true", "1":
		return true
	}
	return false
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}

func envDuration(key string, def time.Duration) time.Duration {
	v, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}
