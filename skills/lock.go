// Package skills implements `ghq skills`: managing agent skills as ghq-cloned
// repos, symlinked into <ghq root>/skills, pinned by a committed lockfile.
//
// This package is additive to upstream ghq — it lives in its own directory and
// is referenced only from cmd_skills.go, so rebasing onto upstream stays clean.
package skills

import (
	"os"
	"sort"

	"github.com/BurntSushi/toml"
)

// Skill is one entry in the lockfile.
type Skill struct {
	Name   string `toml:"name"`
	Repo   string `toml:"repo"`             // host/owner/repo, as ghq knows it
	Subdir string `toml:"subdir,omitempty"` // skill path within the repo
	Locked string `toml:"locked"`           // pinned commit SHA
	Link   string `toml:"link"`             // symlink name under the staging dir
}

// Lock is the whole manifest (skills.lock.toml).
type Lock struct {
	Skill []Skill `toml:"skill"`
}

// LoadLock reads path, returning an empty Lock if it does not exist yet.
func LoadLock(path string) (*Lock, error) {
	l := &Lock{}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return l, nil
	}
	if _, err := toml.DecodeFile(path, l); err != nil {
		return nil, err
	}
	return l, nil
}

// Save writes the manifest to path, keeping entries sorted by name.
func (l *Lock) Save(path string) error {
	sort.Slice(l.Skill, func(i, j int) bool { return l.Skill[i].Name < l.Skill[j].Name })
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return toml.NewEncoder(out).Encode(l)
}

// Upsert inserts s or replaces the existing entry with the same name.
func (l *Lock) Upsert(s Skill) {
	for i := range l.Skill {
		if l.Skill[i].Name == s.Name {
			l.Skill[i] = s
			return
		}
	}
	l.Skill = append(l.Skill, s)
}

// Find returns the entry with the given name, and whether it was present.
func (l *Lock) Find(name string) (Skill, bool) {
	for _, s := range l.Skill {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Remove drops the entry with the given name, reporting whether one was removed.
// It only edits the manifest; symlinks and clones are the caller's concern.
func (l *Lock) Remove(name string) bool {
	for i := range l.Skill {
		if l.Skill[i].Name == name {
			l.Skill = append(l.Skill[:i], l.Skill[i+1:]...)
			return true
		}
	}
	return false
}

// Uses reports how many entries reference the given repo — used to tell whether
// removing a skill leaves its clone unreferenced.
func (l *Lock) Uses(repo string) int {
	n := 0
	for _, s := range l.Skill {
		if s.Repo == repo {
			n++
		}
	}
	return n
}
