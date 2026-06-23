---
name: mempalace-read-docs
description: Retrieve documentation stored in a MemPalace server — answer a question from it, browse a document's sections, or export a whole document. Use when the user wants to read, query, search, fetch, or export docs from MemPalace.
---

# Read documentation from MemPalace

This skill recalls documentation that was filed into MemPalace (see the
`mempalace-ingest-docs` skill). It supports three jobs:

1. **Answer a question** — semantic search across the docs.
2. **Browse** — list what is stored (wings → rooms → drawers).
3. **Export** — reassemble a whole document.

## Access

You can reach the server two ways — use whichever is available:

1. **MCP tools** (`mempalace_*`) — preferred when connected.
2. **REST API** (`/mp/api/v1`) via `curl` — when `ENABLE_REST_API=true`.

A **read-only** API key is enough for everything in this skill.

## Job 1 — Answer a question

1. Run a semantic search with the user's question.
2. Read the top results, then answer **from them**. Quote or cite the
   `source_file` / wing / room so the user can verify.
3. If results are weak, broaden the query or raise the limit, and say so.

```
mempalace_search({ "query": "how do I rotate an API key?", "limit": 5 })
```

REST:

```bash
KEY="YOUR_READONLY_KEY"; BASE="http://localhost:8000/mp/api/v1"
curl -sS -X POST "$BASE/search" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"query": "how do I rotate an API key?", "limit": 5}'
```

To stay inside one document, pass `"wing": "acme-api-docs"` in the search.

## Job 2 — Browse

- `mempalace_list_wings` → which documents exist.
- `mempalace_list_rooms({ "wing": "acme-api-docs" })` → its sections.
- `mempalace_list_drawers({ "wing": "...", "room": "..." })` → the chunks.
- `mempalace_get_drawer({ "drawer_id": "..." })` → one chunk in full.

REST equivalents: `GET /wings`, `GET /rooms?wing=`, `GET /drawers?wing=&room=`,
`GET /drawers/{id}`.

## Job 3 — Export a whole document

1. `list_rooms` for the wing to get every section.
2. For each room, `list_drawers` to get its chunks (use `limit`/`offset` to
   page through all of them — do not stop at the first page).
3. Concatenate the drawer contents, grouped by room, in order. The breadcrumb
   line at the top of each drawer tells you where it belongs.
4. Output clean Markdown.

## Notes

- Search ranks by meaning, not exact words — rephrase if the first try misses.
- Results include a similarity/distance score; lower distance = closer match.
- If a search returns nothing, the doc may not be ingested yet, or the
  embedding API may be down — check the server health endpoint.
