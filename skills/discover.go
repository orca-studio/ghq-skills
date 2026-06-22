package skills

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Found is a single discovered skill within a repo clone.
type Found struct {
	Name   string // from SKILL.md frontmatter `name:`, else the dir base
	Subdir string // path relative to the repo root ("" means repo root)
	Dir    string // absolute path to the skill directory
}

// Discover looks for SKILL.md at the repo root, in any top-level dir, and under
// a conventional skills/ dir. It is intentionally shallow.
func Discover(repoPath string) []Found {
	seen := map[string]bool{}
	var out []Found

	add := func(dir string) {
		md := filepath.Join(dir, "SKILL.md")
		if !isFile(md) || seen[dir] {
			return
		}
		seen[dir] = true
		rel, _ := filepath.Rel(repoPath, dir)
		if rel == "." {
			rel = ""
		}
		out = append(out, Found{Name: nameFor(md, dir), Subdir: rel, Dir: dir})
	}

	add(repoPath)
	scan(repoPath, add)
	scan(filepath.Join(repoPath, "skills"), add)
	return out
}

// SelectByName keeps only skills whose name is in names ("*" matches all).
func SelectByName(found []Found, names []string) []Found {
	want := map[string]bool{}
	for _, n := range names {
		if n == "*" {
			return found
		}
		want[n] = true
	}
	var out []Found
	for _, f := range found {
		if want[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

// SelectBySubdir keeps only the skill whose subdir matches sub.
func SelectBySubdir(found []Found, sub string) []Found {
	sub = filepath.Clean(sub)
	var out []Found
	for _, f := range found {
		if filepath.Clean(f.Subdir) == sub {
			out = append(out, f)
		}
	}
	return out
}

func scan(parent string, add func(string)) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			add(filepath.Join(parent, e.Name()))
		}
	}
}

func nameFor(md, dir string) string {
	if n := frontmatterName(md); n != "" {
		return n
	}
	return filepath.Base(dir)
}

// frontmatterName pulls `name:` out of a leading --- YAML frontmatter block.
func frontmatterName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
