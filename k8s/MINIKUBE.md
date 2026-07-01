# Running MemPalace on minikube

This guide gets a full MemPalace stack — the Go server plus PostgreSQL
(pgvector + Apache AGE) — running on a local [minikube](https://minikube.sigs.k8s.io)
cluster. It's meant for local development and kicking the tires on the
Kubernetes manifests; for a real deployment see the production manifests in
[`k8s/`](.) and the [README](../README.md#deploy-to-kubernetes).

The fastest path is the bundled script:

```bash
./k8s/minikube-setup.sh
```

The rest of this document explains what that script does and how to do it by
hand.

---

## What you get

```
                 host machine
   ┌───────────────────────────────────────┐
   │  Ollama (embeddinggemma) :11434        │
   └───────────────▲───────────────────────┘
                   │ host.minikube.internal
   ┌───────────────┼───────────────────────┐
   │  minikube     │            namespace: mempalace
   │      ┌────────┴─────────┐   ┌────────────────────────┐
   │      │ Deployment       │   │ StatefulSet            │
   │      │ mempalace (Go)   │──►│ mempalace-db           │
   │      │ :8000  ▲         │   │ Postgres + pgvector    │
   │      └────────┼─────────┘   │ + Apache AGE  :5432    │
   │   Service mempalace :80     └────────────────────────┘
   └───────────────┼───────────────────────┘
                   │ kubectl port-forward
              localhost:8000
```

- **mempalace** — the stateless Go server (1 replica).
- **mempalace-db** — PostgreSQL with the `pgvector` and `age` extensions,
  backed by a 10Gi PersistentVolumeClaim.
- The embedding API is **not** run in the cluster. The server reaches an
  embedding API on your host machine (Ollama by default).

---

## Prerequisites

| Tool | Notes |
| --- | --- |
| [minikube](https://minikube.sigs.k8s.io/docs/start/) | with the Docker driver (default on most machines) |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | v1.27+ |
| [Ollama](https://ollama.com) (or any OpenAI-compatible embedding API) | on the host, port `11434` |

Pull the embedding model and serve it **on all interfaces** so the cluster can
reach it through `host.minikube.internal`:

```bash
ollama pull embeddinggemma
OLLAMA_HOST=0.0.0.0 ollama serve
```

> If Ollama only listens on `127.0.0.1`, pods inside minikube cannot connect to
> it. Binding to `0.0.0.0` is what makes `host.minikube.internal` work.

---

## Quick start (scripted)

```bash
# from the repo root
./k8s/minikube-setup.sh
```

The script is idempotent — run it again to rebuild the images and re-apply
after a code change. On success it prints your generated API key and the
port-forward command.

```bash
# expose the server on localhost (keep this running)
kubectl -n mempalace port-forward svc/mempalace 8000:80

# in another terminal — health check needs no auth
curl http://localhost:8000/mp/mcp/health
```

Tear everything down (keeps the cluster itself):

```bash
./k8s/minikube-setup.sh --delete
```

---

## What the script does (manual walkthrough)

If you'd rather run the steps yourself, here they are.

### 1. Start minikube

```bash
minikube start
```

### 2. Build both images straight into minikube

`minikube image build` builds inside the cluster's container runtime, so there
is **no registry and no `docker push`**. The PostgreSQL image compiles Apache
AGE from source, so the first build takes a few minutes.

```bash
minikube image build -t mempalace-postgres:local k8s/postgres
minikube image build -t mempalace-go:local       server
```

### 3. Create the namespace and a Secret

The production `k8s/secret.yaml` is a template with placeholders. For local use,
generate fresh credentials and create the Secret imperatively. Hex output is
used for the password because it ends up inside the connection URL.

```bash
kubectl apply -f k8s/namespace.yaml

DB_PASS=$(openssl rand -hex 24)
API_KEY=$(openssl rand -hex 32)

kubectl -n mempalace create secret generic mempalace-secrets \
  --from-literal=api-key="$API_KEY" \
  --from-literal=db-password="$DB_PASS" \
  --from-literal=db-url="postgres://mempalace:${DB_PASS}@mempalace-db.mempalace.svc.cluster.local:5432/mempalace"

echo "Your API key: $API_KEY"
```

### 4. Apply the manifests and the local patches

Apply the production manifests (minus the Ingress, the template Secret, and the
unused legacy `pvc.yaml`), then patch in the local-only adjustments.

```bash
kubectl apply \
  -f k8s/db-pvc.yaml \
  -f k8s/db-service.yaml \
  -f k8s/db-statefulset.yaml \
  -f k8s/service.yaml \
  -f k8s/deployment.yaml

# point the workloads at the locally built images
kubectl -n mempalace set image statefulset/mempalace-db postgres=mempalace-postgres:local
kubectl -n mempalace set image deployment/mempalace      mempalace=mempalace-go:local

# apply the minikube tweaks (see k8s/minikube/*.yaml)
kubectl -n mempalace patch statefulset mempalace-db --patch-file k8s/minikube/statefulset-patch.yaml
kubectl -n mempalace patch deployment  mempalace    --patch-file k8s/minikube/deployment-patch.yaml
```

The two patch files in [`k8s/minikube/`](minikube/) change only what's needed
for a local cluster:

- **`statefulset-patch.yaml`** — `imagePullPolicy: IfNotPresent` and
  `command: ["postgres", "-c", "shared_preload_libraries=age"]`. **Apache AGE
  must be preloaded**, or the entity graph fails to load. (docker-compose sets
  the same flag.)
- **`deployment-patch.yaml`** — `imagePullPolicy: IfNotPresent`,
  `replicas: 1`, `EMBED_API_URL=http://host.minikube.internal:11434/v1`, and
  `ENABLE_REST_API=true` so you can also poke the REST API with curl.

### 5. Wait for it to come up

```bash
kubectl -n mempalace rollout status statefulset/mempalace-db
kubectl -n mempalace rollout status deployment/mempalace
```

---

## Using it

```bash
kubectl -n mempalace port-forward svc/mempalace 8000:80
```

| Endpoint | Method | Auth |
| --- | --- | --- |
| `http://localhost:8000/mp/mcp/health` | GET | none |
| `http://localhost:8000/mp/mcp` | POST | `Authorization: Bearer <API_KEY>` |
| `http://localhost:8000/mp/api/v1/...` | REST | `Authorization: Bearer <API_KEY>` (enabled in the patch) |

Retrieve the generated API key any time:

```bash
kubectl -n mempalace get secret mempalace-secrets \
  -o jsonpath='{.data.api-key}' | base64 --decode; echo
```

Point your MCP client at `http://localhost:8000/mp/mcp` with that key — see
[MCP_USAGE.md](../MCP_USAGE.md).

---

## Troubleshooting

**Server pod is `CrashLoopBackOff` or not ready**

```bash
kubectl -n mempalace logs deploy/mempalace
```

- *Embedding API errors / connection refused* — Ollama isn't reachable. Confirm
  it runs with `OLLAMA_HOST=0.0.0.0 ollama serve` and that the model is pulled
  (`ollama list` should show `embeddinggemma`).

**Database pod won't start, or AGE errors in the server logs**

```bash
kubectl -n mempalace logs statefulset/mempalace-db
```

- Make sure the StatefulSet picked up the `shared_preload_libraries=age` patch:

  ```bash
  kubectl -n mempalace get statefulset mempalace-db \
    -o jsonpath='{.spec.template.spec.containers[0].command}'; echo
  ```

**`ErrImagePull` / `ImagePullBackOff`**

The local images weren't built into minikube, or the pull policy wasn't
patched. Re-run `./k8s/minikube-setup.sh`, or verify the images exist:

```bash
minikube image ls | grep mempalace
```

**Start over completely**

```bash
./k8s/minikube-setup.sh --delete   # remove just the app
minikube delete                    # or nuke the whole cluster
```
