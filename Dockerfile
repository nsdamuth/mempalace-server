# ============================================================================
# MemPalace — HTTP MCP Server
# ============================================================================
# Multi-stage build: compile C extensions for chromadb in the build stage,
# copy only the final artifacts into the slim runtime image.
# ============================================================================

# --- Build stage ------------------------------------------------------------
FROM python:3.11-slim AS builder

WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# Copy the Python package
COPY mempalace/ /build/

# Install into a local prefix so we can copy it cleanly.
# [http]     → FastAPI + uvicorn
# [pgvector] → psycopg2-binary (optional; needed only when MEMPALACE_DB_URL is set)
RUN pip install --no-cache-dir --prefix=/install ".[http,pgvector]"


# --- Runtime stage ----------------------------------------------------------
FROM python:3.11-slim

WORKDIR /app

# Copy installed packages from build stage
COPY --from=builder /install /usr/local

# Palace data lives on a mounted PersistentVolume
ENV MEMPALACE_PALACE_PATH=/palace

# MCP_API_KEY must be injected via Kubernetes Secret (see k8s/secret.yaml)
# ENV MCP_API_KEY=  # do NOT hardcode secrets here

EXPOSE 8000

# Non-root user for security
RUN groupadd -r mempalace && useradd -r -g mempalace mempalace \
    && mkdir -p /palace \
    && chown -R mempalace:mempalace /palace

USER mempalace

ENTRYPOINT ["uvicorn", "mempalace.http_server:app", \
            "--host", "0.0.0.0", \
            "--port", "8000", \
            "--workers", "1"]
# workers=1: ChromaDB PersistentClient uses a local SQLite file.
# Multiple workers would require separate palace directories or a
# shared backend (PostgreSQL vector extension, Qdrant, etc.).
