package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("rules.md", "# Rules\nDragons breathe fire.\nGoblins steal gold.\n")
	write("lore/history.txt", "The kingdom fell in year 412.\n")
	write("image.bin", "\x89PNG\x00\x00data")
	write(".git/config", "dragons are secret")

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestFiles(t *testing.T) {
	files, err := newLibrary(t).Files()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(files, ","); got != "lore/history.txt,rules.md" {
		t.Errorf("Files() = %s", got)
	}
}

func TestSearch(t *testing.T) {
	l := newLibrary(t)
	got, err := l.Search("DRAGONS")
	if err != nil {
		t.Fatal(err)
	}
	if got != "rules.md:2: Dragons breathe fire.\n" {
		t.Errorf("Search = %q", got)
	}
	if got, _ := l.Search("unicorn"); got != "No matches found." {
		t.Errorf("Search(unicorn) = %q", got)
	}
}

func TestRead(t *testing.T) {
	l := newLibrary(t)
	got, err := l.Read("rules.md", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2: Dragons breathe fire.\n3: Goblins steal gold.\n" {
		t.Errorf("Read = %q", got)
	}
	for _, p := range []string{"../etc/passwd", "/../../etc/passwd", "image.bin"} {
		if _, err := l.Read(p, 0, 0); err == nil {
			t.Errorf("Read(%q) succeeded, want error", p)
		}
	}
}

func TestCall(t *testing.T) {
	l := newLibrary(t)
	got := l.Call("read_file", json.RawMessage(`{"path":"lore/history.txt"}`))
	if got != "1: The kingdom fell in year 412.\n" {
		t.Errorf("read_file = %q", got)
	}
	if got := l.Call("delete_everything", nil); !strings.HasPrefix(got, "error:") {
		t.Errorf("unknown tool = %q", got)
	}
}
