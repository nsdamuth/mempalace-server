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

`mempalace_list_rooms` additionally surfaces active redirects (flagged), so a
merged room name stays visible even after its drawers moved away.

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
