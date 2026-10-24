package scribe

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestSave(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	sent := time.Date(2026, 10, 21, 9, 5, 7, 0, time.FixedZone("PDT", -7*60*60))

	for i, short := range []string{"short", "  0123456789 \n", "éééééééééé"} {
		if p, err := w.Save(strconv.Itoa(i), short, sent); err != nil || p != "" {
			t.Errorf("Save(%q) = %q, %v; want skipped", short, p, err)
		}
	}

	var got []string
	for i, msg := range []string{"# Session one\n\nThe party met a dragon.", "Another long message."} {
		p, err := w.Save(strconv.Itoa(10+i), msg, sent)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.Base(p))
	}
	if got[0] != "2026-10-21T16-05-07Z.md" || got[1] != "2026-10-21T16-05-07Z-2.md" {
		t.Errorf("names = %v", got)
	}

	b, err := os.ReadFile(filepath.Join(dir, got[0]))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "# Session one\n\nThe party met a dragon.\n" {
		t.Errorf("content = %q", b)
	}

	// A message already saved is skipped, also after reopening.
	if p, _ := w.Save("10", "# Session one\n\nThe party met a dragon.", sent); p != "" {
		t.Errorf("second Save of the same message wrote %s", p)
	}
	w2, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := w2.Save("11", "Another long message.", sent); p != "" {
		t.Errorf("Save after reopening wrote %s", p)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("dir has %d entries, want 2 files and the index (no temp files left behind)", len(entries))
	}
}

func TestBackfill(t *testing.T) {
	// 250 messages with IDs 1..250; pages of 100 come back newest first.
	fetch := func(after string) ([]*discordgo.Message, error) {
		a, _ := strconv.Atoi(after)
		var page []*discordgo.Message
		for id := min(a+100, 250); id > a; id-- {
			page = append(page, &discordgo.Message{ID: strconv.Itoa(id)})
		}
		return page, nil
	}
	var ids []int
	n, err := Backfill(fetch, func(m *discordgo.Message) {
		id, _ := strconv.Atoi(m.ID)
		ids = append(ids, id)
	})
	if err != nil || n != 250 {
		t.Fatalf("Backfill = %d, %v", n, err)
	}
	for i, id := range ids {
		if id != i+1 {
			t.Fatalf("message %d has ID %d; want oldest first", i, id)
		}
	}
}
