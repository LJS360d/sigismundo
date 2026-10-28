package catalog

import "testing"

const manifest = `
- id: jpp7
  name: The Jackbox Party Pack 7
  exe: jpp7/Pack7.exe
  nav_delay: 20s
  games:
    - { id: quiplash3, name: Quiplash 3, nav: [Return] }
    - { id: devils, name: The Devils and the Details }
- id: missing
  exe: /nope/game.exe
- id: drawful2
  name: Drawful 2
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
	if c.Entries[0].Exe != "/games/jpp7/Pack7.exe" {
		t.Fatalf("relative exe not resolved: %s", c.Entries[0].Exe)
	}
	if c.Entries[0].NavDelay.Seconds() != 20 {
		t.Fatalf("nav_delay %v", c.Entries[0].NavDelay)
	}
}

func TestResolve(t *testing.T) {
	c, _ := load(t)
	p, ok := c.Resolve("jpp7/quiplash3")
	if !ok || p.Game.Name != "Quiplash 3" || p.Key() != "jpp7/quiplash3" {
		t.Fatalf("resolve game: %v %+v", ok, p)
	}
	if p, ok := c.Resolve("drawful2"); !ok || p.Game != nil {
		t.Fatalf("resolve entry: %v %+v", ok, p)
	}
	if _, ok := c.Resolve("jpp7/nope"); ok {
		t.Fatal("resolved unknown game")
	}
}

func TestSearch(t *testing.T) {
	c, _ := load(t)
	if got := c.Search("", 25); len(got) != 4 {
		t.Fatalf("all: %d", len(got))
	}
	got := c.Search("quip", 25)
	if len(got) != 1 || got[0].Key() != "jpp7/quiplash3" {
		t.Fatalf("quip: %+v", got)
	}
	if got := c.Search("", 2); len(got) != 2 {
		t.Fatalf("limit: %d", len(got))
	}
}

func TestParseErrors(t *testing.T) {
	for _, m := range []string{
		"- { name: x }",
		"- { id: a/b, exe: x }",
		"- { id: a, exe: x }\n- { id: a, exe: y }",
		"- { id: a, exe: x, games: [ { name: g } ] }",
	} {
		if _, _, err := parse([]byte(m), "/", func(string) bool { return true }); err == nil {
			t.Errorf("no error for %q", m)
		}
	}
}
