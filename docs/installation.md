# Installing CodeEagle

A step-by-step guide from nothing to a working setup. It assumes no Go
toolchain, no prior CodeEagle, and no particular comfort with a terminal — but
it does assume you can open one and paste a command into it.

Every step says what you should see when it worked. If you see something else,
the [Troubleshooting](#troubleshooting) section at the end covers the failures
that actually happen.

**What CodeEagle is.** It reads your code, your documents, and your meeting
recordings, and builds a *knowledge graph* from them — a store of the things it
found (functions, files, documents, meetings, people) and the connections
between them (this function calls that one; this meeting decided that thing;
this person attended). You can then ask questions that span all of it, from the
terminal or from Claude Code.

---

## Contents

1. [Install the program](#1-install-the-program)
2. [Set up your first project](#2-set-up-your-first-project)
3. [Add it to Claude Code](#3-add-it-to-claude-code)
4. [Understand the configuration file](#4-understand-the-configuration-file)
5. [Store your API keys safely](#5-store-your-api-keys-safely)
6. [Google Cloud credentials](#6-google-cloud-credentials-only-for-vertex-ai)
7. [Index your meeting recordings](#7-index-your-meeting-recordings)
8. [Install Ollama](#8-install-ollama-for-search-and-document-topics)
9. [Troubleshooting](#troubleshooting)

Steps 1–3 get you a working setup. Steps 5–7 are only needed if you want
meeting transcripts. Step 8 is needed for semantic search.

---

## 1. Install the program

Download the release for your computer from
[GitHub Releases](https://github.com/imyousuf/CodeEagle/releases).

**Which file do I need?** Run this and match the answer:

```bash
uname -s -m
```

| It printed | Download |
|---|---|
| `Linux x86_64` | `codeeagle-linux-amd64.tar.gz` |
| `Linux aarch64` | `codeeagle-linux-arm64.tar.gz` |
| `Darwin x86_64` | `codeeagle-darwin-amd64.tar.gz` (Intel Mac) |
| `Darwin arm64` | `codeeagle-darwin-arm64.tar.gz` (Apple Silicon) |

Then unpack it and move it somewhere your terminal will find it:

```bash
tar -xzf codeeagle-*.tar.gz
sudo mv codeeagle /usr/local/bin/
```

Check it worked:

```bash
codeeagle version
```

You should see a version number, a commit hash and a build date. If you see
`command not found`, see [Troubleshooting](#command-not-found).

> **On a Mac, the first run may be blocked.** macOS refuses to run programs it
> cannot verify. If you see *"cannot be opened because the developer cannot be
> verified"*, run:
>
> ```bash
> xattr -d com.apple.quarantine /usr/local/bin/codeeagle
> ```
>
> That tells macOS you downloaded this deliberately. Only do it for files you
> meant to download.

<details>
<summary>Building from source instead (for developers)</summary>

Requires Go 1.27+ and a C compiler, because some language parsers are C
libraries.

```bash
git clone https://github.com/imyousuf/CodeEagle.git
cd CodeEagle
make build          # binary lands in bin/codeeagle
```
</details>

---

## 2. Set up your first project

Go to the folder you want indexed — a code repository, or just a folder of
documents — and set it up:

```bash
cd ~/projects/my-project
codeeagle init
```

This creates a folder called `.CodeEagle` holding the configuration and the
database. It does not change any of your own files.

Now read the project:

```bash
codeeagle sync
```

The first run takes a while — minutes for a large repository — because it reads
every file. Later runs only look at what changed.

Check what it found:

```bash
codeeagle status
```

You should see something like:

```
Knowledge Graph Status
======================

  Active branch: main
  Total nodes:   6364
  Total edges:   14201

  Nodes by type:
    Function             862
    Method               945
    Document             547
    ...
```

Numbers in the hundreds or thousands mean it worked. **Zero nodes** usually
means it ran somewhere unexpected — see
[The graph is empty](#the-graph-is-empty).

Try a question:

```bash
codeeagle query --type Function --name Serve
codeeagle rag "how does authentication work"
```

`query` looks things up by name. `rag` searches by meaning, and needs
[Ollama](#8-install-ollama-for-search-and-document-topics) installed first.

---

## 3. Add it to Claude Code

This lets Claude Code use everything above on your behalf.

Inside Claude Code, run these two commands:

```
/plugin marketplace add imyousuf/CodeEagle
/plugin install codeeagle@imyousuf-CodeEagle
```

Check it worked by typing `/` — you should see entries beginning
`codeeagle:`. There is no separate server to configure.

| Skill | What it does |
|---|---|
| `/codeeagle:codeeagle` | Look things up — symbols, interfaces, relationships, unused code, test coverage |
| `/codeeagle:codeeagle-sync` | Bring the graph up to date |
| `/codeeagle:codeeagle-review` | Review changes against the conventions already in the codebase |
| `/codeeagle:codeeagle-meetings` | Ask about meetings, decisions and follow-ups |
| `/codeeagle:codeeagle-status` | Check what has been indexed and whether it is current |

---

## 4. Understand the configuration file

Everything lives in `.CodeEagle/config.yaml` inside your project. You can edit
it with any text editor, or run `codeeagle config edit`.

### Where CodeEagle looks for it

This catches people out, so it is worth reading once. CodeEagle looks in this
order and stops at the first it finds:

1. The file named by `--config <path>`
2. The project named by `-p <name>`
3. **Walking up from the folder you are standing in**, looking for `.CodeEagle/`
4. `~/.CodeEagle/config.yaml` — your personal, catch-all configuration

Step 3 is the one to remember: **running the same command from a different
folder can use a different database**, and will quietly find nothing. If a
command reports an empty graph, check where you are standing first.

A good arrangement is a `.CodeEagle` inside each repository for its code, plus
`~/.CodeEagle` for your documents and meetings.

### What each section does

```yaml
project:
  name: my-project          # a label; also how `-p` finds this project

repositories:               # what to read
  - path: /home/you/projects/my-project
    type: single            # `single` or `monorepo`

watch:
  exclude:                  # never read these
    - '**/node_modules/**'
    - '**/.git/**'
    - '**/vendor/**'

languages:                  # which parsers to use
  - go
  - python
  - typescript

graph:
  storage: embedded         # a local database; nothing external to install
  # db_path: /somewhere/else/graph.db   # optional: keep the database elsewhere

federate:                   # optional: also search these when asking questions
  - ~/.CodeEagle            # e.g. your meetings, from inside a code repo

agents:
  llm_provider: claude-cli  # which model answers `codeeagle agent` questions
  model: sonnet

docs:
  provider: ollama          # reads documents and images; see step 8
  max_image_resolution: 1024
  exclude_extensions: ['.lock', '.min.js', '.map']
```

**Worth knowing on day one:** `repositories`, `watch.exclude`, and `languages`.
Everything else has a sensible default.

**Git matters here.** A repository tracked by git records paths relative to the
repository. A plain folder records the folder's name as well, so two folders can
hold a `notes.md` without colliding. If you list more than one repository, the
repository name is recorded for all of them.

---

## 5. Store your API keys safely

Some features call a paid service and need a key. **Do not paste keys into the
config file** — config files get committed to git and shared.

Instead, any value can fetch its own value when the file is read:

```yaml
transcripts:
  baseten_api_key: ${BASETEN_API_KEY}                        # an environment variable
  jev_api_key: $(keyring get typesafe.ai you@example.com)    # a command's output
```

| You write | It means |
|---|---|
| `${NAME}` | the environment variable `NAME` |
| `${NAME:-fallback}` | `NAME`, or `fallback` if it is not set |
| `$(some command)` | whatever that command prints |
| `$$` | a literal `$`, for values that contain one |

If a command fails, CodeEagle stops and says so, rather than carrying on with
an empty key and failing confusingly later. Neither the key nor the command's
output ever appears in an error message.

### Putting a key in your system keyring

**Linux** (install with `pip install keyring` if needed):

```bash
keyring set baseten.co you@example.com     # it will prompt for the key
keyring get baseten.co you@example.com     # check it comes back
```

**macOS** (built in):

```bash
security add-generic-password -s baseten.co -a you@example.com -w
security find-generic-password -s baseten.co -a you@example.com -w
```

Then reference it:

```yaml
  baseten_api_key: $(keyring get baseten.co you@example.com)
```

On macOS use `security find-generic-password -s baseten.co -a you@example.com -w`
inside the `$( )` instead.

> Older configurations use `api_key_env` and `api_key_command`. Those still
> work, but the syntax above does the same thing on every setting, so there is
> no reason to use them in a new configuration.

---

## 6. Google Cloud credentials (only for Vertex AI)

Skip this unless you set `provider: vertex-ai` anywhere. Vertex AI does not use
an API key — it uses Google's *Application Default Credentials*.

**The interactive way**, for your own machine:

```bash
gcloud auth application-default login
```

A browser opens; sign in. This writes
`~/.config/gcloud/application_default_credentials.json`, which CodeEagle finds
automatically.

**The service-account way**, for a server or CI:

```bash
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
```

Or name it in the config:

```yaml
docs:
  provider: vertex-ai
  project: my-gcp-project
  location: us-central1
  credentials_file: /path/to/service-account.json
```

**Check it worked:**

```bash
gcloud auth application-default print-access-token
```

A long string of characters means you are signed in. An error means you are
not.

> CodeEagle decides Vertex AI is available when either
> `GOOGLE_APPLICATION_CREDENTIALS` or `GOOGLE_CLOUD_PROJECT` is set. If it is
> picking Vertex AI when you did not want it, unset those.

---

## 7. Index your meeting recordings

CodeEagle reads recordings that have been transcribed, and works out who spoke,
what was discussed, what was decided, and what people committed to.

**Where recordings come from.** Any of these work:

| Format | Where it comes from |
|---|---|
| `session.json` | a local recorder that separates voices |
| `.vtt`, `.srt` | Zoom, Teams, most recorders — the caption file |
| `.docx` | Teams, "Meeting Recording" transcript export |

Add a section to your config:

```yaml
transcripts:
  enabled: true
  sessions_dir: ~/.local/share/tomoe/sessions   # a path, or a list of paths
  owner: "Your Name"                            # microphone audio is always you
  owner_aliases: ["Yourname"]                   # spellings transcription produces

  provider: baseten
  model: deepseek-ai/DeepSeek-V4.1-Flash
  baseten_api_key: $(keyring get baseten.co you@example.com)

  min_confidence: 0.70
  concurrency: 8
```

**See what a run would cost, before spending anything:**

```bash
codeeagle meetings sync --dry-run
```

```
To enrich:        120 recordings
Formats:          tomoe 118, webvtt 2
Speech:           58.4 hours
Participants:     312 across the recordings to enrich
Prompt tokens:    ~4.1M (two passes per recording)
```

**This costs real money.** Roughly three cents a meeting, so a few hundred
recordings is a few dollars and a couple of hours. Then run it for real:

```bash
codeeagle meetings sync
```

Recordings already done are skipped, so re-running after new meetings only pays
for the new ones. Explore what it found:

```bash
codeeagle meetings list
codeeagle meetings people
codeeagle meetings actions --person Kevin
codeeagle meetings show <id>
```

A transcript sitting in a folder you already index as documents is picked up
automatically — no `sessions_dir` entry needed.

---

## 8. Install Ollama (for search and document topics)

Ollama runs small models on your own machine, free and offline. CodeEagle uses
it for semantic search (`codeeagle rag`) and for reading documents and images.

```bash
curl -fsSL https://ollama.com/install.sh | sh     # Linux
brew install ollama                               # macOS
```

Then fetch the model that turns text into something searchable:

```bash
ollama pull nomic-embed-text-v2-moe
```

Check it is running:

```bash
ollama list
```

Build the search index:

```bash
codeeagle vectorindex
```

It reports progress as it goes. A graph of ~55,000 items takes about ten
minutes.

> **A graphics card makes a large difference.** Ollama uses one automatically if
> you have one. Without one this still works, just slower. `ollama ps` shows
> whether the model is on the GPU or the CPU.

---

## Troubleshooting

### `command not found`

The program is not where your terminal looks. Check:

```bash
ls -l /usr/local/bin/codeeagle
echo $PATH
```

If the file exists but `/usr/local/bin` is missing from `PATH`, add it to your
`~/.bashrc` or `~/.zshrc`:

```bash
export PATH="/usr/local/bin:$PATH"
```

Then open a new terminal.

### The graph is empty

Almost always the wrong folder. CodeEagle walks *up* from where you are
standing looking for `.CodeEagle/`, and falls back to `~/.CodeEagle`. Running
from a different folder uses a different database and finds nothing.

```bash
pwd                         # where am I?
ls -d .CodeEagle            # is the project here?
codeeagle status            # what does it think it has?
```

Either `cd` into the project, or use `--config`:

```bash
codeeagle --config ~/projects/my-project/.CodeEagle/config.yaml status
```

### `codeeagle meetings list` shows nothing

Same cause. Meetings live in whichever database the configuration points at. If
your meetings are in your personal configuration, run from your home folder, or
put `graph.db_path` in both configurations so they share one database.

### "Cannot acquire directory lock"

Another CodeEagle is using the database. Only one program can write at a time.
Usually a `codeeagle watch` or a `codeeagle vectorindex` still running:

```bash
ps aux | grep codeeagle
```

Wait for it, or stop it. Reading commands run by other people are unaffected.

### A credential command fails silently

Run the command on its own and see what it says:

```bash
keyring get baseten.co you@example.com
```

CodeEagle never prints the key or the command's output in an error, so the
error alone will not tell you what went wrong. If the command prints nothing,
the key is not in the keyring under that name.

### Indexing is very slow

`codeeagle sync -v` shows what it is working on. Large binary files, caches and
build folders are the usual cause — add them to `watch.exclude`.

For search indexing, `codeeagle vectorindex` reports its progress and an
estimate. If it is much slower than a few minutes for a large graph, check
whether Ollama is using your graphics card with `ollama ps`.

---

## Where things live

| Path | What it is |
|---|---|
| `.CodeEagle/config.yaml` | this project's settings |
| `.CodeEagle/graph.db` | the knowledge graph |
| `.CodeEagle/vec.db`, `vec.idx` | the search index |
| `~/.CodeEagle/` | your personal configuration, used outside any project |

`.CodeEagle/` is excluded from git by default. Deleting it loses the index, not
your files — re-run `codeeagle sync` to rebuild. **Meeting data is the
exception**: re-creating it costs money, so back up `graph.db` before anything
drastic.
