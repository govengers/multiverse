# multiverse — `multi`

A git-backed markdown **second brain** for agents and humans. `multi` is the
storage-layer CLI: it hides git transport behind `read` / `write` / `search` so
an agent never thinks about sync, and it enforces the conventions that keep a
brain navigable — above all, **every note has a one-line summary**.

## Why

- **Markdown is the source of truth.** Plain `.md` in a git repo. No database, no
  proprietary store, no obfuscation — readable and editable anywhere (Obsidian, an
  editor, `grep`).
- **Git is the transport.** Real conflict resolution (`*.md merge=union`), offline
  commits, full history/audit. The agent calls `read`/`write`/`search`; `multi`
  does pull/commit/push underneath.
- **Writes are guaranteed structured.** `multi write` refuses without a `summary`,
  auto-fills `created`/`retrieved`, and auto-commits. The "never forgotten"
  guarantee is enforced at the only write path.
- **Headless.** No Obsidian app required — works on servers where agents run.

## Install

```bash
just install          # go install ./cmd/multi  →  $GOBIN/multi
# or
just build            # → ./bin/multi
```

## Control panel (TUI)

```bash
multi                 # no args on a terminal → launches the TUI
multi tui             # explicit (aliases: dashboard, ui)
```

A Bubble Tea control panel with three tabbed views:

- **Dashboard** — every brain with git state (branch · clean/dirty · ↑ahead ↓behind), note count, and lint status; plus the resolved scope for the current directory. Keys: `s` sync · `S` sync all · `l`/`L` lint · `r` refresh · `enter` set active.
- **Brains** — manage the global registry: `a` add · `e` rename · `p` set path · `d` delete · `enter` set active.
- **Scope** — bind the current directory: toggle each brain as `space` source / `t` target, then `w` to write `./.multi.yaml` (`c` clears it).

`tab`/`shift+tab` switch views, `q` quits. On a non-interactive shell (agent/CI), `multi` with no args prints help instead of launching.

## Onboarding

```bash
multi onboard                                   # interactive: new or clone
multi init ~/vaults/mybrain --name mybrain --split topics,projects
multi clone git@host:me/brain.git ~/vaults/brain
multi brain add ~/vaults/work --alias w   # register an existing brain directory
multi brain list            # registered brains with their aliases (* = active)
multi brain use mybrain     # set active brain (name or alias)
```

`init` scaffolds the governance docs (`README`, `read`, `write`, `Conventions`,
`Code of Conduct`, `Home`), `Templates/`, a `.gitignore` that keeps large
binaries out of git, and a `.gitattributes` with `*.md merge=union` — then
`git init`s with an initial commit.

> **Recommended layout:** keep every brain in one place — a `~/vaults/`
> directory in your home dir (`~/vaults/<brain>` per brain). It keeps paths
> predictable, makes backups/sync trivial, and matches the examples throughout
> this README.

## Brain aliases

A brain can carry extra names: `dt` for `deep-thought`, or `pdb` on a machine
whose brain is registered as `pdb-brain`, so a committed `.multi.yaml` that
says `pdb` resolves there too.

```bash
multi brain add ~/vaults/pdb-brain --alias pdb   # --alias is repeatable or comma-separated
multi brain alias deep-thought dt                # add aliases to a brain (named by name or alias)
multi brain unalias dt                           # remove aliases
```

An alias resolves everywhere a brain name is accepted: `--brain`, `.multi.yaml`
sources and targets, `multi use` / `scope set`, `brain use`, the active brain
and the `brain:note` qualifier. `.multi.yaml` stores refs exactly as typed
(`use` / `scope set` still check they resolve): a committed file names brains
for the whole team, so `multi use pdb` on a machine where the brain is
`pdb-brain` with alias `pdb` writes `pdb`, which also works where the brain is
named `pdb`. The per-machine `active` brain stores the real name. Aliases are lowercase kebab-case and unique: an alias never equals a
brain name or another brain's alias, no brain is registered under an existing
alias, and a conflicting write fails, naming the owner, without changing the
registry. Brains and repos are separate namespaces.

## Per-directory scope (multiple brains)

The directory you work in declares which brains it **reads from** (sources) and
**writes to** (targets) via a `.multi.yaml`, walked up the tree like `.git`:

```bash
cd ~/projects/my-project
multi use work                      # read+write the work brain here and below
multi use work --read-only          # read-only: no writes land here
multi scope set --source work,personal --target work   # read both, write only work
multi scope                         # show the resolved scope
multi scope clear                   # remove ./.multi.yaml
```

`.multi.yaml`:

```yaml
sources: [work, personal]   # read commands span all of these
targets: [work]             # writes land here (first = default); omit → = sources
read_only: true             # no write targets at all (overrides targets)
```

With a scope active, **reads span every source** (results labeled by brain) and
**writes go to the target**:

```bash
multi list                    # notes from work AND personal, brain-labeled
multi search "onboarding"     # searches all sources
multi read personal:Home      # brain:note qualifier when a name exists in several
multi write --title ... --summary ...   # lands in work (the target)
multi write -b personal --title ...     # override target for one write
multi sync                    # syncs every brain in scope
```

## Resolution order

Each command resolves its scope as: `--brain <name|alias|path>` (one brain, overrides
everything) → nearest `.multi.yaml` → the brain the cwd sits inside → the active
brain in the registry.

## Repo registry - other checkouts by id

A shared repo (an ops repo, say) can refer to other repositories by id, e.g.
`mbag/wilde-it/cluster-csi`. The id is derived from the checkout's `origin`
remote, so it is the same on every machine, and each machine resolves it to its
own local checkout through the user-level registry, so no checkout path is ever
committed:

```bash
multi repo add ~/src/cluster-csi --alias csi   # path defaults to the cwd; prints the derived id
multi repo path csi                            # id or alias; prints only the path: cd "$(multi repo path csi)"
multi repo alias csi cluster-csi               # add aliases to a repo (named by id or alias)
multi repo unalias cluster-csi                 # remove aliases
multi repo list                                # id, aliases, path, origin remote (inferred live)
multi repo rm csi                              # unregister the repo and its aliases; the checkout is left alone
```

A repo id is a platform prefix plus the full repo path:
`git@mercedes-benz.ghe.com:wilde-it/cluster-csi.git` becomes
`mbag/wilde-it/cluster-csi`, `https://git.fsintra.net/apps/intranet.git`
becomes `fsus-gitlab/apps/intranet`. `ssh://`, scp-style (`git@host:path`) and
`http(s)://` remotes are understood; credentials, port and a trailing `.git`
are dropped and the whole id is lowercased, so every clone of a repo gets the
same id. The prefix comes from the hand-edited `platforms` map (lowercase host
-> prefix); a host without an entry is its own prefix
(`gitlab.example.com/group/repo`). A checkout without an origin remote cannot
be registered.

Aliases are short lowercase kebab-case names of your choice, e.g. `multi` for
`github/mxcd/multiverse`. Every name in the registry is unique: an alias never
equals an id or another repo's alias, and a write that would break this fails,
naming the conflicting entry, without changing the registry. Re-adding a
checkout updates its path and merges any new aliases.

`~/.config/multi/config.yaml` (`$MULTI_CONFIG_DIR/config.yaml` when set) keeps
both maps next to the brains:

```yaml
brains:
  - name: work
    path: /Users/me/vaults/work
    aliases: [w]
platforms:
  mercedes-benz.ghe.com: mbag
  gitlab.wilde-it.com: wit
  git.fsintra.net: fsus-gitlab
  github.com: github
repos:
  github/mxcd/multiverse:
    path: /Users/me/github.com/mxcd/multiverse
    aliases: [multi]
```

Only the path is stored; `multi repo list` reads the origin remote from the
checkout, with credentials stripped from http(s) URLs. Entries written by
v1.6 (`<id>: <path>`) still load and resolve under their old id; to move one to
its derived id, `multi repo rm <old-id>`, then `multi repo add <path> --alias
<old-id>`.
`multi repo path` exits non-zero when an id or alias is not registered or its
directory is gone, and the error tells an agent to ask for the path and run
`multi repo add <path>`. Repos never join a scope: `sync`, `status`, `lint` and
`reindex` leave them alone, and multi runs nothing in them beyond
`git rev-parse` and `git remote get-url`. `--brain` has no effect on repo
commands.

## For agents / LLMs

`multi` is meant to be driven directly by shell-capable agents (e.g. Claude Code)
— no MCP server needed. To make one fluent in one shot:

```bash
multi guide              # compact mental model + cheatsheet (the read→write→sync loop)
multi guide --claude-md  # emit a CLAUDE.md block to paste into a project repo
```

Errors are self-correcting (they say how to fix), and read commands take `--json`.

## The agent loop

```bash
# session start — one bounded, deterministic context load
multi wake-up                       # identity notes in full + pinned facts as one-liners

# read — summary first, body only on confirmed relevance
multi list                          # index: every note + its summary
multi summary "Project Overview"    # one note's summary
multi fm "Project Overview"         # full front-matter block
multi search "vue websocket" [--body]  # ranked: ALL terms must match; path > tags > summary > body
multi find --type reference --tag projects --status active
multi similar "reconnect strategy" # semantic search over the shadow index (see below)
multi similar --note "Some Note"    # nearest neighbors of an existing note
multi explore "q1" "q2" "q3"       # one call per task: search+similar per query, 1 hop along links, summaries + top bodies (see below)
multi read "Project Overview"       # the deliberate full read
multi links / backlinks "..." / orphans

# write — structured + auto-committed (+ near-duplicate warning)
multi write --title "Deployment Runbook" --dir projects \
  --summary "how to deploy the service and roll back" \
  --tags projects,deployment --source "..." --freshness "current" \
  --body "..."                      # or --stdin
multi write --pinned ...            # pin a critical fact into `multi wake-up`
multi append "Deployment Runbook" --content "addendum"   # or --stdin

# transport & integrity
multi sync [-m "msg"]               # commit local → pull --rebase → push
multi status
multi lint [--summary|--tags|--fresh|--kebab|--taxonomy] [--json]
multi fix [--dry-run] [--keep-display]   # rename files+dirs to kebab-case & rewrite links
multi reindex [--force]             # refresh the semantic shadow index (incremental)
```

Most read commands accept `--json` for machine consumption.

## The brain conventions

Front matter (`multi write` enforces the core; `multi lint` checks all):

```yaml
type: reference        # moc | reference | decision | hub | meta
status: active         # active | draft | deprecated
tags: [<split>, ...]   # content notes carry one split tag + topic tags
created: <YYYY-MM-DD>   # auto-filled
summary: One-line description — the gate readers read first
source: ...            # provenance
retrieved: <YYYY-MM-DD># auto-filled
freshness: ...         # one-line currency/trust note
```

**Standing rules** (enforced by `multi lint`): every note has a `summary`; every
*content* note carries one split tag and records `source`/`retrieved`/`freshness`;
hubs and the `[[wikilink]]` graph are the index, not folders.

**Closed taxonomy (optional).** A brain can close its `type`/`status`
vocabularies in `.multi/brain.yaml` — then `multi write` rejects and
`multi lint` flags anything outside them, so the vocabulary can't drift as
agents write:

```yaml
taxonomy:
  types: [pattern, reference, decision, lesson, project, journal, moc, feedback]
  statuses: [active, draft, deprecated, archived]
```

**Ignored directories (optional).** A brain that hosts tooling alongside its
notes (a docs site, a visual plan app) can exclude directory names from note
discovery — matched anywhere in the tree. Dot-directories (`.git`, `.obsidian`,
`.multi`) and `node_modules` are always ignored:

```yaml
ignore: [dist, vendor]
```

**Wake-up (session bootstrap).** `multi wake-up` prints a small, deterministic
context block: the notes listed under `wakeup:` in `.multi/brain.yaml` in full
(L0 — keep them short) plus every note with `pinned: true` front matter as a
`path | summary` line (L1). Point your agent's session-start hook at it and
stop re-discovering the brain every session:

```yaml
wakeup: [working-with-me, collaboration-style]
```

**Filing rules (optional).** The ingester's ingest and groom prompts carry
built-in filing rules: where a note goes, how big it may grow, where chronology
lives, how MOCs and dates look. A brain with a different layout names a note
whose body replaces them, so the rules live in the brain next to the notes they
govern and change by editing that note. Without the setting the built-in rules
apply unchanged. A named note that is missing or empty fails the job instead of
falling back to rules the brain does not use:

```yaml
filing_rules: filing-rules
```

**File names are kebab-case.** Every file and directory name is forced to
lowercase-with-hyphens (`projects/deployment-runbook.md`) so brains stay portable
across case-insensitive filesystems (Windows, macOS) and safe to type unquoted.
`multi write` slugs the path automatically while keeping the in-note `# H1` title
human-readable, and you can still reference a note by any casing. To migrate an
existing brain, `multi fix` renames every file and directory and rewrites every
`[[wikilink]]` to match (`--dry-run` to preview, `--keep-display` to preserve a
link's rendered text as `[[slug|Old Name]]`).

## Semantic recall — the shadow index

`multi search` matches words; `multi similar` matches meaning. It runs on a
**derived, disposable** embedding index: one vector per note, cached outside the
repo (`~/.cache/multi/` — never committed), brute-force cosine at query time.
Markdown stays the only source of truth; delete the cache any time and
`multi reindex` rebuilds it incrementally (unchanged notes are skipped by
content hash).

Configure an OpenAI-compatible `/v1/embeddings` endpoint per brain in
`.multi/brain.yaml` — local Ollama, LM Studio, vLLM and OpenAI all speak it:

```yaml
embeddings:
  base_url: http://localhost:11434/v1        # Ollama
  model: qwen3-embedding:0.6b                # ollama pull qwen3-embedding:0.6b
  query_prefix: "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: "
  # doc_prefix: ""                           # qwen3 embeds documents raw
  # api_key_env: OPENAI_API_KEY              # only for endpoints that need auth
```

Model notes: `qwen3-embedding:0.6b` is the recommended default (best
quality-per-RAM on Ollama, 32K context, multilingual, Apache-2.0) — and it
wants the instruct prefix on *queries only*, which the config above bakes in.
Ollama never applies model prefixes for you. `snowflake-arctic-embed2`
(`query_prefix: "query: "`) is a solid runner-up with the best German retrieval
scores. Then:

```bash
multi reindex                       # first build embeds everything; later runs only the diff
multi similar "how do we handle websocket reconnects"
multi similar --note "Some Pattern" # near-duplicate / related-note hunting
```

**Another endpoint on your machine.** `brain.yaml` is shared through git, so
everyone using the brain gets its endpoint. To embed somewhere else yourself —
llama-server on a GPU box, another port — put an `embeddings:` block in your
registry, `~/.config/multi/config.yaml`, at the top (all brains) or under a
brain's entry (that brain; it wins). Its fields replace the brain's own on this
machine only and are never written back to `brain.yaml`; fields it leaves out,
such as `query_prefix`, keep the brain's values:

```yaml
embeddings:                                  # every brain on this machine
  base_url: http://gpu-box:8090/v1           # llama-server --embedding
brains:
  - name: csi
    path: /Users/me/brain/csi
    embeddings:                              # this brain only
      model: qwen3-embedding-4b
```

Vectors from different models do not mix: after changing the model, run
`multi reindex`.

Without an `embeddings:` block, `similar`/`reindex` simply say so and everything
else works unchanged. `multi write` also warns lexically (token overlap, no
network needed) when a new note looks like a near-duplicate of an existing one.

## Explore - one call per task

`multi explore` is the retrieval an agent runs once at the start of a task,
instead of a loop of search / similar / read calls:

```bash
multi explore "vue websocket" "reconnect strategy" "hub realtime" --bodies 3
```

Every query goes through `search` and, when a shadow index exists, `similar`.
The top hits of every list fuse by reciprocal rank into **seeds**; the seeds'
outgoing `[[wikilinks]]` are followed for `--hops` (default 1). MOCs and any
note with more than 30 outgoing links count as hubs: they are reached, listed,
and never expanded. Seeds always rank above linked notes; a linked note takes
half of its best parent's score, scaled by the same status/pin/type weights
as `search`.

The output is plain text for the calling model: every hit as a `path | summary`
line (seeds, then linked), followed by the full bodies of the `--bodies` best
seeds (default 3, `0` for summaries only; MOC bodies are skipped because they
are link lists, not knowledge). The text stays under `--max-bytes` (default
24000, `0` = no cap), because agent harnesses move bigger tool output into a
file the agent often never reads (Claude Code: 30 KB). The budget fills seed
summaries, then bodies, then linked summaries; a body that only partly fits is
cut at a line boundary, and a trailer counts what was cut. `--json` returns the same hits with `hop`,
`score` and `body` fields; `--top` sets how many hits each list contributes
per query (default 5).

Measured on a 3,800-note brain: five queries return about 100 summaries plus
5 bodies in 3-4 s warm (the first call after idle pays the embedding model's
load time). More queries beat deeper hops: `--hops 2` already returns a tenth
of the brain. Give 3-5 short queries that name the technologies, projects and
the verb of the task; one off-topic query seeds off-topic notes. Without an
`embeddings:` block, explore runs on `search` alone.

## Large files

Markdown lives in git. **Large binaries (PDFs, images, archives) do not** — they
bloat every clone forever. Keep them in object storage and link them (see a
brain's `.gitignore`). Object-storage attachment handling is a planned follow-up.

## Ingester — automatic session-end integration (`ingester`)

A companion binary that runs on Claude Code's **Stop** hook and weaves each session's
learnings into a brain — **extending and cross-linking existing notes** instead of
dumping standalone files. It keeps a per-session **ledger** (transcript cursor + the
notes touched so far) so repeated Stop firings *continue* one integration rather than
duplicating, and it does the actual editing by steering a dedicated, **subscription-
backed** interactive Claude session over tmux (so it never spends Agent-SDK / API
credit).

Flow: `Stop → ingester hook` (enqueue + kick the dispatcher, returns instantly) →
detached `ingester dispatch` (single-flight via `flock`) computes the transcript delta,
steers the tmux session to integrate it through `multi --brain <brain>`, waits for a
sentinel report, updates the ledger, then `multi sync`s. A job that stalls times out
into a dead-letter queue — nothing is lost and the host session never blocks.

```
ingester hook       # Stop-hook entry: reads hook JSON on stdin (fast, non-blocking)
ingester dispatch   # detached worker; drains the queue (one integration per session)
ingester run --session <id> --transcript <path>   # one cycle in the foreground (debug)
ingester status     # queue, tmux session, ledgers
ingester session ensure | kill                     # manage the tmux session
```

### Local install (LLM-friendly runbook)

Prerequisites: `go` ≥ 1.26, `tmux`, the `multi` binary, a registered **target brain**
(`multi brain list` shows it), and `claude` (Claude Code) logged in on a subscription.

1. **Build & install both binaries** (ingester ships alongside multi):
   ```bash
   cd <multiverse-repo>
   just install            # → $GOBIN/multi and $GOBIN/ingester  (e.g. ~/go/bin)
   ```
2. **Choose the target brain.** The ingester writes to the brain named `second-brain`
   by default — see `const BrainName` in `internal/ingest/brain.go`. To target another,
   change that constant and re-run `just install`. Confirm it resolves:
   ```bash
   multi brain list        # the target brain must be registered
   ingester status         # prints the home dir, a (down) session, and 0 jobs
   ```
3. **Wire the Stop hook.** Add this object to the `Stop` array in
   `~/.claude/settings.json` (keep any existing entries; use an absolute path):
   ```json
   { "type": "command", "command": "/ABSOLUTE/PATH/TO/ingester hook", "timeout": 10 }
   ```
4. **Recursion guard — already built in, nothing to do.** The steered session is
   launched with `MULTI_INGEST=1` and a hooks-disabled settings file, and
   `ingester hook` no-ops whenever that variable is set, so ingestion can never trigger
   itself.
5. **Verify steering** without a real integration:
   ```bash
   ingester session ensure              # launches the tmux Claude session (subscription)
   tmux attach -t multi-ingest          # watch it; Ctrl-b then d to detach
   ingester session kill
   ```
6. **Full dry-run** on a throwaway transcript (a trivial one should report
   "nothing worth keeping"):
   ```bash
   ingester run --session test --transcript <some-session>.jsonl
   cat ~/.claude/ingester/reports/*_test.md     # ends with:  INGEST-STATUS: done
   cat ~/.claude/ingester/state/test.json        # the ledger
   ```

Data lives under `~/.claude/ingester/` (override via `INGESTER_HOME`): `state/` ledgers ·
`queue/`(+`done`,`failed`) jobs · `reports/` sentinels · `prompts/` per-job instructions ·
`dispatch.log`. Model: **Sonnet** (interactive = subscription quota). To disable, remove
the Stop-hook entry — any queued jobs are inert without it.

## Layout

```
cmd/multi            multi entrypoint
cmd/ingester         ingester entrypoint (session-end integration)
internal/brain       Brain, Note/front matter, index/search, graph, lint, git, scaffold
internal/cli         urfave/cli v3 command tree
internal/config      ~/.config/multi registry of brains and repo checkouts
internal/ingest      ingester: ledger, transcript delta, tmux steering, dispatcher
internal/tui         Bubble Tea control panel
```

## Roadmap (deferred from v1)

- Object-storage attachment path (`mv`-free large-file handling).
- An MCP server surface for agents.
- Background sync daemon (watch + debounced sync) so reads are always current.
- Safe front-matter updates that preserve unknown keys.
```
