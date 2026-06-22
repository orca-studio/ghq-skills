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

// agentSelectFlag picks which agents to act on; shared by the fan-out commands
// and list.
func agentSelectFlag() cli.Flag {
	return &cli.StringSliceFlag{Name: "agent", Aliases: []string{"a"}, Usage: "agent(s) to act on (repeatable; 'all' / 'none'; default $GHQ_DEFAULT_AGENT)"}
}

// agentFlags are shared by get/update/restore/link.
func agentFlags() []cli.Flag {
	return []cli.Flag{
		agentSelectFlag(),
		globalFlag(),
		&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "create a missing project agent dir without prompting"},
	}
}

type skillLink struct{ name, target string }

// fanOutToAgents creates symlinks for each skill in each selected agent's dir,
// resolved at the invocation's scope (project dirs unless global).
func fanOutToAgents(cmd *cli.Command, sc scope, links []skillLink) error {
	agents := resolveAgents(cmd)
	if len(agents) == 0 {
		return nil
	}
	for _, a := range agents {
		var (
			dir string
			err error
		)
		switch {
		case sc.agentGlobal:
			dir, err = agentDir(a, true)
		case sc.projectRoot != "":
			dir, err = agentProjectDir(a, sc.projectRoot)
		default:
			dir, err = agentDir(a, false)
		}
		if err != nil {
			return err
		}
		// Guardrail: in project scope, if the agent's base dir (e.g. .claude) is
		// absent, we're probably in the wrong directory — confirm before scattering
		// a new tree here. Global, --yes, existing dirs, and non-interactive runs
		// proceed without asking.
		if !sc.agentGlobal && !dirExists(dir) && !dirExists(filepath.Dir(dir)) {
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

// listAgents lists the skills wired into the selected agents' dir at one scope —
// project by default, global with -g — the way an agent loads from that scope.
func listAgents(cmd *cli.Command) error {
	sc, err := resolveScope(cmd)
	if err != nil {
		return err
	}
	for _, a := range resolveAgents(cmd) {
		var dir string
		switch {
		case sc.agentGlobal:
			dir, err = agentDir(a, true)
		case sc.projectRoot != "":
			dir, err = agentProjectDir(a, sc.projectRoot)
		default:
			dir, err = agentDir(a, false)
		}
		if err != nil {
			return err
		}
		entries, present := readSkillDir(dir)
		if !present {
			fmt.Printf("%s  %s  (not present)\n", a, dir)
			continue
		}
		fmt.Printf("%s  %s\n", a, dir)
		if len(entries) == 0 {
			fmt.Println("  (empty)")
			continue
		}
		for _, e := range entries {
			mark := "ok"
			if e.broken {
				mark = "BROKEN"
			}
			fmt.Printf("  %-26s %-8s %s\n", e.name, mark, e.target)
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
