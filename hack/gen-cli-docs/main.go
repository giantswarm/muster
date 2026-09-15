package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/giantswarm/muster/v5/cmd"
)

const defaultOutputDir = "docs/reference/cli"

func main() {
	outputDir := defaultOutputDir
	if len(os.Args) > 1 {
		outputDir = os.Args[1]
	}
	if err := run(outputDir); err != nil {
		fmt.Fprintln(os.Stderr, "gen-cli-docs:", err)
		os.Exit(1)
	}
}

// run writes one markdown page per visible command below root into outputDir
// and removes pages of commands that no longer exist.
func run(outputDir string) error {
	root := cmd.RootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	// outputDir is named on the command line by the developer running the
	// generator (default docs/reference/cli); it is not untrusted input.
	if err := os.MkdirAll(outputDir, 0o750); err != nil { //nolint:gosec // see above
		return err
	}

	written := map[string]bool{}
	var visit func(c *cobra.Command) error
	visit = func(c *cobra.Command) error {
		if c != root && (c.Hidden || c.Name() == "help") {
			return nil
		}
		c.DisableAutoGenTag = true
		page, err := render(c)
		if err != nil {
			return err
		}
		name := fileName(c)
		if err := os.WriteFile(filepath.Join(outputDir, name), page, 0o600); err != nil { //nolint:gosec // outputDir, see run
			return err
		}
		written[name] = true
		for _, sub := range c.Commands() {
			if err := visit(sub); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return err
	}
	return removeStale(outputDir, written)
}

// render produces the page of one command: Cobra's markdown with the title
// as the level-one heading, its sections one level below, links rewritten to
// this directory's file names and the generating user's home directory
// replaced by "~" so the page does not depend on who rendered it.
func render(c *cobra.Command) ([]byte, error) {
	var buf bytes.Buffer
	if err := doc.GenMarkdownCustom(c, &buf, linkHandler); err != nil {
		return nil, err
	}
	page := buf.Bytes()
	title := []byte("## " + c.CommandPath() + "\n")
	if bytes.HasPrefix(page, title) {
		page = append([]byte("# "+c.CommandPath()+"\n"), page[len(title):]...)
	}
	page = bytes.ReplaceAll(page, []byte("\n### "), []byte("\n## "))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		page = bytes.ReplaceAll(page, []byte(home), []byte("~"))
	}
	return tidy(page), nil
}

// tidy strips trailing whitespace from every line and ends the page with one
// newline, the form the repository's pre-commit hooks require.
func tidy(page []byte) []byte {
	lines := bytes.Split(page, []byte("\n"))
	for i, line := range lines {
		lines[i] = bytes.TrimRight(line, " \t\r")
	}
	out := bytes.TrimRight(bytes.Join(lines, []byte("\n")), "\n")
	return append(out, '\n')
}

// fileName maps a command to its page: the root command is README.md, every
// other command is its path below "muster" joined with dashes.
func fileName(c *cobra.Command) string {
	return linkHandler(strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md")
}

// linkHandler rewrites Cobra's default page names (muster_auth_login.md) to
// the names used here (auth-login.md, README.md for the root command).
func linkHandler(name string) string {
	base := strings.TrimSuffix(name, ".md")
	if base == "muster" {
		return "README.md"
	}
	return strings.ReplaceAll(strings.TrimPrefix(base, "muster_"), "_", "-") + ".md"
}

// removeStale deletes markdown pages in dir that were not written in this run.
func removeStale(dir string, written map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var stale []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !written[e.Name()] {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		// dir is the generator's own output directory, named on the command line
		// by the developer; name is an entry read from that directory.
		if err := os.Remove(filepath.Join(dir, filepath.Base(name))); err != nil { //nolint:gosec // see above
			return err
		}
	}
	return nil
}
