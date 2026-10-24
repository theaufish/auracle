// Package docs exposes a directory of text files to the model as tools it can
// use to list, search and read them.
package docs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/aufishgamelabs/auracle/internal/ollama"
)

const (
	maxSearchResults = 50
	maxReadChars     = 20000
	maxLineChars     = 300
	// Files with a NUL byte in this many leading bytes are treated as binary.
	sniffLen = 8000
)

// Library gives read-only access to the text files under a directory.
// Paths can never escape the directory.
type Library struct {
	root *os.Root
	fsys fs.FS
}

// Open returns a Library for the files under dir.
func Open(dir string) (*Library, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Library{root: root, fsys: root.FS()}, nil
}

// Close releases the directory handle.
func (l *Library) Close() error { return l.root.Close() }

// Files returns the slash-separated paths of every text file in the library,
// skipping hidden files and directories (names starting with "." or "_").
func (l *Library) Files() ([]string, error) {
	var files []string
	err := fs.WalkDir(l.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && isHidden(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && l.isText(p) {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}

// isHidden reports whether a file or directory name is ignored: names
// starting with "." or "_".
func isHidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// hasHiddenPart reports whether any element of the slash-separated path p is
// hidden, so a hidden file can't be read by naming it, or one inside a hidden
// directory.
func hasHiddenPart(p string) bool {
	if p == "." {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if isHidden(part) {
			return true
		}
	}
	return false
}

func (l *Library) isText(p string) bool {
	f, err := l.fsys.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, sniffLen)
	n, _ := io.ReadFull(f, buf)
	return !bytes.ContainsRune(buf[:n], 0)
}

// Search returns "path:line: text" for every line containing query,
// ignoring case.
func (l *Library) Search(query string) (string, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return "", fmt.Errorf("query is empty")
	}
	files, err := l.Files()
	if err != nil {
		return "", err
	}

	var out strings.Builder
	matches := 0
	for _, p := range files {
		f, err := l.fsys.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if !strings.Contains(strings.ToLower(line), query) {
				continue
			}
			if matches == maxSearchResults {
				f.Close()
				fmt.Fprintf(&out, "(stopped after %d matches; try a more specific query)\n", maxSearchResults)
				return out.String(), nil
			}
			fmt.Fprintf(&out, "%s:%d: %s\n", p, n, truncate(strings.TrimSpace(line), maxLineChars))
			matches++
		}
		f.Close()
	}
	if matches == 0 {
		return "No matches found.", nil
	}
	return out.String(), nil
}

// Read returns the lines of p from start to end (1-based, inclusive),
// numbered. end <= 0 means the end of the file.
func (l *Library) Read(p string, start, end int) (string, error) {
	p = path.Clean(strings.TrimPrefix(p, "/"))
	if !fs.ValidPath(p) || hasHiddenPart(p) || !l.isText(p) {
		return "", fmt.Errorf("%s is not a readable text file in the library", p)
	}
	f, err := l.fsys.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if start < 1 {
		start = 1
	}
	var out strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		if n < start {
			continue
		}
		if end > 0 && n > end {
			break
		}
		if out.Len() > maxReadChars {
			fmt.Fprintf(&out, "(truncated; continue with start_line=%d)\n", n)
			break
		}
		fmt.Fprintf(&out, "%d: %s\n", n, sc.Text())
	}
	if out.Len() == 0 {
		return "(no lines in that range)", nil
	}
	return out.String(), sc.Err()
}

// Tools implements ollama.ToolHandler.
func (l *Library) Tools() []ollama.Tool {
	return []ollama.Tool{
		tool("list_files", "List the paths of all files in the knowledge base.", nil, nil),
		tool("search_files",
			"Case-insensitive search for a word or phrase across every file. Returns matching lines as path:line: text.",
			map[string]any{"query": prop("string", "Word or phrase to search for. Short queries match more.")},
			[]string{"query"}),
		tool("read_file",
			"Read a file from the knowledge base, with line numbers.",
			map[string]any{
				"path":       prop("string", "File path as returned by list_files or search_files."),
				"start_line": prop("integer", "First line to read (default 1)."),
				"end_line":   prop("integer", "Last line to read (default end of file)."),
			},
			[]string{"path"}),
	}
}

// Call implements ollama.ToolHandler.
func (l *Library) Call(name string, raw json.RawMessage) string {
	var args struct {
		Query     string `json:"query"`
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "error: invalid arguments: " + err.Error()
		}
	}

	var out string
	var err error
	switch name {
	case "list_files":
		var files []string
		if files, err = l.Files(); err == nil {
			out = strings.Join(files, "\n")
			if out == "" {
				out = "The knowledge base is empty."
			}
		}
	case "search_files":
		out, err = l.Search(args.Query)
	case "read_file":
		out, err = l.Read(args.Path, args.StartLine, args.EndLine)
	default:
		err = fmt.Errorf("unknown tool %q", name)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

func tool(name, desc string, props map[string]any, required []string) ollama.Tool {
	if props == nil {
		props = map[string]any{}
	}
	params := map[string]any{"type": "object", "properties": props}
	if required != nil {
		params["required"] = required
	}
	return ollama.Tool{
		Type:     "function",
		Function: ollama.ToolFunction{Name: name, Description: desc, Parameters: params},
	}
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
