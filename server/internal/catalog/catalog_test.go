package catalog

import "testing"

const manifest = `
- id: jpp7
  name: The Jackbox Party Pack 7
  description: Quiplash 3 and friends
  exe: jpp7/Pack7.exe
  image: jpp7/cover.png
- id: missing
  exe: /nope/game.exe
- id: drawful2
  exe: /abs/Drawful2.exe
`

func load(t *testing.T) (*Catalog, []string) {
	t.Helper()
	c, missing, err := parse([]byte(manifest), "/games", func(p string) bool { return p != "/nope/game.exe" })
	if err != nil {
		t.Fatal(err)
	}
	return c, missing
}

func TestParse(t *testing.T) {
	c, missing := load(t)
	if len(c.Entries) != 2 || len(missing) != 1 || missing[0] != "missing" {
		t.Fatalf("entries %d missing %v", len(c.Entries), missing)
	}
	e := c.Entries[0]
	if e.Exe != "/games/jpp7/Pack7.exe" || e.Image != "/games/jpp7/cover.png" {
		t.Fatalf("relative paths not resolved: %s %s", e.Exe, e.Image)
	}
	if e.ImageURL != "/api/catalog/jpp7/image" {
		t.Fatalf("image url %q", e.ImageURL)
	}
	if d := c.Entries[1]; d.Name != "drawful2" || d.ImageURL != "" {
		t.Fatalf("defaults %+v", d)
	}
}

func TestGet(t *testing.T) {
	c, _ := load(t)
	if e, ok := c.Get("drawful2"); !ok || e.Exe != "/abs/Drawful2.exe" {
		t.Fatalf("get: %v %+v", ok, e)
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatal("got dropped entry")
	}
}

func TestParseErrors(t *testing.T) {
	for _, m := range []string{
		"- { name: x }",
		"- { id: a/b, exe: x }",
		"- { id: a, exe: x }\n- { id: a, exe: y }",
	} {
		if _, _, err := parse([]byte(m), "/", func(string) bool { return true }); err == nil {
			t.Errorf("no error for %q", m)
		}
	}
}
