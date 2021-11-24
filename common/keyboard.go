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
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/dop251/goja"
	"github.com/grafana/xk6-browser/api"
	"github.com/grafana/xk6-browser/keyboardlayout"
)

// Ensure Keyboard implements the api.Keyboard interface
var _ api.Keyboard = &Keyboard{}

const (
	ModifierKeyAlt int64 = 1 << iota
	ModifierKeyControl
	ModifierKeyMeta
	ModifierKeyShift
)

// Keyboard represents a keyboard input device
type Keyboard struct {
	ctx         context.Context
	session     *Session
	modifiers   int64
	pressedKeys map[int64]bool
	layoutName  string
}

// NewKeyboard creates a new keyboard
func NewKeyboard(ctx context.Context, session *Session) *Keyboard {
	return &Keyboard{
		ctx:         ctx,
		session:     session,
		modifiers:   0,
		pressedKeys: make(map[int64]bool),
		layoutName:  "us",
	}
}

func (k *Keyboard) down(key string) error {
	keyInput := keyboardlayout.KeyInput(key)
	layout := keyboardlayout.GetKeyboardLayout(k.layoutName)
	if _, ok := layout.ValidKeys[keyInput]; !ok {
		return fmt.Errorf("%q is not a valid key for layout %q", key, k.layoutName)
	}

	keyDef := k.keyDefinitionFromKey(keyInput)
	k.modifiers &= ^k.modifierBitFromKeyName(keyDef.Key)
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
		return fmt.Errorf("unable to key down: %w", err)
	}

	return nil
}

func (k *Keyboard) up(key string) error {
	keyInput := keyboardlayout.KeyInput(key)
	layout := keyboardlayout.GetKeyboardLayout(k.layoutName)
	if _, ok := layout.ValidKeys[keyInput]; !ok {
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
		return fmt.Errorf("unable to key up: %w", err)
	}

	return nil
}

func (k *Keyboard) insertText(text string) error {
	action := input.InsertText(text)
	if err := action.Do(cdp.WithExecutor(k.ctx, k.session)); err != nil {
		return fmt.Errorf("unable to send character: %w", err)
	}
	return nil
}

func (k *Keyboard) keyDefinitionFromKey(keyString keyboardlayout.KeyInput) keyboardlayout.KeyDefinition {
	layout := keyboardlayout.GetKeyboardLayout(k.layoutName)
	srcKeyDef, ok := layout.Keys[keyString]
	// Find based on key value instead of code
	if !ok {
		for key, def := range layout.Keys {
			if def.Key != string(keyString) {
				continue
			}
			keyString, srcKeyDef = key, def
			ok = true // don't look for a shift key below
		}
	}
	// try to find with the shift key value
	shift := k.modifiers & ModifierKeyShift
	if !ok {
		for key, def := range layout.Keys {
			if def.ShiftKey != string(keyString) {
				continue
			}
			keyString, srcKeyDef = key, def
		}
		shift = k.modifiers | ModifierKeyShift
	}

	var keyDef keyboardlayout.KeyDefinition
	if srcKeyDef.Key != "" {
		keyDef.Key = srcKeyDef.Key
	}
	if shift != 0 && srcKeyDef.ShiftKey != "" {
		keyDef.Key = srcKeyDef.ShiftKey
	}
	if srcKeyDef.KeyCode != 0 {
		keyDef.KeyCode = srcKeyDef.KeyCode
	}
	if shift != 0 && srcKeyDef.ShiftKeyCode != 0 {
		keyDef.KeyCode = srcKeyDef.ShiftKeyCode
	}
	if srcKeyDef.KeyCode != 0 {
		keyDef.KeyCode = srcKeyDef.KeyCode
	}
	if keyString != "" {
		keyDef.Code = string(keyString)
	}
	if srcKeyDef.Location != 0 {
		keyDef.Location = srcKeyDef.Location
	}
	if len(srcKeyDef.Key) == 1 {
		keyDef.Text = srcKeyDef.Key
	}
	if srcKeyDef.Text != "" {
		keyDef.Text = srcKeyDef.Text
	}
	if shift != 0 && srcKeyDef.ShiftKey != "" {
		keyDef.Text = srcKeyDef.ShiftKey
	}
	// If any modifiers besides shift are pressed, no text should be sent
	if k.modifiers & ^ModifierKeyShift != 0 {
		keyDef.Text = ""
	}
	return keyDef
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

func (k *Keyboard) press(key string, opts *KeyboardOptions) error {
	if opts.Delay != 0 {
		t := time.NewTimer(time.Duration(opts.Delay * time.Hour.Milliseconds()))
		select {
		case <-k.ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
	if err := k.down(key); err != nil {
		return err
	}
	return k.up(key)
}

func (k *Keyboard) typ(text string, opts *KeyboardOptions) error {
	layout := keyboardlayout.GetKeyboardLayout(k.layoutName)
	for _, c := range text {
		if opts.Delay != 0 {
			t := time.NewTimer(time.Duration(opts.Delay * time.Hour.Milliseconds()))
			select {
			case <-k.ctx.Done():
				t.Stop()
			case <-t.C:
			}
		}
		keyInput := keyboardlayout.KeyInput(c)
		if _, ok := layout.ValidKeys[keyInput]; ok {
			if err := k.press(string(c), opts); err != nil {
				return err
			}
			continue
		}
		if err := k.insertText(string(c)); err != nil {
			return err
		}
	}
	return nil
}

// Down
func (k *Keyboard) Down(key string) {
	if err := k.down(key); err != nil {
		k6Throw(k.ctx, "cannot press down key: %w", err)
	}
}

// Press
func (k *Keyboard) Press(key string, opts goja.Value) {
	kbdOpts := NewKeyboardOptions()
	if err := kbdOpts.Parse(k.ctx, opts); err != nil {
		k6Throw(k.ctx, "cannot parse options: %w", err)
	}
	if err := k.press(key, kbdOpts); err != nil {
		k6Throw(k.ctx, "cannot press key: %w", err)
	}
}

// InsertText
func (k *Keyboard) InsertText(text string) {
	if err := k.insertText(text); err != nil {
		k6Throw(k.ctx, "cannot insert text: %w", err)
	}
}

// Type
func (k *Keyboard) Type(text string, opts goja.Value) {
	kbdOpts := NewKeyboardOptions()
	if err := kbdOpts.Parse(k.ctx, opts); err != nil {
		k6Throw(k.ctx, "cannot parse options: %w", err)
	}
	if err := k.typ(text, kbdOpts); err != nil {
		k6Throw(k.ctx, "cannot type text: %w", err)
	}
}

// Up
func (k *Keyboard) Up(key string) {
	if err := k.up(key); err != nil {
		k6Throw(k.ctx, "cannot press up key: %w", err)
	}
}
