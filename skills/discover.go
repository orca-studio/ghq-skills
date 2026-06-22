package skills

import (
	"bufio"
	"encoding/json"
	"io/fs"
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

// walkDepth bounds the recursive fallback so it stays cheap on large repos.
// A SKILL.md nested at skills/<category>/<skill>/ sits 3 levels deep; allow a
// little headroom beyond that.
const walkDepth = 4

// Discover finds the skills in a repo clone. When the repo ships a Claude Code
// plugin manifest (.claude-plugin/plugin.json), its `skills` array is the
// authoritative list and is honored verbatim — matching what `npx skills` reads.
// Otherwise it falls back to a bounded recursive walk for any directory holding
// a SKILL.md.
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

	if dirs := manifestSkillDirs(repoPath); dirs != nil {
		for _, dir := range dirs {
			add(dir)
		}
		return out
	}

	walk(repoPath, add)
	return out
}

// manifestSkillDirs reads a Claude Code plugin manifest and resolves its
// `skills` array to absolute directories. It returns nil when no manifest is
// present (so the caller falls back to walking), and a non-nil (possibly empty)
// slice when one is — a manifest is always authoritative once found.
func manifestSkillDirs(repoPath string) []string {
	for _, rel := range []string{
		filepath.Join(".claude-plugin", "plugin.json"),
		filepath.Join("claude-plugin", "plugin.json"),
	} {
		data, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		var m struct {
			Skills []string `json:"skills"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		dirs := make([]string, 0, len(m.Skills))
		for _, s := range m.Skills {
			dirs = append(dirs, filepath.Join(repoPath, filepath.Clean(s)))
		}
		return dirs
	}
	return nil
}

// walk recurses up to walkDepth directories deep, calling add on each. Once a
// SKILL.md is found in a directory it does not descend into that directory's
// subtree (a skill's own bundled files are not separate skills). It also skips
// hidden and version-control directories.
func walk(root string, add func(string)) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != root {
			base := filepath.Base(path)
			if base == ".git" || strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			if rel, _ := filepath.Rel(root, path); strings.Count(rel, string(filepath.Separator))+1 > walkDepth {
				return fs.SkipDir
			}
		}
		if isFile(filepath.Join(path, "SKILL.md")) {
			add(path)
			// A repo-root SKILL.md doesn't preclude nested skills; keep
			// descending there. For any other skill dir, its bundled files
			// are not separate skills, so prune.
			if path != root {
				return fs.SkipDir
			}
		}
		return nil
	})
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
