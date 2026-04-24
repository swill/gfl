# CLAUDE.md — confluencer

## Project Overview

`confluencer` is a standalone Go CLI tool that implements deterministic, bidirectional synchronisation between Markdown files tracked in a Git repository and pages in an Atlassian Confluence instance. It operates entirely through Git hooks and the Confluence REST API, with no dependency on CI infrastructure, external services, LLMs, or Pandoc.

The tool understands the hierarchical structure of a Confluence space and mirrors it as a directory tree of Markdown files. The mapping between Confluence pages and local files is derived automatically from this tree and maintained in a tracked index file. There is no manual per-file configuration.

`confluencer` is maintained as its own standalone repository. Compiled binaries are distributed as GitHub release artifacts. Developers install `confluencer` onto their PATH independently (e.g. via `go install`, downloading a release binary, or a package manager). Go source is never present in the consuming repository.

---

## Core Design Principles

- **Tree-aware sync**: The tool understands the full Confluence page hierarchy rooted at a configured anchor page and mirrors it bidirectionally as a directory tree.
- **Page ID is the stable identity**: Confluence page IDs never change when a page is renamed or moved. All mapping, rename detection, and history preservation logic treats the page ID as the canonical identifier on the Confluence side, and the tracked index as the bridge to local file paths.
- **Automatic mapping**: There is no manual file-to-page mapping. The correspondence between `.md` files and Confluence pages is derived from the tree structure, a deterministic slugification rule, and the persistent index.
- **Deterministic output**: Both conversion directions (Markdown → Confluence storage XML, Confluence storage XML → Markdown) are implemented as purpose-built lexers in Go. Given the same input, the output is always identical. This is the primary mechanism preventing formatting drift loops.
- **Git is not the enforced source of truth**: Either side (Git or Confluence) may receive edits, renames, additions, or deletions. The tooling reconciles both directions on every pull and push.
- **Non-blocking on failure**: A Confluence write failure must never permanently block a developer from pushing to Git.
- **Reduce developer-visible failure modes**: Unsupported constructs are preserved, not rejected. Ambiguous slug collisions are disambiguated deterministically, not flagged as errors. The developer is prompted to intervene only for genuinely ambiguous cases (content conflicts, orphaned pages).
- **No external runtime dependencies**: The binary is self-contained. Consuming repositories require only `confluencer` on the developer's PATH, a `.env` file with Confluence credentials, and a POSIX shell for hook shims.
- **No Go toolchain required in consuming repositories**: Developers install `confluencer` independently (pre-compiled binaries, `go install`, or package manager). The consuming repository contains only configuration and hook shims.

---

## Repository Structure (confluencer source repo)

```
confluencer/
  main.go
  cmd/
    root.go           # root cobra command
    helpers.go        # shared constants and helper functions (repoRoot, file paths)
    init.go           # populate a git repo from an existing Confluence tree
    push.go           # push orchestration (pre-push hook, fire-and-forget to Confluence)
    pull.go           # pull orchestration (post-commit + post-merge + post-rewrite hooks, branch-based rebase)
    install.go        # copies hook shims from .confluencer/hooks/ into .git/hooks/
    status.go         # reports pending Confluence writes and orphan warnings
  lexer/
    normalise.go      # Markdown normalisation rules (see Markdown Normalisation)
    cf_to_md.go       # Confluence storage XML → Markdown (pure)
    md_to_cf.go       # Markdown AST → Confluence storage XML (pure)
    fence.go          # Confluence-native fence encode/decode for unsupported constructs
    cf_to_md_test.go
    md_to_cf_test.go
    roundtrip_test.go # property-style round-trip tests for every supported construct
    slugify.go        # deterministic page title → filename and filename → title conversion
    slugify_test.go
  api/
    client.go         # Confluence REST client, auth, error handling, version/ETag handling
    content.go        # GET/PUT/POST/DELETE page content, page tree walking
    attachments.go    # GET/POST attachments
  gitutil/
    commits.go        # parse pre-push stdin, walk commit ranges, detect sync commits
    diff.go           # identify changed, added, renamed, and deleted .md files per commit
    mv.go             # git mv wrapper for rename and promotion operations
    baseline.go       # locate the last sync commit and extract baseline content via git show
  tree/
    confluence.go     # fetch and represent Confluence page hierarchy
    local.go          # represent local directory/file hierarchy from working tree
    diff.go           # compare Confluence tree vs local tree, produce typed change set
    plan.go           # topologically order change operations (two-phase renames)
    promote.go        # handle .md → dir/index.md promotion in both directions
  index/
    index.go          # read/write .confluencer-index.json
    pending.go        # read/write/append .confluencer-pending (NDJSON)
  config/
    config.go         # read/write .confluencer.json
    env.go            # load credentials from .env
  go.mod
  go.sum
```

The lexer package owns both the conversion functions and `slugify` because slugification is a pure text transform with no Confluence or tree dependencies, and grouping it with the lexer keeps all pure transforms in one place.

---

## Consuming Repository Structure

```
<repo-root>/
  .confluencer/
    hooks/
      pre-push                    # tracked — shell shim, created by confluencer init
      post-commit                 # tracked — shell shim, created by confluencer init
      post-merge                  # tracked — shell shim, created by confluencer init
      post-rewrite                # tracked — shell shim, created by confluencer init

  docs/                           # local root (configured in .confluencer.json)
    index.md                      # content of the Confluence root anchor page
    _attachments/                 # tracked — page-tree-mirrored asset tree
      architecture/
        database-design/
          diagram.png
    architecture/
      index.md
      database-design.md
    ...                           # mirrored Confluence hierarchy (see Tree Structure)

  .confluencer.json               # tracked — root anchor config, cached space key
  .confluencer-index.json         # tracked — stable ID-to-path index (see Index File)
  .env                            # gitignored — Confluence credentials
  .confluencer-pending            # gitignored — NDJSON queue of failed writes
  .gitignore                      # must include: .env, .confluencer-pending
```

---

## Configuration

### `.confluencer.json` (tracked, in repo root)

Declares the root anchor for the sync scope. All page-to-file mappings are derived from the Confluence page tree rooted at `confluence_root_page_id` and recorded in `.confluencer-index.json`.

```json
{
  "confluence_root_page_id": "123456789",
  "confluence_space_key": "DOCS",
  "local_root": "docs/",
  "attachments_dir": "docs/_attachments"
}
```

`confluence_space_key` is derived from the root page's metadata on `confluencer init` and cached here so that subsequent `POST /rest/api/content` calls (new-page creation) do not need to re-fetch it every run. `confluence_base_url` is not stored here — it comes from the `.env` file.

### `.env` (gitignored, in repo root)

```
CONFLUENCE_BASE_URL=https://yourorg.atlassian.net/wiki
CONFLUENCE_USER=your.email@yourorg.com
CONFLUENCE_API_TOKEN=your_api_token
```

Credentials are read exclusively from environment variables. The tool never accepts credentials as CLI flags to prevent exposure in process listings or shell history.

If any of the three required variables are absent, the tool prints an actionable error referencing `.env` setup and exits 1. Silent misconfiguration is not acceptable.

---

## Index File

### `.confluencer-index.json` (tracked, in repo root)

The index is the stable bridge between Confluence's identity system (page IDs, which never change) and Git's identity system (file paths, which may change via renames). It is written by `confluencer init` and updated by every `confluencer pull` and `confluencer push` that results in a structural change (new page, rename, promotion, deletion).

The index is committed as part of every sync commit so that any developer who clones or pulls the repository has the current mapping without needing to run a full sync.

```json
{
  "pages": [
    {
      "confluence_page_id": "123456789",
      "confluence_title": "Root Page",
      "local_path": "docs/index.md",
      "parent_page_id": null,
      "version": 5
    },
    {
      "confluence_page_id": "234567890",
      "confluence_title": "Architecture",
      "local_path": "docs/architecture/index.md",
      "parent_page_id": "123456789",
      "version": 12
    },
    {
      "confluence_page_id": "345678901",
      "confluence_title": "Database Design",
      "local_path": "docs/architecture/database-design.md",
      "parent_page_id": "234567890",
      "version": 3
    }
  ]
}
```

`parent_page_id` is stored so that push-direction tree reconstruction (e.g. creating a new child page) does not need to re-derive parentage from the filesystem.

`version` is the Confluence page version number at the time of the last sync. During pull, the tool compares each page's current Confluence version against the stored version. If they differ, the page body is fetched and compared to detect content changes. This avoids fetching bodies for pages that haven't changed. The version is updated in the index after every successful pull.

The index must be updated atomically with any file operation it describes. A sync commit that renames a file must also update the index entry for that file in the same commit. The index and the filesystem must never be out of sync at any committed state.

The branch-based pull uses the most recent `chore(sync): confluence` commit as the base point for the temporary sync branch. This commit is located via `gitutil/baseline.go`.

---

## Pending Queue File

### `.confluencer-pending` (gitignored, in repo root)

Newline-delimited JSON (NDJSON), one record per failed operation. Written by `confluencer push` when a Confluence write fails; drained on the next push or via `confluencer push --retry`.

Record types:

```json
{"type":"content","page_id":"345678901","local_path":"docs/architecture/database-design.md","attempt":1,"last_error":"409 version conflict","queued_at":"2026-04-13T09:20:11Z"}
{"type":"rename","page_id":"345678901","old_path":"docs/architecture/database-design.md","new_path":"docs/architecture/db-design.md","new_title":"Db Design","attempt":1,"last_error":"network timeout","queued_at":"..."}
{"type":"create","parent_page_id":"234567890","local_path":"docs/architecture/new-page.md","title":"New Page","attempt":1,"last_error":"...","queued_at":"..."}
{"type":"delete","page_id":"345678901","local_path":"docs/architecture/database-design.md","attempt":1,"last_error":"...","queued_at":"..."}
{"type":"attachment","page_id":"345678901","local_path":"docs/_attachments/architecture/database-design/diagram.png","attempt":1,"last_error":"...","queued_at":"..."}
```

On retry, entries are processed in the order they were queued. A successful retry removes the entry. A repeated failure increments `attempt` and updates `last_error` and `queued_at`.

---

## Tree Structure and File Conventions

### Hierarchy Mirroring

The Confluence page hierarchy rooted at `confluence_root_page_id` is mirrored as a directory tree under `local_root`. The mapping rules are:

- A Confluence page with **no children** is represented as a single `.md` file named after the page title (slugified).
- A Confluence page with **one or more children** is represented as a directory named after the page title (slugified), containing:
  - `index.md` — the content of that page (even if the Confluence page has no content body, `index.md` is created as an empty file).
  - One `.md` file or subdirectory per child page, following the same rules recursively.
- The root anchor page is always represented as `index.md` directly inside `local_root`, with its children as siblings.

Empty `index.md` files are a fully supported state in both directions. A page with a body → empty or vice versa is a content change and handled identically to any other content change.

### Attachments

Attachments are stored under a central `_attachments/` tree that mirrors the page hierarchy. For every page at logical path `<page-path>`, its attachments live at:

```
_attachments/<page-path-without-trailing-index.md>/<attachment-filename>
```

Examples:

| Page | Page path | Attachment filename | Local attachment path |
|---|---|---|---|
| Root | `docs/index.md` | `logo.png` | `docs/_attachments/logo.png` |
| Architecture | `docs/architecture/index.md` | `overview.svg` | `docs/_attachments/architecture/overview.svg` |
| Database Design (flat) | `docs/architecture/database-design.md` | `schema.png` | `docs/_attachments/architecture/database-design/schema.png` |
| API Reference (flat sibling of Architecture) | `docs/api-reference.md` | `flow.png` | `docs/_attachments/api-reference/flow.png` |

Key properties of this layout:

- **No collisions across pages**: two pages can each reference an attachment named `image.png` without interference.
- **Confluence filename is preserved**: the leaf filename matches the Confluence attachment filename exactly, so no attachment rename is ever performed on the Confluence side. Upload and download both key on `(page_id, filename)`.
- **Page renames move attachments**: renaming a page also `git mv`s its attachment subdirectory. A renamed or promoted page's attachments travel with it in the same sync commit.
- **Flat and promoted pages use the same rule**: a page at `foo/bar.md` and a page at `foo/bar/index.md` both have their attachments at `_attachments/foo/bar/`. Promotion does not move the attachment directory.

Markdown image references use paths relative to the `.md` file, e.g. from `docs/architecture/database-design.md`:

```markdown
![schema](../_attachments/architecture/database-design/schema.png)
```

`md_to_cf` recognises any path under `_attachments/` (resolved relative to the file) as a Confluence attachment reference and emits `<ac:image><ri:attachment filename="..."/></ac:image>` with just the leaf filename.

### Example

```
Confluence:                            Git (docs/):
  Root Page (has content)                index.md
    ├── Architecture (has content)       _attachments/
    │     ├── Database Design              architecture/
    │     └── API Design                     database-design/
    ├── Onboarding (no content body)             schema.png
    │     ├── For Developers             architecture/
    │     └── For Managers                 index.md
    └── API Reference (leaf)               database-design.md
                                           api-design.md
                                         onboarding/
                                           index.md         ← empty file
                                           for-developers.md
                                           for-managers.md
                                         api-reference.md
```

### Slugification Rules

Page titles are converted to filenames and directory names using the following deterministic rules, applied in order:

1. Convert to lowercase.
2. Replace all whitespace sequences with a single hyphen.
3. Replace all underscores with hyphens (so titles containing underscores produce hyphen-separated slugs, keeping the filename convention uniform).
4. Remove all characters that are not alphanumeric or hyphens.
5. Collapse consecutive hyphens to a single hyphen.
6. Strip leading and trailing hyphens.

If the result is empty (e.g. a page titled entirely in non-Latin characters that all get stripped), fall back to `page-<confluence_page_id>` as the slug. This is exceedingly rare in practice.

#### Sibling collision disambiguation

If two or more sibling pages produce the same slug (e.g. "Database Design" and "Database-Design" both → `database-design`), disambiguate by appending `-<page-id-suffix>` to all but the canonical sibling. The canonical sibling is the one with the numerically lowest Confluence page ID; all others receive a `-<last-6-digits-of-page-id>` suffix.

Example: pages 100000 "Database Design" and 100042 "Database-Design" both slugify to `database-design`. Result: `database-design.md` for page 100000, `database-design-100042.md` for page 100042.

This rule is deterministic (page IDs never change), collision-free (page IDs are unique), and stable across sibling renames (renaming one page does not change the slug of the other, because the canonical winner is selected by page ID, not by slug order).

Slugification must be implemented as a pure function with comprehensive unit tests, as it is foundational to the correctness of the entire mapping. The sibling-collision rule is tested separately from the pure slug transform because it takes a sibling set as additional input.

### Reverse Slugification (filename → page title)

Used only when a file is renamed in Git and the new title must be written to Confluence. Rules:

1. Strip the `.md` extension.
2. Strip any trailing `-<6-digit-collision-suffix>` if present.
3. Replace all hyphens and underscores with spaces.
4. Apply title case (capitalise the first letter of each word).

This is a best-effort conversion. The title is only written to Confluence when necessary — see Title Stability Rule below.

#### Title Stability Rule

On a push-direction rename, the Confluence page title is updated **only if** the slugification of the current Confluence title (from the index) differs from the new filename's base slug. If `slugify(index.confluence_title) == filename_slug`, the Confluence title is preserved verbatim. This prevents capitalisation and punctuation drift:

- Pull-side creates page "API Design" → `api-design.md`. Index title = "API Design".
- Developer renames file to `rest-api-design.md` locally. New slug = `rest-api-design`.
- `slugify("API Design") = "api-design"` ≠ `"rest-api-design"` → title change is needed. New title via reverse-slug = `"Rest Api Design"`.
- Developer renames file back to `api-design.md` (no net change). New slug = `api-design` = `slugify(index.confluence_title)` → **no title update**. Capitalisation preserved.

Developers who require a specific capitalisation or punctuation set the title in Confluence and let pull propagate it; they should not attempt to encode capitalisation in filenames.

---

## Markdown Normalisation

Both lexers emit and consume Markdown in a single canonical form. The normalisation rules fix every point where Markdown tolerates ambiguity, so that round-trip comparisons reduce to byte equality.

1. **Encoding**: UTF-8, no BOM.
2. **Line endings**: LF (`\n`) only. Any CRLF or CR input is converted on read; output is always LF.
3. **Trailing whitespace**: No trailing spaces, tabs, or other whitespace at the end of any line.
4. **End of file**: Exactly one trailing newline (`\n`). Files are never empty-of-newline and never have multiple trailing newlines.
5. **Block separation**: Exactly one blank line between top-level blocks (heading, paragraph, list, blockquote, code block, table, thematic break, fence-preserved block). Two consecutive blank lines are never emitted.
6. **Headings**: ATX style (`#` through `######`) only. A space follows the leading `#`s. No closing `#`s. Setext headings (underlined with `=` or `-`) are not emitted; if seen on input, they are treated equivalent to ATX and normalised on next write.
7. **Emphasis markers**: `*text*` for emphasis, `**text**` for strong. (The `cf_to_md` mapping table previously showed `_text_`; this is superseded by the normalisation rule. `_` is reserved for underscores in identifiers, which some Markdown parsers treat as emphasis — `*` is chosen to avoid that ambiguity.)
8. **List markers**: `-` for unordered lists; `1.` for every item in ordered lists (not incrementing — all items start with `1.`). Nested lists indent by exactly two spaces per level.
9. **Fenced code blocks**: Triple backticks (` ``` `) only, never tilde. Language tag immediately follows the opening fence, lowercased (e.g. ` ```go `). No trailing language tag on the closing fence.
10. **Inline code**: Single backticks. For code containing backticks, use the minimum doubled-backtick delimiter as per CommonMark.
11. **Links**: Inline `[text](url)` form only. Reference-style links are not emitted; on input they are resolved to inline form before normalisation.
12. **Images**: Inline `![alt](path)` form only. Alt text is required for attachment images — it defaults to the leaf filename (without extension) if Confluence provides no alt.
13. **Tables**: GFM pipe tables. Header separator uses `---` per column. Alignment colons are preserved when present in source (e.g. `:---:` for centre, `---:` for right, `:---` for left); `cf_to_md` extracts alignment from Confluence `style="text-align: ..."` attributes. Cells are not padded to column width.
14. **Blockquotes**: `>` followed by one space, then content. Nested blockquotes are `> >`.
15. **Thematic break**: `---` on its own line.
16. **Line breaks**: Both hard breaks (`\\\n`) and soft breaks (bare newlines within a paragraph) are preserved as backslash-newline (`\\\n`). Confluence content relies on line breaks for layout, so collapsing soft breaks to spaces would destroy the author's intent. No hard wrap is performed; line length is unlimited.

Normalisation is implemented in `lexer/normalise.go` as a function `Normalise(md string) string`. Both lexer outputs pass through this function before being returned. Round-trip tests assert byte equality after normalisation on both sides.

---

## Rename, Move, and Promotion Handling

Renames, moves, and promotions are first-class operations in both directions. The Confluence page ID is the stable identity that enables detection of all three without losing Git file history.

### Typed change set (tree diff output)

`tree/diff.go` produces a change set of the following distinct types. Each type corresponds to a specific operation plan; merging them into a generic "something changed" bucket hides information developers need to understand sync commits.

| Type | Condition | Operation |
|---|---|---|
| `content_changed` | Page ID in index, local file exists, `cf_to_md` output differs from current file content | Write new Markdown to existing path |
| `renamed_in_place` | Page ID in index, Confluence title changed, parent page ID unchanged | `git mv` within same directory |
| `moved` | Page ID in index, parent page ID changed, title may or may not have changed | `git mv` across directories |
| `ancestor_renamed` | Page ID in index, page unchanged but an ancestor's title changed (directory rename) | Page is swept up by the ancestor's `git mv`; no standalone operation |
| `promoted` | Page ID in index as `<slug>.md`, Confluence now has ≥1 child page | `git mv <slug>.md <slug>/index.md`, then create children |
| `demoted` | Page ID in index as `<slug>/index.md`, Confluence now has 0 child pages | `git mv <slug>/index.md <slug>.md`, then remove the now-empty directory |
| `created` | Page ID not in index, page exists in Confluence tree | Write new file at computed path, add index entry |
| `deleted` | Page ID in index, 404 from direct `GET /content/{id}` | `git rm` file (and its attachment subdirectory), remove index entry |
| `orphaned` | Page ID in index, page exists on Confluence but ancestry is outside sync scope | Warn, leave local file untouched, flag for reconciliation |
| `missing_unknown` | Page ID in index, page fetch returned network or 5xx error | Warn, leave local file untouched, skip this run |

### Two-phase rename application

When multiple renames or moves in a single sync might collide (e.g. page A's new path is page B's old path because B was renamed in the same pull), operations are applied in two phases:

1. **Stash phase**: every file to be renamed is `git mv`d to a staging path `<local_root>/.confluencer-staging/<page-id>.md`.
2. **Place phase**: every staged file is `git mv`d to its final computed path.

This makes the operation safe regardless of order and produces clean `git log --follow` history. The staging directory is created and removed within the same sync; it never appears in a committed tree.

### Rename in Confluence → Git

Detected during `confluencer pull` by comparing expected local paths (from current Confluence titles via slugification) against recorded `local_path` in the index.

1. Emit one of `renamed_in_place`, `moved`, `ancestor_renamed`, `promoted`, or `demoted` in the change set per the table above.
2. Plan two-phase rename operations if collisions exist.
3. Execute the `git mv` operations.
4. Write updated content if the body also changed.
5. Update index entries (`local_path`, `confluence_title`, `parent_page_id`).
6. Move attachment subdirectories alongside the page.
7. Stage everything — moved files, content changes, attachment moves, index update — in the same sync commit.

### Rename in Git → Confluence

Detected during `confluencer push` from the commit diff's rename records (Git's rename similarity detection plus explicit `R` records). For each rename:

1. Look up the old path in the index to retrieve the page ID.
2. Apply the Title Stability Rule to decide whether a title change is needed.
3. If a title change is needed, `PUT /rest/api/content/{id}` with the updated title (and current body; fetch first for the current version number).
4. If the rename crosses directories (move), the new parent page ID is derived from the new directory's `index.md`; a separate `PUT` may be needed to update `ancestors` (Confluence supports move via the ancestors field).
5. Update the index entry with the new `local_path`, new title, and new parent page ID.

Directory renames in Git propagate to every descendant's index entry. The `index.md` within a renamed directory carries the page ID of the parent, which drives the title update for the parent page itself.

### Promotion: Flat File to Directory

When a flat page gains its first child (either direction), the flat `.md` is promoted to `<slug>/index.md`:

1. Create the directory `<slug>/`.
2. `git mv <slug>.md <slug>/index.md` — preserving history.
3. Write any new child files.
4. Update every affected index entry.
5. Attachment subdirectory `_attachments/<slug>/` is unchanged (the page-tree-mirroring rule gives flat and promoted pages the same attachment path).
6. Stage all changes in the same sync commit.

**Push direction**: developer creates `architecture/new-page.md` while `architecture.md` exists → promote, then create the Confluence child. Any content in `architecture.md` is preserved as `architecture/index.md` — never discarded.

**Pull direction**: Confluence page "Architecture" gains a first child → detect, promote, write the new child file.

### Demotion: Directory to Flat File

The inverse: a page that previously had children loses them all. `<slug>/index.md` → `<slug>.md`, and the now-empty `<slug>/` directory is removed. Demotion is rare but must be handled to avoid drift.

---

## Deletion Handling

Deletions are supported in both directions. Git history preserves the content, so accidental deletes are recoverable via revert — but the developer is the one choosing to delete, and the sync tool propagates that decision rather than silently declining.

### Pull direction: delete from Confluence → delete locally

During `confluencer pull`, if a page ID in the index is absent from the fetched tree, its status is disambiguated by issuing a direct `GET /content/{id}`:

- **404**: page was deleted in Confluence. Emit `deleted` in the change set.
  1. `git rm <local_path>`.
  2. `git rm -r <_attachments-subdir>` if present.
  3. If the deleted page had a parent who now has no other children, emit `demoted` for the parent in the same sync.
  4. Remove the index entry.
  5. Stage everything in the sync commit.
- **200 with ancestry outside the sync root**: page was moved out of scope. Emit `orphaned`. Warn but leave local file untouched. Surfaced via `confluencer status`.
- **network or 5xx error**: emit `missing_unknown`. Warn and leave untouched; retry on next pull.

### Push direction: delete locally → delete from Confluence

During `confluencer push`, commits in the range may contain `D` (delete) records for tracked `.md` files. For each:

1. Look up the page ID in the index.
2. `DELETE /rest/api/content/{id}`. If the page is already 404, treat as success.
3. Remove the corresponding index entry.

Confluence's `DELETE /content/{id}` cascades to descendants, so deleting a parent page also deletes its children server-side. However, the current implementation does not optimise for this — each deleted `.md` triggers an individual DELETE call. Redundant 404s from child pages that were already cascade-deleted are handled gracefully.

### Safety

No confirmation prompt is issued: the developer's explicit `git rm` (push) or their explicit action of deleting the page in Confluence (pull) is the confirmation. `confluencer status` lists all deletions that are about to happen on the next push so that a `git status` + `confluencer status` pre-push check is possible.

---

## Concurrent Writer / Version Conflict Resolution

When Confluence content changes between syncs, conflicts are resolved through git's native merge machinery via the branch-based pull approach. Push is a fire-and-forget operation that does not attempt local conflict resolution.

### Push-side handling

`confluencer push` fetches the current Confluence page version before writing. On a 409 (version conflict from a concurrent edit), it re-fetches the current version and retries the PUT once. If the retry also fails, the operation is queued to `.confluencer-pending`. Push does not perform three-way merges or modify local files.

### Pull-side handling (branch-based rebase)

`confluencer pull` writes Confluence changes on a temporary `confluencer-sync` branch and rebases the developer's branch onto it. This delegates conflict resolution entirely to git's merge machinery:

- **Clean rebase**: Confluence changes and local changes are in different files or non-overlapping regions. The rebase succeeds silently.
- **Conflict**: Confluence changes and local changes overlap. Git produces standard conflict markers in the working tree. The developer resolves them using their normal tools (editor, `git mergetool`, etc.) and completes the rebase.
- **Rebase failure fallback**: If rebase fails entirely (e.g. complex history), pull falls back to `git merge` with the sync branch. Conflicts are surfaced as standard merge conflicts.

This approach replaces the previous custom three-way merge algorithm with git's battle-tested merge infrastructure, producing familiar conflict markers and workflows.

---

## Git Hook Behaviour

Pull-direction sync is installed on `post-commit`, `post-merge`, and `post-rewrite`. Push-direction sync is on `pre-push` as a fire-and-forget write to Confluence (no local commits or index updates). All pull-direction hooks are guarded by the `CONFLUENCER_HOOK_ACTIVE` environment variable to prevent recursion — pull performs git operations (branch, checkout, rebase, commit) that would otherwise re-trigger the hooks.

### `pre-push` hook → `confluencer push`

Fires before Git transmits commits to the remote. The hook shim runs `confluencer push`, which sends local changes to Confluence without creating any local commits. This means the hook never modifies the commit range being pushed, avoiding the fundamental problem of pre-push commits being excluded from the push.

**Commit range detection:**

`confluencer push` supports two invocation modes:

- **Hook mode** (stdin is a pipe from Git): parses pre-push stdin to identify the commit range (format: `<local-ref> <local-sha1> <remote-ref> <remote-sha1>`). Skip delete-branch refs.
- **Direct mode** (stdin is a terminal): finds the most recent `chore(sync): confluence` commit on the current branch and diffs from there to HEAD. If no sync commit exists, diffs from the beginning of history. This allows `confluencer push` to work without a Git remote configured.

**Sequence:**

1. Drain `.confluencer-pending` first — retry queued entries, remove successes, update failures.
2. If `--retry` flag is set, stop here (drain-only mode).
3. Detect invocation mode (terminal vs pipe) and compute commit range(s) as described above.
4. For each range, compute the diff to identify `.md` files that were added, modified, renamed, or deleted.
5. Filter out any `.md` file whose most recent modifying commit in the range carries the sync marker (`chore(sync): confluence`) in its message. These files' Confluence representations were already written when that content first entered the repo.
6. Process each diff by its action type:
   - **Deleted** (`D`): look up page ID in index, `DELETE /rest/api/content/{id}`. Already-deleted pages (404) are treated as success.
   - **Renamed** (`R`): apply the Title Stability Rule to decide whether a title change is needed. Fetch current page for version number. Convert local content via `md_to_cf`. `PUT` with new title, body, and parent ID (derived from directory structure).
   - **Added** (`A`): if the file is already tracked in the index (e.g. created by `confluencer init`), treat as a content update. Otherwise, convert local content via `md_to_cf`, derive parent page ID from the directory's `index.md` entry (falling back to root page), `POST /rest/api/content` to create the page.
   - **Modified** (`M`): fetch current page for version number. Convert local content via `md_to_cf`. `PUT /rest/api/content/{id}` with fetched version + 1. On 409, re-fetch the current version and retry once.
7. On unrecoverable failure per item, append to `.confluencer-pending`, warn on stderr, continue.
8. Exit 0 in all cases except catastrophic errors (e.g. cannot read config file). The Git push proceeds.

Push does not save the index or create commits. Version tracking and index updates are pull's responsibility.

**Note:** Promotions and demotions (flat file ↔ directory with `index.md`) are handled implicitly via Git's rename detection — Git sees the `git mv` as a rename, and the push processes it as a rename operation. Attachment uploads during push are not currently implemented; attachment changes propagate via pull-direction sync or manual pending queue entries.

### `post-commit` hook → `confluencer pull`

Fires after every `git commit`. Runs `confluencer pull` to fetch any Confluence-side changes and rebase them into the local branch. This ensures Confluence edits are captured before the next push, without requiring pull to run during the push itself. Guarded by `CONFLUENCER_HOOK_ACTIVE` to prevent recursion (pull creates its own commits during the branch-based rebase).

### `post-merge` hook → `confluencer pull`

Fires after any merge completes (including fast-forward). Responsible for pulling Confluence changes into the local tree. Guarded by `CONFLUENCER_HOOK_ACTIVE`.

### `post-rewrite` hook → `confluencer pull`

Fires after `git rebase` and `git commit --amend`. Runs the same pull logic so that developers with rebase-based workflows don't miss Confluence sync. Guarded by `CONFLUENCER_HOOK_ACTIVE`.

### Shared pull sequence (branch-based rebase)

Invoked by `post-commit`, `post-merge`, `post-rewrite` (and `confluencer pull` directly):

1. Acquire a short-lived file lock (`.confluencer/.pull.lock`) to prevent concurrent hook double-fires. If lock is held, exit 0 silently — the holder will do the work. When invoked directly (not from a hook), a stale lock from a crashed run is removed and retried.
2. Fetch the full Confluence page tree rooted at `confluence_root_page_id` via the REST API (structure only — bodies are not fetched at this stage).
3. Load `.confluencer-index.json`.
4. Compute the structural change set (see Typed change set table) — covers creates, deletes, renames, moves, promotions, and demotions.
5. Detect content changes via version comparison: for each page in the tree that has no structural change, compare the Confluence version number against the version stored in the index. For pages with a version mismatch, fetch the body individually, convert via `cf_to_md`, and compare against the local file. Only pages with actual content differences are added as `content_changed` entries.
6. If the change set is empty, update index version numbers and exit.
7. **Branch-based application:**
   a. Record the current branch name.
   b. Stash any uncommitted changes if present.
   c. Find the base SHA — the last `chore(sync): confluence` commit, or HEAD if none exists.
   d. Create a temporary `confluencer-sync` branch at the base SHA.
   e. Check out the sync branch.
   f. Apply all changes on the sync branch:
      - `deleted`: remove files and index entries.
      - `renamed_in_place` / `moved` / `ancestor_renamed` / `promoted` / `demoted`: execute `git mv`s, move attachments.
      - `content_changed`: write new Markdown content.
      - `created`: write new file at computed path, add index entry.
      - `orphaned` / `missing_unknown`: log warnings, take no action.
   g. Download any new or changed attachments to `_attachments/<page-path>/`.
   h. Update the index (including version numbers for all pages in the tree).
   i. `git add` all changed files plus the updated index.
   j. `git commit -m "chore(sync): confluence"`.
   k. Check out the original branch.
   l. Rebase the original branch onto the sync branch, leveraging git's native merge machinery for conflict resolution.
   m. If rebase fails (conflicts), fall back to `git merge` and emit conflict warnings.
   n. Delete the `confluencer-sync` branch.
   o. Pop the stash if one was created.
8. Release the lock. Exit 0.

This branch-based approach leverages git's merge machinery instead of a custom three-way merge algorithm. Conflicts are surfaced as standard git merge conflicts that developers resolve with their normal tools.

**`post-commit` specifics**: fires after every commit, adding 2-5 seconds of latency for the Confluence API call. Fails silently if Confluence is unreachable — the developer is never blocked from committing.

**`post-merge` specifics**: the sync commit is rebased onto the merge commit, keeping the history linear.

**`post-rewrite` specifics**: after a rebase, the rewrite may have clobbered prior sync commits' messages if the developer squashed or reworded them. The sync commit emitted by the post-rewrite pull re-establishes the marker on the new tip. Potential double-sync across post-commit + post-merge + post-rewrite is prevented by the file lock and the `CONFLUENCER_HOOK_ACTIVE` environment variable guard.

---

## `confluencer init`

Populates a local Git repository from an existing Confluence page tree. Primary onboarding path for teams migrating an existing Confluence space into this workflow.

**Usage:**
```
confluencer init --page-id <root-page-id> [--local-root <path>]
```

`--local-root` defaults to `docs/` if not specified.

**Sequence:**

1. Verify `.confluencer.json` does not already exist (exit with error if it does).
2. Load credentials from `.env` (validates `CONFLUENCE_BASE_URL`, `CONFLUENCE_USER`, `CONFLUENCE_API_TOKEN` are set).
3. Fetch the full page tree rooted at `<root-page-id>` recursively with body content. The space key is extracted from the root page metadata for caching.
4. Compute local paths from the tree via `tree.ComputePaths`, which handles slugification, `index.md` conventions, and sibling collision disambiguation.
5. For each page (breadth-first walk):
   a. Convert storage XML through `cf_to_md` with resolvers for cross-page links and attachment references.
   b. Write the Markdown file, creating directories as needed.
   c. Record page ID, title, parent ID, and local path in the index.
6. Download all attachments to `_attachments/<page-path>/`.
7. Write `.confluencer.json` including the cached space key.
8. Write `.confluencer-index.json`.
9. Append to `.gitignore` if entries for `.env` and `.confluencer-pending` are not already present.
10. Create `.confluencer/hooks/` with hook shims for `pre-push`, `post-commit`, `post-merge`, and `post-rewrite`.
11. Install hooks into `.git/hooks/` (same operation as `confluencer install`).
12. Print a summary.
13. Do not make any Git commits. Leave staging to the developer.

If `.confluencer.json` already exists, exit with an error. If a local `.md` already exists at a path init would write to, the file is overwritten with the Confluence content (no three-way merge is performed during init).

---

## Lexer Specifications

The lexers are **pure**: no network, filesystem, or index access. Attachment handling and page-link resolution happen in the surrounding orchestrator — the lexers emit and accept structured placeholder tokens for those references. This lets round-trip tests run entirely in-memory without mocking I/O.

### `cf_to_md` — Confluence Storage XML → Markdown

**Input:** Confluence storage format string (`body.storage.value`) and a `PageResolver` interface for resolving `<ri:page>` references (takes title and space key, returns local path or `ok=false`). The orchestrator injects a resolver backed by the index or the fetched tree.

**Tokenizer:** `golang.org/x/net/html` — handles malformed HTML gracefully.

**Construct mapping:**

| Confluence element | Markdown output |
|---|---|
| `<h1>` – `<h6>` | ATX headers `#` – `######` |
| `<p>` | Paragraph (blank line separation) |
| `<strong>`, `<b>` | `**text**` |
| `<em>`, `<i>` | `*text*` |
| `<s>`, `<del>` (GFM extension enabled) | `~~text~~` |
| `<ul>` / `<ol>` / `<li>` | `-` / `1.` list items, 2-space indent per nesting level |
| GFM task list `<ul class="task-list">` | `- [ ]` / `- [x]` |
| `<code>` (inline) | `` `text` `` |
| `<pre>` | Fenced code block (no language) |
| `<ac:structured-macro ac:name="code">` | Fenced code block with language from `<ac:parameter ac:name="language">` |
| `<a href="url">` (external) | `[text](url)` |
| `<a href>` resolving to a local file | `[text](<relative-path-to-file>)` |
| `<ac:link><ri:page .../>` | `[Page Title](<relative-path>)` via `PageResolver` (path form, not slug form) |
| `<ac:image><ri:attachment ac:filename="x.png"/></ac:image>` | `![x](<relative-path>/_attachments/<page-path>/x.png)` |
| `<ac:image><ri:url ri:value="url"/></ac:image>` | `![](url)` |
| `<table>` | GFM pipe table (column alignment extracted from `style="text-align: ..."` on `<th>`/`<td>` elements → `:---:`, `---:`, `:---` separators) |
| `<ac:structured-macro ac:name="note\|warning\|tip\|info">` | `> **Note/Warning/Tip/Info:** body text` (blockquote form) |
| `<ac:structured-macro ac:name="toc">` | Omitted entirely |
| `<hr/>` | `---` |
| Any other `<ac:structured-macro>` | **Fence-preserved** (see below) |
| Any unknown HTML element | **Fence-preserved** for block-level; inline text preserved with element dropped for pure-text-bearing spans |

**Output:** UTF-8 Markdown string passed through `Normalise()`.

### `md_to_cf` — Markdown → Confluence Storage XML

**Input:** UTF-8 Markdown content, and an `AttachmentResolver` for mapping attachment paths → leaf filenames + page IDs. The orchestrator injects a resolver backed by the index and filesystem.

**Parser frontend:** `github.com/yuin/goldmark` with extensions `extension.GFM` (tables, strikethrough, task lists, linkify), `extension.Footnote` (if we choose to support footnotes — default on), and a custom parser for the Confluence-native fence (see below).

**Construct mapping:**

| Markdown / AST node | Confluence storage XML output |
|---|---|
| ATX Heading (level 1–6) | `<h1>` – `<h6>` |
| Paragraph | `<p>` |
| Strong | `<strong>` |
| Emphasis | `<em>` |
| Strikethrough | `<s>` |
| Task list item | `<ac:task-list><ac:task>...</ac:task></ac:task-list>` (Confluence task macro) |
| Bullet/ordered list/item | `<ul>` / `<ol>` / `<li>` |
| Inline code | `<code>` |
| Fenced code block (lang) | `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">lang</ac:parameter><ac:plain-text-body><![CDATA[...]]></ac:plain-text-body></ac:structured-macro>` |
| Fenced code block (no lang) | Same macro, language parameter omitted |
| Link (external URL) | `<a href="url">text</a>` |
| Link (resolves to local `.md`) | `<ac:link><ri:page ri:content-title="..."/><ac:plain-text-link-body><![CDATA[text]]></ac:plain-text-link-body></ac:link>` |
| Image (`_attachments/...` path) | `<ac:image><ri:attachment ri:filename="..."/></ac:image>` |
| Image (remote URL) | `<ac:image><ri:url ri:value="url"/></ac:image>` |
| Blockquote | `<blockquote>` |
| Table | `<table><tbody>` with `<tr><th>` / `<tr><td>` (alignment colons → `style="text-align: ..."` on cells) |
| Hard break (`\\\n`) / Soft break (bare newline) | `<br/>` (both treated as significant line breaks) |
| Thematic break | `<hr/>` |
| Confluence-native fence block | Verbatim splice of the fenced storage XML |
| Inline fence token | Verbatim splice inline |
| Raw HTML block not inside a fence | Wrapped in `<p>` and escaped as plain text (do **not** emit `<ac:structured-macro ac:name="html">` — many Confluence Cloud instances disable it) |

CommonMark backslash escapes (e.g. `\*`, `\_`, `\\`) are resolved to their literal characters before XML encoding. This prevents escape accumulation on round trips — goldmark's AST preserves the backslash in the raw segment, and without stripping it, each round trip would add another layer of escaping.

**Output:** Well-formed Confluence storage XML string, suitable for `body.storage.value`.

### Confluence-Native Fence Preservation

Confluence supports constructs Markdown cannot represent (Jira macros, user mentions, panels with custom styling, layouts, unknown structured macros). To preserve round-trip fidelity, `cf_to_md` wraps the verbatim storage XML of any unsupported construct in a single HTML-comment block — the **fence**. `md_to_cf` recognises the fence and splices the original XML back in unchanged.

**Block form** (for block-level constructs):

```markdown
<!-- confluencer:storage:block:v1:b64
PGFjOnN0cnVjdHVyZWQtbWFjcm8gYWM6bmFtZT0iamlyYSIgYWM6c2NoZW1hLXZlcnNpb249IjEi
PjxhYzpwYXJhbWV0ZXIgYWM6bmFtZT0ia2V5Ij5QUk9KLTEyMzwvYWM6cGFyYW1ldGVyPjwvYWM6
c3RydWN0dXJlZC1tYWNybz4=
-->
```

The opening line carries the version (`v1`) and encoding tag (`b64`). The body is the storage XML, base64-encoded with standard alphabet, line-wrapped at 76 characters. The whole thing is one CommonMark HTML block (HTML block start condition 2: starts with `<!--`, ends on the line containing `-->`), so goldmark parses and emits it verbatim — no special handling required in `Normalise`.

**Why base64 instead of a readable inner XML comment:**

- The closing comment delimiter `-->` must not appear inside an HTML comment. Storage XML legitimately contains `-->` (e.g. `<![CDATA[ ... -->`), so emitting raw XML inside `<!-- ... -->` requires an escape mechanism. Base64 makes this impossible by construction.
- A single HTML block (no inner sentinel comments) is simpler to parse on the way back and survives every CommonMark renderer unchanged.
- Storage XML is already opaque to humans reading the Markdown; readability of the fence body is not a meaningful goal. Diffability is preserved at the fence boundary, which is enough for review.

**Inline form:** Deferred to v2. v1 promotes any inline unsupported construct to a block fence on its own line. This loses positional fidelity inside a paragraph but never loses the construct itself, which is the load-bearing guarantee.

**Properties:**

- Invisible in rendered Markdown (HTML comments never display).
- Byte-stable: the fence is emitted with deterministic encoding and wrapping, so `cf_to_md(md_to_cf(storage_xml))` yields identical bytes for unsupported constructs.
- Normalisation-safe: a single HTML block passes through `Normalise` verbatim — fence content is not re-parsed.
- Versioned: `v1` and the `b64` tag let us evolve the encoding without breaking existing documents.

`lexer/fence.go` owns encoding and decoding:

- `EncodeBlockFence(storageXML string) string` — produces the v1/b64 fence block.
- `DecodeBlockFence(htmlBlock string) (storageXML string, ok bool)` — given the body of an HTML block, returns the original XML if it matches the v1/b64 fence shape.

Every unknown `<ac:structured-macro>` (and other unsupported constructs) seen by `cf_to_md` produces a block fence; every HTML block seen by `md_to_cf` is offered to `DecodeBlockFence` and, if recognised, replaced verbatim with the decoded storage XML. Round-trip tests cover a representative set of unknown macros, and a property test verifies that `DecodeBlockFence(EncodeBlockFence(x)) == x` for arbitrary XML payloads.

---

## Round-Trip Idempotency

This is the most critical correctness property of the system. Both lexers must satisfy:

- `Normalise(cf_to_md(md_to_cf(markdown))) == Normalise(markdown)` for every construct in the supported set.
- `md_to_cf(cf_to_md(storage_xml)) == md_to_cf(cf_to_md(md_to_cf(cf_to_md(storage_xml))))` for every construct — stated as a fixed-point property: one round trip reaches a canonical form, further round trips do not change it.

For constructs in the supported mapping tables, both forms are equal modulo normalisation.

For unsupported constructs preserved via the Confluence-native fence, the storage XML is reproduced byte-for-byte. (The Markdown representation is the fence; the original storage XML survives unchanged.)

Round-trip tests covering every row of both mapping tables, plus a suite of fence-preserved constructs, must pass before hook orchestration is implemented. If idempotency breaks for any construct, every pull produces a spurious diff and drives an infinite loop of sync commits.

---

## Confluence REST API Usage

All requests use Basic Auth (`CONFLUENCE_USER:CONFLUENCE_API_TOKEN`, base64-encoded in the `Authorization` header) and `Content-Type: application/json` unless noted.

**Fetch page content and current version:**
```
GET {CONFLUENCE_BASE_URL}/rest/api/content/{id}?expand=body.storage,version,ancestors,space
```

**Fetch child pages (one level):**
```
GET {CONFLUENCE_BASE_URL}/rest/api/content/{id}/child/page?limit=200&start=<offset>
```

Pagination required for pages with >200 children.

**Create a new page:**
```
POST {CONFLUENCE_BASE_URL}/rest/api/content
Body: {
  "type": "page",
  "title": "<page title>",
  "ancestors": [{ "id": "<parent-page-id>" }],
  "space": { "key": "<space-key>" },
  "body": {
    "storage": {
      "value": "<confluence storage XML>",
      "representation": "storage"
    }
  }
}
```

**Update page content, title, or parent:**
```
PUT {CONFLUENCE_BASE_URL}/rest/api/content/{id}
Body: {
  "version": { "number": <current_version + 1> },
  "title": "<page title>",
  "type": "page",
  "ancestors": [{ "id": "<parent-page-id>" }],
  "body": {
    "storage": {
      "value": "<confluence storage XML>",
      "representation": "storage"
    }
  }
}
```

A 409 response triggers the Concurrent Writer / Version Conflict Resolution algorithm.

**Delete a page (cascades to descendants):**
```
DELETE {CONFLUENCE_BASE_URL}/rest/api/content/{id}
```

**Fetch attachment list for a page:**
```
GET {CONFLUENCE_BASE_URL}/rest/api/content/{id}/child/attachment?filename={filename}
```

**Download attachment binary:**
```
GET {CONFLUENCE_BASE_URL}/rest/api/content/{id}/child/attachment/{attachmentId}/download
```

**Upload attachment (creates if new, updates if existing by filename):**
```
POST {CONFLUENCE_BASE_URL}/rest/api/content/{id}/child/attachment
Content-Type: multipart/form-data
X-Atlassian-Token: no-check
Body: multipart file upload (field name: "file")
```

---

## Failure Handling

### Confluence write failure during `pre-push`

1. Append a structured NDJSON record to `.confluencer-pending` (see Pending Queue File).
2. Warn on stderr: `[confluencer] WARNING: Failed to <op> <path> in Confluence (page <id>, attempt <n>). Queued in .confluencer-pending.`
3. Exit 0 — the Git push proceeds.
4. On next `pre-push`, drain pending entries first in queued order.
5. `confluencer status` surfaces all outstanding pending writes.
6. `confluencer push --retry` drains pending queue independently of any Git push.

### Confluence read failure during pull

1. Warn on stderr identifying the failed page.
2. Emit `missing_unknown` in the change set (not `deleted`); skip the page this run.
3. The local file is left in its current state.

### Version conflict during push

On a 409 from Confluence, push re-fetches the current page version and retries the PUT once. If the retry also fails, the operation is queued to `.confluencer-pending`. Push does not modify local files or perform three-way merges.

### Conflict during pull

Pull writes Confluence changes on a temporary branch and rebases the developer's branch onto it. Conflicts are surfaced as standard git merge/rebase conflicts. The developer resolves them with their normal tools and completes the rebase or merge.

---

## Binary Distribution

The `confluencer` source repository publishes pre-compiled binaries as GitHub release artifacts for:

- `confluencer-linux-amd64`
- `confluencer-darwin-amd64`
- `confluencer-darwin-arm64`
- `confluencer-windows-amd64.exe`

Developers install `confluencer` onto their PATH independently of any consuming repository. Supported methods include downloading a release binary, `go install github.com/swill/confluencer@latest`, or a package manager. The consuming repository does not bundle or manage the binary — it only contains configuration and hook shims.

### Hook shims (tracked at `.confluencer/hooks/`)

Created automatically by `confluencer init`. The shims invoke `confluencer` from the developer's PATH.

**`pre-push`:**
```sh
#!/bin/sh
set -e
confluencer push
```

**`post-commit`:**
```sh
#!/bin/sh
set -e
if [ -n "$CONFLUENCER_HOOK_ACTIVE" ]; then
  exit 0
fi
export CONFLUENCER_HOOK_ACTIVE=1
confluencer pull
```

**`post-merge`:**
```sh
#!/bin/sh
set -e
if [ -n "$CONFLUENCER_HOOK_ACTIVE" ]; then
  exit 0
fi
export CONFLUENCER_HOOK_ACTIVE=1
confluencer pull
```

**`post-rewrite`:**
```sh
#!/bin/sh
set -e
if [ -n "$CONFLUENCER_HOOK_ACTIVE" ]; then
  exit 0
fi
export CONFLUENCER_HOOK_ACTIVE=1
confluencer pull
```

### `confluencer install`

Copies (not symlinks) `.confluencer/hooks/pre-push`, `.confluencer/hooks/post-commit`, `.confluencer/hooks/post-merge`, and `.confluencer/hooks/post-rewrite` into `.git/hooks/` and marks them executable. Idempotent — safe to run multiple times. Used by developers who clone an existing confluencer-managed repository and need to activate the hooks locally.

---

## CLI Reference

| Command | Description |
|---|---|
| `confluencer init --page-id <id> [--local-root <path>]` | Populate local repo from existing Confluence tree. Writes `.confluencer.json`, `.confluencer-index.json`, `.confluencer/hooks/`, and installs hooks into `.git/hooks/`. Does not commit. |
| `confluencer install` | Copy hook shims from `.confluencer/hooks/` into `.git/hooks/`. Used after cloning an existing confluencer-managed repo. |
| `confluencer push` | Invoked by pre-push hook. Fire-and-forget write of changed, added, renamed, and deleted Markdown to Confluence. Drain `.confluencer-pending` first. Does not modify local files or create commits. |
| `confluencer push --retry` | Drain `.confluencer-pending` outside of a Git push. |
| `confluencer pull` | Invoked by post-commit, post-merge, and post-rewrite hooks. Fetch Confluence tree, apply typed change set on a temporary branch, rebase current branch onto it. Commits as `chore(sync): confluence`. |
| `confluencer status` | Report pending writes, orphaned pages, and pending deletions. |
| `confluencer version` | Print version, commit, and build date. |

---

## Developer Onboarding

### First-time setup (new repo, no existing confluencer config)

```sh
# Ensure confluencer is on your PATH (e.g. go install, download release binary)
cd <repo>

# Populate credentials
cp .env.example .env
# Edit .env — set CONFLUENCE_BASE_URL, CONFLUENCE_USER, CONFLUENCE_API_TOKEN

# Initialise: fetches Confluence tree, writes config, index, hooks
confluencer init --page-id <root-page-id>

# Review and commit the generated files
git add .
git commit -m "initialise confluencer"
```

### Cloning an existing confluencer-managed repo

```sh
git clone <repo>
cd <repo>

# Populate credentials
cp .env.example .env
# Edit .env — set CONFLUENCE_BASE_URL, CONFLUENCE_USER, CONFLUENCE_API_TOKEN

# Install Git hooks (hook shims are already tracked in .confluencer/hooks/)
confluencer install
```

After either path, all `git commit`, `git pull`, `git pull --rebase`, `git merge`, `git rebase`, and `git push` operations automatically sync with Confluence. No further configuration is required.

---

## Implementation Invariants

1. **Round-trip idempotency**: `Normalise(cf_to_md(md_to_cf(x))) == Normalise(x)` for supported constructs; unsupported constructs round-trip byte-for-byte via the Confluence-native fence. Round-trip tests pass before any hook orchestration is written.
2. **Page ID is the stable identity**: Rename detection, history preservation, and index management key on Confluence page IDs. File paths and page titles are derived, mutable properties.
3. **Index and filesystem are always consistent at commit boundaries**: Index entries are updated only for successful operations. Failed push operations are recorded in `.confluencer-pending` and their index entries are updated when the pending entry drains. No committed state has the index out of sync with the filesystem.
4. **Sync commits are attributable**: All commits produced by `confluencer pull` carry the message prefix `chore(sync): confluence`. Human-authored commits must never use this prefix. It is the sole mechanism for sync-commit identification and the base point for branch-based pull operations.
5. **Renames use `git mv`**: All rename, move, promotion, and demotion operations use `git mv` so that history is traceable via `git log --follow`. Multi-file renames use the two-phase stash-and-place protocol to avoid collisions.
6. **Attachments are co-committed**: A sync commit that modifies a `.md` includes all attachment files referenced by it, under `_attachments/<page-path>/`, in the same commit.
7. **Push never blocks permanently**: Any Confluence write failure results in the push proceeding and the failure being queued in `.confluencer-pending`. The developer is never left unable to push.
8. **Credentials never appear in logs, flags, or commits**: Credential access is via environment variables only.
9. **Deletions propagate both directions**: Pages deleted in Confluence are deleted locally (via `git rm`, with full content recoverable from Git history). Files deleted in Git are deleted in Confluence (via `DELETE`, with Confluence's own version history preserved server-side). No confirmation prompt — explicit developer action (`git rm` or Confluence UI delete) is the confirmation.
10. **Slugification and reverse slugification are pure functions**: Given the same input, the output is always identical. Sibling-collision disambiguation is deterministic on page ID ordering. All three have comprehensive unit tests independent of the tree logic.
11. **Lexers are pure**: No network, filesystem, or index access inside the lexer package. I/O resolution happens in the orchestrator via injected `PageResolver` and `AttachmentResolver` interfaces. This makes round-trip tests fast and deterministic.
12. **Title Stability Rule**: A push-direction rename updates the Confluence page title only if the new filename's slug differs from the slug of the current Confluence title. Otherwise the title is preserved verbatim.
