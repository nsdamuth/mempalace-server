---
name: mempalace-ingest-docs
description: Ingest a complex document (Markdown, README, spec, handbook) into a MemPalace server by splitting it into well-organized, semantically searchable sections. Use when the user wants to store, file, import, load, or "remember" documentation in MemPalace.
---

# Ingest documentation into MemPalace

This skill loads a long document into a MemPalace server so it can be recalled
later by meaning. A document is broken into small pieces and filed using the
palace structure:

- **Wing** — the whole document or product (e.g. `acme-api-docs`)
- **Room** — a top-level section (e.g. `authentication`, `getting-started`)
- **Drawer** — one chunk of content (a subsection or a few paragraphs)

## Before you start

You need a running MemPalace server and a **write** API key. You can reach it
two ways — use whichever is available:

1. **MCP tools** (`mempalace_*`) — if the MemPalace MCP server is connected to
   this client. Preferred.
2. **REST API** (`/mp/api/v1`) via `curl` — if `ENABLE_REST_API=true` on the
   server. Use the same write key.

If neither the wing/room are given, ask the user for the document and a short
wing name (a slug for the doc/product).

## Steps

1. **Read the document.** Load the file the user points to (or the text they
   paste).

2. **Plan the taxonomy.**
   - Pick one **wing** = a slug for the whole document (e.g. `acme-api-docs`).
   - Use each top-level heading (`#` / `##`) as a **room** (slug it, e.g.
     "Getting Started" → `getting-started`).
   - Keep the original heading path at the top of each chunk so it stays
     readable on its own.

3. **Chunk the content.** Split each section into drawers of roughly
   200–400 words. Never split in the middle of a code block or table. Each
   drawer must make sense on its own — prepend a one-line breadcrumb such as
   `# Acme API > Authentication > API keys` to the content.

4. **Avoid duplicates.** For each chunk, call `mempalace_check_duplicate`
   (or skip if re-ingesting on purpose). Skip chunks that already exist.

5. **File each chunk** with `mempalace_add_drawer`:
   - `wing` = the document slug
   - `room` = the section slug
   - `content` = the breadcrumb + chunk text
   - `source_file` = the original file path
   - `added_by` = `docs-ingest`

6. **Link related sections (optional).** If two sections in different wings are
   related, create a `mempalace_create_tunnel` between their rooms.

7. **Report.** Tell the user the wing name, how many rooms and drawers were
   created, and any chunks skipped as duplicates.

## MCP example (preferred)

Call the tools directly. One `add_drawer` per chunk:

```
mempalace_add_drawer({
  "wing": "acme-api-docs",
  "room": "authentication",
  "content": "# Acme API > Authentication > API keys\n\nAll requests need a Bearer token ...",
  "source_file": "docs/api.md",
  "added_by": "docs-ingest"
})
```

## REST example (fallback, ENABLE_REST_API=true)

```bash
KEY="YOUR_WRITE_KEY"
BASE="http://localhost:8000/mp/api/v1"

curl -sS -X POST "$BASE/drawers" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "wing": "acme-api-docs",
    "room": "authentication",
    "content": "# Acme API > Authentication > API keys\n\nAll requests need a Bearer token ...",
    "source_file": "docs/api.md",
    "added_by": "docs-ingest"
  }'
```

Loop this once per chunk. Check the response `success` field.

## Good practices

- One wing per document keeps retrieval clean.
- Keep chunks self-contained — the reader (or an AI) may see one drawer alone.
- Re-ingesting? Either rely on `check_duplicate`, or list and delete the old
  wing's drawers first so you don't get stale copies.
- Store facts that change over time (versions, dates, limits) as knowledge-graph
  entries with `mempalace_kg_add` instead of plain drawers.
