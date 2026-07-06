package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestExpandPastedBlocksImage(t *testing.T) {
	m := &chatTUI{pastedBlocks: []pastedBlock{
		{label: "[image #1]", text: "@.reasonix/attachments/clipboard-20260601-010203.000001.png", image: true},
		{label: "[Pasted text #2 · 3 lines]", text: "a\nb\nc"},
	}}
	got := m.expandPastedBlocks("look at [image #1] and [Pasted text #2 · 3 lines]")
	want := "look at @.reasonix/attachments/clipboard-20260601-010203.000001.png and " +
		renderFoldedPasteBlock(m.pastedBlocks[1])
	if got != want {
		t.Fatalf("expandPastedBlocks = %q, want %q", got, want)
	}
	if displayLineForImageRefs(got) != "look at [image1] and "+renderFoldedPasteBlock(m.pastedBlocks[1]) {
		t.Fatalf("image ref should collapse to a label in the bubble: %q", displayLineForImageRefs(got))
	}
}

func TestDisplayLineForImageRefs(t *testing.T) {
	got := displayLineForImageRefs("describe @.reasonix/attachments/clipboard-20260601-010203.000001.png @.reasonix/attachments/clipboard-20260601-010204.000002-000002.jpg")
	want := "describe [image1] [image2]"
	if got != want {
		t.Fatalf("displayLineForImageRefs = %q, want %q", got, want)
	}
}

func TestPastedFileRef(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := pastedFileRef(pdf); !ok || got != "@"+filepath.Clean(pdf) {
		t.Fatalf("pastedFileRef(existing pdf) = %q, %v", got, ok)
	}
	if got, ok := pastedFileRef(`"` + pdf + `"`); !ok || got != "@"+filepath.Clean(pdf) {
		t.Fatalf("pastedFileRef(quoted pdf) = %q, %v", got, ok)
	}
	if _, ok := pastedFileRef("just-a-word"); ok {
		t.Fatal("a bare word with no separator must not be a file ref")
	}
	if _, ok := pastedFileRef(filepath.Join(dir, "missing.pdf")); ok {
		t.Fatal("a non-existent path must not be a file ref")
	}
	if _, ok := pastedFileRef(dir); ok {
		t.Fatal("a directory must not be a file ref")
	}
}

func TestPastedFileRefShellEscapedSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Application Support", "report 2026.pdf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(path, " ", `\ `)

	if got, ok := pastedFileRef(escaped); !ok || got != "@"+filepath.Clean(path) {
		t.Fatalf("pastedFileRef(shell escaped pdf) = %q, %v; want @%s", got, ok, filepath.Clean(path))
	}
}

func TestPastedImageSources(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
		ok   bool
	}{
		{
			name: "data URL",
			text: "data:image/png;base64,aaa",
			want: []string{"data:image/png;base64,aaa"},
			ok:   true,
		},
		{
			name: "markdown images",
			text: "![a](/tmp/a.png)\n![b](file:///tmp/b.jpg)",
			want: []string{"/tmp/a.png", "file:///tmp/b.jpg"},
			ok:   true,
		},
		{
			name: "shell escaped path with spaces",
			text: `/Users/jawa/Library/Application\ Support/CleanShot/media/CleanShot\ 2026-07-06\ at\ 11.33.14@2x.png`,
			want: []string{`/Users/jawa/Library/Application\ Support/CleanShot/media/CleanShot\ 2026-07-06\ at\ 11.33.14@2x.png`},
			ok:   true,
		},
		{
			name: "sentence with image path remains text",
			text: `see /tmp/CleanShot\ 2026.png`,
			ok:   false,
		},
		{
			name: "plain text",
			text: "hello /tmp/a.png",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pastedImageSources(c.text)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("sources = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPasteShellEscapedImagePathInsertsImageToken(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	path := filepath.Join(root, "Library", "Application Support", "CleanShot", "CleanShot 2026-07-06 at 11.33.14@2x.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestChatTUI()
	next, _ := m.Update(tea.PasteMsg{Content: strings.ReplaceAll(path, " ", `\ `)})
	updated := next.(chatTUI)

	if got := updated.input.Value(); got != "[image #1] " {
		t.Fatalf("input after paste = %q, want image token", got)
	}
	if len(updated.pastedBlocks) != 1 || !updated.pastedBlocks[0].image {
		t.Fatalf("pastedBlocks = %+v, want one image block", updated.pastedBlocks)
	}
	if text := updated.pastedBlocks[0].text; !strings.HasPrefix(text, "@.reasonix/attachments/clipboard-") || !strings.HasSuffix(text, ".png") {
		t.Fatalf("image block text = %q, want saved attachment ref", text)
	}
}
