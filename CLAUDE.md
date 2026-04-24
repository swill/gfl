# CLAUDE.md — confluencer

## Project Overview

`confluencer` is a standalone Go CLI that implements deterministic, bidirectional synchronisation between Markdown files tracked in a Git repository and pages in an Atlassian Confluence instance. It operates through Git hooks and the Confluence REST API — no CI, no external services, no LLMs, no Pandoc.

It understands the hierarchical structure of a Confluence space and mirrors it as a directory tree of Markdown files. The page ↔ file mapping is derived automatically from the tree and maintained in a tracked index file. There is no per-file configuration.

`confluencer` is maintained as its own repository. Developers install the binary onto their PATH (`go install`, release artifact, or package manager). Consuming repositories contain only configuration and hook shims — no Go toolchain required.

## Core Design Principles

- **Tree-aware sync.** The full Confluence hierarchy rooted at a configured anchor page is mirrored bidirectionally as a directory tree.
- **Page ID is the stable identity.** Confluence page IDs never change; they are the canonical identifier on the Confluence side, and the index is the bridge to local file paths.
- **Deterministic output.** Both conversion directions are purpose-built Go lexers with canonical output. Given the same input, the output is always identical — this is the primary mechanism preventing formatting drift loops.
- **Either side may be the source of truth.** Edits, renames, additions, or deletions on either Git or Confluence are reconciled on every push and pull.
- **Non-blocking on failure.** A Confluence write failure never permanently blocks a push; it queues and retries.
- **Minimise user-visible failure modes.** Unsupported constructs are preserved (not rejected); sibling-slug collisions are disambiguated deterministically (not flagged as errors). Developers are only prompted for genuinely ambiguous cases.
- **Pure lexers.** No network, filesystem, or index access in the lexer package. I/O resolution happens in the orchestrator via injected resolvers. Round-trip tests run entirely in memory.

## Repository Structure

```
confluencer/
  main.go
  cmd/       root, init, install, push, pull, status, version, helpers
  lexer/     pure text transforms: normalise, cf_to_md, md_to_cf, fence, slugify
  api/       Confluence REST v1 client: content, attachments
  gitutil/   commits, diff, mv/branch/rebase/stash, baseline, merge-file
  tree/      CfTree/CfNode, PathMap, typed change set (Diff), PlanMoves, promote
  index/     .confluencer-index.json, .confluencer-pending (NDJSON)
  config/    .confluencer.json, .env credential loading
```

Slugify lives in `lexer/` because it's a pure text transform with no Confluence or tree dependencies — grouping all pure transforms there keeps the package boundary clean.

## Consuming Repository

```
<repo-root>/
  .confluencer/hooks/        # tracked shims: pre-push, post-commit, post-merge, post-rewrite
  .confluencer.json          # tracked — root page ID, cached space key, local root, attachments dir
  .confluencer-index.json    # tracked — page ID to path index
  .confluencer-pending       # gitignored — NDJSON queue of failed writes
  .env                       # gitignored — Confluence credentials
  docs/                      # local root (configured in .confluencer.json)
    index.md
    _attachments/            # page-tree-mirrored assets
    architecture/
      index.md
      database-design.md
    ...
```

## Configuration

### `.confluencer.json` (tracked)

```json
{
  "confluence_root_page_id": "123456789",
  "confluence_space_key": "DOCS",
  "local_root": "docs/",
  "attachments_dir": "docs/_attachments"
}
```

`confluence_space_key` is cached on `confluencer init` from the root page metadata so that new-page `POST` calls don't re-fetch it every run.

### `.env` (gitignored)

```
CONFLUENCE_BASE_URL=https://yourorg.atlassian.net/wiki
CONFLUENCE_USER=your.email@yourorg.com
CONFLUENCE_API_TOKEN=your_api_token
```

Credentials are read from env vars — never from CLI flags, to prevent exposure in process listings. Environment variables take precedence over `.env`. Missing credentials yield an actionable error referencing `.env`.

## Index and Pending Files

### `.confluencer-index.json` (tracked)

The stable bridge between page IDs and local paths. Entries:

```json
{"confluence_page_id": "123", "confluence_title": "Architecture",
 "local_path": "docs/architecture/index.md", "parent_page_id": "999", "version": 12}
```

- `version` — Confluence page version at last sync. Pull compares this to the live version to skip body fetches for unchanged pages.
- `parent_page_id` — avoids re-deriving parentage from the filesystem during push.
- Entries written sorted by `local_path` for deterministic diffs.
- Updated in the same commit as any file operation it describes.

### `.confluencer-pending` (gitignored)

NDJSON queue of failed Confluence operations. Record types: `content`, `rename`, `create`, `delete`, `attachment`. Drained on the next push (oldest first) or manually via `confluencer push --retry`. Successful retries remove the entry; failures increment `attempt` and update `last_error` / `queued_at`.

## Tree Structure

### Hierarchy Mirroring

- A page with **no children** → a flat `.md` file named after the slugified title.
- A page with **one or more children** → a directory named after the slugified title, containing `index.md` (the page's own body) plus one child `.md` or subdirectory per child.
- The root anchor page is always `index.md` directly under `local_root`.

Empty `index.md` files are a fully supported state. A body → empty (or vice versa) is a content change handled like any other.

### Attachments

Attachments live under `_attachments/` mirroring the page hierarchy. For a page at logical path `<page-path>`, its attachments are at:

```
<attachments_dir>/<page-path-without-trailing-index.md>/<filename>
```

| Page | Attachment path |
|---|---|
| `docs/index.md` | `docs/_attachments/<file>` |
| `docs/architecture/index.md` | `docs/_attachments/architecture/<file>` |
| `docs/architecture/database-design.md` | `docs/_attachments/architecture/database-design/<file>` |

Properties:

- **No collisions** — two pages can each reference `image.png` without interference.
- **Confluence filename preserved verbatim** — no Confluence-side attachment renames are ever performed; upload and download both key on `(page_id, filename)`.
- **Page renames move attachments** — the attachment subdirectory is `git mv`'d in the same sync commit.
- **Flat and promoted pages use the same attachment dir** — promotion does not move attachments.

Markdown images use paths relative to the `.md` file:

```markdown
![schema](../_attachments/architecture/database-design/schema.png)
```

`md_to_cf` recognises any path under `_attachments/` and emits `<ac:image><ri:attachment ri:filename="…"/></ac:image>` with just the leaf filename.

### Slugification (`lexer/slugify.go`)

Page title → slug, applied in order:

1. Lowercase.
2. Collapse whitespace runs to single hyphens.
3. Underscores → hyphens.
4. Drop all non-`[a-z0-9-]` characters.
5. Collapse consecutive hyphens; trim leading/trailing hyphens.
6. Empty result falls back to `page-<pageID>`.

**Sibling collision disambiguation** (`DisambiguateSiblings`): when two or more siblings produce the same slug, the one with the numerically lowest page ID keeps the plain slug; every other colliding sibling gets `-<last-6-digits-of-page-id>` appended. Deterministic, collision-free, and stable across renames (the canonical winner is selected by page ID, not slug order).

**Reverse slugification** (filename → title, used only for push-side renames that actually need a title change):

1. Strip `.md`.
2. Strip any trailing `-DDDDDD` collision suffix.
3. Hyphens/underscores → spaces.
4. Title case.

### Title Stability Rule

On a push-direction rename, the Confluence page title is updated **only if** `Slugify(indexTitle) != filenameSlug`. If the new filename slugifies to the same value as the current Confluence title, the title is preserved verbatim — this prevents capitalisation and punctuation drift on no-op renames. Implemented as `lexer.TitleSlugsMatch`.

Developers who need specific capitalisation set it in Confluence and let pull propagate it; they should not try to encode capitalisation in filenames.

## Typed Change Set (`tree/diff.go`)

`tree.Diff` produces a slice of `tree.Change`, one per differing page:

| Type | Condition | Operation |
|---|---|---|
| `ContentChanged` | Body differs, path unchanged | Write new Markdown |
| `RenamedInPlace` | Title/slug changed, same parent | `git mv` within same dir |
| `Moved` | Parent page changed | `git mv` across dirs, update ancestors |
| `AncestorRenamed` | Path changed only because an ancestor was renamed | Swept up by ancestor's `git mv` |
| `Promoted` | Flat `.md` gained a child | `git mv <slug>.md <slug>/index.md` |
| `Demoted` | `<slug>/index.md` lost all children | `git mv <slug>/index.md <slug>.md` |
| `Created` | Not in index, exists in tree | Write new file, add index entry |
| `Deleted` | In index, direct `GET /content/{id}` → 404 | `git rm` file + attachment subdir |
| `Orphaned` | In index, page exists but ancestry outside sync scope | Warn, leave local untouched |
| `MissingUnknown` | In index, fetch returned network/5xx | Warn, skip this run |

Classification priority in `classifyPathChange`: Moved > Promoted/Demoted > RenamedInPlace > AncestorRenamed.

### Two-Phase Rename Protocol (`tree.PlanMoves`)

When any move's destination equals another move's source (a collision), all moves use stash-and-place:

1. **Stash** — `git mv` each involved file to `<localRoot>/.confluencer-staging/<page-id>.md`.
2. **Place** — `git mv` each staged file to its final path.

No-collision runs use a single `PhaseDirect` op per move. The staging directory is created and removed within the same sync and never appears in a committed tree.

## Lexer

Pure functions — no I/O. The orchestrator injects `PageResolver` and `AttachmentResolver` for cross-page links and attachment references so round-trip tests stay in memory.

### Normalisation (`lexer/normalise.go`)

`Normalise(md string) string` returns Markdown in canonical form. Both lexer outputs pass through it before being returned.

- UTF-8, no BOM, LF line endings, exactly one trailing newline.
- No trailing whitespace; exactly one blank line between top-level blocks.
- ATX headings only (`#` … `######`).
- Emphasis: `*text*`, `**text**`, `~~text~~` (GFM strikethrough).
- Lists: `-` unordered; `1.` for every ordered item (not incrementing); 2-space indent per nesting level.
- Fenced code: triple backticks with lowercased language tag.
- Links: inline `[text](url)` only.
- Images: inline `![alt](path)`.
- Tables: GFM pipe tables; alignment colons preserved (`:---:` centre, `:---` left, `---:` right).
- Blockquotes: `> ` prefix.
- Thematic break: `---`.
- Both hard (`\\\n`) and soft line breaks are preserved as `\\\n` — Confluence relies on significant line breaks for layout.

`Normalise` is idempotent: `Normalise(Normalise(x)) == Normalise(x)`. HTML blocks (including fences — see below) pass through verbatim.

### cf_to_md and md_to_cf

The full construct mapping lives alongside the implementations in `lexer/cf_to_md.go` and `lexer/md_to_cf.go`. Notable choices:

- `cf_to_md` uses `golang.org/x/net/html` (lenient) for tokenisation.
- `md_to_cf` uses `goldmark` with the GFM extension; backslash-escapes in the AST are resolved to literals before XML encoding to prevent escape accumulation on round trips.
- Confluence code macros are emitted as `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">…</ac:parameter><ac:plain-text-body><![CDATA[…]]></ac:plain-text-body></ac:structured-macro>`.
- Cross-page links: `<ac:link><ri:page …/><ac:plain-text-link-body>…</ac:plain-text-link-body></ac:link>`.
- Image attachment refs: `<ac:image><ri:attachment ri:filename="…"/></ac:image>`.
- Tables extract/emit alignment via `style="text-align: …"` on `<th>`/`<td>`.
- Raw HTML blocks that do **not** match the fence format are wrapped in `<p>` and escaped as text — we never emit `<ac:structured-macro ac:name="html">` (many Confluence Cloud instances disable it).
- `<ac:structured-macro ac:name="toc">` is dropped; unknown `<ac:structured-macro>` is fence-preserved.

### Confluence-Native Fence (`lexer/fence.go`)

Constructs Markdown cannot represent (Jira macros, user mentions, panels, layouts, unknown structured macros) are preserved as a single HTML-comment block with base64-encoded storage XML:

```
<!-- confluencer:storage:block:v1:b64
<base64 body wrapped at 76 cols>
-->
```

- Base64 avoids the problem that storage XML can legitimately contain `-->` (e.g. inside CDATA).
- goldmark parses this as one HTML block (start condition 2) and the canonical renderer emits it verbatim — no special handling in `Normalise`.
- `DecodeBlockFence(EncodeBlockFence(x)) == x` for arbitrary XML (property-tested).

v1 emits every unsupported construct as a block fence on its own line. Inline positional fidelity inside paragraphs is deferred to v2 — v1 never loses the construct, which is the load-bearing guarantee.

### Round-Trip Idempotency

The primary correctness property:

- `Normalise(cf_to_md(md_to_cf(md))) == Normalise(md)` for every construct in the supported mapping.
- `md_to_cf(cf_to_md(xml))` reaches a fixed point after one round trip.

Fence-preserved constructs round-trip byte-for-byte in storage XML. If idempotency breaks for any construct, every pull produces a spurious diff and drives an infinite sync-commit loop. Round-trip tests cover the full mapping (see `lexer/roundtrip_test.go`) and pass before any hook orchestration.

## Push (`cmd/push.go`)

`confluencer push` is a fire-and-forget write to Confluence. It never creates local commits, modifies tracked files, or updates version state — version tracking and structural catch-up are pull's job.

Invocation modes:

- **Hook mode** (stdin is a pipe from Git): parses pre-push stdin (`<local-ref> <local-sha> <remote-ref> <remote-sha>`). Delete-branch refs are skipped.
- **Direct mode** (stdin is a terminal): diffs from `LastSyncCommit` (or beginning of history if none) to `HEAD`. Works without a Git remote.

Sequence:

1. Drain `.confluencer-pending` first, in queue order.
2. If `--retry`, stop here (drain-only).
3. For each commit range, compute `.md` diffs (with rename detection, `-M`) and filter out files whose most recent modifying commit in the range is a sync commit — their bodies were already pushed.
4. Sort so `index.md` files come first (parents enter the index before children look them up).
5. Process each diff:
   - **Deleted** → `DELETE /content/{id}`; 404 is treated as success.
   - **Renamed** → apply the Title Stability Rule, fetch for version, `PUT` with updated title/body (and ancestors if parent changed).
   - **Added** → if already in the index, treat as an update; otherwise resolve the parent via the directory structure and `POST /content`. Intermediate directory pages are auto-created (`ensureParentPages`) so a deep new file doesn't fail because its ancestors have no Confluence page yet.
   - **Modified** → `GET` for version, convert, `PUT`. On `409`, re-fetch the version and retry once; if that fails too, queue.
6. Unrecoverable per-item failures append to `.confluencer-pending`. Exit 0 in all normal paths — the Git push proceeds.

Attachment uploads during push are not implemented today; attachment propagation is pull-driven, with queued `attachment` pending entries retried on the next push.

## Pull (`cmd/pull.go`) — Branch-Based Rebase

Triggered by `post-commit`, `post-merge`, `post-rewrite`, and direct invocation. All hook shims are guarded by `CONFLUENCER_HOOK_ACTIVE` to prevent recursion, since pull creates its own commits.

Sequence:

1. Acquire an exclusive file lock at `<git-dir>/confluencer-pull.lock`. If held, exit silently — the holder will do the work. Direct invocation reclaims stale locks.
2. Fetch the full Confluence tree from `confluence_root_page_id` (structure only — bodies fetched on demand).
3. Resolve pages in the index but absent from the tree via direct `GET /content/{id}`:
   - 404 → `StatusDeleted`.
   - 200 with out-of-scope ancestry → `StatusOrphaned`.
   - Network/5xx → `StatusUnknown`.
4. Compute the structural change set via `tree.Diff`.
5. For pages with no structural change, compare the live Confluence version to the stored index version. For mismatches, fetch the body, run `cf_to_md`, and compare to the local file — only actual differences produce a `ContentChanged` entry. Matching versions require no work.
6. Warn on `Orphaned` / `MissingUnknown` (informational only, no file changes). If nothing actionable remains, update versions and exit.
7. **Branch-based application:**
   - Remember current branch; stash uncommitted changes if any.
   - Determine the base SHA for the sync branch (the "last-known in-sync with Confluence" point):
     1. Most recent `chore(sync): confluence` commit, else
     2. Most recent commit that modified `.confluencer-index.json` (the index is rewritten only by `init` and `pull`, so this always points at a committed in-sync state).
     3. If neither exists, error — developer must commit after `confluencer init` before pulling.

     Falling back to HEAD is unsafe: HEAD may contain local edits diverged from Confluence, and the sync branch's fast-forward would silently overwrite them.
   - Create a temp `confluencer-sync` branch at the base SHA; check it out.
   - Apply changes in order: deletions → planned moves (via `PlanMoves`, with attachment subdir moves) → creates → content changes.
   - Download new/changed attachments.
   - Update the index (including versions for every page in the tree); stage and commit as `chore(sync): confluence`.
   - Check out the original branch; `git rebase confluencer-sync`.
   - On rebase failure, abort and fall back to `git merge confluencer-sync`. Conflicts surface as standard git merge conflicts, resolved with the developer's normal tools.
   - Delete the sync branch; pop the stash.
8. Release the lock.

## Hooks

`confluencer init` writes shims to `.confluencer/hooks/` and installs them into `.git/hooks/` in the same step. `confluencer install` performs just the copy — used when cloning an existing confluencer-managed repo.

```sh
# pre-push
#!/bin/sh
set -e
confluencer push

# post-commit / post-merge / post-rewrite (same shape)
#!/bin/sh
set -e
if [ -n "$CONFLUENCER_HOOK_ACTIVE" ]; then
  exit 0
fi
export CONFLUENCER_HOOK_ACTIVE=1
confluencer pull
```

- `pre-push` has no guard — push never creates commits, so it can't re-trigger itself.
- `post-commit` runs pull after every commit so Confluence-side edits are caught before the next push. Fails silently if Confluence is unreachable — the developer is never blocked from committing.
- `post-rewrite` re-establishes the sync marker on the new tip after `rebase` / `commit --amend`.
- Double-firing across post-commit / post-merge / post-rewrite is prevented by the file lock and the env-var guard.

## Confluence REST API (`api/`)

Basic Auth. See `api/content.go` and `api/attachments.go` for the exact endpoints. Implemented operations:

- `GetPage(id)` with `expand=body.storage,version,ancestors,space`
- `GetChildren(parentID)` — paginated
- `FetchTree(rootID, fetchBody)` — BFS walk
- `CreatePage(space, parent, title, xml)`
- `UpdatePage(id, version, title, xml, parentID)` — empty `parentID` = unchanged
- `DeletePage(id)` — cascades to descendants server-side
- `GetAttachments(pageID, filename?)`, `DownloadAttachment(path)`, `UploadAttachment(pageID, filename, data)`

`api.IsConflict` / `api.IsNotFound` classify errors from `APIError`.

## CLI Reference

| Command | Description |
|---|---|
| `confluencer init --page-id <id> [--local-root <path>]` | Populate local repo from an existing Confluence tree. Writes config, index, hook shims; installs hooks. Does not commit. |
| `confluencer install` | Copy hook shims from `.confluencer/hooks/` into `.git/hooks/`. Idempotent. |
| `confluencer push` | Push local `.md` changes to Confluence (pre-push hook or direct). |
| `confluencer push --retry` | Drain `.confluencer-pending` without a Git push. |
| `confluencer pull` | Fetch Confluence tree and apply the change set via branch-based rebase. |
| `confluencer status` | Report outstanding pending writes (type, path, attempt, last error). |
| `confluencer version` | Print version, commit, build date. |

## Installation and Onboarding

Developers install `confluencer` onto their PATH independently — via `go install github.com/swill/confluencer@latest`, a downloaded release binary, or a package manager. The consuming repository never bundles the Go toolchain or a compiled binary.

See `README.md` for the developer setup steps.

## Implementation Invariants

1. **Round-trip idempotency.** `Normalise(cf_to_md(md_to_cf(x))) == Normalise(x)` for supported constructs; unsupported constructs round-trip byte-for-byte via the fence.
2. **Page ID is the stable identity.** Rename detection, history preservation, and index management all key on page IDs. Paths and titles are derived, mutable properties.
3. **Index and filesystem are consistent at every commit.** Index entries are updated only for successful operations. Failed push operations live in `.confluencer-pending` until they drain.
4. **Sync commits are attributable.** All commits from `confluencer pull` carry the `chore(sync): confluence` prefix. Human commits must never use it — it is the base point for the branch-based rebase.
5. **Renames use `git mv`.** So `git log --follow` traces history. Collisions use the two-phase stash-and-place protocol.
6. **Attachments are co-committed.** A sync commit that modifies a `.md` includes all its referenced attachments under `_attachments/<page-path>/`.
7. **Push never blocks permanently.** Any Confluence write failure queues and the push proceeds.
8. **Credentials never appear in logs, flags, or commits.** Env vars only.
9. **Lexers are pure.** No network/filesystem/index access in `lexer/`. Resolvers are injected.
10. **Title Stability Rule.** A push-side rename updates the Confluence title only if `Slugify(indexTitle) != filenameSlug`.
