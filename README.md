# ghq-skills

A fork of [`x-motemen/ghq`](https://github.com/x-motemen/ghq) that adds a
**`ghq skills`** subcommand: manage [agent skills](https://github.com/vercel-labs/skills)
the ghq way.

![ghq skills architecture: GitHub repos are cloned under the ghq root, pinned by skills.lock.toml (the source of truth), and symlinked into project or global agent dirs for claude-code and codex](docs/architecture.png)

## The goal

Make your agent skills *reproducible*. Every skill stays a **real git
clone** under the ghq root, pinned in a committable lockfile, and symlinked into your
agents' skills dirs — so a project's skill set is version-controlled, shareable, and
restorable to the exact commit.

## Features

- **Explicit lockfile, real git pins.** `skills.lock.toml` records each skill as
  *repo + pinned commit* — no md5 black box like `npx skills`. *"Did upstream
  change?"* is a real `git fetch` + SHA compare; *"reproduce this set"* is a real
  `git checkout`.

- **Project scope first, global when you want it.** Inside a git repo the **project**
  is the default — the lockfile lives at the repo root, skills wire into
  `./.claude/skills`. `-g` switches to a personal cross-project store.

- **The lockfile is the canonical source of truth.** Clones, store, and agent dirs
  are all *derived* from it. Commit the lockfile (not the symlinks); a teammate runs
  `ghq skills restore` to get the identical pinned set in their own ghq root.

- **Default + multi-agent fan-out.** One store wires the same skills into
  `claude-code` (the default agent), `codex`, and any agent you register — `-a` to
  pick, `-a all` for every supported agent.

- **One binary, reuses ghq.** Clones land in the standard `~/ghq/...` tree using
  ghq's own clone + path machinery **in-process** — nothing extra to install.

## Install

Requires Go and `git`. `make install` builds and installs the **`ghq`** binary
into `$GOBIN` (or `$(go env GOPATH)/bin`). Because this binary *is* `ghq`, it
replaces your existing ghq and adds the `skills` subcommand.

```sh
git clone https://github.com/orca-studio/ghq-skills.git
cd ghq-skills
make install
ghq skills --help
```

## Quick start

```sh
# Inside a project (project scope is the default):
ghq skills get larksuite/cli          # clone, pick skills, wire ./.claude/skills
git add skills.lock.toml              # commit the source of truth for your team

# A teammate, after cloning the project:
ghq skills restore                    # reproduce the exact pinned set

# Personal / cross-project (global scope):
ghq skills get owner/repo -g          # lock + wire into ~/.claude/skills
```

See `ghq skills --help` for the full command set (`get`, `update`, `status`,
`list`, `manifest`, `lock`, `restore`, `rm`) and flags.
