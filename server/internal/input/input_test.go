package input

import "testing"

func TestKeyMapping(t *testing.T) {
	for code, want := range map[string]string{
		"KeyA": "a", "Digit7": "7", "Enter": "Return", "ArrowDown": "Down", "F5": "F5", "Space": "space",
	} {
		got, ok := FromBrowserCode(code)
		if !ok || got != want {
			t.Errorf("%s -> %q %v, want %q", code, got, ok, want)
		}
		if _, ok := keysymByName(got); !ok {
			t.Errorf("no keysym for %q", got)
		}
	}
	if _, ok := FromBrowserCode("MetaLeft"); ok {
		t.Error("MetaLeft should be unmapped")
	}
	if s, _ := keysymByName("F12"); s != 0xffc9 {
		t.Errorf("F12 = %#x", s)
	}
	if s, _ := keysymByName("Q"); s != 'q' {
		t.Errorf("Q = %#x", s)
	}
}
