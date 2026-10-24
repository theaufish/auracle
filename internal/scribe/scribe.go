// Package scribe saves long chat messages as Markdown files named after the
// time they were sent.
package scribe

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// fileTime names files by UTC send time. It has no colons, which macOS and
// Windows don't allow in file names, and sorts chronologically.
const fileTime = "2006-01-02T15-04-05Z"

// indexName lists the IDs of saved messages, one per line, so a message is
// never saved twice. Its leading "." hides it from the auracle bot.
const indexName = ".scribe-saved"

// Writer saves messages longer than a minimum length to a directory.
type Writer struct {
	dir      string
	minChars int

	// Messages arrive concurrently, both live and from the history; this keeps
	// two from picking the same name or saving the same message twice.
	mu    sync.Mutex
	saved map[string]bool
}

// Open returns a Writer that saves messages longer than minChars characters
// to dir, skipping messages it saved before.
func Open(dir string, minChars int) (*Writer, error) {
	w := &Writer{dir: dir, minChars: minChars, saved: make(map[string]bool)}
	f, err := os.Open(filepath.Join(dir, indexName))
	if errors.Is(err, fs.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if id := strings.TrimSpace(sc.Text()); id != "" {
			w.saved[id] = true
		}
	}
	return w, sc.Err()
}

// Save writes the message's content to <dir>/<sent>.md if it has more than
// the minimum number of characters and hasn't been saved before. It returns
// the path written, or "" if the message was skipped. A message sent in the
// same second as an earlier one gets a -2, -3, … suffix.
func (w *Writer) Save(id, content string, sent time.Time) (string, error) {
	content = strings.TrimSpace(content)
	if utf8.RuneCountInString(content) <= w.minChars {
		return "", nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.saved[id] {
		return "", nil
	}

	// Write under a hidden name first, which the auracle bot ignores, so it
	// never sees a half-written file.
	tmp, err := os.CreateTemp(w.dir, ".scribe-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	base := sent.UTC().Format(fileTime)
	for n := 1; ; n++ {
		name := base + ".md"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.md", base, n)
		}
		p := filepath.Join(w.dir, name)
		// Link fails if p exists, so an existing file is never overwritten.
		err := os.Link(tmp.Name(), p)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return p, w.record(id)
	}
}

// record adds id to the index of saved messages.
func (w *Writer) record(id string) error {
	w.saved[id] = true
	f, err := os.OpenFile(filepath.Join(w.dir, indexName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(id + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// FetchFunc returns up to 100 messages sent after the message afterID, in any
// order. afterID "0" means the start of the channel.
type FetchFunc func(afterID string) ([]*discordgo.Message, error)

// Backfill reads the channel's whole history, oldest first, and passes each
// message to save. It returns the number of messages read.
func Backfill(fetch FetchFunc, save func(*discordgo.Message)) (int, error) {
	after, read := "0", 0
	for {
		msgs, err := fetch(after)
		if err != nil {
			return read, err
		}
		if len(msgs) == 0 {
			return read, nil
		}
		// Discord returns pages newest first; IDs are snowflakes, which
		// increase with time.
		slices.SortFunc(msgs, func(a, b *discordgo.Message) int { return compareIDs(a.ID, b.ID) })
		for _, m := range msgs {
			save(m)
		}
		read += len(msgs)
		after = msgs[len(msgs)-1].ID
	}
}

func compareIDs(a, b string) int {
	x, _ := strconv.ParseUint(a, 10, 64)
	y, _ := strconv.ParseUint(b, 10, 64)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}
