/*
 *
 * xk6-browser - a browser automation extension for k6
 * Copyright (C) 2021 Load Impact
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package common

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/xk6-browser/api"
	"github.com/grafana/xk6-browser/k6ext"
	"github.com/grafana/xk6-browser/keyboardlayout"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/dop251/goja"
)

var _ api.Keyboard = &Keyboard{}

const (
	ModifierKeyAlt int64 = 1 << iota
	ModifierKeyControl
	ModifierKeyMeta
	ModifierKeyShift
)

// Keyboard represents a keyboard input device.
// Each Page has a publicly accessible Keyboard.
type Keyboard struct {
	ctx     context.Context
	session session

	modifiers   int64          // like shift, alt, ctrl, ...
	pressedKeys map[int64]bool // tracks keys through down() and up()
	layoutName  string         // us by default
	layout      keyboardlayout.KeyboardLayout
}

// NewKeyboard returns a new keyboard with a "us" layout.
func NewKeyboard(ctx context.Context, s session) *Keyboard {
	return &Keyboard{
		ctx:         ctx,
		session:     s,
		pressedKeys: make(map[int64]bool),
		layoutName:  "us",
		layout:      keyboardlayout.GetKeyboardLayout("us"),
	}
}

// Down sends a key down message to a session target.
func (k *Keyboard) Down(key string) {
	if err := k.down(key); err != nil {
		k6ext.Panic(k.ctx, "cannot send key down: %w", err)
	}
}

// Up sends a key up message to a session target.
func (k *Keyboard) Up(key string) {
	if err := k.up(key); err != nil {
		k6ext.Panic(k.ctx, "cannot send key up: %w", err)
	}
}

// Press sends a key press message to a session target.
// It delays the action if `Delay` option is specified.
// A press message consists of successive key down and up messages.
func (k *Keyboard) Press(key string, opts goja.Value) {
	kbdOpts := NewKeyboardOptions()
	if err := kbdOpts.Parse(k.ctx, opts); err != nil {
		k6ext.Panic(k.ctx, "cannot parse keyboard options: %w", err)
	}

	if err := k.comboPress(key, kbdOpts); err != nil {
		k6ext.Panic(k.ctx, "cannot press key: %w", err)
	}
}

// InsertText inserts a text without dispatching key events.
func (k *Keyboard) InsertText(text string) {
	if err := k.insertText(text); err != nil {
		k6ext.Panic(k.ctx, "cannot insert text: %w", err)
	}
}

// Type sends a press message to a session target for each character in text.
// It delays the action if `Delay` option is specified.
//
// It sends an insertText message if a character is not among
// valid characters in the keyboard's layout.
func (k *Keyboard) Type(text string, opts goja.Value) {
	kbdOpts := NewKeyboardOptions()
	if err := kbdOpts.Parse(k.ctx, opts); err != nil {
		k6ext.Panic(k.ctx, "cannot parse keyboard options: %w", err)
	}
	if err := k.typ(text, kbdOpts); err != nil {
		k6ext.Panic(k.ctx, "cannot type text: %w", err)
	}
}

func (k *Keyboard) down(key string) error {
	keyInput := keyboardlayout.KeyInput(key)
	if _, ok := k.layout.ValidKeys[keyInput]; !ok {
		return fmt.Errorf("%q is not a valid key for layout %q", key, k.layoutName)
	}

	keyDef := k.keyDefinitionFromKey(keyInput)
	k.modifiers |= k.modifierBitFromKeyName(keyDef.Key)
	text := keyDef.Text
	_, autoRepeat := k.pressedKeys[keyDef.KeyCode]
	k.pressedKeys[keyDef.KeyCode] = true

	keyType := input.KeyDown
	if text == "" {
		keyType = input.KeyRawDown
	}

	action := input.DispatchKeyEvent(keyType).
		WithModifiers(input.Modifier(k.modifiers)).
		WithKey(keyDef.Key).
		WithWindowsVirtualKeyCode(keyDef.KeyCode).
		WithCode(keyDef.Code).
		WithLocation(keyDef.Location).
		WithIsKeypad(keyDef.Location == 3).
		WithText(text).
		WithUnmodifiedText(text).
		WithAutoRepeat(autoRepeat)
	if err := action.Do(cdp.WithExecutor(k.ctx, k.session)); err != nil {
		return fmt.Errorf("cannot execute dispatch key event down: %w", err)
	}

	return nil
}

func (k *Keyboard) up(key string) error {
	keyInput := keyboardlayout.KeyInput(key)
	if _, ok := k.layout.ValidKeys[keyInput]; !ok {
		return fmt.Errorf("'%s' is not a valid key for layout '%s'", key, k.layoutName)
	}

	keyDef := k.keyDefinitionFromKey(keyInput)
	k.modifiers &= ^k.modifierBitFromKeyName(keyDef.Key)
	delete(k.pressedKeys, keyDef.KeyCode)

	action := input.DispatchKeyEvent(input.KeyUp).
		WithModifiers(input.Modifier(k.modifiers)).
		WithKey(keyDef.Key).
		WithWindowsVirtualKeyCode(keyDef.KeyCode).
		WithCode(keyDef.Code).
		WithLocation(keyDef.Location)
	if err := action.Do(cdp.WithExecutor(k.ctx, k.session)); err != nil {
		return fmt.Errorf("cannot execute dispatch key event up: %w", err)
	}

	return nil
}

func (k *Keyboard) insertText(text string) error {
	action := input.InsertText(text)
	if err := action.Do(cdp.WithExecutor(k.ctx, k.session)); err != nil {
		return fmt.Errorf("cannot execute insert text: %w", err)
	}
	return nil
}

func (k *Keyboard) keyDefinitionFromKey(key keyboardlayout.KeyInput) keyboardlayout.KeyDefinition {
	shift := k.modifiers & ModifierKeyShift

	// Find directly from the keyboard layout
	srcKeyDef, ok := k.layout.Keys[key]
	// Try to find based on key value instead of code
	if !ok {
		srcKeyDef, ok = k.layout.KeyDefinition(key)
	}
	// Try to find with the shift key value
	// e.g. for `@`, the shift modifier needs to
	// be used.
	var foundInShift bool
	if !ok {
		srcKeyDef = k.layout.ShiftKeyDefinition(key)
		shift = k.modifiers | ModifierKeyShift
		foundInShift = true
	}

	var keyDef keyboardlayout.KeyDefinition
	if srcKeyDef.Key != "" {
		keyDef.Key = srcKeyDef.Key
	}
	if len(srcKeyDef.Key) == 1 {
		keyDef.Text = srcKeyDef.Key
	}
	if shift != 0 && srcKeyDef.ShiftKeyCode != 0 {
		keyDef.KeyCode = srcKeyDef.ShiftKeyCode
	}
	if srcKeyDef.KeyCode != 0 {
		keyDef.KeyCode = srcKeyDef.KeyCode
	}
	if key != "" {
		keyDef.Code = string(key)
	}
	if srcKeyDef.Location != 0 {
		keyDef.Location = srcKeyDef.Location
	}
	if srcKeyDef.Text != "" {
		keyDef.Text = srcKeyDef.Text
	}
	// Shift is only used on keys which are `KeyX`` (where X is
	// A-Z), or on keys which require shift to be pressed e.g.
	// `@`, and shift must be pressed as well as a shiftKey
	// text value present for the key.
	// Not all keys have a text value when shift is pressed
	// e.g. `Control`.
	// When a key such as `2` is pressed, we must ignore shift
	// otherwise we would type `@`.
	isKeyXOrOnShiftLayerAndShiftUsed := (isKeyX(string(key)) || foundInShift) &&
		shift != 0 &&
		srcKeyDef.ShiftKey != ""
	if isKeyXOrOnShiftLayerAndShiftUsed {
		keyDef.Key = srcKeyDef.ShiftKey
		keyDef.Text = srcKeyDef.ShiftKey
	}
	// If any modifiers besides shift are pressed, no text should be sent
	if k.modifiers & ^ModifierKeyShift != 0 {
		keyDef.Text = ""
	}
	return keyDef
}

func isKeyX(key string) bool {
	_, ok := map[string]interface{}{
		"KeyA": nil,
		"KeyB": nil,
		"KeyC": nil,
		"KeyD": nil,
		"KeyE": nil,
		"KeyF": nil,
		"KeyG": nil,
		"KeyH": nil,
		"KeyI": nil,
		"KeyJ": nil,
		"KeyK": nil,
		"KeyL": nil,
		"KeyM": nil,
		"KeyN": nil,
		"KeyO": nil,
		"KeyP": nil,
		"KeyQ": nil,
		"KeyR": nil,
		"KeyS": nil,
		"KeyT": nil,
		"KeyU": nil,
		"KeyV": nil,
		"KeyW": nil,
		"KeyX": nil,
		"KeyY": nil,
		"KeyZ": nil,
	}[key]

	return ok
}

func (k *Keyboard) modifierBitFromKeyName(key string) int64 {
	switch key {
	case "Alt":
		return ModifierKeyAlt
	case "Control":
		return ModifierKeyControl
	case "Meta":
		return ModifierKeyMeta
	case "Shift":
		return ModifierKeyShift
	}
	return 0
}

func (k *Keyboard) comboPress(keys string, opts *KeyboardOptions) error {
	if opts.Delay != 0 {
		t := time.NewTimer(time.Duration(opts.Delay) * time.Millisecond)
		select {
		case <-k.ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return context.Canceled
		case <-t.C:
		}
	}

	kk := split(keys)
	for _, key := range kk {
		if err := k.down(key); err != nil {
			return fmt.Errorf("cannot do key down: %w", err)
		}
	}

	for i := range kk {
		key := kk[len(kk)-i-1]
		if err := k.up(key); err != nil {
			return fmt.Errorf("cannot do key up: %w", err)
		}
	}

	return nil
}

// This splits the string on `+`.
// If `+` on it's own is passed, it will return ["+"].
// If `++` is passed in, it will return ["+", ""].
// If `+++` is passed in, it will return ["+", "+"].
func split(keys string) []string {
	var (
		kk = make([]string, 0, len(keys))
		s  strings.Builder
	)
	for _, r := range keys {
		if r == '+' && s.Len() > 0 {
			kk = append(kk, s.String())
			s.Reset()
		} else {
			s.WriteRune(r)
		}
	}
	kk = append(kk, s.String())

	return kk
}

func (k *Keyboard) press(key string, opts *KeyboardOptions) error {
	if opts.Delay != 0 {
		t := time.NewTimer(time.Duration(opts.Delay) * time.Millisecond)
		select {
		case <-k.ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
	if err := k.down(key); err != nil {
		return fmt.Errorf("cannot do key down: %w", err)
	}
	return k.up(key)
}

func (k *Keyboard) typ(text string, opts *KeyboardOptions) error {
	layout := keyboardlayout.GetKeyboardLayout(k.layoutName)
	for _, c := range text {
		if opts.Delay != 0 {
			t := time.NewTimer(time.Duration(opts.Delay) * time.Millisecond)
			select {
			case <-k.ctx.Done():
				t.Stop()
			case <-t.C:
			}
		}
		keyInput := keyboardlayout.KeyInput(c)
		if _, ok := layout.ValidKeys[keyInput]; ok {
			if err := k.press(string(c), opts); err != nil {
				return fmt.Errorf("cannot press key: %w", err)
			}
			continue
		}
		if err := k.insertText(string(c)); err != nil {
			return fmt.Errorf("cannot insert text: %w", err)
		}
	}
	return nil
}
