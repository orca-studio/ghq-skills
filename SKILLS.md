# `ghq skills` — agent skill management, the ghq way

This fork adds a `skills` subcommand to ghq. Every skill stays a real git clone
under the ghq root; a lockfile (`skills.lock.toml`) pins each skill to an upstream
commit, and symlinks expose them in agents' skills dirs.

Unlike `npx skills` (which packages content and records an md5 checksum), this
keeps full git history — so "did upstream change?" is a real `git fetch` + SHA
compare, not a hash mismatch.

## Scope: project vs. global

Commands operate in one of two scopes; `-g`/`--global` selects global, otherwise
the project is used when you're inside a git repo:

| | lockfile | skills materialize into | canonical store |
|---|---|---|---|
| **project** (default in a repo) | `<repo>/skills.lock.toml` | `<repo>/.claude/skills`, … | — |
| **global** (`-g`, or outside a repo) | `<ghq root>/skills/skills.lock.toml` | `~/.claude/skills`, … | `<ghq root>/skills/` |
| **`--lockfile <p>`** | `<p>` | agent dirs | `<p>`'s dir |

In **project** scope the repo root holds only the lockfile (commit it); skills are
wired straight into the project's agent dirs. In **global** scope a canonical
symlink store also lives under `<ghq root>/skills` (no `.git`, so `ghq list`/`rm`
ignore it; override with `GHQ_SKILLS_ROOT`).

```
~/ghq/                                      # ghq root (GHQ_ROOT)
├─ github.com/<owner>/<repo>/...            # real clones (ghq owns)
│         └─ skills/foo/SKILL.md
└─ skills/                                  # global store + global lockfile
          ├─ skills.lock.toml
          └─ foo -> ../github.com/<owner>/<repo>/skills/foo
```

## Usage

A source is `owner/repo` (ghq shorthand), a full URL, or an SSH remote — and a
repo may hold many skills under `skills/`. Select by name, like `npx skills`:

```sh
ghq skills get owner/repo --list                 # enumerate skills, don't lock
ghq skills get owner/repo                         # lock ALL skills in the repo
ghq skills get owner/repo --skill pdf --skill docx   # lock specific skills by name
ghq skills add owner/repo                          # `add` is an alias for `get`
ghq skills update [name]                            # pull, advance the lock
ghq skills status                                   # show drift behind upstream
ghq skills list                                     # skills wired into an agent's dir at the project scope
ghq skills list -g                                   # global scope (~/.claude/skills), even inside a repo
ghq skills manifest                                  # canonical manifest (installed/pinned set)
ghq skills lock                                     # restore clones to pinned commits
ghq skills restore                                  # clone + pin every locked skill (fresh checkout)
ghq skills restore --wire-only -a codex             # just (re)wire links for another agent; no clone/checkout
```

### Targeting agents (claude-code, codex, …)

`--agent`/`-a` chooses which agents' skills dirs to wire (at the current scope —
project dirs unless `-g`):

```sh
ghq skills get owner/repo                 # default agent ($GHQ_DEFAULT_AGENT = claude-code)
ghq skills get owner/repo -a codex        # a specific agent (repeatable)
ghq skills get owner/repo -a all          # every $GHQ_SUPPORTED_AGENTS
ghq skills get owner/repo -a none         # lockfile only, no agent dirs
ghq skills get owner/repo -g              # global scope: ~/.claude/skills + global store
ghq skills restore --wire-only -a codex   # wire already-locked skills into another agent
```

Config (env):

| Var | Default | Meaning |
|---|---|---|
| `GHQ_SUPPORTED_AGENTS` | `claude-code,codex` | agents for `-a all` |
| `GHQ_DEFAULT_AGENT` | `claude-code` | used when `-a` is omitted |
| `GHQ_AGENT_<NAME>` | built-in for the two above | that agent's **global** skills dir |
| `GHQ_AGENT_<NAME>_PROJECT` | built-in | that agent's **project** skills dir |

Built-in dirs: claude-code → `~/.claude/skills` / `.claude/skills`; codex →
`~/.codex/skills` / `.codex/skills`. Add any other agent by setting its env vars
and listing it in `GHQ_SUPPORTED_AGENTS`. `restore` also fans out, so teammates
get their agent dirs wired in one step.

**Guardrail:** in project scope, if the agent's base dir (e.g. `.claude/`) does
not exist in the current directory, you're likely in the wrong place, so it
prompts before creating a new tree. It proceeds without asking when the base dir
already exists, with `-g`/global, with `-y`/`--yes`, or on a non-interactive
stdin (so CI and scripted `restore` are never blocked).

### Choosing the lockfile

The lockfile follows the scope (see above): the project's `<repo>/skills.lock.toml`
by default inside a repo, the global one with `-g` or outside a repo. `--lockfile
<path>` overrides both. So `list`/`manifest`/`status` inside a project report the
project set; add `-g` to see the global set.

### Two views: wired vs. manifest

Two commands answer two different questions:

- **`ghq skills list` — what's wired:** the skills physically in an agent's dir at
  one scope (project by default, global with `-g`). Reflects the agent's real
  view at that scope, including links not managed by ghq. `-a` picks agents.
- **`ghq skills manifest`:** the canonical lockfile — the set you've installed/
  pinned via ghq (the reproducible SoT), independent of which agent dirs are wired.

Note `list` shows a single scope (consistent with `-g` everywhere): to see global
skills run `list -g`, even inside a repo.

## Team-shared project skills

The lockfile is portable (repo + pinned commit, no machine paths), so commit it
to a project and teammates reproduce the exact set with `restore`:

```sh
# you, once — inside the repo, project scope is the default:
ghq skills get larksuite/cli --skill lark-base --skill lark-doc
git add skills.lock.toml && git commit -m "pin agent skills"

# teammate, after cloning the project — reproduce at the pinned commits:
ghq skills restore
```

`restore` clones each repo into the teammate's own ghq root, checks out the
**pinned** commit (it never advances the lock, unlike `update`), and wires the
project agent dirs. Commit the lockfile, not the symlinks — the agent dirs point
into each person's ghq root and are regenerated by `restore`:

```gitignore
# .claude/skills/.gitignore  (and .codex/skills/.gitignore)
*
!.gitignore
```

## Design

`ghq skills` reuses ghq's own machinery in-process — the getter for cloning and
`LocalRepositoryFromURL` for path resolution — so there is no shelling out and
no second binary. All new code is additive:

- `cmd_skills.go` — the subcommand (the only touch to existing files is one line
  registering `commandSkills` in `commands.go`).
- `skills/` — self-contained package: lockfile, SKILL.md discovery, git helpers.

This keeps rebasing onto `upstream` (x-motemen/ghq) trivial.

## Tracking upstream ghq

```sh
git fetch upstream
git rebase upstream/master      # additive changes rarely conflict
```
