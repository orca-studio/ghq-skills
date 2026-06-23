package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/x-motemen/ghq/skills"
)

// useTempGhqRoot points ghq's local-repository root at an empty temp dir so the
// source resolver (newURL + LocalRepositoryFromURL) walks a controlled tree.
func useTempGhqRoot(t *testing.T) {
	t.Helper()
	setEnv(t, envGhqRoot, t.TempDir())
	orig := _localRepositoryRoots
	t.Cleanup(func() { _localRepositoryRoots = orig; localRepoOnce = &sync.Once{} })
	_localRepositoryRoots = nil
	localRepoOnce = &sync.Once{}
}

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

// rm accepts a source (owner/repo): with a non-TTY stdin it removes every locked
// skill from that repo, leaving other repos' skills and all clones untouched.
func TestSkillsRmBySource(t *testing.T) {
	newTempDir(t)
	useTempGhqRoot(t)
	store := t.TempDir()
	setEnv(t, "GHQ_SKILLS_ROOT", store)
	setEnv(t, "GHQ_AGENT_CLAUDE_CODE", t.TempDir())

	lock := &skills.Lock{Skill: []skills.Skill{
		{Name: "foo", Repo: "github.com/acme/kit", Subdir: "skills/foo", Link: "foo"},
		{Name: "bar", Repo: "github.com/acme/kit", Subdir: "skills/bar", Link: "bar"},
		{Name: "solo", Repo: "github.com/other/repo", Subdir: "solo", Link: "solo"},
	}}
	if err := lock.Save(filepath.Join(store, "skills.lock.toml")); err != nil {
		t.Fatal(err)
	}

	out, _, err := capture(func() {
		if e := newApp().Run(context.Background(), []string{"ghq", "skills", "rm", "acme/kit", "-g"}); e != nil {
			t.Errorf("rm acme/kit: %v", e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := skills.LoadLock(filepath.Join(store, "skills.lock.toml"))
	if len(got.Skill) != 1 || got.Skill[0].Name != "solo" {
		t.Fatalf("lock after rm source = %+v, want only solo", got.Skill)
	}
	if !strings.Contains(out, "removed foo") || !strings.Contains(out, "removed bar") {
		t.Errorf("expected both acme/kit skills removed, got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/acme/kit is now unreferenced") {
		t.Errorf("expected unreferenced-clone note for acme/kit, got:\n%s", out)
	}
}

// --skill narrows a source to specific names without prompting.
func TestSkillsRmSourceWithSkillFlag(t *testing.T) {
	newTempDir(t)
	useTempGhqRoot(t)
	store := t.TempDir()
	setEnv(t, "GHQ_SKILLS_ROOT", store)
	setEnv(t, "GHQ_AGENT_CLAUDE_CODE", t.TempDir())

	lock := &skills.Lock{Skill: []skills.Skill{
		{Name: "foo", Repo: "github.com/acme/kit", Link: "foo"},
		{Name: "bar", Repo: "github.com/acme/kit", Link: "bar"},
	}}
	if err := lock.Save(filepath.Join(store, "skills.lock.toml")); err != nil {
		t.Fatal(err)
	}

	if e := newApp().Run(context.Background(), []string{"ghq", "skills", "rm", "acme/kit", "--skill", "foo", "-g"}); e != nil {
		t.Fatalf("rm --skill: %v", e)
	}
	got, _ := skills.LoadLock(filepath.Join(store, "skills.lock.toml"))
	if len(got.Skill) != 1 || got.Skill[0].Name != "bar" {
		t.Fatalf("lock after rm --skill foo = %+v, want only bar", got.Skill)
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
