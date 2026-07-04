# Using MemPalace as an MCP Server

This guide explains how to use the MemPalace server through the
[Model Context Protocol (MCP)](https://modelcontextprotocol.io) — the way it
is meant to be used. MCP lets an AI agent (the **client**) call the server's
tools to store and recall memories.

> New here? Start with the [README](README.md) for install and setup.
> Main project: **https://github.com/MemPalace/mempalace**

---

## 1. The endpoint

MemPalace speaks MCP over HTTP (the *Streamable HTTP* transport).

| | |
| --- | --- |
| **URL** | `POST http://<host>:8000/mp/mcp` |
| **Auth** | `Authorization: Bearer <MCP_API_KEY>` (required on every request) |
| **Content-Type** | `application/json` |
| **Health check** | `GET http://<host>:8000/mp/mcp/health` |

Every request must carry the bearer token. Without it, the server rejects the
request.

There are two kinds of token:

| Token | Env variable | Access |
| --- | --- | --- |
| Full | `MCP_API_KEY` | read **and** write |
| Read-only | `MCP_API_KEY_READONLY` (optional) | read only |

A read-only token may call non-mutating tools only. Calling a write tool with
it returns a JSON-RPC error with code `-32003` ("write permission required").
Write tools are marked **✏️** in the [tool reference](#5-tool-reference) below.

Supported protocol versions (newest first):
`2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05`.

---

## 2. Connect a client

You usually do **not** call the endpoint by hand. An MCP client does the
protocol for you. Point it at the URL above with your API key.

### Claude Desktop / Claude Code

These clients speak MCP over stdio, so use the small
[`mcp-remote`](https://www.npmjs.com/package/mcp-remote) bridge to reach an
HTTP server. Add this to your MCP config:

```json
{
  "mcpServers": {
    "mempalace": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "http://localhost:8000/mp/mcp",
        "--header",
        "Authorization: Bearer YOUR_MCP_API_KEY"
      ]
    }
  }
}
```

After restarting the client, the `mempalace_*` tools appear and the agent can
call them on its own.

### A native HTTP MCP client

If your client supports the Streamable HTTP transport directly, just give it:

- URL: `http://localhost:8000/mp/mcp`
- Header: `Authorization: Bearer YOUR_MCP_API_KEY`

---

## 3. The protocol, step by step

For reference, this is what the client does under the hood. You can reproduce
it with `curl` to verify the server.

**1. Initialize** — handshake and version negotiation. The response includes an
`Mcp-Session-Id` header you may reuse on later calls.

```bash
curl -i -X POST http://localhost:8000/mp/mcp \
  -H "Authorization: Bearer YOUR_MCP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {"protocolVersion": "2025-06-18", "capabilities": {}}
  }'
```

**2. List tools** — discover what is available.

```bash
curl -X POST http://localhost:8000/mp/mcp \
  -H "Authorization: Bearer YOUR_MCP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}'
```

**3. Call a tool** — `method` is always `tools/call`; the tool name goes in
`params.name`, and its arguments in `params.arguments`.

```bash
curl -X POST http://localhost:8000/mp/mcp \
  -H "Authorization: Bearer YOUR_MCP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "mempalace_search",
      "arguments": {"query": "what did we decide about auth?", "limit": 5}
    }
  }'
```

The result comes back as MCP `content` of type `text` — the text is JSON with
the actual data.

> Note: arguments not declared in a tool's schema are silently dropped, so you
> cannot pass unexpected fields.

---

## 4. Core concepts

MemPalace organizes memories like a memory palace:

- **Wing** — a broad area, e.g. a project (`project-x`).
- **Room** — a topic inside a wing (`decisions`).
- **Drawer** — a single stored memory (the actual content).

On top of that:

- **Semantic search** finds drawers by meaning, using embeddings.
- **Tunnels** link related rooms across different wings.
- **Knowledge graph (KG)** stores facts as *subject → predicate → object* with
  time windows, so you can ask "what was true on a given date?".
- **Entity graph** (optional, needs Apache AGE) stores entities and relations.

---

## 5. Tool reference

All tools are prefixed `mempalace_`. The agent picks them automatically; this
list is for understanding what exists. Tools marked **✏️** mutate state and
require a full-access key; the rest are readable with a read-only key too.

### Browse & inspect

| Tool | What it does |
| --- | --- |
| `mempalace_status` | Palace overview — total drawers, wing and room counts |
| `mempalace_list_wings` | List all wings with drawer counts |
| `mempalace_list_rooms` | List rooms within a wing (or all rooms) |
| `mempalace_get_taxonomy` | Full tree: wing → room → drawer count |
| `mempalace_memories_filed_away` | Recent filing activity (count today + latest timestamp) |

### Store & recall memories (drawers)

| Tool | What it does |
| --- | --- |
| `mempalace_search` | Semantic search — returns drawers with similarity scores |
| `mempalace_check_duplicate` | Check if content already exists before filing |
| `mempalace_add_drawer` ✏️ | File verbatim content into the palace |
| `mempalace_get_drawer` | Fetch a single drawer by ID |
| `mempalace_list_drawers` | List drawers, with wing/room filter and pagination |
| `mempalace_update_drawer` ✏️ | Update a drawer's content and/or metadata |
| `mempalace_delete_drawer` ✏️ | Delete a drawer by ID |

#### Filing content: what `add_drawer` returns

`mempalace_add_drawer` has **two surprising-but-intentional behaviors** that
integrators should handle explicitly:

**1. Drawer IDs are deterministic → re-filing is idempotent.**
The ID is `sha256(wing/room/content[:500])` (first 16 hex chars). Filing the
same content into the same `wing`/`room` again does **not** error and does
**not** create a duplicate — it returns the existing ID with a `reason`:

```jsonc
// First call
{ "success": true, "drawer_id": "a1b2c3d4e5f60718" }
// Second call with identical content
{ "success": true, "reason": "already_exists", "drawer_id": "a1b2c3d4e5f60718" }
```

Callers should treat `already_exists` as success, and must not assume a
returned ID means a *new* drawer was created.

**2. Pure bullet lists are split into one drawer per bullet.**
If the `content` is a bullet list, each bullet is stored as its own drawer
(for precise retrieval). The response shape then changes — there is **no**
`drawer_id`; you get counts instead:

```jsonc
{
  "success": true,
  "bullets_stored": 3,   // newly created this call
  "bullets_total": 5,    // total bullets seen (2 already existed → idempotency)
  "wing": "…",
  "room": "…"
}
```

Callers that read `response.drawer_id` unconditionally will get `undefined`
for bullet-list input. Branch on the presence of `bullets_stored` vs
`drawer_id`.

### Diary

| Tool | What it does |
| --- | --- |
| `mempalace_diary_write` ✏️ | Write a diary entry (stored as a drawer) |
| `mempalace_diary_read` | Read recent diary entries for an agent |

### Tunnels (cross-wing links)

| Tool | What it does |
| --- | --- |
| `mempalace_traverse` | Walk the palace graph from a room to connected ideas |
| `mempalace_find_tunnels` | Find rooms that bridge two wings |
| `mempalace_follow_tunnels` | Follow tunnels from a room to connected rooms |
| `mempalace_create_tunnel` ✏️ | Create a cross-wing tunnel between two locations |
| `mempalace_list_tunnels` | List all explicit tunnels (optional wing filter) |
| `mempalace_delete_tunnel` ✏️ | Delete a tunnel by ID |
| `mempalace_graph_stats` | Palace graph overview |

### Knowledge graph — facts over time

| Tool | What it does |
| --- | --- |
| `mempalace_kg_add` ✏️ | Add a fact: subject → predicate → object, with optional time window |
| `mempalace_kg_query` | Query an entity's facts; filter by `as_of` date |
| `mempalace_kg_invalidate` ✏️ | Mark a fact as no longer true |
| `mempalace_kg_timeline` | Chronological timeline of facts |
| `mempalace_kg_stats` | KG overview: entities, facts, current vs expired |

### Entity graph (optional, Apache AGE)

| Tool | What it does |
| --- | --- |
| `mempalace_kg_add_entity` ✏️ | Add or update an entity (merge by name) |
| `mempalace_kg_add_relation` ✏️ | Add a directed relation between two entities |
| `mempalace_kg_get_entity` | Fetch an entity and its direct relations |
| `mempalace_kg_search_entities` | Search entities by name (optional type filter) |
| `mempalace_kg_delete_entity` ✏️ | Delete an entity and its relations |
| `mempalace_kg_traverse` | Traverse the graph from an entity up to a depth |

> **Populating the entity graph.** By default the graph is filled **only** by
> the explicit `kg_add_entity` / `kg_add_relation` calls above — `add_drawer`
> is storage-only and does not touch it. The server can optionally
> auto-populate the graph on every `add_drawer` (structural links, or
> LLM-extracted entities/relations); it is **off by default**. See
> [Knowledge-graph auto-population](README.md#knowledge-graph-auto-population)
> in the README to enable it.

### Meta

| Tool | What it does |
| --- | --- |
| `mempalace_get_aaak_spec` | The AAAK compressed-memory format spec |
| `mempalace_get_hook_settings` | View hook behavior (silent save, desktop toast) |
| `mempalace_set_hook_settings` ✏️ | Change hook behavior |
| `mempalace_hook_settings` ✏️ | Combined get/set alias (upstream-compatible; prefer the split tools) |
| `mempalace_reconnect` | Reconnect to the database (no-op; auto-reconnects) |

If Apache AGE is not installed, the entity-graph tools return a clear error and
everything else keeps working.

---

## 6. A typical flow

A well-behaved agent usually:

1. **Recalls first** — calls `mempalace_search` to see what it already knows.
2. **Avoids duplicates** — calls `mempalace_check_duplicate` before storing.
3. **Files the memory** — calls `mempalace_add_drawer` with a clear `wing`,
   `room`, and the content.
4. **Records facts** — uses `mempalace_kg_add` for things that change over time
   (status, dates, relationships).
5. **Connects ideas** — creates a `mempalace_create_tunnel` when content in one
   project relates to another.

---

## 7. Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `401` / request rejected | Missing or wrong `Authorization: Bearer` token |
| `-32003` write permission required | Used the read-only key for a write tool |
| `unknown tool` | Tool name misspelled — check `tools/list` |
| Search returns nothing | Embedding API unreachable, or no drawers yet |
| Entity-graph tools error | Apache AGE not installed (other tools still work) |
| Server won't start | `MEMPALACE_DB_URL` not set, or DB unreachable |

Check server logs and the health endpoint first:

```bash
curl http://localhost:8000/mp/mcp/health
```
