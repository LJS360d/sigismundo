// Package input injects mouse and keyboard events into the session's X
// display through the XTEST extension.
package input

import (
	"fmt"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type Injector struct {
	mu      sync.Mutex
	conn    *xgb.Conn
	root    xproto.Window
	width   int
	height  int
	codes   map[xproto.Keysym]xproto.Keycode
	pressed map[xproto.Keycode]bool
	buttons map[byte]bool
}

func Open(display string) (*Injector, error) {
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("xtest: %w", err)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	in := &Injector{
		conn:    conn,
		root:    screen.Root,
		width:   int(screen.WidthInPixels),
		height:  int(screen.HeightInPixels),
		codes:   map[xproto.Keysym]xproto.Keycode{},
		pressed: map[xproto.Keycode]bool{},
		buttons: map[byte]bool{},
	}
	first, last := setup.MinKeycode, setup.MaxKeycode
	km, err := xproto.GetKeyboardMapping(conn, first, byte(last-first+1)).Reply()
	if err != nil {
		conn.Close()
		return nil, err
	}
	per := int(km.KeysymsPerKeycode)
	for i := 0; i*per < len(km.Keysyms); i++ {
		// Only the unshifted column: shifted symbols are typed by holding Shift.
		if sym := km.Keysyms[i*per]; sym != 0 {
			if _, ok := in.codes[sym]; !ok {
				in.codes[sym] = xproto.Keycode(int(first) + i)
			}
		}
	}
	return in, nil
}

// Move puts the pointer at x, y in display pixels.
func (in *Injector) Move(x, y int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	return xtest.FakeInputChecked(in.conn, xproto.MotionNotify, 0, 0, in.root, int16(x), int16(y), 0).Check()
}

// MoveRel puts the pointer at a fraction (0-1) of the screen size.
func (in *Injector) MoveRel(fx, fy float64) error {
	return in.Move(int(fx*float64(in.width-1)), int(fy*float64(in.height-1)))
}

// Button presses or releases mouse button 1-3 (left, middle, right), 4-5
// (wheel up, down).
func (in *Injector) Button(b byte, down bool) error {
	if b < 1 || b > 5 {
		return fmt.Errorf("bad button %d", b)
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	typ := byte(xproto.ButtonRelease)
	if down {
		typ = xproto.ButtonPress
	}
	in.buttons[b] = down
	return xtest.FakeInputChecked(in.conn, typ, b, 0, in.root, 0, 0, 0).Check()
}

// Key presses or releases a key by X keysym name ("Return", "a", "Up").
func (in *Injector) Key(name string, down bool) error {
	sym, ok := keysymByName(name)
	if !ok {
		return fmt.Errorf("unknown key %q", name)
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	code, ok := in.codes[sym]
	if !ok {
		return fmt.Errorf("key %q not in keymap", name)
	}
	typ := byte(xproto.KeyRelease)
	if down {
		typ = xproto.KeyPress
	}
	in.pressed[code] = down
	return xtest.FakeInputChecked(in.conn, typ, byte(code), 0, in.root, 0, 0, 0).Check()
}

// ReleaseAll lifts every held key and button, used when control changes hands.
func (in *Injector) ReleaseAll() {
	in.mu.Lock()
	defer in.mu.Unlock()
	for code, down := range in.pressed {
		if down {
			xtest.FakeInput(in.conn, xproto.KeyRelease, byte(code), 0, in.root, 0, 0, 0)
		}
	}
	for b, down := range in.buttons {
		if down {
			xtest.FakeInput(in.conn, xproto.ButtonRelease, b, 0, in.root, 0, 0, 0)
		}
	}
	clear(in.pressed)
	clear(in.buttons)
}

func (in *Injector) Close() {
	in.ReleaseAll()
	in.conn.Close()
}

var named = map[string]xproto.Keysym{
	"BackSpace": 0xff08, "Tab": 0xff09, "Return": 0xff0d, "Escape": 0xff1b, "Delete": 0xffff,
	"Home": 0xff50, "Left": 0xff51, "Up": 0xff52, "Right": 0xff53, "Down": 0xff54,
	"Page_Up": 0xff55, "Page_Down": 0xff56, "End": 0xff57, "Insert": 0xff63,
	"Shift_L": 0xffe1, "Shift_R": 0xffe2, "Control_L": 0xffe3, "Control_R": 0xffe4,
	"Alt_L": 0xffe9, "Alt_R": 0xffea, "Super_L": 0xffeb, "Caps_Lock": 0xffe5,
	"space": 0x20, "minus": 0x2d, "equal": 0x3d, "bracketleft": 0x5b, "bracketright": 0x5d,
	"backslash": 0x5c, "semicolon": 0x3b, "apostrophe": 0x27, "grave": 0x60,
	"comma": 0x2c, "period": 0x2e, "slash": 0x2f,
}

func keysymByName(name string) (xproto.Keysym, bool) {
	if s, ok := named[name]; ok {
		return s, true
	}
	if len(name) == 1 {
		c := name[0]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			return xproto.Keysym(c), true
		case c >= 'A' && c <= 'Z':
			return xproto.Keysym(c + 32), true
		}
	}
	if n, ok := strings.CutPrefix(name, "F"); ok {
		var f int
		if _, err := fmt.Sscanf(n, "%d", &f); err == nil && f >= 1 && f <= 12 {
			return xproto.Keysym(0xffbe + f - 1), true
		}
	}
	return 0, false
}

// FromBrowserCode maps a KeyboardEvent.code to the keysym name Key expects.
func FromBrowserCode(code string) (string, bool) {
	if l, ok := strings.CutPrefix(code, "Key"); ok && len(l) == 1 {
		return strings.ToLower(l), true
	}
	if d, ok := strings.CutPrefix(code, "Digit"); ok && len(d) == 1 {
		return d, true
	}
	if len(code) >= 2 && code[0] == 'F' && code[1] >= '1' && code[1] <= '9' {
		_, ok := keysymByName(code)
		return code, ok
	}
	n, ok := browserCodes[code]
	return n, ok
}

var browserCodes = map[string]string{
	"Backspace": "BackSpace", "Tab": "Tab", "Enter": "Return", "NumpadEnter": "Return", "Escape": "Escape",
	"Delete": "Delete", "Home": "Home", "End": "End", "PageUp": "Page_Up", "PageDown": "Page_Down", "Insert": "Insert",
	"ArrowLeft": "Left", "ArrowUp": "Up", "ArrowRight": "Right", "ArrowDown": "Down",
	"ShiftLeft": "Shift_L", "ShiftRight": "Shift_R", "ControlLeft": "Control_L", "ControlRight": "Control_R",
	"AltLeft": "Alt_L", "AltRight": "Alt_R", "CapsLock": "Caps_Lock",
	"Space": "space", "Minus": "minus", "Equal": "equal", "BracketLeft": "bracketleft", "BracketRight": "bracketright",
	"Backslash": "backslash", "Semicolon": "semicolon", "Quote": "apostrophe", "Backquote": "grave",
	"Comma": "comma", "Period": "period", "Slash": "slash",
}
