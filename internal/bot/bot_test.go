package bot

import (
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	long := strings.Repeat("word ", 1000) // 5000 chars
	chunks := split(long, maxDiscordMessage)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want at least 3", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > maxDiscordMessage {
			t.Errorf("chunk %d has %d chars, over limit", i, n)
		}
	}
	if got := strings.Join(chunks, " "); strings.Fields(got)[0] != "word" || len(strings.Fields(got)) != 1000 {
		t.Errorf("words lost while splitting")
	}

	if got := split("short", 10); len(got) != 1 || got[0] != "short" {
		t.Errorf("split(short) = %q", got)
	}
}

func TestStripMention(t *testing.T) {
	for _, in := range []string{"<@123> hello", "<@!123> hello", "hello <@123>"} {
		if got := stripMention(in, "123"); got != "hello" {
			t.Errorf("stripMention(%q) = %q", in, got)
		}
	}
}

func TestUnclearReply(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		r := unclearReply()
		if r == "" {
			t.Fatal("empty unclear reply")
		}
		seen[r] = true
	}
	if len(seen) < 2 {
		t.Errorf("got %d distinct replies, want variety", len(seen))
	}
}
