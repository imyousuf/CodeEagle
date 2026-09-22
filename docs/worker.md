# Keeping every project indexed automatically

CodeEagle indexes a project when you run `codeeagle sync`. On a machine with
several projects that means remembering to run it, in the right directory,
for each one. The **worker** does it for you: it watches every project
registered on the machine and re-syncs whichever one's files changed.

It runs on **Linux** and **macOS**, and can install itself to start when you
log in.

---

## Contents

1. [What it does](#1-what-it-does)
2. [Registering more than one project](#2-registering-more-than-one-project)
3. [What actually gets watched](#3-what-actually-gets-watched)
4. [Running it](#4-running-it)
5. [Running it at login](#5-running-it-at-login-linux-and-macos)
6. [What triggers a sync](#6-what-triggers-a-sync)
7. [Tuning it](#7-tuning-it)
8. [Watching what it does](#8-watching-what-it-does)
9. [Troubleshooting](#9-troubleshooting)

---

## 1. What it does

```bash
codeeagle worker
```

It reads the list of projects registered on this machine, watches the
directories each of them indexes, and when one project's files have been quiet
for a moment, runs that project's sync — from that project's directory, with
that project's configuration.

Each sync runs as a separate `codeeagle sync` process. That matters: a sync
opens a database and may talk to a model for minutes, and if it fails, only
that project is affected. The other projects carry on being watched.

---

## 2. Registering more than one project

The worker's list of projects is the registry at **`~/.codeeagle.conf`**. This
is the file that makes multi-project work possible, and it is worth
understanding.

`codeeagle init` adds an entry each time you set a project up:

```bash
cd ~/projects/my-service && codeeagle init
cd ~/projects/other-service && codeeagle init
cd ~ && codeeagle init          # a "project" can be your home directory
```

The result:

```yaml
projects:
    - name: my-service
      root: /home/you/projects/my-service
      config_dir: /home/you/projects/my-service/.CodeEagle
    - name: other-service
      root: /home/you/projects/other-service
      config_dir: /home/you/projects/other-service/.CodeEagle
    - name: you
      root: /home/you
      config_dir: /home/you/.CodeEagle
```

Three fields per project:

| Field | Meaning |
|---|---|
| `name` | What you call it. Used by `--project` and in the logs. |
| `root` | The directory a sync runs **from**. |
| `config_dir` | That project's `.CodeEagle`, holding its `config.yaml` and its graph. |

**Each project keeps its own graph.** They are separate databases under each
`config_dir`. A project is not aware of the others unless you ask for it with
`federate:` in its config.

**Only registered projects are watched.** The worker does not scan your disk
looking for `.CodeEagle` directories. If a project is missing from the worker,
it is missing from the registry — run `codeeagle init` in it.

To see what the worker makes of the registry:

```bash
codeeagle worker --list
```

```
PROJECT              ROOT                          WATCHES
-------              ----                          -------
my-service           /home/you/projects/my-service  /home/you/projects/my-service
other-service        /home/you/projects/other       /home/you/projects/other
you                  /home/you                      /home/you/Documents
                                                    /home/you/Downloads

3 project(s), 4 director(ies) after removing nested duplicates.
```

Anything unusable is reported rather than silently dropped:

```
Skipping old-project: read /home/you/gone/.CodeEagle/config.yaml: no such file or directory
```

---

## 3. What actually gets watched

**Not the project root — the directories that project's `config.yaml` lists
under `repositories:`.**

That distinction is the whole reason multiple projects work on one machine.
A root may contain far more than the project indexes. A home-directory project
looks like this:

```yaml
# ~/.CodeEagle/config.yaml
repositories:
  - path: /home/you/Documents
  - path: /home/you/Pictures
```

Its `root` is `/home/you`, which *contains* `~/projects/my-service`. If the
worker watched roots, every edit in `my-service` would look like a change to
your home project, and the same file would be indexed into two graphs. Watching
`repositories:` avoids that entirely.

**Where trees genuinely do overlap**, a change is attributed to the **most
specific** project that indexes it. If project A indexes `/code` and project B
indexes `/code/lib`, then editing `/code/lib/x.go` syncs B, not A.

**Some paths never trigger a sync**, whatever the configuration says:

- `.CodeEagle/` — where sync writes its own databases. Without this, finishing
  one sync would start the next, forever.
- `.git/` — churns on every checkout and is not indexed anyway.
- Anything matching that project's `watch.exclude`, and anything in its
  `.gitignore`.

Excludes are per project, so one project's patterns never suppress another's:

```yaml
watch:
  exclude:
    - "**/node_modules/**"
    - "**/dist/**"
    - "**/.claude/**"
```

This is worth setting. An excluded directory is never even given a filesystem
watch, so a large `node_modules` costs nothing rather than thousands of
watches.

---

## 4. Running it

```bash
codeeagle worker --list     # what would be watched; changes nothing
codeeagle worker --once     # sync every project once, then exit
codeeagle worker            # watch, and sync on change
codeeagle worker --project my-service    # just one project
```

`codeeagle worker` runs in the foreground until you interrupt it. A sync
already in flight is allowed to finish rather than being killed halfway.

---

## 5. Running it at login (Linux and macOS)

```bash
codeeagle service install
codeeagle service status
codeeagle service uninstall
```

| Platform | Mechanism | Where it lives |
|---|---|---|
| **Linux** | systemd **user** unit | `~/.config/systemd/user/codeeagle-worker.service` |
| **macOS** | launchd **LaunchAgent** | `~/Library/LaunchAgents/com.github.imyousuf.codeeagle.worker.plist` |

Neither needs root. Neither runs for anyone else on the machine.

Preview before committing to it:

```bash
codeeagle service install --dry-run
```

### Why at login, and not at boot

Syncs read credentials out of your login keyring — config values written as
`$(keyring get baseten.co you@example.com)`. A service started before anyone
logs in has no unlocked keyring to read, and CodeEagle stops rather than carry
on with an empty credential. A boot-time service would therefore fail on its
first sync, reporting a credential-command error rather than the real problem.

Binding the worker to the login session removes that. The cost is that it does
not run while you are logged out, which is the right trade on a machine you
use.

### Install from the binary you mean to keep

The service records the **path of the binary you ran `install` from**. Install
from your real installation:

```bash
cd /path/to/CodeEagle && make install    # installs to $GOPATH/bin
codeeagle service install                 # now records that path
```

Installing from a build inside a temporary directory or a git worktree records
a path that may not exist later.

### After upgrading

Re-running `codeeagle service install` is safe and rewrites the file, so do
that after moving the binary. Otherwise a restart is enough, since each sync is
a fresh process:

```bash
systemctl --user restart codeeagle-worker.service   # Linux
launchctl kickstart -k gui/$UID/com.github.imyousuf.codeeagle.worker   # macOS
```

---

## 6. What triggers a sync

A sync happens when a watched file changes and the project then stays quiet
for `--settle` (30 seconds by default). A burst of changes — a build, a branch
checkout, a bulk copy — costs one sync, not one per file.

What the sync then indexes depends on the kind of directory:

- **A git repository**: everything that differs from `HEAD`, including files
  that are **modified, staged or untracked**, plus removing the ones deleted.
  You do not have to commit for your work to be indexed.
- **A plain directory** (a Documents folder, a photo library): every file whose
  modification time is newer than what the graph recorded.

A change arriving *while* a sync is running is not folded into it — the running
sync may already have walked past that file — so it is kept and syncs
afterwards.

---

## 7. Tuning it

```bash
codeeagle worker --settle 60s --concurrency 2
codeeagle service install --settle 60s --concurrency 2
```

| Flag | Default | What it is for |
|---|---|---|
| `--settle` | `30s` | How long a project must be quiet. Raise it if you save constantly, or if syncs are slow. |
| `--concurrency` | `1` | How many projects sync at once. One by default because a sync can saturate a GPU or spend money on a hosted model. |
| `--project` | all | Limit to named projects. |
| `--quiet` | off | Report only failures. |

A project whose sync fails is retried with an exponentially growing delay, so a
broken configuration cannot spin.

---

## 8. Watching what it does

**Linux:**

```bash
journalctl --user -u codeeagle-worker.service -f
```

**macOS:**

```bash
tail -f ~/Library/Logs/codeeagle-worker.log
```

A healthy log looks like this:

```
18:54:31 Watching 7 director(ies) for 4 project(s); settle 30s, 1 sync(s) at a time
19:04:31 Syncing agentic-test-runner (/home/you/projects/agentic-test-runner)
         Sync complete: 1 files indexed, 5629 nodes, 18758 edges
19:06:48 Synced agentic-test-runner in 2m17s
```

`Watching ... director(ies) for ... project(s)` appears at startup; if the
number of projects is lower than you expect, check `codeeagle worker --list`
for skipped entries.

---

## 9. Troubleshooting

### Nothing is being watched

```bash
codeeagle worker --list
```

An empty list means the registry is empty. Run `codeeagle init` in each
project.

### A project is missing from the list

`--list` prints a reason for every entry it skipped — usually a `config.yaml`
that has moved or been deleted. Fix the path in `~/.codeeagle.conf`, or
re-run `codeeagle init` there.

### Files change but nothing syncs

- The path may be excluded. Check that project's `watch.exclude` and its
  `.gitignore`.
- The directory may not be listed under `repositories:` in that project's
  config. Being inside the project root is not enough.
- The settle period may not have elapsed; wait `--settle` plus a few seconds.

### `Cannot acquire directory lock` from another command

A sync holds an exclusive lock on the project's database while it runs, so
`codeeagle query` and similar can fail while the worker is syncing. Wait for
the sync to finish and try again.

### The service is installed but not running

**Linux:**

```bash
systemctl --user status codeeagle-worker.service
journalctl --user -u codeeagle-worker.service -n 50
```

**macOS:**

```bash
launchctl print gui/$UID/com.github.imyousuf.codeeagle.worker
```

A service that starts and immediately exits is usually pointing at a binary
that has moved. Re-run `codeeagle service install` from your real
installation.

### Syncs are slower than expected

Document and image enrichment calls a model per file. On a large or
image-heavy project the first sync is long; later ones only touch what
changed. `--concurrency 1` means projects queue behind each other, which is
deliberate.
