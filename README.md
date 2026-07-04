# MemPalace Server

A fast, self-hosted memory server for AI agents — written in Go.

This is the **server** for [MemPalace](https://github.com/MemPalace/mempalace).
It gives your AI agents a long-term memory they can search, grow, and connect
over time. Agents talk to it over the [MCP](https://modelcontextprotocol.io)
protocol via simple HTTP.

> Looking for the main project, docs, and clients?
> 👉 **https://github.com/MemPalace/mempalace**

---

## What it does

MemPalace stores memories like a real memory palace:

- **Wings** → broad areas (e.g. a project)
- **Rooms** → topics inside a wing
- **Drawers** → the actual memories

On top of that, it builds connections between memories:

- **Semantic search** — find memories by meaning, not just keywords.
- **Knowledge graph** — facts with time (what was true, and when).
- **Entity graph** — people, things, and how they relate.
- **Tunnels** — links between related memories.
- **Room hygiene** *(opt-in)* — merge/rename rooms without fragmenting the
  taxonomy, plus a scheduled "dream" job that proposes near-duplicate merges.
  See [ROOM_REDIRECTS.md](./ROOM_REDIRECTS.md).

Agents use all of this through ready-made MCP tools (search, add, recall,
traverse, and more).

---

## Why use it

- **Fast & small** — a single Go binary, low memory, quick startup.
- **Self-hosted & private** — your data stays in your own PostgreSQL.
- **Multilingual** — uses the `embeddinggemma` model (100+ languages out of the box).
- **Bring your own embeddings** — works with any OpenAI-compatible API
  (Ollama, LM Studio, LocalAI, OpenAI, …).
- **Multi-tenant** — keep many users or projects fully separated.
- **Production-ready** — Docker Compose for local use, Kubernetes manifests for deploy.
- **Secure by default** — every request needs an API key.

---

## How it works

```
┌─────────────┐     MCP over HTTP      ┌──────────────────┐
│  AI agent   │ ─────────────────────► │  MemPalace Server│
│ (MCP client)│                        │      (Go)        │
└─────────────┘                        └────────┬─────────┘
                                                 │
                          ┌──────────────────────┼───────────────────────┐
                          ▼                       ▼                       ▼
                   ┌────────────┐         ┌──────────────┐        ┌──────────────┐
                   │ PostgreSQL │         │  Embedding   │        │ Apache AGE   │
                   │ + pgvector │         │  API (Ollama)│        │ (entity graph)│
                   └────────────┘         └──────────────┘        └──────────────┘
```

---

## Requirements

- **Docker** and **Docker Compose** (easiest path), or **Go 1.26+** to build from source.
- An **embedding API**. The default config expects [Ollama](https://ollama.com)
  with the `embeddinggemma` model:

  ```bash
  ollama pull embeddinggemma
  ```

---

## Quick start (Docker Compose)

This is the fastest way to try it. It starts PostgreSQL (with pgvector + AGE)
and the MemPalace server together.

**1. Get the code**

```bash
git clone https://github.com/sefodo26/mempalace-server.git
cd mempalace-server
```

**2. Make sure your embedding API is running**

```bash
ollama pull embeddinggemma
ollama serve
```

**3. Point the server at your embedding API**

Open `docker-compose.yml` and set `EMBED_API_URL`.

- Ollama runs on your host machine → use `http://host.docker.internal:11434/v1`
  (works on macOS, Windows *and* Linux — the compose file maps
  `host.docker.internal` to the host gateway for you).
- Ollama runs somewhere else → use that address.

While you're there, **change `MCP_API_KEY`** to your own secret.

**4. Start everything**

```bash
docker compose up --build
```

The server is now live at **http://localhost:8000**.

**5. Check it works**

```bash
curl http://localhost:8000/mp/mcp/health
```

---

## Connect an AI agent

The MCP endpoint is:

```
POST http://localhost:8000/mp/mcp
Authorization: Bearer <your MCP_API_KEY>
```

Point your MCP client (for example the [MemPalace](https://github.com/MemPalace/mempalace)
client) at this URL with your API key. See the main project for client setup.

👉 For a full guide — client config, the protocol step by step, and every
available tool — see **[MCP_USAGE.md](MCP_USAGE.md)**.

### Store and share documentation

Want to load whole documents into the palace and let customers search them?
Three ready-made Claude Code skills and a step-by-step guide live in
[`skills/`](skills/):

- [`mempalace-ingest-docs`](skills/mempalace-ingest-docs/SKILL.md) — file a document into MemPalace
- [`mempalace-read-docs`](skills/mempalace-read-docs/SKILL.md) — search and export it
- [`mempalace-zettelkasten`](skills/mempalace-zettelkasten/SKILL.md) — capture ideas as atomic, linked notes that resurface fast
- [`skills/Beispiel.md`](skills/Beispiel.md) — full walkthrough, including a read-only key for customers

#### Installing the skills

The skills work with [Claude Code](https://claude.com/claude-code) (and other
clients that support the same skill format). Copy the ones you want into your
skills folder:

```bash
# Per user (available in every project)
mkdir -p ~/.claude/skills
cp -r skills/mempalace-ingest-docs   ~/.claude/skills/
cp -r skills/mempalace-read-docs     ~/.claude/skills/
cp -r skills/mempalace-zettelkasten  ~/.claude/skills/
```

Or install them **per project** instead, so they ship with one repo:

```bash
mkdir -p .claude/skills
cp -r /path/to/mempalace-server/skills/mempalace-ingest-docs   .claude/skills/
cp -r /path/to/mempalace-server/skills/mempalace-read-docs     .claude/skills/
cp -r /path/to/mempalace-server/skills/mempalace-zettelkasten  .claude/skills/
```

Then **restart Claude Code** (or run `/doctor` to reload). Each skill is a folder
with a `SKILL.md`; nothing else to build. The skills become available
automatically — just describe what you want (e.g. *"ingest docs/api.md into
MemPalace"*), or invoke one directly with `/mempalace-ingest-docs`.

> The skills call the MemPalace server, so connect it first (see
> [MCP_USAGE.md](MCP_USAGE.md)) or enable the REST API. See
> [`skills/Beispiel.md`](skills/Beispiel.md) for the end-to-end walkthrough.

---

## Access control: read vs write keys

The server supports two kinds of API key:

| Key | Env variable | Can do |
| --- | --- | --- |
| **Full** | `MCP_API_KEY` | Everything — read **and** write |
| **Read-only** | `MCP_API_KEY_READONLY` | Read only — search, list, get, … |

The read-only key is **optional**. Set it to give some clients (dashboards,
read-only agents, monitoring) safe access that cannot change anything.

- A read-only key may call non-mutating operations only. Any write — add,
  update, delete, create tunnel, add/invalidate facts, change settings — is
  rejected (`403` for REST, JSON-RPC error `-32003` for MCP).
- The two keys **must be different**; the server refuses to start otherwise.
- This applies to **both** the MCP endpoint and the optional REST API.

```bash
# Full access
MCP_API_KEY="super-secret-write-key"
# Optional read-only access
MCP_API_KEY_READONLY="another-secret-read-key"
```

---

## Optional: plain REST/JSON API

Most users only need MCP. But if you want to talk to the palace from a normal
script, a `curl` command, or a non-MCP app, you can turn on a simple REST API.

It is **off by default**. Enable it with:

```
ENABLE_REST_API=true
```

It uses the **same `MCP_API_KEY`** for auth and lives under `/mp/api/v1`.
It is a thin wrapper over the same logic as MCP — same validation, same storage.

| Method | Path | What it does |
| --- | --- | --- |
| `GET` | `/mp/api/v1/health` | Health check |
| `GET` | `/mp/api/v1/status` | Palace overview (counts) |
| `GET` | `/mp/api/v1/wings` | List wings |
| `GET` | `/mp/api/v1/rooms?wing=` | List rooms |
| `GET` | `/mp/api/v1/taxonomy` | Full wing → room tree |
| `POST` | `/mp/api/v1/search` | Semantic search |
| `GET` | `/mp/api/v1/drawers?wing=&room=&limit=&offset=` | List drawers |
| `POST` | `/mp/api/v1/drawers` | Add a drawer |
| `GET` | `/mp/api/v1/drawers/{id}` | Get one drawer |
| `PATCH` | `/mp/api/v1/drawers/{id}` | Update a drawer |
| `DELETE` | `/mp/api/v1/drawers/{id}` | Delete a drawer |

Examples:

```bash
KEY="your-secret-key"

# Search
curl -X POST http://localhost:8000/mp/api/v1/search \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"query": "what did we decide about auth?", "limit": 5}'

# Add a memory
curl -X POST http://localhost:8000/mp/api/v1/drawers \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"wing": "project-x", "room": "decisions", "content": "We use JWT auth."}'

# List memories
curl http://localhost:8000/mp/api/v1/drawers?wing=project-x \
  -H "Authorization: Bearer $KEY"
```

### Try it with Bruno

A ready-to-use [Bruno](https://www.usebruno.com) collection for all REST
endpoints is in the [`bruno/`](bruno/) folder. Bruno is a free, open-source
API client (like Postman) that stores requests as plain files in your repo.

1. Install Bruno, then **Open Collection** and pick the `bruno/` folder.
2. Select the **Local** environment (top-right) and set your values:
   - `baseUrl` — e.g. `http://localhost:8000`
   - `apiKey` — your `MCP_API_KEY`
   - `drawerId` — a real drawer ID (copy one from *Add Drawer* / *List Drawers*)
3. Send any request. Bearer auth is applied automatically to all of them.

---

## Configuration

All settings come from environment variables.

| Variable | What it does | Default |
| --- | --- | --- |
| `MEMPALACE_DB_URL` | PostgreSQL connection string (**required**) | – |
| `MCP_API_KEY` | Full-access API key (read + write) | – |
| `MCP_API_KEY_READONLY` | Optional read-only API key (see below) | empty |
| `EMBED_API_URL` | OpenAI-compatible embedding API | `http://localhost:11434/v1` |
| `EMBED_API_KEY` | API key for the embedding API (if needed) | empty |
| `EMBED_MODEL` | Embedding model name | `embeddinggemma` |
| `EMBED_DIM` | Embedding size — **must match the model** | `768` |
| `MEMPALACE_TENANT_ID` | Keeps data separate per tenant | `default` |
| `MEMPALACE_HNSW_EF_SEARCH` | Search quality (higher = better, slower) | `100` |
| `ENABLE_REST_API` | Turn on the optional REST/JSON API (see below) | `false` |
| `MEMPALACE_GRAPH_AUTO_POPULATE` | Auto-populate the entity graph on `add_drawer` (see below) | `false` |
| `MEMPALACE_GRAPH_EXTRACTOR` | Extraction strategy: `structural` or `llm` | `structural` |
| `MEMPALACE_ROOM_REDIRECTS` | Enable room merge/rename redirects (see below) | `false` |
| `LLM_API_URL` | OpenAI-compatible chat API (only for `llm` extractor) | empty |
| `LLM_API_KEY` | API key for the chat API (if needed) | empty |
| `LLM_MODEL` | Chat model name (only for `llm` extractor) | empty |
| `PORT` | Port the server listens on | `8000` |

### Knowledge-graph auto-population

By default, `add_drawer` is **storage-only**: it stores the memory (vector +
metadata) and does **not** touch the Apache AGE entity graph. The graph is
populated only by explicit `kg_add_entity` / `kg_add_relation` calls. This is a
deliberate design choice — extraction is the client's job, which keeps the
write path deterministic and free of LLM calls.

If you *do* want the graph filled automatically, set
`MEMPALACE_GRAPH_AUTO_POPULATE=true` and pick a strategy:

- **`structural`** (default, no LLM) — deterministic. Every drawer becomes a
  graph node linked to its room and wing:
  `(Drawer) -[:IN_ROOM]-> (Room) -[:PART_OF]-> (Wing)`. These nodes carry the
  `entity_type` `Drawer` / `Room` / `Wing`, so they stay distinguishable from
  entities you add yourself. No network calls, no new dependencies.
- **`llm`** — extracts real entities and relations from the drawer content via
  an OpenAI-compatible chat model. Set `LLM_API_URL`, `LLM_MODEL` (and
  `LLM_API_KEY` if required). Closer to what MemPalace-family users may expect,
  at the cost of an LLM call in the `add_drawer` write path.

Both require Apache AGE to be available; if it isn't, auto-population is
silently disabled (a log line explains why). Population is **best-effort** — if
extraction fails, the drawer is still filed; the graph write is just skipped and
logged. Re-filing the same content is idempotent (graph MERGE), matching
`add_drawer`'s existing idempotency.

### Room redirects

Off by default. Set `MEMPALACE_ROOM_REDIRECTS=true` to merge or rename rooms
without fragmenting the taxonomy: an old room forwards to a canonical one, its
drawers move with a pure metadata update (no re-embedding), and `add_drawer` /
`mempalace_search` / `mempalace_list_drawers` transparently follow the redirect
and echo where they landed. When the flag is off the feature is fully absent —
the redirect tools are not registered and drawer/search behavior is unchanged.

See **[ROOM_REDIRECTS.md](./ROOM_REDIRECTS.md)** for the tool list and a concrete
`Auth → Authentication` merge walkthrough.

### A note on `EMBED_DIM`

`EMBED_DIM` must equal the embedding size your model actually returns — it
**cannot be larger**. `embeddinggemma` returns **768** values. It also supports
Matryoshka truncation *down* to **512 / 256 / 128** (smaller = faster and less
storage, with a small quality drop). For bigger vectors you need a different
model (e.g. OpenAI `text-embedding-3-large` = 3072).

---

## Run from source (without Docker)

You need a PostgreSQL with the **pgvector** extension (and optionally
**Apache AGE** for the entity graph).

```bash
cd server

export MEMPALACE_DB_URL="postgres://user:pass@localhost:5432/mempalace"
export MCP_API_KEY="your-secret-key"
export EMBED_API_URL="http://localhost:11434/v1"
export EMBED_MODEL="embeddinggemma"
export EMBED_DIM="768"

go run ./cmd/mempalace
```

The server creates all the tables it needs on first start.

### Project layout

The repo is a Go workspace (`go.work`) of three modules:

- **`core/`** (`mempalace/core`) — shared library: storage, embedding client,
  config, and the `consolidate` clustering logic.
- **`server/`** (`mempalace/server`) — the MCP/HTTP server (imports `core`).
- **`dreamjob/`** (`mempalace/dreamjob`) — the standalone room-consolidation job
  (imports `core`), run as a Kubernetes CronJob. See
  [ROOM_REDIRECTS.md](./ROOM_REDIRECTS.md).

Run the dream job against the same database once:

```bash
cd dreamjob
export MEMPALACE_DB_URL="postgres://user:pass@localhost:5432/mempalace"
export EMBED_API_URL="http://localhost:11434/v1" EMBED_MODEL="embeddinggemma" EMBED_DIM="768"
go run .
```

---

## Test environment (PostgreSQL + pgvector + AGE)

To run tests or try the entity-graph features, spin up an **isolated** database
with both extensions. It uses port **5433**, ephemeral `tmpfs` storage (clean on
every restart), and runs no app container — so it never touches your dev stack.

```bash
# Start (first run builds the AGE image; later runs are cached)
docker compose -f docker-compose.test.yml up -d --build

# Connection string
postgres://mempalace:mempalace_test@localhost:5433/mempalace_test

# Stop and wipe
docker compose -f docker-compose.test.yml down
```

Point the server at it and the entity graph (AGE) is provisioned automatically:

```bash
cd server
MEMPALACE_DB_URL="postgres://mempalace:mempalace_test@localhost:5433/mempalace_test" \
MCP_API_KEY="test-key" PORT="8099" \
EMBED_API_URL="http://localhost:11434/v1" \
go run ./cmd/mempalace
# logs: "graph: kg_mp_default ready" → AGE is working
```

Verify the extensions directly:

```bash
docker exec mempalace-test-testdb-1 psql -U mempalace -d mempalace_test \
  -c "SELECT extname, extversion FROM pg_extension WHERE extname IN ('age','vector');"
```

---

## Deploy to Kubernetes

Ready-to-use manifests are in the [`k8s/`](k8s/) folder (namespace, PostgreSQL
StatefulSet, server Deployment, Service, Ingress, and Secret).

```bash
kubectl apply -f k8s/
```

Edit `k8s/secret.yaml` and `k8s/deployment.yaml` for your own database, API key,
and embedding API before applying.

### Try it locally on minikube

Want to run the full stack on a local Kubernetes cluster first? A one-shot
script builds the images, creates the secret, and applies everything:

```bash
./k8s/minikube-setup.sh
```

See **[k8s/MINIKUBE.md](k8s/MINIKUBE.md)** for the full guide.

---

## License

[MIT](LICENSE)
