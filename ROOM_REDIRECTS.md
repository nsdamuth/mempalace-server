# Room Redirects

Merge or rename a room without fragmenting the taxonomy or leaving dangling
references. A **redirect** is a one-way `old room → canonical room` forwarding
pointer: old references keep resolving, the old name stays discoverable, and the
underlying drawers move with a pure metadata update — **no re-embedding**,
because a room label is not part of a drawer's content vector.

This is the non-destructive "consolidation" counterpart to write-time duplicate
prevention: it cleans up fragmentation that already happened.

> Redirects are **directional** and single-target (a room forwards to at most
> one place), unlike [tunnels](./MCP_USAGE.md), which are symmetric topical
> links. The two are separate constructs on purpose — a redirect never shows up
> in tunnel traversal.

---

## Enabling the feature

Room redirects are **opt-in** and controlled by a single environment variable.

| Variable | Default | Effect |
| --- | --- | --- |
| `MEMPALACE_ROOM_REDIRECTS` | `false` | `true` exposes the four redirect tools and makes `add_drawer` / `mempalace_search` / `mempalace_list_drawers` transparently follow redirects. |

When it is **off** (the default), the feature is fully absent: no redirect tools
are registered in `tools/list`, and drawer/search behavior is unchanged.

**docker-compose:**

```yaml
services:
  mempalace:
    environment:
      MEMPALACE_ROOM_REDIRECTS: "true"
```

**Plain env / shell:**

```bash
export MEMPALACE_ROOM_REDIRECTS=true
```

On the next start the server provisions the `room_redirects` table (idempotent)
and logs `room redirects enabled`.

---

## Tools

Exposed only when `MEMPALACE_ROOM_REDIRECTS=true`:

| Tool | Purpose |
| --- | --- |
| `mempalace_redirect_room` | Merge/rename: forward an old room to a new one, optionally moving its drawers. |
| `mempalace_resolve_room` | Resolve a room to its canonical target, following the whole chain. |
| `mempalace_list_redirects` | List all active redirects with reasons. |
| `mempalace_delete_redirect` | Remove a redirect by its old (`from`) endpoint. |
| `mempalace_list_merge_candidates` | List the dream job's proposed merges to review. |
| `mempalace_apply_merge_candidate` | Apply a proposed merge by its candidate ID. |
| `mempalace_dismiss_merge_candidate` | Reject a proposed merge (rooms stay separate). |

`mempalace_list_rooms` additionally surfaces active redirects (flagged), so a
merged room name stays visible even after its drawers moved away. The last three
tools review the output of the [dream consolidation job](#the-dream-consolidation-job).

---

## Concrete example

Scenario: over time, memories about authentication were filed under three
near-duplicate rooms in the `backend` wing — `Auth`, `auth-flow`, and the
canonical `Authentication`. We consolidate everything into `Authentication`.

### 1. Merge `Auth` → `Authentication` and move its drawers

```jsonc
// tools/call → mempalace_redirect_room
{
  "from_wing": "backend",
  "from_room": "Auth",
  "to_wing":   "backend",
  "to_room":   "Authentication",
  "reason":    "consolidating auth rooms"
  // move_drawers defaults to true
}
```

Response — the drawers physically moved, and a forwarding pointer now exists:

```json
{
  "success": true,
  "redirect": {
    "from_wing": "backend", "from_room": "Auth",
    "to_wing": "backend",   "to_room": "Authentication",
    "reason": "consolidating auth rooms",
    "created_at": "2026-07-04T12:00:00+00:00"
  },
  "drawers_moved": 12,
  "moved_to": { "wing": "backend", "room": "Authentication" }
}
```

Repeat for `auth-flow`:

```jsonc
// tools/call → mempalace_redirect_room
{ "from_wing": "backend", "from_room": "auth-flow",
  "to_wing": "backend", "to_room": "Authentication",
  "reason": "consolidating auth rooms" }
```

### 2. Old room names now transparently resolve

Any tool that takes a room follows the redirect and tells you where it landed.
A search against the **old** name still works:

```jsonc
// tools/call → mempalace_search
{ "query": "token refresh", "wing": "backend", "room": "Auth" }
```

```json
{
  "results": [ /* … hits from the Authentication room … */ ],
  "count": 3,
  "redirected": {
    "from_wing": "backend", "from_room": "Auth",
    "to_wing": "backend",   "to_room": "Authentication",
    "chain": ["backend/Auth", "backend/Authentication"],
    "reason": "consolidating auth rooms",
    "hops": 1
  }
}
```

Same for `add_drawer`: filing into `Auth` lands the drawer in `Authentication`
and echoes a `redirected` block, so the write is never silently rerouted.

> A client that ignores the `redirected` block still gets correct data — the
> server already followed the chain. Clients that read it learn the canonical
> name and converge over time.

### 3. Inspect the state

```jsonc
// tools/call → mempalace_resolve_room
{ "wing": "backend", "room": "auth-flow" }
```

```json
{
  "wing": "backend", "room": "Authentication",
  "redirected": true,
  "chain": ["backend/auth-flow", "backend/Authentication"],
  "reason": "consolidating auth rooms"
}
```

```jsonc
// tools/call → mempalace_list_rooms  { "wing": "backend" }
```

```json
{
  "rooms": [
    { "wing": "backend", "room": "Authentication", "drawers": 27 }
  ],
  "redirects": [
    { "from_wing": "backend", "from_room": "Auth",      "to_wing": "backend", "to_room": "Authentication", "reason": "consolidating auth rooms" },
    { "from_wing": "backend", "from_room": "auth-flow", "to_wing": "backend", "to_room": "Authentication", "reason": "consolidating auth rooms" }
  ]
}
```

The old names are gone from the live taxonomy (their drawers moved) but remain
visible as flagged redirects.

### 4. Undo a redirect (optional)

```jsonc
// tools/call → mempalace_delete_redirect
{ "from_wing": "backend", "from_room": "Auth" }
```

```json
{ "deleted": true, "from_wing": "backend", "from_room": "Auth" }
```

Removing the redirect stops the forwarding; it does **not** move drawers back.

---

## Preventing duplicates at write time

Redirects cure fragmentation; this stops the easy cases from ever forming. When
`MEMPALACE_ROOM_REDIRECTS=true`, `add_drawer` checks — after following existing
redirects — whether a room with an **equivalent name** already exists in the
wing (equivalent = identical after normalization: lowercase, and `-`/`_`/spaces
unified). If so, the write is **folded into the existing room** instead of
creating a new variant:

- `add_drawer(room="auth")` when `Auth` already exists → the drawer is filed
  under `Auth`, a redirect `auth → Auth` is recorded (so later reads of `auth`
  resolve too), any stray drawers under `auth` are moved, and the response
  carries a `canonicalized` block.

```json
{
  "success": true, "drawer_id": "…", "wing": "backend", "room": "Auth",
  "canonicalized": {
    "from_room": "auth", "to_room": "Auth", "wing": "backend",
    "reason": "folded into existing room with an equivalent name"
  }
}
```

The fold is only for normalization-equivalent names — genuinely different topics
are never merged here; those are left to the dream job's review flow. And a pair
you explicitly **dismissed** (see below) is honored: it is never auto-folded and
stays a separate room.

---

## Remembering decisions

Every merge decision is persisted in `room_merge_candidates.status`, so it sticks
across dream runs:

- **Applied** — creating a redirect (via `redirect_room` or
  `apply_merge_candidate`) marks the matching candidate `applied`, so the dream
  job never re-proposes an already-performed merge.
- **Dismissed** — a rejected proposal is marked `dismissed`: it drops out of
  `pending` lists, is not re-surfaced by later dream runs, is not auto-folded at
  write time, and the room **stays independent**.

---

## The "dream" consolidation job

Merging by hand finds the fragments you already know about. The **dream job**
(`cmd/dreamjob`) finds them for you: a separate microservice that scans the
palace's rooms on a schedule, detects near-duplicate rooms, and files merge
**proposals**. It **never merges anything itself** — a human or LLM reviews the
proposals over MCP and applies or dismisses each. Prevention (write-time follow),
cure (redirects), and now discovery (the dream) form the full loop.

### How it finds candidates

Two tiers, both within a single wing (same room name in different wings is
legitimately distinct):

1. **Exact** — name normalization (lowercase, unify `-`/`_`/whitespace). `Auth`,
   `auth`, `AUTH` collapse. Safe, deterministic.
2. **Semantic** — embeds room names and clusters by cosine similarity above
   `MEMPALACE_DREAM_THRESHOLD`. Catches `Auth` ≈ `Authentication`. If the
   embedding endpoint is unreachable the job falls back to exact-only.

The canonical target of each cluster is the room with the most drawers. Proposals
are written to the `room_merge_candidates` table, keyed by a **symmetric** hash of
the room pair — so at most one row ever exists per pair (`A→B` and `B→A` collapse,
even if the canonical direction flips between runs). Re-runs are idempotent and a
reviewer's decision (applied/dismissed) sticks.

### Its own container

The job ships as a **dedicated image** (`server/Dockerfile.dreamjob`), separate
from the server — it runs to completion and serves no HTTP.

- **Kubernetes:** [`k8s/dreamjob-cronjob.yaml`](./k8s/dreamjob-cronjob.yaml) runs
  it daily (`schedule: "0 3 * * *"`, `concurrencyPolicy: Forbid`). It reuses the
  server's DB + embedding env.
- **Local (docker compose):** guarded by a profile, so it does not start with
  `docker compose up`. Run on demand:

  ```bash
  docker compose run --rm dreamjob
  ```

Job-specific env:

| Variable | Default | Effect |
| --- | --- | --- |
| `MEMPALACE_DREAM_SEMANTIC` | `true` | Also cluster by embedding similarity (not just name normalization). |
| `MEMPALACE_DREAM_THRESHOLD` | `0.88` | Cosine cutoff for the semantic tier (higher = stricter). |

> The job does **not** need `MEMPALACE_ROOM_REDIRECTS` — it only writes
> proposals. The review/apply tools below live in the server and require that
> flag (they use the redirect machinery).

### Reviewing and applying proposals (over MCP)

These three tools are exposed when `MEMPALACE_ROOM_REDIRECTS=true`:

```jsonc
// tools/call → mempalace_list_merge_candidates   { "status": "pending" }
```

```json
{
  "candidates": [
    {
      "id": "9f1c2a7b3d4e5f60",
      "from_wing": "backend", "from_room": "Auth",
      "to_wing": "backend",   "to_room": "Authentication",
      "tier": "semantic", "score": 0.94, "from_drawers": 4,
      "status": "pending"
    }
  ],
  "count": 1
}
```

Apply one — it forwards the room, moves its drawers, and marks the candidate
`applied`:

```jsonc
// tools/call → mempalace_apply_merge_candidate   { "id": "9f1c2a7b3d4e5f60" }
```

```json
{
  "success": true,
  "redirect": { "from_room": "Auth", "to_room": "Authentication", "...": "..." },
  "drawers_moved": 4,
  "moved_to": { "wing": "backend", "room": "Authentication" },
  "candidate_id": "9f1c2a7b3d4e5f60"
}
```

Or reject it — the two rooms are genuinely different and should stay separate:

```jsonc
// tools/call → mempalace_dismiss_merge_candidate   { "id": "9f1c2a7b3d4e5f60" }
```

A dismissed pair stays out of future `pending` lists even if the next dream run
proposes it again.

---

## Notes & current limits

- **Alias-only mode:** pass `"move_drawers": false` to create the forwarding
  pointer without moving drawers — the old room keeps its contents and is
  resolved at read time.
- **Cycle-safe:** `redirect_room` rejects a redirect that would form a loop
  (e.g. `A → B` when `B` already resolves to `A`). Resolution is additionally
  bounded to 32 hops and breaks on any cycle.
- **Room-only filters** (a `room` without a `wing`) are **not** redirect-resolved
  — ambiguous across wings. Provide both `wing` and `room` to get resolution.
- **Tunnels and the AGE entity graph are not yet cascaded** on a merge; a tunnel
  pointing at an old room keeps its literal endpoint. Read-time resolution via
  `resolveRoom` is the first step here.
- **Temporal validity** (`valid_from` / `valid_to`) is not yet modeled — the
  redirect records `created_at` only. This is the natural extension point for
  time-scoped "where did this live last month?" queries.
