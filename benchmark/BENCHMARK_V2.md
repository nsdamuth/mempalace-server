# Benchmark results — v2 (Minikube re-run, 2026-07-04)

A second capture of the MemPalace end-to-end suite, run against a freshly
deployed minikube stack. This round adds two axes that `RESULTS.md` (v1) did not
cover:

1. **Chat-model sweep** — the same three modes (`latency`, `sessions`, `tokens`)
   plus LongMemEval, run under two different agent models (`qwen3:4b` vs
   `qwen3.5:4b`).
2. **Knowledge-graph auto-population** (`MEMPALACE_GRAPH_AUTO_POPULATE=true`) —
   the `structural` (deterministic, no LLM) extractor vs the `llm` extractor.

> Single machine, single run each. The **shape** of each result is the point,
> not the third significant figure.

**Environment.** MemPalace Go server on minikube (Docker driver) + PostgreSQL 16
(pgvector + Apache AGE), Apple-silicon laptop. Embeddings: `embeddinggemma`
(768-dim) via Ollama on the host. Agent/judge model per run as noted below,
`temperature=0`. Corpus: 37 atomic facts, 40 probe questions.

---

## 0. Deployment findings (infra, not part of the suite)

Two things had to be fixed before the stack was usable — worth recording because
the setup script does not currently handle them:

- **Ollama must listen on `0.0.0.0`.** By default the macOS app binds
  `127.0.0.1:11434`, so the in-cluster pods cannot reach the embedding/LLM API
  via `host.minikube.internal`. Fixed with
  `launchctl setenv OLLAMA_HOST 0.0.0.0` + full app restart.
- **`minikube image build` fails for the Go server image.** It errors with
  `resolve : lstat /Users: no such file or directory` yet the setup script still
  prints `✓ Built`, so the pod lands in `ImagePullBackOff`. Worked around by
  building with the host Docker daemon and loading the result:
  `docker build -f server/Dockerfile -t mempalace-go:local . && minikube image load mempalace-go:local`.
  *(The postgres image, built without `-f`, is unaffected.)*

---

## 1. Chat-model sweep — `qwen3:4b` vs `qwen3.5:4b`

Same stack, same flags, only `GEN_MODEL` differs. Graph auto-population **off**.

| Flag | Metric | `qwen3:4b` | `qwen3.5:4b` | v1 (`sample-run.txt`) |
| --- | --- | --- | --- | --- |
| **latency** | retrieve mean | 103 ms | 98 ms | 95 ms |
| | **generate mean** | **10 341 ms** | **785 ms** | 787 ms |
| | retrieval share | 1 % | 11 % | 11 % |
| | answer correct | 40/40 (100 %) | 39/40 (97.5 %) | 39/40 (97.5 %) |
| **sessions** | recall@1 / @5 | 100 % / 100 % | 100 % / 100 % | 100 % / 100 % |
| | stateless agent | 5/35 (14.3 %) | 0/35 (0 %) | 0/35 (0 %) |
| | MemPalace agent | 35/35 (100 %) | 34/35 (97.1 %) | 34/35 (97.1 %) |
| **tokens** | indexing | 95 ms/card | 93 ms/card | 98 ms/card |
| | tokens saved / query | 675 (80 %) | 706 (80 %) | 706 (80 %) |
| | correct (full / card) | 100 % / 100 % | 95 % / 97.5 % | 95 % / 97.5 % |
| **longmemeval** | R@5 / R@10 (500 Q) | 0.970 / 0.988 | 0.970 / 0.988¹ | 0.972 / 0.988 |

¹ LongMemEval is retrieval-only (no chat model in the loop), so the result is
identical across agent models; it was run once.

**Read.**

- **`qwen3.5:4b` reproduces the v1 numbers almost to the millisecond**
  (generate 785 ms vs 787 ms) — it is the model behind the original
  `sample-run.txt`. The documented "retrieval ≈ 11 %, LLM ≈ 89 %" split holds.
- **`qwen3:4b` is ~13× slower to generate** (10.3 s vs 0.79 s) but **more
  accurate** (100 % vs 97.5 %). The slower model shrinks the *apparent* retrieval
  share to 1 %, which only reinforces the v1 conclusion that the storage/Go layer
  is not where wall-clock goes.
- **Retrieval quality is model-independent** — recall@k and LongMemEval are
  identical regardless of the agent model, as expected.

---

## 2. Knowledge-graph auto-population — `structural` vs `llm`

`MEMPALACE_GRAPH_AUTO_POPULATE=true`. `add_drawer` then also writes to the AGE
graph **synchronously**, so the effect shows up in **filing/indexing time**;
retrieval and generation (which use vector+FTS, not the graph) are unaffected.
Agent model held at `qwen3.5:4b`. Graph dropped + recreated empty before each
config so entity/relation counts are clean.

| Config | Filing (indexing) | Δ vs graph-off | Entities | Relations | Retrieval / correctness |
| --- | --- | --- | --- | --- | --- |
| graph **off** (baseline) | 93 ms/card | — | — | — | 98 ms · 97.5 % |
| **structural** (no LLM) | **111 ms/card** | +18 ms/card (~19 %) | **57** | **51** | 99 ms · 97.5 % |
| **llm** — openai provider + thinking model | ✗ times out | — | 0 | 0 | — (§2.1) |
| **llm** — ollama provider, `think=false`, `qwen3.5:4b` | **1410 ms/card** | +1317 ms/card (~15×) | **67** | **33** | 97 ms · 97.5 % |

**Read (structural).** The deterministic extractor adds a small, flat write-path
cost (~18 ms/card) and populates a real graph — **57 entities, 51 relations**
from 37 cards — with **zero impact on the read path** (retrieval 99 ms,
correctness 97.5 %, unchanged). Graph writes MERGE by name, so re-filing the
same corpus is idempotent.

### 2.1 The `llm` extractor does not work with a thinking model ⚠️

Running the `llm` extractor with `LLM_MODEL=qwen3.5:4b` **failed**, and the
failure is instructive:

- One extraction call to `/v1/chat/completions` took **84 seconds** and returned
  a reply full of reasoning prose ("*Wait, I'll stick closer to explicit
  text…*", "*Let's construct the final string.*"). `qwen3.5:4b` (and `qwen3:4b`)
  are **thinking models**.
- The server's LLM client (`server/internal/llm/client.go`) sends a **fixed**
  request body — no `think:false`, no reasoning-off switch — and `json_object`
  mode alone does **not** suppress thinking on the Ollama OpenAI endpoint.
- The client timeout is 60 s, so **every extraction hits `context deadline
  exceeded`** → the graph stays empty (0 entities / 0 relations).
- Because `populateGraph` runs **synchronously** inside `add_drawer`, the first
  filing blocks ~60 s, long enough that `kubectl port-forward` resets the idle
  connection → the benchmark crashes with `RemoteDisconnected`.

**Conclusion.** Neither `qwen3:4b` nor `qwen3.5:4b` is usable as the graph
extractor as-is — both "think" for ~80 s/call, which cannot be disabled without
a server code change (passing `think:false` / a reasoning-off flag from
`llm.Client`). A **non-thinking instruct model** is required.

> **Fixed** in §2.2 via a new `LLM_PROVIDER=ollama` mode that calls Ollama's
> native `/api/chat` with `think=false`. (A no-code alternative is to use a
> non-thinking instruct model such as `qwen2.5:3b` with the default `openai`
> provider.)

### 2.2 `llm` extractor made to work — new `LLM_PROVIDER` env var

The finding in §2.1 was fixed in the server so a thinking model *can* be used.
`server/internal/llm/client.go` now speaks two dialects, chosen by a new env var
`LLM_PROVIDER`:

- **`openai`** (default) — OpenAI `/v1/chat/completions`. Works with OpenAI,
  LM Studio, LocalAI and Ollama's compat layer, but **cannot suppress
  reasoning**, so it logs a note at startup: *use a non-thinking model*. Point
  `LLM_API_URL` at the base incl. `/v1`.
- **`ollama`** — Ollama-native `/api/chat` with `think=false` (+ `format:"json"`),
  which disables reasoning so thinking models work. Point `LLM_API_URL` at the
  Ollama root **without** `/v1`.

Re-running the `llm` extractor with `LLM_PROVIDER=ollama` + `LLM_MODEL=qwen3.5:4b`
(the exact model that failed in §2.1):

| Metric | Value |
| --- | --- |
| Filing (indexing) | **1410 ms/card** (52.2 s for 37 cards) |
| Graph populated | **67 entities, 33 relations** |
| Extraction per card | ~1.4 s, no timeouts (was ≥84 s + timeout before) |
| Read path (retrieve / correct) | 97 ms · 97.5 % — unchanged |

**Read.** With reasoning disabled, each extraction is ~1.4 s and the graph
populates cleanly. Compared to the deterministic `structural` extractor the LLM
is **~13× more expensive at write time** (1410 vs 111 ms/card) and produces a
**different** graph — more entities (67 vs 57), fewer relations (33 vs 51). The
LLM leans toward naming more distinct entities but is more conservative about
linking them; `structural` emits denser, deterministic edges. Neither touches
the read path: retrieval and answer correctness are identical to graph-off.

**Takeaway.** Use `structural` for cheap, deterministic, edge-dense graphs on
the write path; use `llm` (via `LLM_PROVIDER=ollama` so thinking models are
usable) when you want model-judged entities and can absorb ~1.4 s/card of filing
latency. Never use the `openai` provider with a reasoning model — it will time
out and populate nothing (§2.1).

---

## How to reproduce

```bash
# 1. bring up the stack (see §0 for the two required fixes)
./k8s/minikube-setup.sh
kubectl -n mempalace port-forward svc/mempalace 8000:80

export MP_KEY="$(kubectl -n mempalace get secret mempalace-secrets \
  -o jsonpath='{.data.api-key}' | base64 --decode)"

# 2. model sweep (§1)
GEN_MODEL=qwen3:4b   python3 mempalace_bench.py all
GEN_MODEL=qwen3.5:4b python3 mempalace_bench.py all
python3 longmemeval_adapter.py /path/longmemeval_s_cleaned.json --workers 8

# 3. graph auto-populate (§2) — set server env, restart pod, re-run
#    structural (deterministic, no LLM):
kubectl -n mempalace set env deploy/mempalace \
  MEMPALACE_GRAPH_AUTO_POPULATE=true MEMPALACE_GRAPH_EXTRACTOR=structural
#    llm via Ollama native /api/chat with think=false (thinking models OK):
kubectl -n mempalace set env deploy/mempalace \
  MEMPALACE_GRAPH_AUTO_POPULATE=true MEMPALACE_GRAPH_EXTRACTOR=llm \
  LLM_PROVIDER=ollama LLM_API_URL=http://host.minikube.internal:11434 LLM_MODEL=qwen3.5:4b
GEN_MODEL=qwen3.5:4b python3 mempalace_bench.py tokens   # filing cost
```

Entity/relation counts were read directly from AGE:

```sql
LOAD 'age'; SET search_path=ag_catalog,public;
SELECT count(*) FROM cypher('kg_mp_default', $$ MATCH (n:Entity) RETURN n $$)      AS (n agtype);
SELECT count(*) FROM cypher('kg_mp_default', $$ MATCH ()-[r]->() RETURN r $$)      AS (r agtype);
```
