package skills

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(found []Found) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A plugin manifest is authoritative: only its listed skills are returned, even
// when other SKILL.md dirs exist on disk (e.g. deprecated/).
func TestDiscoverManifest(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, "skills", "engineering", "tdd"), "tdd")
	writeSkill(t, filepath.Join(repo, "skills", "productivity", "teach"), "teach")
	writeSkill(t, filepath.Join(repo, "skills", "deprecated", "old"), "old")

	manifest := `{"name":"x","skills":["./skills/engineering/tdd","./skills/productivity/teach"]}`
	if err := os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude-plugin", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Discover(repo)
	eq(t, names(got), []string{"tdd", "teach"})
	for _, f := range got {
		if f.Subdir == "" || !filepath.IsAbs(f.Dir) {
			t.Fatalf("bad subdir/dir: %+v", f)
		}
	}
}

// Without a manifest, the recursive walk finds nested SKILL.md dirs and prunes
// into a skill's own subtree.
func TestDiscoverWalk(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "root")
	writeSkill(t, filepath.Join(repo, "skills", "engineering", "tdd"), "tdd")
	writeSkill(t, filepath.Join(repo, "top"), "top")
	// Nested under a found skill — must NOT be reported as a separate skill.
	writeSkill(t, filepath.Join(repo, "top", "bundled"), "bundled")

	got := Discover(repo)
	eq(t, names(got), []string{"root", "tdd", "top"})
}

// A walk deeper than walkDepth is pruned.
func TestDiscoverWalkDepthCap(t *testing.T) {
	repo := t.TempDir()
	deep := filepath.Join(repo, "a", "b", "c", "d", "e")
	writeSkill(t, deep, "toodeep")
	if got := Discover(repo); len(got) != 0 {
		t.Fatalf("expected nothing past depth cap, got %v", names(got))
	}
}
