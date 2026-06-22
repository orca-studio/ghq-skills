package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
)

// Agent fan-out: skills always live in the canonical manifest root (the lockfile
// dir); --agent additionally symlinks them into each agent's own skills dir, so
// the same store powers claude-code, codex, etc.
//
// Config-driven. Defaults:
//   GHQ_SUPPORTED_AGENTS  claude-code,codex
//   GHQ_DEFAULT_AGENT     claude-code
//
// Per-agent dir overrides (NAME uppercased, '-' -> '_'):
//   GHQ_AGENT_<NAME>          global skills dir   (e.g. GHQ_AGENT_CLAUDE_CODE)
//   GHQ_AGENT_<NAME>_PROJECT  project skills dir

func supportedAgents() []string {
	if v := os.Getenv("GHQ_SUPPORTED_AGENTS"); v != "" {
		return splitComma(v)
	}
	return []string{"claude-code", "codex"}
}

func defaultAgent() string {
	if v := os.Getenv("GHQ_DEFAULT_AGENT"); v != "" {
		return v
	}
	return "claude-code"
}

// builtinAgentDirs returns the well-known (global, project) skills dirs for an
// agent, or empty strings if unknown (then an env override is required).
func builtinAgentDirs(name string) (global, project string) {
	switch name {
	case "claude-code":
		return "~/.claude/skills", ".claude/skills"
	case "codex":
		return "~/.codex/skills", ".codex/skills"
	}
	return "", ""
}

// agentDir resolves an agent's skills dir at the chosen scope, applying env
// overrides. Global dirs expand '~'; project dirs are made absolute from CWD.
func agentDir(name string, global bool) (string, error) {
	envKey := "GHQ_AGENT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	g, p := builtinAgentDirs(name)
	if v := os.Getenv(envKey); v != "" {
		g = v
	}
	if v := os.Getenv(envKey + "_PROJECT"); v != "" {
		p = v
	}
	if global {
		if g == "" {
			return "", fmt.Errorf("unknown agent %q: set %s to its global skills dir", name, envKey)
		}
		return expandHome(g), nil
	}
	if p == "" {
		return "", fmt.Errorf("unknown agent %q: set %s_PROJECT to its project skills dir", name, envKey)
	}
	return filepath.Abs(p)
}

// resolveAgents decides which agents to fan out to:
//   -a all      -> every $GHQ_SUPPORTED_AGENTS
//   -a none     -> none (canonical store only)
//   -a x -a y   -> those agents
//   (omitted)   -> $GHQ_DEFAULT_AGENT
func resolveAgents(cmd *cli.Command) []string {
	names := cmd.StringSlice("agent")
	if len(names) == 0 {
		return []string{defaultAgent()}
	}
	if len(names) == 1 {
		switch names[0] {
		case "all":
			return supportedAgents()
		case "none":
			return nil
		}
	}
	return names
}

// agentFlags are shared by get/restore/link.
func agentFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{Name: "agent", Aliases: []string{"a"}, Usage: "also symlink skills into this agent's dir (repeatable; 'all' / 'none'; default $GHQ_DEFAULT_AGENT)"},
		&cli.BoolFlag{Name: "global", Aliases: []string{"g"}, Usage: "use the agent's global skills dir (e.g. ~/.claude/skills); default is the project dir (./.claude/skills)"},
		&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "create a missing project agent dir without prompting"},
	}
}

type skillLink struct{ name, target string }

// fanOutToAgents creates symlinks for each skill in each selected agent's dir.
func fanOutToAgents(cmd *cli.Command, links []skillLink) error {
	agents := resolveAgents(cmd)
	if len(agents) == 0 {
		return nil
	}
	global := cmd.Bool("global")
	for _, a := range agents {
		dir, err := agentDir(a, global)
		if err != nil {
			return err
		}
		// Guardrail: in project scope, if the agent's base dir (e.g. .claude) is
		// absent, we're probably in the wrong directory — confirm before scattering
		// a new tree here. Global, --yes, existing dirs, and non-interactive runs
		// proceed without asking.
		if !global && !dirExists(dir) && !dirExists(filepath.Dir(dir)) {
			if !confirmCreate(cmd, dir) {
				fmt.Printf("skipped %s (%s)\n", dir, a)
				continue
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, l := range links {
			if err := symlinkForce(l.target, filepath.Join(dir, l.name)); err != nil {
				return err
			}
		}
		fmt.Printf("wired %d skill(s) -> %s (%s)\n", len(links), dir, a)
	}
	return nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// confirmCreate asks whether to create a missing project agent dir. It returns
// true on -y, on a non-interactive stdin (don't block automation), or a "y"
// answer; defaults to false (just Enter).
func confirmCreate(cmd *cli.Command, dir string) bool {
	if cmd.Bool("yes") {
		return true
	}
	fd := os.Stdin.Fd()
	if !isatty.IsTerminal(fd) && !isatty.IsCygwinTerminal(fd) {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s does not exist here. Create it? [y/N] ", dir)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

type skillEntry struct {
	name, target string
	broken       bool
}

// readSkillDir returns the actual skills in dir (present=false if dir is absent).
// A skill is a symlink (a wired skill link) or a real directory containing a
// SKILL.md — anything else (.DS_Store, the lockfile, stray files) is ignored.
func readSkillDir(dir string) (entries []skillEntry, present bool) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	for _, e := range des {
		full := filepath.Join(dir, e.Name())
		if e.Type()&os.ModeSymlink == 0 {
			if !e.IsDir() || !fileExists(filepath.Join(full, "SKILL.md")) {
				continue
			}
		}
		target, _ := os.Readlink(full) // "" if not a symlink
		_, statErr := os.Stat(full)
		entries = append(entries, skillEntry{name: e.Name(), target: target, broken: statErr != nil})
	}
	return entries, true
}

// listResolution shows what each selected agent actually resolves: its search
// dirs in precedence order (project, then global), with origin and shadowing —
// the same way the agent itself would find skills.
func listResolution(cmd *cli.Command) error {
	projectRoot := findProjectRoot()
	for _, a := range resolveAgents(cmd) {
		fmt.Printf("%s\n", a)
		seen := map[string]bool{} // names already provided by a higher-precedence dir

		type scope struct{ label, dir string }
		var scopes []scope
		if projectRoot != "" {
			if pdir, err := agentProjectDir(a, projectRoot); err == nil {
				scopes = append(scopes, scope{"project", pdir})
			}
		}
		if gdir, err := agentDir(a, true); err == nil {
			scopes = append(scopes, scope{"global", gdir})
		}

		for _, sc := range scopes {
			entries, present := readSkillDir(sc.dir)
			if !present {
				fmt.Printf("  %-7s %s  (not present)\n", sc.label, sc.dir)
				continue
			}
			fmt.Printf("  %-7s %s\n", sc.label, sc.dir)
			for _, e := range entries {
				mark := "ok"
				if e.broken {
					mark = "BROKEN"
				}
				shadow := ""
				if seen[e.name] {
					shadow = "  (shadowed)"
				}
				fmt.Printf("    %-26s %-8s %s%s\n", e.name, mark, e.target, shadow)
			}
			for _, e := range entries {
				seen[e.name] = true
			}
		}
	}
	return nil
}

// findProjectRoot returns the nearest ancestor dir containing .git, or "".
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if dirExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// agentProjectDir resolves an agent's project skills dir relative to projectRoot
// (rather than the current dir).
func agentProjectDir(name, projectRoot string) (string, error) {
	envKey := "GHQ_AGENT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	_, p := builtinAgentDirs(name)
	if v := os.Getenv(envKey + "_PROJECT"); v != "" {
		p = v
	}
	if p == "" {
		return "", fmt.Errorf("unknown agent %q: set %s_PROJECT", name, envKey)
	}
	if filepath.IsAbs(p) {
		return p, nil
	}
	return filepath.Join(projectRoot, p), nil
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
