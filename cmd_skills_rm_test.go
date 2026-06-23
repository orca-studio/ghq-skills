package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/x-motemen/ghq/skills"
)

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// rm drops the lockfile entry + store/agent symlinks but leaves the clone, and
// notes a clone only when its last referencing skill is removed.
func TestSkillsRm(t *testing.T) {
	newTempDir(t) // cwd has no .git → -g resolves cleanly to global scope

	store := t.TempDir()
	setEnv(t, "GHQ_SKILLS_ROOT", store)
	agentDir := t.TempDir()
	setEnv(t, "GHQ_AGENT_CLAUDE_CODE", agentDir)

	// Stand-in clones (real dirs the symlinks point at). rm must not delete these.
	larkClone := t.TempDir()
	grillClone := t.TempDir()
	larkBase := filepath.Join(larkClone, "skills", "lark-base")
	larkDoc := filepath.Join(larkClone, "skills", "lark-doc")
	grilling := filepath.Join(grillClone, "grilling")
	for _, d := range []string{larkBase, larkDoc, grilling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	lock := &skills.Lock{Skill: []skills.Skill{
		{Name: "lark-base", Repo: "github.com/larksuite/cli", Subdir: "skills/lark-base", Locked: "aaa", Link: "lark-base"},
		{Name: "lark-doc", Repo: "github.com/larksuite/cli", Subdir: "skills/lark-doc", Locked: "aaa", Link: "lark-doc"},
		{Name: "grilling", Repo: "github.com/mattpocock/skills", Subdir: "grilling", Locked: "bbb", Link: "grilling"},
	}}
	if err := lock.Save(filepath.Join(store, "skills.lock.toml")); err != nil {
		t.Fatal(err)
	}
	// Wire store + agent symlinks as get/restore would.
	for _, s := range lock.Skill {
		tgt := map[string]string{"lark-base": larkBase, "lark-doc": larkDoc, "grilling": grilling}[s.Name]
		mustSymlink(t, tgt, filepath.Join(store, s.Link))
		mustSymlink(t, tgt, filepath.Join(agentDir, s.Link))
	}

	out, _, err := capture(func() {
		if e := newApp().Run(context.Background(), []string{"ghq", "skills", "rm", "lark-base", "grilling", "-g"}); e != nil {
			t.Errorf("rm: %v", e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lockfile: only lark-doc remains.
	got, _ := skills.LoadLock(filepath.Join(store, "skills.lock.toml"))
	if len(got.Skill) != 1 || got.Skill[0].Name != "lark-doc" {
		t.Fatalf("lock after rm = %+v, want only lark-doc", got.Skill)
	}

	// Removed skills' symlinks gone from both store and agent dir.
	for _, name := range []string{"lark-base", "grilling"} {
		if exists(filepath.Join(store, name)) {
			t.Errorf("store symlink %s should be gone", name)
		}
		if exists(filepath.Join(agentDir, name)) {
			t.Errorf("agent symlink %s should be gone", name)
		}
	}
	// Survivor untouched.
	if !exists(filepath.Join(store, "lark-doc")) || !exists(filepath.Join(agentDir, "lark-doc")) {
		t.Error("lark-doc links should survive")
	}

	// Clones never deleted.
	for _, d := range []string{larkBase, larkDoc, grilling, larkClone, grillClone} {
		if !exists(d) {
			t.Errorf("clone path %s must not be deleted", d)
		}
	}

	// grilling was the only skill from mattpocock/skills → note it; larksuite/cli
	// still has lark-doc → no note for it.
	if !strings.Contains(out, "github.com/mattpocock/skills is now unreferenced") {
		t.Errorf("expected unreferenced-clone note for mattpocock/skills, got:\n%s", out)
	}
	if strings.Contains(out, "github.com/larksuite/cli is now unreferenced") {
		t.Errorf("larksuite/cli is still referenced; should not be noted:\n%s", out)
	}
}

// A typo'd name aborts the whole op without removing anything.
func TestSkillsRmUnknownAborts(t *testing.T) {
	newTempDir(t)
	store := t.TempDir()
	setEnv(t, "GHQ_SKILLS_ROOT", store)
	setEnv(t, "GHQ_AGENT_CLAUDE_CODE", t.TempDir())

	lock := &skills.Lock{Skill: []skills.Skill{
		{Name: "lark-base", Repo: "github.com/larksuite/cli", Link: "lark-base"},
	}}
	if err := lock.Save(filepath.Join(store, "skills.lock.toml")); err != nil {
		t.Fatal(err)
	}

	err := newApp().Run(context.Background(), []string{"ghq", "skills", "rm", "lark-base", "typo", "-g"})
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("expected error naming the missing skill, got: %v", err)
	}
	got, _ := skills.LoadLock(filepath.Join(store, "skills.lock.toml"))
	if len(got.Skill) != 1 {
		t.Fatalf("lock should be unchanged after abort, got %+v", got.Skill)
	}
}
