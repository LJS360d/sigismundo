// Package catalog loads the hand-written manifest of launchable apps.
package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is one launchable executable.
type Entry struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
	// Exe and Image are absolute, or relative to the catalog file's directory.
	Exe   string   `yaml:"exe" json:"-"`
	Args  []string `yaml:"args" json:"-"`
	Image string   `yaml:"image" json:"-"`
	// ImageURL is where the API serves Image.
	ImageURL string `yaml:"-" json:"image,omitempty"`
}

type Catalog struct {
	Entries []Entry
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
		e.Exe = resolve(dir, e.Exe)
		if !exists(e.Exe) {
			missing = append(missing, e.ID)
			continue
		}
		if e.Image != "" {
			e.Image = resolve(dir, e.Image)
			e.ImageURL = "/api/catalog/" + e.ID + "/image"
		}
		c.Entries = append(c.Entries, e)
	}
	return c, missing, nil
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func (c *Catalog) Get(id string) (*Entry, bool) {
	for i := range c.Entries {
		if c.Entries[i].ID == id {
			return &c.Entries[i], true
		}
	}
	return nil, false
}
