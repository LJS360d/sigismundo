// Package catalog loads the hand-written manifest of launchable games.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Entry is one executable: a pack with several games, or a single game.
type Entry struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	// Exe is absolute, or relative to the catalog file's directory.
	Exe  string   `yaml:"exe" json:"-"`
	Args []string `yaml:"args" json:"-"`
	// NavDelay is how long after launch the menu accepts nav input.
	NavDelay time.Duration `yaml:"nav_delay" json:"-"`
	Games    []Game        `yaml:"games" json:"games,omitempty"`
}

type Game struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	// Nav is the key sequence from the pack menu to the game; empty stops at the menu.
	Nav []string `yaml:"nav" json:"-"`
}

type Catalog struct {
	Entries []Entry
}

// Pick is a resolved selection: an entry, and optionally one of its games.
type Pick struct {
	Entry *Entry
	Game  *Game
}

func (p Pick) Key() string {
	if p.Game != nil {
		return p.Entry.ID + "/" + p.Game.ID
	}
	return p.Entry.ID
}

func (p Pick) Name() string {
	if p.Game != nil {
		return p.Game.Name + " (" + p.Entry.Name + ")"
	}
	return p.Entry.Name
}

// Load reads the manifest and drops entries whose exe is missing. The second
// return lists the dropped entries for logging.
func Load(path string) (*Catalog, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return parse(b, filepath.Dir(path), func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

func parse(b []byte, dir string, exists func(string) bool) (*Catalog, []string, error) {
	var entries []Entry
	if err := yaml.Unmarshal(b, &entries); err != nil {
		return nil, nil, fmt.Errorf("catalog: %w", err)
	}
	c := &Catalog{}
	var missing []string
	seen := map[string]bool{}
	for _, e := range entries {
		if e.ID == "" || e.Exe == "" || strings.Contains(e.ID, "/") {
			return nil, nil, fmt.Errorf("catalog: entry %q needs an id without '/' and an exe", e.Name)
		}
		if seen[e.ID] {
			return nil, nil, fmt.Errorf("catalog: duplicate id %q", e.ID)
		}
		seen[e.ID] = true
		if e.Name == "" {
			e.Name = e.ID
		}
		if !filepath.IsAbs(e.Exe) {
			e.Exe = filepath.Join(dir, e.Exe)
		}
		if !exists(e.Exe) {
			missing = append(missing, e.ID)
			continue
		}
		for i := range e.Games {
			if e.Games[i].ID == "" {
				return nil, nil, fmt.Errorf("catalog: game %q in %q needs an id", e.Games[i].Name, e.ID)
			}
			if e.Games[i].Name == "" {
				e.Games[i].Name = e.Games[i].ID
			}
		}
		c.Entries = append(c.Entries, e)
	}
	return c, missing, nil
}

// Resolve turns "entry" or "entry/game" into a Pick.
func (c *Catalog) Resolve(key string) (Pick, bool) {
	entryID, gameID, _ := strings.Cut(key, "/")
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.ID != entryID {
			continue
		}
		if gameID == "" {
			return Pick{Entry: e}, true
		}
		for j := range e.Games {
			if e.Games[j].ID == gameID {
				return Pick{Entry: e, Game: &e.Games[j]}, true
			}
		}
	}
	return Pick{}, false
}
