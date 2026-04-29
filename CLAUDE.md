# CLAUDE.md — gfl

## Project Overview

`gfl` is a standalone Go CLI that implements deterministic, bidirectional synchronisation between Markdown files tracked in a Git repository and pages in an Atlassian Confluence instance. It operates through Git hooks and the Confluence REST API — no CI, no external services, no LLMs, no Pandoc.

It mirrors the hierarchical structure of a Confluence space rooted at a configured anchor page as a directory tree of Markdown files. Each managed `.md` file carries its Confluence identity in a front-matter block; a persistent local Git branch named `confluence` represents the last-known Confluence-side tree state. Pull and push are diff/merge operations between that branch and your working branch.

`gfl` is maintained as its own repository. Developers install the binary onto their PATH (`go install`, release artifact, or package manager). Consuming repositories contain only configuration and hook shims — no Go toolchain required.

## Core Design Principles

- **Page identity lives with the file.** Every managed `.md` file has front-matter naming `confluence_page_id` (stable across renames and moves) and `confluence_version` (last seen Confluence version). There is no separate index file.
- **Git is the reconciliation engine.** A local branch called `confluence` always represents the last-known Confluence state. Pull updates that branch, then `git merge`s it into the working branch. Push diffs the working branch against `confluence`, sends the result to Confluence, then fast-forwards `confluence` to match. Conflicts are ordinary `git merge` conflicts.
- **Deterministic conversion.** Both directions are purpose-built Go lexers with canonical output. Given the same input, the output is always identical — primary defence against formatting drift loops.
- **Either side may be the source of truth.** Edits, renames, additions, and deletions on either Git or Confluence are reconciled on every push and pull.
- **Self-recovery on partial push failure.** Failed operations re-appear in the next push's diff and are retried. There is no pending queue.
- **Pure lexers.** No network, filesystem, or git access in the lexer package. Resolvers are injected at call sites.
- **Sync output lands on the working branch.** Push commits its sync chore on the current branch (typically `main`), then advances `confluence` to point at that commit. Collaborators pulling `origin/main` always see the latest synced state — `confluence` never gets ahead of `main`.

## Repository Structure

```
gfl/
  main.go
  cmd/       root, init, install, push, pull, status, version, render, helpers
  lexer/     pure text transforms: normalise, frontmatter, cf_to_md, md_to_cf, fence, slugify (incl. DisambiguateSiblings)
  api/       Confluence REST v1 client: content, attachments
  gitutil/   branch/diff/merge/stash/mv primitives, content-at-ref reads
  tree/      CfTree/CfNode, PathMap (slug-based path computation), AttachmentDir
  config/    .gfl.json, .env credential loading
```

## Consuming Repository

```
<repo-root>/
  .gfl/hooks/        # tracked shims: pre-push, post-commit, post-merge, post-rewrite
  .gfl.json          # tracked — root page ID, cached space key, local root, attachments dir
  .env                       # gitignored — Confluence credentials
  docs/                      # local root (configured in .gfl.json)
    index.md
    _attachments/            # page-tree-mirrored assets
    architecture/
      index.md
      database-design.md
    ...
```

Plus a local-only `confluence` branch maintained by the tool.

## Configuration

### `.gfl.json` (tracked)

```json
{
  "confluence_root_page_id": "123456789",
  "confluence_space_key": "DOCS",
  "local_root": "docs/",
  "attachments_dir": "docs/_attachments"
}
```

`confluence_space_key` is cached on `gfl init` from the root page metadata so that new-page POST calls don't re-fetch it every run.

### `.env` (gitignored)

```
CONFLUENCE_BASE_URL=https://yourorg.atlassian.net/wiki
CONFLUENCE_USER=your.email@yourorg.com
CONFLUENCE_API_TOKEN=your_api_token
```

Credentials are read from env vars — never CLI flags, to prevent process-listing exposure. Environment variables take precedence over `.env`. Missing credentials yield an actionable error referencing `.env`.

## Front-Matter

Every managed `.md` file begins with a YAML-subset front-matter block:

```markdown
---
confluence_page_id: "5233836047"
confluence_version: 12
---

# Body content starts here
```

- `confluence_page_id` — the Confluence page ID (always quoted; Confluence IDs are stringly-typed and frequently exceed 32 bits).
- `confluence_version` — the Confluence version number at last sync. Used to detect updates and to compute the version for the next write.
- Unknown keys are preserved verbatim, after the known keys, in their original order (forward-compatibility).
- String values are always double-quoted; the closing `---` is followed by exactly one blank line before the body.

`Normalise` preserves the front-matter at the top in canonical form and normalises the body below. The lexer itself stays pure — the orchestrator (init, pull, push) extracts and re-applies front-matter via `cmd/render.go`; `cf_to_md` and `md_to_cf` only ever see body content.

## The `confluence` Branch

A persistent local Git branch named `confluence` is the canonical representation of "what Confluence looked like at last sync."

- **Seeded** on first pull from the current HEAD via `EnsureBranchFromHead`.
- **Advanced by pull** with a `chore(sync): confluence @ <ts>` commit *on the `confluence` branch itself*, which is then merged into the working branch.
- **Advanced by push** by committing `chore(sync): confluence-push @ <ts>` on the working branch and fast-forwarding `confluence` to that commit (`gitutil.SetBranchRef`). Post-push, `confluence` and the working branch tip are byte-equal.
- **Local-only by default.** You can push it to `origin` if you want a shared canonical view, but the tool doesn't require it.
- **Don't commit to it manually.** Treat it as machine-managed. The hooks and direct invocations of `gfl pull` / `push` are the only legitimate writers.

## Tree Structure

### Hierarchy Mirroring

- A page with **no children** → a flat `.md` file named after the slugified title.
- A page with **one or more children** → a directory named after the slugified title, containing `index.md` (the page's own body) plus one child `.md` or subdirectory per child.
- The root anchor page is always `index.md` directly under `local_root`.

Empty `index.md` files are a fully supported state. A body → empty (or vice versa) is a content change handled like any other.

### Attachments

Attachments live under `_attachments/` mirroring the page hierarchy. For a page at logical path `<page-path>`, its attachments live at `<attachments_dir>/<page-path-without-trailing-index.md>/<filename>`:

| Page | Attachment path |
|---|---|
| `docs/index.md` | `docs/_attachments/<file>` |
| `docs/architecture/index.md` | `docs/_attachments/architecture/<file>` |
| `docs/architecture/database-design.md` | `docs/_attachments/architecture/database-design/<file>` |

- Two pages can each reference `image.png` without interference.
- Confluence filenames are preserved verbatim (no Confluence-side attachment renames). Upload and download both key on `(page_id, filename)`.
- Page renames `git mv` the attachment subdirectory alongside the `.md` file.
- Flat and promoted (`index.md`) forms of the same page share the same attachment directory.

Markdown images use paths relative to the `.md` file:

```markdown
![schema](../_attachments/architecture/database-design/schema.png)
```

`md_to_cf` recognises any path under `_attachments/` and emits `<ac:image><ri:attachment ri:filename="…"/></ac:image>` with just the leaf filename. Path computation lives in `tree.AttachmentDir`.

### Slugification (`lexer/slugify.go`)

Page title → slug: lowercase, whitespace runs and underscores → single hyphen, drop non-`[a-z0-9-]`, collapse and trim hyphens. Empty result falls back to `page-<pageID>`.

**Sibling collision disambiguation** (`lexer.DisambiguateSiblings`, called from `tree.ComputePaths`): when two or more siblings produce the same slug, the one with the numerically lowest page ID keeps the plain slug; every other gets `-<last-6-digits-of-page-id>` appended. Deterministic and stable across renames.

**Reverse slugification** (`lexer.ReverseSlugify`, used on push-side creates and renames): strip `.md`, strip any trailing `-DDDDDD` collision suffix, hyphens/underscores → spaces, title case.

### Title Stability Rule

On a push-direction rename, the Confluence page title is updated **only if** `Slugify(currentTitle) != filenameSlug` (`lexer.TitleSlugsMatch`). If the new filename slugifies to the same value as the current Confluence title, the title is preserved verbatim — preventing capitalisation and punctuation drift on no-op renames.

Developers who need specific capitalisation set it in Confluence and let pull propagate it; they should not try to encode capitalisation in filenames.

## Lexer

Pure functions — no I/O. The orchestrator injects `PageResolver` and `AttachmentResolver` for cross-page links and attachment references. The full construct mapping lives alongside the implementations in `lexer/cf_to_md.go` and `lexer/md_to_cf.go`.

### Front-matter (`lexer/frontmatter.go`)

`ExtractFrontMatter` / `ApplyFrontMatter` / `FrontMatter` struct. Strict parser (typed `PageID`, `Version`, plus an `Extra` slice for forward-compatibility).

### Normalisation (`lexer/normalise.go`)

`Normalise(md string) string` returns Markdown in canonical form. Both lexer outputs pass through it before being returned. UTF-8/LF/single trailing newline; ATX headings; `*`/`**`/`~~` emphasis; `-` bullets; `1.` ordered items with 2-space indent; triple-backtick code with lowercased language tag; inline links and images; GFM pipe tables (alignment colons preserved); `> ` blockquotes; `---` thematic break. Both hard (`\\\n`) and soft line breaks are preserved as `\\\n` — Confluence relies on significant line breaks for layout. A leading front-matter block is preserved at the top in canonical form.

`Normalise` is idempotent: `Normalise(Normalise(x)) == Normalise(x)`. Malformed front-matter falls through to body-only normalisation rather than erroring.

### cf_to_md and md_to_cf

- `cf_to_md` parses storage XML via `encoding/xml`.
- `md_to_cf` uses `goldmark` with the GFM extension; backslash escapes are resolved to literals before XML encoding to prevent escape accumulation on round trips.
- Confluence code macros are emitted as `<ac:structured-macro ac:name="code">…</ac:structured-macro>` with a `language` parameter and a CDATA `plain-text-body`.
- Cross-page links use `<ac:link><ri:page …/>…</ac:link>`; attachment images use `<ac:image><ri:attachment ri:filename="…"/></ac:image>`.
- Tables extract/emit alignment via `style="text-align: …"` on `<th>`/`<td>`.
- Raw HTML blocks not matching the fence format are wrapped in `<p>` and escaped — we never emit `<ac:structured-macro ac:name="html">` (many Confluence Cloud instances disable it).
- `<ac:structured-macro ac:name="toc">` is dropped; unknown `<ac:structured-macro>` is fence-preserved.

### Confluence-Native Fence (`lexer/fence.go`)

Constructs Markdown can't represent (Jira macros, user mentions, panels, layouts, unknown structured macros) are preserved as a single HTML-comment block with base64-encoded storage XML:

```
<!-- gfl:storage:block:v1:b64
<base64 body wrapped at 76 cols>
-->
```

`DecodeBlockFence(EncodeBlockFence(x)) == x` for arbitrary XML (property-tested).

### Round-Trip Idempotency

The primary correctness property:

- `Normalise(cf_to_md(md_to_cf(body))) == Normalise(body)` for every construct in the supported mapping (body-only — front-matter is orchestrator-managed).
- `md_to_cf(cf_to_md(xml))` reaches a fixed point after one round trip.
- Fence-preserved constructs round-trip byte-for-byte in storage XML.

## Pull (`cmd/pull.go`)

Triggered by post-commit, post-merge, post-rewrite, and direct invocation. Hook shims are guarded by `GFL_HOOK_ACTIVE` to prevent recursion (pull creates its own commits).

1. Acquire an exclusive file lock at `<git-dir>/gfl-pull.lock`. If held, exit silently — the holder will do the work. Direct invocation reclaims stale locks.
2. Refuse to operate with a dirty working tree (refuse rather than stash, to keep behaviour predictable).
3. Ensure the local `confluence` branch exists (seed from HEAD on first run via `EnsureBranchFromHead`).
4. Fetch the Confluence tree (structure only — bodies fetched on demand) and compute the expected `PathMap`.
5. Switch to the `confluence` branch.
6. Walk the working tree under `local_root`, parsing front-matter to map `page_id → {path, version}` (`scanManagedFiles`).
7. Compute the plan (`planPull`):
   - Page in tree, not in local: pending write (create).
   - Page in tree, in local at same path, version differs: pending write (update).
   - Page in tree, in local at different path: rename (and a pending write if version also differs).
   - Page in local, not in tree: delete candidate.
8. Confirm delete candidates via direct `GET /content/{id}`:
   - 404 → confirmed delete.
   - 200 → orphaned (page moved out of sync scope; warn, leave local file).
   - Network/5xx → unknown (warn, skip this run).
9. Apply the plan: renames first (with a two-phase staging protocol if any rename's destination is another rename's source); then deletes; then pending writes (fetch body, convert, render with front-matter, write file, download attachments).
10. `chore(sync): confluence @ <ts>` commit on the `confluence` branch via `CommitAllOnHead`. If nothing actually changed, the commit is a no-op and the merge step is skipped.
11. Switch back to the working branch.
12. `git merge confluence`. On conflict, surface guidance ("resolve with your editor and `git merge --continue`") and exit 0 — leaving the merge state for the user.

Two-phase rename protocol (when any rename's destination equals another rename's source): move all sources into `<local_root>/.gfl-staging/<i>.md`, then move each staged file to its final path. The staging directory is created and removed within the same sync and never appears in a committed tree.

## Push (`cmd/push.go`)

Triggered by pre-push and direct invocation.

1. Set `GFL_HOOK_ACTIVE=1` immediately so the post-commit / post-merge / post-rewrite hooks self-suppress when push commits its sync chore on the working branch (otherwise they'd recursively invoke `gfl pull`).
2. Verify the `confluence` branch exists (error otherwise — direct user to run pull first).
3. `gitutil.DiffBranches(confluenceBranch, "HEAD", "*.md")` with rename detection. If empty, "no changes to push" and exit.
4. Fetch the Confluence tree once up front so step 7's canonicalisation can run.
5. Sort the diffs: `index.md` files first (parents before children), then non-index files, then renames, then deletes.
6. For each diff, dispatch on action:
   - **Added**: read body from `HEAD`. If front-matter already names a `page_id` that genuinely exists, treat as adopt-then-update; otherwise `POST /content`. Auto-create intermediate parent pages via `ensurePushParents`, writing intermediate `index.md` files to the working tree so they land in the user's next commit (and so subsequent diffs in this run can read their front-matter).
   - **Modified**: read `page_id` from the `confluence` branch's copy of the file (the canonical bridge). `GET /content/{id}` for current version, then `PUT` with new body. On 409, refetch and retry once.
   - **Deleted**: read `page_id` from the `confluence` branch's old-path copy. `DELETE /content/{id}`; 404 treated as success.
   - **Renamed**: read `page_id` from the `confluence` branch's old path. Apply Title Stability Rule for the new title. Update parent if the directory changed. `PUT` with new title, body, and parent.
7. **Canonicalise** each successful op's body (`canonicalisePushOps`): re-render the storage XML we just sent through `CfToMd` with the same resolvers a future pull would use. Without this step, lossy steps in `CfToMd` (e.g. HTML whitespace collapse) would surface as phantom Confluence-side changes on the next pull and conflict with concurrent main-side edits on the same line.
8. **Advance the `confluence` branch on the working branch** (`advanceConfluenceBranch`):
   - Stash if the working tree is dirty (typically clean during a pre-push hook, but direct invocation may not be).
   - Apply each `pushOp` to the *current* (working) branch's working tree: `writeManagedFile` for adds/updates, `gitutil.Move` + `writeManagedFile` for renames, `gitutil.Remove` for deletes. Each managed file gets canonical front-matter (page_id, version) and a normalised body.
   - Commit `chore(sync): confluence-push @ <ts>` on the working branch via `CommitAllOnHead`.
   - Fast-forward `confluence` to that commit using `gitutil.SetBranchRef` (`git branch -f`). No checkout, no merge — `confluence` and the working branch tip are byte-equal afterwards.
   - Pop stash if stashed.

Failures don't queue. Whatever didn't succeed will simply re-appear in the next push's diff.

## Hooks

`gfl init` writes shims to `.gfl/hooks/` and installs them into `.git/hooks/` in the same step. `gfl install` performs just the copy — used when cloning an existing gfl-managed repo.

```sh
# pre-push
#!/bin/sh
set -e
gfl push

# post-commit / post-merge / post-rewrite (same shape)
#!/bin/sh
set -e
if [ -n "$GFL_HOOK_ACTIVE" ]; then
  exit 0
fi
export GFL_HOOK_ACTIVE=1
gfl pull
```

- `pre-push` has no shim-level guard. Push self-suppresses recursive hook firing by setting `GFL_HOOK_ACTIVE=1` before any git operations — so when push commits its sync chore on the working branch, the post-commit hook exits early.
- `post-commit` runs pull after every commit so Confluence-side edits are caught before the next push.
- `post-rewrite` re-establishes sync after `rebase` / `commit --amend`.
- The pull file lock prevents concurrent pulls; the env-var guard prevents pull's own commit from re-firing pull.

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
| `gfl init --page-id <id> [--local-root <path>]` | Populate local repo from an existing Confluence tree. Writes config, files (with front-matter), and hook shims; installs hooks. Does not commit. |
| `gfl install` | Copy hook shims from `.gfl/hooks/` into `.git/hooks/`. Idempotent. |
| `gfl push` | Diff against the `confluence` branch and write changes to Confluence; commit a sync chore on the working branch and fast-forward `confluence` to it. |
| `gfl pull` | Update the `confluence` branch from Confluence and merge it into the current working branch. |
| `gfl status` | List files differing between the working branch and `confluence`. |
| `gfl version` | Print version, commit, build date. |

## Implementation Invariants

1. **Round-trip idempotency.** `Normalise(cf_to_md(md_to_cf(x))) == Normalise(x)` for supported constructs (body-only); unsupported constructs round-trip byte-for-byte via the fence; front-matter round-trips through `ExtractFrontMatter` / `ApplyFrontMatter`.
2. **Page ID is the stable identity, carried in front-matter.** Rename detection, history preservation, and identity all key on `confluence_page_id`. Paths and titles are derived, mutable properties.
3. **The `confluence` branch is the only authoritative cache.** No separate index file; no separate pending file. The branch's tip *is* the last-known Confluence-mirror state.
4. **Sync output lands on the working branch.** Pull commits `chore(sync): confluence @ <ts>` on the `confluence` branch and merges it into the working branch. Push commits `chore(sync): confluence-push @ <ts>` directly on the working branch and fast-forwards `confluence` to it. After a successful push, `confluence` and the working branch tip are byte-equal. Human commits must never use either prefix.
5. **Push canonicalises before recording.** Successful ops have their bodies round-tripped through `CfToMd` before being committed, so push-side and pull-side commits are byte-identical for the same Confluence content.
6. **Renames use `git mv`.** So `git log --follow` traces history. Local-side rename collisions use the two-phase staging protocol.
7. **Attachments are co-committed.** A sync commit that modifies a `.md` includes all its referenced attachments under `_attachments/<page-path>/`.
8. **Push never blocks permanently.** Any Confluence write failure surfaces a warning; the diff is recomputed on the next push.
9. **Credentials never appear in logs, flags, or commits.** Env vars only.
10. **Lexers are pure.** No network/filesystem/git access in `lexer/`. Resolvers are injected.
11. **Title Stability Rule.** A push-side rename updates the Confluence title only if `Slugify(currentTitle) != filenameSlug`.
