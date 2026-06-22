package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
//   --all-agents          -> every supported agent
//   -a x -a y             -> those agents ("none" alone disables fan-out)
//   (omitted)             -> $GHQ_DEFAULT_AGENT
func resolveAgents(cmd *cli.Command) []string {
	if cmd.Bool("all-agents") {
		return supportedAgents()
	}
	names := cmd.StringSlice("agent")
	if len(names) == 0 {
		return []string{defaultAgent()}
	}
	if len(names) == 1 && names[0] == "none" {
		return nil
	}
	return names
}

// agentFlags are shared by get/restore/link.
func agentFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{Name: "agent", Aliases: []string{"a"}, Usage: "also symlink skills into this agent's dir (repeatable; 'none' to skip; default $GHQ_DEFAULT_AGENT)"},
		&cli.BoolFlag{Name: "all-agents", Usage: "fan out to all $GHQ_SUPPORTED_AGENTS"},
		&cli.BoolFlag{Name: "project", Usage: "use the agent's project skills dir (e.g. ./.claude/skills) instead of the global one"},
	}
}

type skillLink struct{ name, target string }

// fanOutToAgents creates symlinks for each skill in each selected agent's dir.
func fanOutToAgents(cmd *cli.Command, links []skillLink) error {
	agents := resolveAgents(cmd)
	if len(agents) == 0 {
		return nil
	}
	global := !cmd.Bool("project")
	for _, a := range agents {
		dir, err := agentDir(a, global)
		if err != nil {
			return err
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

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
