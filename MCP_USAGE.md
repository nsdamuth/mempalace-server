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
request. The token is whatever you set as `MCP_API_KEY` on the server.

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
list is for understanding what exists.

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
| `mempalace_add_drawer` | File verbatim content into the palace |
| `mempalace_get_drawer` | Fetch a single drawer by ID |
| `mempalace_list_drawers` | List drawers, with wing/room filter and pagination |
| `mempalace_update_drawer` | Update a drawer's content and/or metadata |
| `mempalace_delete_drawer` | Delete a drawer by ID |

### Diary

| Tool | What it does |
| --- | --- |
| `mempalace_diary_write` | Write a diary entry (stored as a drawer) |
| `mempalace_diary_read` | Read recent diary entries for an agent |

### Tunnels (cross-wing links)

| Tool | What it does |
| --- | --- |
| `mempalace_traverse` | Walk the palace graph from a room to connected ideas |
| `mempalace_find_tunnels` | Find rooms that bridge two wings |
| `mempalace_follow_tunnels` | Follow tunnels from a room to connected rooms |
| `mempalace_create_tunnel` | Create a cross-wing tunnel between two locations |
| `mempalace_list_tunnels` | List all explicit tunnels (optional wing filter) |
| `mempalace_delete_tunnel` | Delete a tunnel by ID |
| `mempalace_graph_stats` | Palace graph overview |

### Knowledge graph — facts over time

| Tool | What it does |
| --- | --- |
| `mempalace_kg_add` | Add a fact: subject → predicate → object, with optional time window |
| `mempalace_kg_query` | Query an entity's facts; filter by `as_of` date |
| `mempalace_kg_invalidate` | Mark a fact as no longer true |
| `mempalace_kg_timeline` | Chronological timeline of facts |
| `mempalace_kg_stats` | KG overview: entities, facts, current vs expired |

### Entity graph (optional, Apache AGE)

| Tool | What it does |
| --- | --- |
| `mempalace_kg_add_entity` | Add or update an entity (merge by name) |
| `mempalace_kg_add_relation` | Add a directed relation between two entities |
| `mempalace_kg_get_entity` | Fetch an entity and its direct relations |
| `mempalace_kg_search_entities` | Search entities by name (optional type filter) |
| `mempalace_kg_delete_entity` | Delete an entity and its relations |
| `mempalace_kg_traverse` | Traverse the graph from an entity up to a depth |

### Meta

| Tool | What it does |
| --- | --- |
| `mempalace_get_aaak_spec` | The AAAK compressed-memory format spec |
| `mempalace_hook_settings` | Get/set hook behavior (silent save, desktop toast) |
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
| `unknown tool` | Tool name misspelled — check `tools/list` |
| Search returns nothing | Embedding API unreachable, or no drawers yet |
| Entity-graph tools error | Apache AGE not installed (other tools still work) |
| Server won't start | `MEMPALACE_DB_URL` not set, or DB unreachable |

Check server logs and the health endpoint first:

```bash
curl http://localhost:8000/mp/mcp/health
```
