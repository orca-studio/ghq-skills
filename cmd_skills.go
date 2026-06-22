package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	"github.com/x-motemen/ghq/skills"
)

var commandSkills = &cli.Command{
	Name:  "skills",
	Usage: "Manage agent skills cloned under the ghq root",
	Description: `
    Treat agent skills the ghq way: each skill stays a real git clone under the
    ghq root, pinned by a lockfile (skills.lock.toml) and symlinked into agents'
    skills dirs.

    Scope: inside a git repo, commands default to the project — lockfile at the
    repo root, skills wired into the project's agent dirs (e.g. ./.claude/skills).
    Use -g/--global for the global scope (<ghq root>/skills + ~/.claude/skills),
    which is also the default outside a repo. --lockfile overrides explicitly.

    The lockfile is portable (repo + pinned commit, no machine paths), so commit
    ./skills.lock.toml for team-shared skills and teammates run 'ghq skills
    restore' to reproduce the exact set.`,
	Commands: []*cli.Command{
		commandSkillsGet,
		commandSkillsUpdate,
		commandSkillsStatus,
		commandSkillsList,
		commandSkillsManifest,
		commandSkillsLock,
		commandSkillsRestore,
	},
}

var commandSkillsGet = &cli.Command{
	Name:      "get",
	Aliases:   []string{"add"},
	Usage:     "Clone a skill repo, lock it, and wire it into agent dirs (project by default)",
	ArgsUsage: "<source>",
	Flags: append([]cli.Flag{
		&cli.StringSliceFlag{Name: "skill", Aliases: []string{"s"}, Usage: "select skills by name (repeatable; '*' = all). Default: all"},
		&cli.StringFlag{Name: "subdir", Usage: "select the skill at this subdir (disambiguates odd layouts)"},
		&cli.BoolFlag{Name: "list", Aliases: []string{"l"}, Usage: "list available skills in the repo without locking"},
		&cli.BoolFlag{Name: "update", Aliases: []string{"u"}, Usage: "pull if the repo is already cloned"},
		&cli.BoolFlag{Name: "p", Usage: "clone with SSH"},
		lockfileFlag(),
	}, agentFlags()...),
	Action: func(ctx context.Context, cmd *cli.Command) error {
		source := cmd.Args().First()
		if source == "" {
			return errors.New("usage: ghq skills get <source>")
		}

		// Reuse ghq's own getter in-process: clones (or updates) and hands back
		// the resolved local repository — no shelling out to ghq.
		g := &getter{update: cmd.Bool("update"), ssh: cmd.Bool("p"), recursive: true}
		info, err := g.get(ctx, source)
		if err != nil {
			return err
		}
		path := info.localRepository.FullPath
		repo := info.localRepository.RelPath

		sha, err := skills.Head(path)
		if err != nil {
			return err
		}

		found := skills.Discover(path)
		if len(found) == 0 {
			return fmt.Errorf("no SKILL.md found in %s", path)
		}

		if cmd.Bool("list") {
			for _, f := range found {
				loc := f.Subdir
				if loc == "" {
					loc = "."
				}
				fmt.Printf("%-24s %s\n", f.Name, loc)
			}
			return nil
		}

		if names := cmd.StringSlice("skill"); len(names) > 0 {
			found = skills.SelectByName(found, names)
			if len(found) == 0 {
				return fmt.Errorf("no skills matched --skill %v (try `ghq skills get %s --list`)", names, source)
			}
		}
		if sub := cmd.String("subdir"); sub != "" {
			found = skills.SelectBySubdir(found, sub)
			if len(found) == 0 {
				return fmt.Errorf("no SKILL.md under subdir %q", sub)
			}
		}

		lock, sc, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(sc.lockPath), 0o755); err != nil {
			return err
		}
		if sc.storeRoot != "" {
			if err := os.MkdirAll(sc.storeRoot, 0o755); err != nil {
				return err
			}
		}
		var links []skillLink
		for _, f := range found {
			if sc.storeRoot != "" {
				if err := symlinkForce(f.Dir, filepath.Join(sc.storeRoot, f.Name)); err != nil {
					return err
				}
			}
			lock.Upsert(skills.Skill{Name: f.Name, Repo: repo, Subdir: f.Subdir, Locked: sha, Link: f.Name})
			links = append(links, skillLink{name: f.Name, target: f.Dir})
			fmt.Printf("locked %-24s @ %s\n", f.Name, shortSHA(sha))
		}
		if err := lock.Save(sc.lockPath); err != nil {
			return err
		}
		return fanOutToAgents(cmd, sc, links)
	},
}

var commandSkillsUpdate = &cli.Command{
	Name:      "update",
	Usage:     "Pull upstream for locked skills and advance the lock",
	ArgsUsage: "[name]",
	Flags:     append(agentFlags(), lockfileFlag()),
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, sc, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			return errors.New("nothing locked yet (run `ghq skills get` first)")
		}
		only := cmd.Args().First()
		pulled := map[string]string{} // one pull per repo
		var links []skillLink

		for i := range lock.Skill {
			s := &lock.Skill[i]
			if only != "" && s.Name != only {
				continue
			}
			path, ok := pulled[s.Repo]
			if !ok {
				g := &getter{update: true, recursive: true}
				info, err := g.get(ctx, s.Repo)
				if err != nil {
					return err
				}
				path = info.localRepository.FullPath
				pulled[s.Repo] = path
			}
			sha, err := skills.Head(path)
			if err != nil {
				return err
			}
			target := filepath.Join(path, s.Subdir)
			if sc.storeRoot != "" {
				if err := symlinkForce(target, filepath.Join(sc.storeRoot, s.Link)); err != nil {
					return err
				}
			}
			links = append(links, skillLink{name: s.Link, target: target})
			if sha == s.Locked {
				fmt.Printf("%-24s unchanged @ %s\n", s.Name, shortSHA(sha))
			} else {
				fmt.Printf("%-24s %s -> %s\n", s.Name, shortSHA(s.Locked), shortSHA(sha))
				s.Locked = sha
			}
		}
		if err := lock.Save(sc.lockPath); err != nil {
			return err
		}
		return fanOutToAgents(cmd, sc, links)
	},
}

var commandSkillsStatus = &cli.Command{
	Name:  "status",
	Usage: "Show how far each locked skill has drifted behind upstream",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "no-fetch", Usage: "compare without fetching (use cached refs)"},
		lockfileFlag(),
		globalFlag(),
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, _, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			fmt.Println("no skills locked")
			return nil
		}
		fetched := map[string]bool{}
		for _, s := range lock.Skill {
			path, err := skillRepoPath(s.Repo)
			if err != nil {
				fmt.Printf("%-24s MISSING (%v)\n", s.Name, err)
				continue
			}
			if !cmd.Bool("no-fetch") && !fetched[s.Repo] {
				_ = skills.Fetch(path)
				fetched[s.Repo] = true
			}
			remote, err := skills.RemoteHead(path)
			if err != nil {
				fmt.Printf("%-24s ? (no upstream)\n", s.Name)
				continue
			}
			if remote == s.Locked {
				fmt.Printf("%-24s up to date @ %s\n", s.Name, shortSHA(s.Locked))
			} else {
				n, _ := skills.CountRange(path, s.Locked, remote)
				fmt.Printf("%-24s %d behind: %s -> %s\n", s.Name, n, shortSHA(s.Locked), shortSHA(remote))
			}
		}
		return nil
	},
}

var commandSkillsList = &cli.Command{
	Name:  "list",
	Usage: "List skills wired into an agent's dir at the current scope (project, or -g global)",
	Description: `
    Lists the skills an agent loads at one scope: the project dir (e.g.
    <project>/.claude/skills) by default inside a repo, or the global dir
    (~/.claude/skills) with -g — even when inside a repo. Choose agents with -a
    (default $GHQ_DEFAULT_AGENT, or 'all').

    For the installed/pinned set, see 'ghq skills manifest'.`,
	Flags:  []cli.Flag{agentSelectFlag(), globalFlag()},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return listAgents(cmd)
	},
}

var commandSkillsManifest = &cli.Command{
	Name:  "manifest",
	Usage: "List the canonical manifest (the installed/pinned set)",
	Description: `
    Dumps the resolved lockfile (skills.lock.toml) — the skills you've installed
    and pinned via ghq, the reproducible source of truth. Resolved from a project
    lockfile if found by walking up from the current dir, else the global one.

    For what an agent actually loads from its dirs, see 'ghq skills list'.`,
	Flags: []cli.Flag{lockfileFlag(), globalFlag()},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, _, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			fmt.Println("no skills locked")
			return nil
		}
		for _, s := range lock.Skill {
			mark := "ok"
			if path, err := skillRepoPath(s.Repo); err != nil {
				mark = "MISSING"
			} else if _, err := os.Stat(filepath.Join(path, s.Subdir)); err != nil {
				mark = "MISSING"
			}
			loc := s.Repo
			if s.Subdir != "" {
				loc = filepath.Join(s.Repo, s.Subdir)
			}
			fmt.Printf("%-24s %-8s %s @ %s\n", s.Name, mark, loc, shortSHA(s.Locked))
		}
		return nil
	},
}

var commandSkillsLock = &cli.Command{
	Name:   "lock",
	Usage:  "Check out each clone at its pinned commit (restore locked state)",
	Flags:  []cli.Flag{lockfileFlag(), globalFlag()},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, _, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			fmt.Println("no skills locked")
			return nil
		}
		done := map[string]bool{}
		for _, s := range lock.Skill {
			if done[s.Repo] {
				continue
			}
			done[s.Repo] = true
			path, err := skillRepoPath(s.Repo)
			if err != nil {
				fmt.Printf("%-24s MISSING (%v)\n", s.Name, err)
				continue
			}
			if err := skills.Checkout(path, s.Locked); err != nil {
				return fmt.Errorf("checkout %s @ %s: %w", s.Repo, shortSHA(s.Locked), err)
			}
			fmt.Printf("%-24s @ %s\n", s.Repo, shortSHA(s.Locked))
		}
		return nil
	},
}

var commandSkillsRestore = &cli.Command{
	Name:  "restore",
	Usage: "Clone and pin every locked skill, then wire it (or just re-wire with --wire-only)",
	Description: `
    Reproduce the exact skill set recorded in a lockfile — for fresh checkouts
    and teammates. For each entry it clones the repo if missing, checks it out at
    the pinned commit (never advancing it, unlike 'update'), and wires the
    symlinks. Idempotent.

    With --wire-only it skips cloning and checkout entirely and just (re)creates
    the symlinks for already-cloned skills — useful to add an agent later
    (e.g. 'ghq skills restore --wire-only -a codex') or to repair links without
    touching working trees or pins.`,
	Flags: append(agentFlags(), lockfileFlag(),
		&cli.BoolFlag{Name: "wire-only", Usage: "only (re)create symlinks; do not clone or checkout"}),
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, sc, err := loadSkillsLock(cmd)
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			return fmt.Errorf("no skills in %s", sc.lockPath)
		}
		if sc.storeRoot != "" {
			if err := os.MkdirAll(sc.storeRoot, 0o755); err != nil {
				return err
			}
		}
		wireOnly := cmd.Bool("wire-only")
		var links []skillLink
		cloned := map[string]string{} // clone once per repo
		for _, s := range lock.Skill {
			var path string
			if wireOnly {
				// Use the existing clone only; never clone or checkout.
				p, err := skillRepoPath(s.Repo)
				if err == nil {
					_, err = os.Stat(p)
				}
				if err != nil {
					fmt.Printf("%-24s MISSING (not cloned; run restore without --wire-only)\n", s.Name)
					continue
				}
				path = p
			} else {
				p, ok := cloned[s.Repo]
				if !ok {
					// Clone if missing; do NOT update (we want the pinned commit).
					g := &getter{recursive: true}
					info, err := g.get(ctx, s.Repo)
					if err != nil {
						return err
					}
					p = info.localRepository.FullPath
					cloned[s.Repo] = p
				}
				path = p
				// Pin to the locked commit, fetching first if not present yet.
				if err := skills.Checkout(path, s.Locked); err != nil {
					_ = skills.Fetch(path)
					if err := skills.Checkout(path, s.Locked); err != nil {
						return fmt.Errorf("%s: checkout %s: %w", s.Repo, shortSHA(s.Locked), err)
					}
				}
			}
			target := filepath.Join(path, s.Subdir)
			if sc.storeRoot != "" {
				if err := symlinkForce(target, filepath.Join(sc.storeRoot, s.Link)); err != nil {
					return err
				}
			}
			links = append(links, skillLink{name: s.Link, target: target})
			if !wireOnly {
				fmt.Printf("restored %-24s @ %s\n", s.Name, shortSHA(s.Locked))
			}
		}
		return fanOutToAgents(cmd, sc, links)
	},
}

// lockfileFlag is shared by every skills subcommand so they all honor an
// explicit lockfile path.
func lockfileFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "lockfile",
		Aliases: []string{"f"},
		Usage:   "explicit skills.lock.toml path (overrides project/global scope; its dir holds the store)",
	}
}

// globalFlag selects global scope. Used by read/no-fanout commands; the fan-out
// commands get the same flag via agentFlags().
func globalFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:    "global",
		Aliases: []string{"g"},
		Usage:   "operate on the global scope (<ghq root>/skills) instead of the current project",
	}
}

// scope captures where a command reads/writes for the current invocation.
//
// Default is project scope when inside a git repo: the lockfile lives at the
// repo root and skills materialize only into agent dirs (no store-symlink pile
// at the root). -g/--global (or running outside a repo) switches to global: the
// lockfile and a canonical symlink store live under <ghq root>/skills, and
// agents use their global dirs. An explicit --lockfile overrides the location
// and treats its directory as the store.
type scope struct {
	lockPath    string // resolved lockfile path
	storeRoot   string // dir for canonical-store symlinks; "" means none (project mode)
	agentGlobal bool   // fan out to agents' global dirs (true) or project dirs (false)
	projectRoot string // project root for project agent dirs; "" when global
}

func resolveScope(cmd *cli.Command) (scope, error) {
	// Explicit lockfile: its directory is the store; agent scope follows -g.
	if f := cmd.String("lockfile"); f != "" {
		abs, err := filepath.Abs(expandHome(f))
		if err != nil {
			return scope{}, err
		}
		return scope{lockPath: abs, storeRoot: filepath.Dir(abs), agentGlobal: cmd.Bool("global"), projectRoot: findProjectRoot()}, nil
	}
	// Project scope: default when inside a git repo and not -g.
	if !cmd.Bool("global") {
		if root := findProjectRoot(); root != "" {
			return scope{lockPath: filepath.Join(root, "skills.lock.toml"), storeRoot: "", agentGlobal: false, projectRoot: root}, nil
		}
	}
	// Global scope: -g, or not inside a repo.
	p, err := globalLockPath()
	if err != nil {
		return scope{}, err
	}
	return scope{lockPath: p, storeRoot: filepath.Dir(p), agentGlobal: true}, nil
}

func globalLockPath() (string, error) {
	if root := os.Getenv("GHQ_SKILLS_ROOT"); root != "" {
		return filepath.Join(expandHome(root), "skills.lock.toml"), nil
	}
	base, err := primaryLocalRepositoryRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "skills", "skills.lock.toml"), nil
}

// loadSkillsLock resolves the scope and loads its lockfile.
func loadSkillsLock(cmd *cli.Command) (*skills.Lock, scope, error) {
	sc, err := resolveScope(cmd)
	if err != nil {
		return nil, scope{}, err
	}
	lock, err := skills.LoadLock(sc.lockPath)
	return lock, sc, err
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// skillRepoPath resolves a locked repo's local clone path without cloning,
// reusing ghq's URL and path machinery.
func skillRepoPath(repo string) (string, error) {
	u, err := newURL(repo, false, false)
	if err != nil {
		return "", err
	}
	local, err := LocalRepositoryFromURL(u, false)
	if err != nil {
		return "", err
	}
	return local.FullPath, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// symlinkForce points link at target, replacing an existing symlink but never
// clobbering a real file or directory.
func symlinkForce(target, link string) error {
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink; refusing to overwrite", link)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}
