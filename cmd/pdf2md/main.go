// Command pdf2md splits a PDF into Markdown files, one per page, so its text
// can be used as part of the bot's DOCS_DIR.
//
// Usage:
//
//	pdf2md [-o dir] [-title title] [-skip-empty=false] file.pdf
//
// Each page N is written to <dir>/<name>-pNNN.md, where <name> is the PDF's
// file name without its extension. dir defaults to the PDF's directory.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/aufishgamelabs/auracle/internal/pdfmd"
)

func main() {
	outDir := flag.String("o", "", "output directory (default: the PDF's directory)")
	title := flag.String("title", "", "title used in each page's heading (default: derived from the file name)")
	skipEmpty := flag.Bool("skip-empty", true, "don't write pages with no text, such as full-page images")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: pdf2md [flags] file.pdf\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	in := flag.Arg(0)

	name := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
	if *outDir == "" {
		*outDir = filepath.Dir(in)
	}
	if *title == "" {
		*title = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	}

	if err := run(in, *outDir, name, *title, *skipEmpty); err != nil {
		slog.Error("conversion failed", "file", in, "err", err)
		os.Exit(1)
	}
}

func run(in, outDir, name, title string, skipEmpty bool) error {
	f, r, err := pdf.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	pages := r.NumPage()
	width := len(fmt.Sprint(pages))
	written, empty, failed := 0, 0, 0
	for n := 1; n <= pages; n++ {
		text, err := pageMarkdown(r.Page(n))
		if err != nil {
			slog.Warn("could not read page", "page", n, "err", err)
			failed++
			continue
		}
		if text == "" {
			empty++
			if skipEmpty {
				slog.Debug("skipping page with no text", "page", n)
				continue
			}
		}

		out := filepath.Join(outDir, fmt.Sprintf("%s-p%0*d.md", name, max(width, 3), n))
		doc := fmt.Sprintf("# %s, page %d\n\n%s\n", title, n, text)
		if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
			return err
		}
		written++
	}

	slog.Info("done", "pages", pages, "written", written, "empty", empty, "failed", failed, "dir", outDir)
	return nil
}

// pageMarkdown converts one page, turning a panic in the PDF library (which
// happens on some malformed pages) into an error.
func pageMarkdown(p pdf.Page) (md string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	if p.V.IsNull() {
		return "", fmt.Errorf("page not found")
	}

	width := 612.0 // US Letter, if the page doesn't say
	if box := p.V.Key("MediaBox"); box.Len() == 4 {
		width = box.Index(2).Float64() - box.Index(0).Float64()
	}
	return pdfmd.Page(p.Content().Text, width, 2), nil
}
