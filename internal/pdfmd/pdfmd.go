// Package pdfmd converts the text on a PDF page into Markdown, rebuilding
// lines, columns, paragraphs, tables and headings from glyph positions and
// font sizes.
package pdfmd

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/ledongthuc/pdf"
)

const (
	// Glyphs whose baselines differ by less than this fraction of the font
	// size are on the same line.
	sameLineTolerance = 0.4
	// A horizontal gap wider than this many font sizes is a space. Text layers
	// usually contain explicit spaces, and glyph widths in OCR'd PDFs are
	// imprecise, so only clear gaps without a space character count.
	wordGap = 1.0
	// A horizontal gap wider than this many font sizes separates table cells.
	cellGap = 2.5
	// The column gutter is searched for in this middle fraction of the page.
	gutterSearch = 0.3
	// The gutter must be at least this many points wide.
	minGutter = 6.0
	// A line indented this many font sizes more than the line above starts a
	// paragraph.
	indent = 0.6
	// A vertical gap more than this many times the usual line spacing starts
	// a paragraph.
	paragraphGap = 1.4
	// Text this much larger than the body text may be a heading.
	headingRatio = 1.15
	// A line is only a heading if at most this fraction of its characters are
	// body-sized, so a large first letter doesn't make a paragraph a heading.
	headingBodyShare = 0.3
)

// listItem matches lines that start a list or table row, like "1." or "• ".
var listItem = regexp.MustCompile(`^(\d+[.)]?|[•●▪◦*–-])\s`)

// fieldLine matches lines that start with a short label, like "Range: 30 feet".
var fieldLine = regexp.MustCompile(`^[A-Z][\w'’ ]{0,20}: `)

// segment is the text of one line within one column.
type segment struct {
	x0, y   float64
	size    float64 // largest font size in the line
	heading bool
	cells   bool // contains table cell separators
	text    string
}

// Page converts the glyphs of one page into Markdown. width is the page width
// in PDF units. Headings start at level headingLevel (e.g. 2 for "##").
func Page(glyphs []pdf.Text, width float64, headingLevel int) string {
	lines := groupLines(glyphs)
	if len(lines) == 0 {
		return ""
	}
	body := bodySize(lines)
	gutter := findGutter(lines, width)

	// Read the left column top to bottom, then the right. Lines that cross the
	// gutter, such as full-width titles, are read with the left column.
	var left, right []segment
	for _, l := range lines {
		l, r := splitAt(l, gutter)
		if s, ok := makeSegment(l, body); ok {
			left = append(left, s)
		}
		if s, ok := makeSegment(r, body); ok {
			right = append(right, s)
		}
	}

	var b strings.Builder
	writeColumn(&b, left, body, headingLevel)
	writeColumn(&b, right, body, headingLevel)
	return strings.TrimSpace(b.String())
}

// groupLines groups glyphs into lines, top of the page first, each sorted
// left to right.
func groupLines(glyphs []pdf.Text) [][]pdf.Text {
	var gs []pdf.Text
	for _, g := range glyphs {
		g.S = strings.NewReplacer("\n", "", "\r", "").Replace(g.S)
		if g.S != "" {
			gs = append(gs, g)
		}
	}
	// PDF y grows upward, so higher y is nearer the top.
	slices.SortStableFunc(gs, func(a, b pdf.Text) int { return cmp.Compare(b.Y, a.Y) })

	var lines [][]pdf.Text
	for _, g := range gs {
		if n := len(lines); n > 0 {
			ref := lines[n-1][0]
			if math.Abs(ref.Y-g.Y) <= sameLineTolerance*math.Max(ref.FontSize, g.FontSize) {
				lines[n-1] = append(lines[n-1], g)
				continue
			}
		}
		lines = append(lines, []pdf.Text{g})
	}
	for _, l := range lines {
		slices.SortStableFunc(l, func(a, b pdf.Text) int { return cmp.Compare(a.X, b.X) })
	}
	return lines
}

func blank(g pdf.Text) bool { return strings.TrimSpace(g.S) == "" }

// findGutter returns the x position of the gap between two columns of text,
// or 0 if the page has a single column. It looks for the widest vertical
// strip near the middle of the page that almost no text crosses.
func findGutter(lines [][]pdf.Text, width float64) float64 {
	if width <= 0 {
		return 0
	}
	bins := make([]int, int(width)+1)
	for _, l := range lines {
		for _, g := range l {
			if blank(g) {
				continue
			}
			for x := max(int(g.X), 0); x <= min(int(g.X+g.W), len(bins)-1); x++ {
				bins[x]++
			}
		}
	}

	// Allow a few crossings, from full-width titles or wide tables.
	limit := max(1, len(lines)/30)
	lo, hi := int(width*gutterSearch), int(width*(1-gutterSearch))
	bestStart, bestLen := 0, 0
	for x := lo; x <= hi; {
		if bins[x] > limit {
			x++
			continue
		}
		start := x
		for x <= hi && bins[x] <= limit {
			x++
		}
		if x-start > bestLen {
			bestStart, bestLen = start, x-start
		}
	}
	if float64(bestLen) < minGutter {
		return 0
	}
	return float64(bestStart) + float64(bestLen)/2
}

// splitAt divides a line into the glyphs left and right of the gutter. A line
// with text crossing the gutter is kept whole on the left.
func splitAt(line []pdf.Text, gutter float64) (left, right []pdf.Text) {
	if gutter == 0 {
		return line, nil
	}
	for _, g := range line {
		if !blank(g) && g.X < gutter && g.X+g.W > gutter {
			return line, nil
		}
	}
	for _, g := range line {
		if g.X+g.W/2 < gutter {
			left = append(left, g)
		} else {
			right = append(right, g)
		}
	}
	return left, right
}

// makeSegment joins a line's glyphs into text. It reports false if the line
// is empty.
func makeSegment(line []pdf.Text, body float64) (segment, bool) {
	var s segment
	var text strings.Builder
	var prevEnd float64
	bodyChars, chars := 0, 0
	for i, g := range line {
		if blank(g) {
			text.WriteString(" ")
			continue
		}
		if i == 0 || s.x0 == 0 {
			s.x0, s.y = g.X, g.Y
		} else if gap := g.X - prevEnd; gap > cellGap*g.FontSize {
			text.WriteString(" | ")
			s.cells = true
		} else if gap > wordGap*g.FontSize {
			text.WriteString(" ")
		}
		text.WriteString(g.S)
		prevEnd = g.X + g.W
		s.size = math.Max(s.size, g.FontSize)
		n := len([]rune(g.S))
		chars += n
		if g.FontSize < body*headingRatio {
			bodyChars += n
		}
	}
	s.text = strings.Join(strings.Fields(text.String()), " ")
	if s.text == "" {
		return s, false
	}
	s.heading = body > 0 && s.size >= body*headingRatio &&
		float64(bodyChars) <= headingBodyShare*float64(chars)
	return s, true
}

// bodySize returns the most common font size on the page, weighted by the
// number of characters set in it.
func bodySize(lines [][]pdf.Text) float64 {
	counts := map[float64]int{}
	for _, l := range lines {
		for _, g := range l {
			if !blank(g) {
				counts[math.Round(g.FontSize*2)/2] += len(g.S)
			}
		}
	}
	var best float64
	for size, n := range counts {
		if n > counts[best] || (n == counts[best] && size < best) {
			best = size
		}
	}
	return best
}

// writeColumn writes a column's lines as Markdown paragraphs, table rows and
// headings.
func writeColumn(b *strings.Builder, segs []segment, body float64, headingLevel int) {
	spacing := lineSpacing(segs)

	var para string
	flush := func() {
		if para != "" {
			b.WriteString(para + "\n\n")
			para = ""
		}
	}

	for i, s := range segs {
		var prev *segment
		if i > 0 {
			prev = &segs[i-1]
		}
		gap := prev != nil && prev.y-s.y > paragraphGap*spacing

		switch {
		case s.heading:
			level := headingFor(s.size, body, headingLevel)
			// Consecutive heading lines of the same size are one wrapped heading.
			if prev != nil && prev.heading && !gap && headingFor(prev.size, body, headingLevel) == level {
				out := strings.TrimSuffix(b.String(), "\n\n") + " " + s.text + "\n\n"
				b.Reset()
				b.WriteString(out)
				continue
			}
			flush()
			b.WriteString(strings.Repeat("#", level) + " " + s.text + "\n\n")

		case s.cells:
			// Table rows keep their own lines.
			flush()
			b.WriteString(s.text + "\n\n")

		default:
			newPara := prev == nil || prev.heading || prev.cells || gap ||
				listItem.MatchString(s.text) || fieldLine.MatchString(s.text) ||
				(s.x0-prev.x0 > indent*body && !listItem.MatchString(prev.text))
			if newPara {
				flush()
				para = s.text
			} else {
				para = joinLines(para, s.text)
			}
		}
	}
	flush()
}

// joinLines appends a wrapped line to a paragraph, rejoining words split by a
// soft hyphen.
func joinLines(para, line string) string {
	if p, ok := strings.CutSuffix(para, "­"); ok {
		return p + line
	}
	return para + " " + line
}

// headingFor returns the Markdown heading level for text of the given size.
func headingFor(size, body float64, base int) int {
	level := base + 2
	switch {
	case size >= body*2:
		level = base
	case size >= body*1.5:
		level = base + 1
	}
	return min(level, 6)
}

// lineSpacing returns the typical vertical distance between consecutive
// lines in a column.
func lineSpacing(segs []segment) float64 {
	var gaps []float64
	for i := 1; i < len(segs); i++ {
		if d := segs[i-1].y - segs[i].y; d > 0 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return 12
	}
	slices.Sort(gaps)
	return gaps[len(gaps)/2]
}
