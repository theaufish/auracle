package pdfmd

import (
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
)

// text lays out s as one glyph per character, like a PDF text layer.
func text(s string, x, y, size float64) []pdf.Text {
	var gs []pdf.Text
	w := size * 0.5
	for _, r := range s {
		gs = append(gs, pdf.Text{S: string(r), X: x, Y: y, W: w, FontSize: size})
		x += w
	}
	return gs
}

func page(parts ...[]pdf.Text) []pdf.Text {
	var all []pdf.Text
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

func TestTwoColumns(t *testing.T) {
	// Right column lines share y positions with the left column and are
	// listed first, so reading order must come from position, not stream order.
	glyphs := page(
		text("Right one.", 330, 700, 8),
		text("Right two.", 330, 690, 8),
		text("Left one", 40, 700, 8),
		text("continues.", 40, 690, 8),
	)
	got := Page(glyphs, 600, 2)
	want := "Left one continues.\n\nRight one. Right two."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHeadingsAndParagraphs(t *testing.T) {
	glyphs := page(
		text("Big Title", 40, 760, 20),
		text("Section", 40, 730, 10),
		text("First paragraph", 40, 715, 8),
		text("wraps here.", 40, 705, 8),
		text("Indented starts", 46, 695, 8),
		text("another.", 40, 685, 8),
		text("Range: 30 feet", 40, 675, 8),
		text("Duration: 1 minute", 40, 665, 8),
	)
	got := Page(glyphs, 600, 2)
	want := strings.Join([]string{
		"## Big Title",
		"#### Section",
		"First paragraph wraps here.",
		"Indented starts another.",
		"Range: 30 feet",
		"Duration: 1 minute",
	}, "\n\n")
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestTableAndList(t *testing.T) {
	glyphs := page(
		text("1st", 40, 700, 8), text("+2", 80, 700, 8), text("Rage", 120, 700, 8),
		text("01 A goblin hand", 40, 680, 8),
		text("02 A crystal that", 40, 670, 8),
		text("glows", 52, 660, 8),
	)
	got := Page(glyphs, 600, 2)
	want := "1st | +2 | Rage\n\n01 A goblin hand\n\n02 A crystal that glows"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSoftHyphen(t *testing.T) {
	glyphs := page(text("imagina­", 40, 700, 8), text("tion rules.", 40, 690, 8))
	if got := Page(glyphs, 600, 2); got != "imagination rules." {
		t.Errorf("got %q", got)
	}
}

func TestEmpty(t *testing.T) {
	if got := Page(nil, 600, 2); got != "" {
		t.Errorf("got %q", got)
	}
}
