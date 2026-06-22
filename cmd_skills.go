package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
	"github.com/x-motemen/ghq/skills"
)

var commandSkills = &cli.Command{
	Name:  "skills",
	Usage: "Manage agent skills cloned under the ghq root",
	Description: `
    Treat agent skills the ghq way: each skill stays a real git clone under the
    ghq root, symlinked into <root>/skills, pinned by a committed lockfile
    (skills.lock.toml). Because the manifest dir has no .git, plain 'ghq list'
    ignores it. Override the manifest root with GHQ_SKILLS_ROOT.`,
	Commands: []*cli.Command{
		commandSkillsGet,
		commandSkillsUpdate,
		commandSkillsStatus,
		commandSkillsList,
		commandSkillsLock,
	},
}

var commandSkillsGet = &cli.Command{
	Name:      "get",
	Aliases:   []string{"add"},
	Usage:     "Clone a skill repo, link it into the manifest root, and lock it",
	ArgsUsage: "<source>",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{Name: "skill", Aliases: []string{"s"}, Usage: "select skills by name (repeatable; '*' = all). Default: all"},
		&cli.StringFlag{Name: "subdir", Usage: "select the skill at this subdir (disambiguates odd layouts)"},
		&cli.BoolFlag{Name: "list", Aliases: []string{"l"}, Usage: "list available skills in the repo without locking"},
		&cli.BoolFlag{Name: "update", Aliases: []string{"u"}, Usage: "pull if the repo is already cloned"},
		&cli.BoolFlag{Name: "p", Usage: "clone with SSH"},
	},
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

		lock, lockPath, err := loadSkillsLock()
		if err != nil {
			return err
		}
		root := filepath.Dir(lockPath)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		for _, f := range found {
			if err := symlinkForce(f.Dir, filepath.Join(root, f.Name)); err != nil {
				return err
			}
			lock.Upsert(skills.Skill{Name: f.Name, Repo: repo, Subdir: f.Subdir, Locked: sha, Link: f.Name})
			fmt.Printf("linked %-24s -> %s @ %s\n", f.Name, f.Dir, shortSHA(sha))
		}
		return lock.Save(lockPath)
	},
}

var commandSkillsUpdate = &cli.Command{
	Name:      "update",
	Usage:     "Pull upstream for locked skills and advance the lock",
	ArgsUsage: "[name]",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, lockPath, err := loadSkillsLock()
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			return errors.New("nothing locked yet (run `ghq skills get` first)")
		}
		root := filepath.Dir(lockPath)
		only := cmd.Args().First()
		pulled := map[string]string{} // one pull per repo

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
			if err := symlinkForce(filepath.Join(path, s.Subdir), filepath.Join(root, s.Link)); err != nil {
				return err
			}
			if sha == s.Locked {
				fmt.Printf("%-24s unchanged @ %s\n", s.Name, shortSHA(sha))
			} else {
				fmt.Printf("%-24s %s -> %s\n", s.Name, shortSHA(s.Locked), shortSHA(sha))
				s.Locked = sha
			}
		}
		return lock.Save(lockPath)
	},
}

var commandSkillsStatus = &cli.Command{
	Name:  "status",
	Usage: "Show how far each locked skill has drifted behind upstream",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "no-fetch", Usage: "compare without fetching (use cached refs)"},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, _, err := loadSkillsLock()
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
	Usage: "List locked skills and flag broken symlinks",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, lockPath, err := loadSkillsLock()
		if err != nil {
			return err
		}
		if len(lock.Skill) == 0 {
			fmt.Println("no skills locked")
			return nil
		}
		root := filepath.Dir(lockPath)
		for _, s := range lock.Skill {
			mark := "ok"
			if _, err := os.Stat(filepath.Join(root, s.Link)); err != nil {
				mark = "BROKEN"
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
	Name:  "lock",
	Usage: "Check out each clone at its pinned commit (restore locked state)",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		lock, _, err := loadSkillsLock()
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

// loadSkillsLock resolves the manifest root (<ghq root>/skills, or
// GHQ_SKILLS_ROOT) and loads its lockfile.
func loadSkillsLock() (*skills.Lock, string, error) {
	root := os.Getenv("GHQ_SKILLS_ROOT")
	if root == "" {
		base, err := primaryLocalRepositoryRoot()
		if err != nil {
			return nil, "", err
		}
		root = filepath.Join(base, "skills")
	}
	lockPath := filepath.Join(root, "skills.lock.toml")
	lock, err := skills.LoadLock(lockPath)
	return lock, lockPath, err
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
