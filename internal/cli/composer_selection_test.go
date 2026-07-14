package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

func newComposerMouseTestTUI(t *testing.T, width, height int) chatTUI {
	t.Helper()
	m := newChatTUI(control.New(control.Options{}), "", make(chan event.Event, 1), width)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(chatTUI)
}

func updateComposerMouseTestTUI(t *testing.T, m chatTUI, msg tea.Msg) chatTUI {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(chatTUI)
}

func TestComposerMouseClickMovesCursorAcrossWideRunes(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("你好abc")
	x, y, ok := m.composerOrigin()
	if !ok {
		t.Fatal("composer should expose a mouse origin while visible")
	}

	// Each CJK rune occupies two terminal cells. Clicking after both should put
	// the textarea cursor before the ASCII suffix, not leave it at the end.
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	if got := m.input.Column(); got != 2 {
		t.Fatalf("cursor column = %d, want 2 after two wide runes", got)
	}
	if m.composerSel.active {
		t.Fatal("a plain click should position the cursor without leaving a selection")
	}
}

func TestComposerMouseLayoutRoundTripsTextareaCursor(t *testing.T) {
	cases := []struct {
		width int
		value string
	}{
		{40, ""},
		{20, "hello world and wrapped words"},
		{16, "1234567890中文"},
		{18, "alpha  beta\n中文 mixed\n\nlast"},
		{18, "one\ntwo\nthree\nfour\nfive\nsix\nseven"},
	}
	for _, tc := range cases {
		m := newComposerMouseTestTUI(t, tc.width, 14)
		m.input.SetValue(tc.value)
		for offset := 0; offset <= len([]rune(tc.value)); offset++ {
			m.setComposerCursor(offset)
			local := m.input.Cursor()
			x, y, ok := m.composerOrigin()
			if !ok || local == nil {
				t.Fatalf("value %q offset %d has no composer cursor", tc.value, offset)
			}
			caret, ok := m.composerCaretAt(x+local.X, y+local.Y, false)
			if !ok || caret.offset != offset {
				t.Fatalf("value %q offset %d round-tripped to %+v (ok=%v)", tc.value, offset, caret, ok)
			}
		}
	}
}

func TestComposerMouseDragSelectsAndTypingReplaces(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("你好abc")
	x, y, _ := m.composerOrigin()

	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 4, Y: y, Button: tea.MouseLeft})

	if got := m.selectedComposerText(); got != "你好" {
		t.Fatalf("selected composer text = %q, want %q", got, "你好")
	}
	highlighted := m.renderComposerInput()
	if !strings.Contains(highlighted, selStyle.Render("你好")) {
		t.Fatalf("rendered composer should highlight exactly the selected wide runes: %q", highlighted)
	}

	m = updateComposerMouseTestTUI(t, m, tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := m.input.Value(); got != "Xabc" {
		t.Fatalf("typing over selection produced %q, want %q", got, "Xabc")
	}
	if m.composerSel.active {
		t.Fatal("typing over a selection should clear the selection")
	}
}

func TestComposerMouseBackwardDragKeepsLogicalSelection(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("alpha beta")
	x, y, _ := m.composerOrigin()

	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x + 10, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	if got := m.selectedComposerText(); got != "beta" {
		t.Fatalf("backward drag selected %q, want %q", got, "beta")
	}
}

func TestComposerMouseSelectionSnapsToGraphemeClusters(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("e\u0301x 👨‍👩‍👧‍👦z")
	x, y, _ := m.composerOrigin()

	// The combining accent is a separate rune but part of the same one-cell
	// grapheme, so a one-cell drag must select both runes together.
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	if got := m.selectedComposerText(); got != "e\u0301" {
		t.Fatalf("combining grapheme selection = %q, want %q", got, "e\u0301")
	}

	// The family emoji contains seven runes but renders as one two-cell grapheme.
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	if got := m.selectedComposerText(); got != "👨‍👩‍👧‍👦" {
		t.Fatalf("emoji grapheme selection = %q, want family emoji", got)
	}
}

func TestComposerSelectionTracksSoftWrapAndNewlines(t *testing.T) {
	m := newComposerMouseTestTUI(t, 16, 14)
	m.input.SetValue("1234567890中文\nsecond")
	x, y, _ := m.composerOrigin()

	// The configured textarea content width is 12 cells. Drag from the final two
	// ASCII characters on the first visual row through the two wide CJK runes on
	// the wrapped row and into the explicit second logical line.
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x + 10, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 3, Y: y + 2, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 3, Y: y + 2, Button: tea.MouseLeft})

	if got, want := m.selectedComposerText(), "中文\nsec"; got != want {
		t.Fatalf("wrapped multi-line selection = %q, want %q", got, want)
	}

	m = updateComposerMouseTestTUI(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, want := m.input.Value(), "1234567890ond"; got != want {
		t.Fatalf("backspace over wrapped selection = %q, want %q", got, want)
	}
}

func TestComposerSelectionPasteAndCopyTakePrecedence(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("alpha beta")
	x, y, _ := m.composerOrigin()
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 10, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 10, Y: y, Button: tea.MouseLeft})

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(chatTUI)
	if cmd == nil {
		t.Fatal("Ctrl+C with a composer selection should issue a clipboard command")
	}
	if got := m.input.Value(); got != "alpha beta" {
		t.Fatalf("Ctrl+C changed composer value to %q", got)
	}
	if got := m.selectedComposerText(); got != "beta" {
		t.Fatalf("Ctrl+C should preserve composer selection, got %q", got)
	}

	m = updateComposerMouseTestTUI(t, m, tea.PasteMsg{Content: "gamma"})
	if got := ansi.Strip(m.input.Value()); got != "alpha gamma" {
		t.Fatalf("paste over selection produced %q, want %q", got, "alpha gamma")
	}
}

func TestComposerSelectionDoesNotTurnCommandShortcutIntoText(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("keep this")
	x, y, _ := m.composerOrigin()
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 4, Y: y, Button: tea.MouseLeft})

	// Ctrl+Y is an application command and must not replace the selected draft.
	m = updateComposerMouseTestTUI(t, m, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if got := m.input.Value(); got != "keep this" {
		t.Fatalf("Ctrl+Y changed selected composer text to %q", got)
	}
}

func TestFailedImagePastePreservesComposerSelection(t *testing.T) {
	m := newComposerMouseTestTUI(t, 40, 12)
	m.input.SetValue("keep this")
	x, y, _ := m.composerOrigin()
	m = updateComposerMouseTestTUI(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseMotionMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m = updateComposerMouseTestTUI(t, m, tea.MouseReleaseMsg{X: x + 4, Y: y, Button: tea.MouseLeft})

	m = updateComposerMouseTestTUI(t, m, tea.PasteMsg{Content: "/path/that/does/not/exist.png"})
	if got := m.input.Value(); got != "keep this" {
		t.Fatalf("failed image paste changed composer value to %q", got)
	}
	if got := m.selectedComposerText(); got != "keep" {
		t.Fatalf("failed image paste should preserve selection, got %q", got)
	}
}
