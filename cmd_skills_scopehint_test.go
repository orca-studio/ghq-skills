package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGlobalLock points GHQ_SKILLS_ROOT at a fresh dir holding a one-skill
// lockfile, so global scope resolves to a non-empty lock.
func writeGlobalLock(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	setEnv(t, "GHQ_SKILLS_ROOT", root)
	body := "[[skill]]\nname = \"lark-base\"\nrepo = \"github.com/larksuite/cli\"\nlocked = \"deadbeef\"\nlink = \"lark-base\"\n"
	if err := os.WriteFile(filepath.Join(root, "skills.lock.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// In a repo with no project lock, project-scope `status` should point the user
// at the populated global scope instead of silently reporting nothing.
func TestScopeHintProjectSuggestsGlobal(t *testing.T) {
	dir := newTempDir(t) // chdir's into a temp dir; restored on cleanup
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlobalLock(t)

	out, _, err := capture(func() {
		newApp().Run(context.Background(), []string{"ghq", "skills", "status"})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "project scope by default") || !strings.Contains(out, "re-run with `-g`") {
		t.Errorf("expected scope-explaining -g hint, got:\n%s", out)
	}
}

// With -g the user is already in the scope that has the skills, so no hint
// (the global lock is non-empty here anyway, but the symmetric guard must not
// misfire for the populated scope).
func TestScopeHintNoSuggestionOutsideRepo(t *testing.T) {
	newTempDir(t) // temp dir with no .git → findProjectRoot() == ""
	writeGlobalLock(t)

	// An empty project lock can't exist here (no repo), and global is populated,
	// so a bare `status` resolves to global and prints skills — no hint line.
	// Use --lockfile to an empty file to force the empty branch and confirm the
	// hint stays silent when there's no other scope to point at.
	empty := filepath.Join(t.TempDir(), "skills.lock.toml")
	out, _, err := capture(func() {
		newApp().Run(context.Background(), []string{"ghq", "skills", "status", "--lockfile", empty})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out, "re-run with") || strings.Contains(out, "re-run without") {
		t.Errorf("explicit --lockfile should never hint, got:\n%s", out)
	}
}
