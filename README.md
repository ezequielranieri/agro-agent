# agro-agent

Multi-tenant AI backend for agricultural cooperatives: a **tool-calling agent**
over real PostgreSQL data, with **human-in-the-loop** for write actions,
**RAG over technical documents** (pgvector), and a **golden-set eval harness**.
A learning/portfolio project demonstrating **Clean / Hexagonal Architecture**,
defense-in-depth prompt-injection controls, and deterministic, verifiable agent
behavior in idiomatic Go.

> [Español](./README.es.md)

> Documentation: [DECISIONS.md](./DECISIONS.md) — architecture decisions & project constitution

## The problem

A cooperative's agronomists should be able to ask questions in natural
language — "which lots have delayed applications?", "summarize the last 30
days", "what's the protocol for applying herbicides to wheat?" — and get
answers grounded in **real data**, never in the model's memory.

But the agent also wants to *write*: "schedule a glyphosate application on lot
4". An AI that can mutate production data directly is a liability. Agro-agent
solves it with a durable **human-in-the-loop** flow: the agent creates a
pending approval request with an opaque token; an agronomist approves via HTTP
presenting the token; only then is the application inserted — after full
re-validation of the context.

## Architecture

```
┌────────────────────────────────────────────────────────────────────┐
│                    HTTP layer (stdlib net/http)                    │
│  server.go · middleware.go (JWT auth, tenant, role, SSE) · handlers │
└───────────────▲─────────────────────────────────▲──────────────────┘
                │                                 │
                │                llm.Provider     │
                │               (Gemini adapter)  │
┌───────────────┴─────────────────────────────────┴──────────────────┐
│                    Application layer (use cases)                   │
│  internal/agent    — orchestrator: tool-calling loop, max-iter,        │
│                      domain router (discernment)                       │
│  internal/router   — deterministic query classifier: data vs documents │
│  internal/approval — HITL service: create/approve/reject, TTL,          │
│                      token (hash-only), context re-validation          │
│  internal/embedding— RAG: Embedder port (Gemini)                    │
│  internal/eval     — golden-set runner + summary                    │
└───────────────▲─────────────────────────────────▲──────────────────┘
                │                                 │
┌───────────────┴─────────────────────────────────┴──────────────────┐
│                  Infrastructure layer (adapters)                   │
│  store/pg     — lotes, aplicaciones, rendimientos, documentos       │
│                (pgvector HNSW cosine search)                        │
│  approval/pg  — approval store, resolvers, application writer,      │
│                auditor (fail-open)                                  │
│  llm          — Gemini chat + embeddings (thought_signature,        │
│                output dimensionality)                               │
└────────────────────────────────────────────────────────────────────┘

Domain layer (internal/domain, internal/tenant, internal/identity):
pure entities + tenant/actor carried in the context, ZERO dependencies.
```

```
cmd/
├── api/     composition root: HTTP backend (JWT + SSE + approvals)
├── demo/    one-shot CLI demo against real Postgres + Gemini
├── embed/   idempotent document embedding indexer
├── eval/    runs the golden set and reports PASS/FAIL
└── mktoken/ dev-only JWT issuer (agro-agent never mints tokens)
internal/    ports (interfaces) + adapters, hexagonal
db/          schema.sql (v1+v2+v3) · seed.sql · migrations/
```

## Tools (the product)

| Tool | Kind | Purpose |
|---|---|---|
| `consultar_lotes` | read | lots, optionally by campaign/crop |
| `consultar_aplicaciones` | read | applications by lot/campaign/period/status |
| `consultar_rendimientos` | read | yields per lot-campaign |
| `resumir_aplicaciones` | read | 30-day summary |
| `detectar_retrasos` | read | planned applications past their date |
| `buscar_documentos` | read (RAG) | pgvector cosine search over technical docs |
| `programar_aplicacion` | **write (HITL)** | creates a pending approval request — never inserts directly |
| `consultar_aprobaciones` | read | approval request states |

## Human-in-the-loop (the differentiator)

1. The agent calls `programar_aplicacion` → creates a `PENDIENTE` approval
   request with an **opaque token** (32 random bytes; only its SHA-256 hash is
   persisted).
2. An `admin`/`agronomo` approves via `POST /api/v1/approvals/{id}/approve`
   presenting the token. A `productor` gets **403**.
3. On approve, the service **re-validates** everything: state still pending,
   not expired (24h TTL), payload re-parsed (fail-closed), lot/product/campaign
   resolved **inside the tenant** — then inserts the application
   (`planificada`) and marks the request `ejecutado`.
4. Every step is audited (`approval.crear` / `approval.aprobar`) with a
   fail-open auditor: auditing must never take the flow down.

The agent loop never changes: the human decides **materialization**, the AI
proposes. This is the "AI proposes, human disposes" pattern.

## RAG

Documents (manuals, protocols, campaign reports) are indexed into
`documentos.embedding vector(768)` via `cmd/embed` (idempotent). The
`buscar_documentos` tool embeds the user's query **server-side** and returns
the top-k by cosine similarity **inside the tenant** — the LLM can cite the
source file, and structured data never leaks into document search (nor vice
versa).

## Discernment router

The agent does not hand the LLM every tool on each iteration. A deterministic
classifier (`internal/router`, keyword rules over the normalized query —
lowercase, accent-insensitive, full-word matching) decides the **domain** of
the question — `datos` (DB) vs `documentos` (RAG) — and the orchestrator
exposes **only the tools of those domains**. Every tool declares its domain
(`tools.Dominio`); descriptions reinforce the same boundary ("use X, NOT Y").
It is a **bias, not a barrier**: uncertain queries get all tools, and a
router failure degrades to today's behavior. Result: fewer misrouted calls,
less noise in the LLM context, lower cost — and a measurable guarantee via
eval `ForbiddenTools` (must not call RAG for data questions, and vice versa).

## Evals

`cmd/eval` runs a golden set (`internal/eval/cases.go`): each case checks the
expected tool appears **in order** (subsequence, allowing exploration) or —
for hybrid questions — that **all required tools** appear in any order, the
answer contains required real data, **forbidden tools are never called**
(discernment: data questions must not trigger RAG, document questions must
not trigger data tools), and — critically — **does not contain hallucinated
numbers**. Cases that write (HITL) are skipped by default so
eval runs are read-only. The harness is deterministic in tests (scripted fake
provider) and measures routing accuracy + anti-hallucination against the real
LLM.

## Security model

| Concern | Mechanism |
|---|---|
| Tenant isolation | `tenant_id` on every table + composite FKs `(tenant_id, id)`; tenant comes from the **context** (JWT claim), never from LLM input |
| Prompt injection | Every tool parses params with `json.Decoder` + `DisallowUnknownFields` — unknown fields fail closed |
| Write authority | HITL tokens: opaque 32-byte random, stored as SHA-256 only, timing-safe compare |
| Context re-validation | Approve re-parses the payload and re-resolves IDs inside the tenant before inserting |
| Model behavior | Temperature 0.2, system prompt forbids inventing data, eval harness enforces it |
| Auth | HS256 JWT verified locally (`sub`/`tenant_id`/`role`, 15 min TTL); the demo issues tokens with `cmd/mktoken`, same HS256/JWT shape as agro-iam. Live agro-iam wiring is **not** implemented (UUID tenant ids, English role codes) — see [Integration with agro-iam](#integration-with-agro-iam). agro-agent never mints tokens on its own |

## Quickstart

Requires: Go 1.26+, Docker with Compose.

```bash
# 1. start PostgreSQL (pgvector) — schema + seed applied automatically
docker compose up -d

# 2. configure
cp .env.example .env   # set GEMINI_API_KEY, JWT_SECRET

# 3. index documents for RAG (idempotent)
go run ./cmd/embed

# 4. run the API
go run ./cmd/api

# sanity check
curl http://localhost:8080/healthz   # -> ok
```

### One-shot CLI demo

```bash
GEMINI_API_KEY=... go run ./cmd/demo "¿Hay lotes con retraso en las aplicaciones planificadas?"
GEMINI_API_KEY=... go run ./cmd/demo "¿Cuál es el protocolo recomendado para aplicar herbicidas en trigo?"
```

### Eval harness

```bash
GEMINI_API_KEY=... go run ./cmd/eval            # read-only (skips HITL cases)
GEMINI_API_KEY=... go run ./cmd/eval --writes   # include write cases
```

### API

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | none | liveness (plain `ok`) |
| `POST` | `/api/v1/chat` | Bearer JWT | chat (JSON, or SSE with `Accept: text/event-stream`); rate-limited per IP (default 10 req/min, `CHAT_RATE_LIMIT`) |
| `GET` | `/api/v1/approvals?status=` | Bearer JWT | list approval requests |
| `POST` | `/api/v1/approvals/{id}/approve` | Bearer JWT, admin/agronomo | approve with token |
| `POST` | `/api/v1/approvals/{id}/reject` | Bearer JWT, admin/agronomo | reject |
| `GET` | `/api/v1/lotes` | Bearer JWT | list lots |
| `GET` | `/api/v1/aplicaciones` | Bearer JWT | list applications |

Dev token (never in production): `JWT_SECRET=... go run ./cmd/mktoken -tenant 1 -user 2 -role agronomo`
(the tool self-verifies the token against the real backend verifier before printing).

### Integration with agro-iam

Honest status: agro-agent authenticates against the **same HS256/JWT format**
agro-iam uses (`sub`/`tenant_id`/`role`, 15 min TTL), so the demo flow works
with `cmd/mktoken`-issued tokens and the shapes are deliberately compatible.
**Live agro-iam wiring is NOT implemented**: agro-iam uses UUID `tenant_id`s
and English role codes (`agronomist`/`producer`/...), while agro-agent expects
an integer `tenant_id` and Spanish roles (`admin`/`agronomo`/`productor`). A
real agro-iam token would fail verification or role checks here today. Closing
that gap (UUID tenant parsing + role vocabulary alignment) is future work; the
auth code itself (`internal/auth`, `requireAuth`) is intentionally unchanged.

### LLM quota

The free tier of Gemini is 5 requests/minute and 20/day. The agent can call
the LLM up to 5 times per request (tool-calling loop) and the chat endpoint is
rate-limited per IP (default 10 req/min, `CHAT_RATE_LIMIT`), but at peak the
daily quota can still be exhausted — expect `429`/`RESOURCE_EXHAUSTED` errors
that the provider layer retries once (bounded) before surfacing.

## Testing

Plain `go test ./...` — no testcontainers, no testify. Unit tests use fakes
(scripted LLM provider, fake stores) and always run. Integration tests
(`internal/store/pg`, `internal/approval`) are **gated** behind `AGRO_TEST_DB`
and skip by default — they run only when the env var points to the compose
database:

```bash
AGRO_TEST_DB="postgres://postgres:postgres@localhost:5432/agro" go test ./internal/store/pg ./internal/approval -v
```

All green today: build + vet + 60+ tests.

## Configuration (env)

| Variable | Default | Purpose |
|---|---|---|
| `AGRO_DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/agro` | pgx DSN |
| `GEMINI_API_KEY` | — (required) | Gemini API key (chat + embeddings) |
| `GEMINI_EMBED_MODEL` | `gemini-embedding-2` | embeddings model (768 dims via output dimensionality) |
| `JWT_SECRET` | — (required) | HS256 key for demo JWT (`cmd/mktoken`); the value `change-me` is rejected at startup |
| `PORT` | `8080` | HTTP bind port |

## Roadmap

- [x] Schema v1 + seed — 13 tables, multi-tenant, demo stories
- [x] 5 read tools + ports/adapters + orchestrator + Gemini adapter
- [x] HTTP API — JWT auth, tenant isolation, chat JSON + SSE streaming
- [x] HITL — approval requests, opaque tokens, RBAC, re-validation, audit
- [x] RAG — pgvector, `buscar_documentos`, `cmd/embed`
- [x] Discernment router — deterministic domain classifier + filtered tool exposure
- [x] Evals — golden set, tool routing + anti-hallucination + discernment harness
- [ ] Eval live run (free-tier daily quota)
- [ ] Deployment (render.com, like agro-iam)
- [ ] Live agro-iam wiring — UUID tenant ids + role vocabulary alignment

## License

Learning/portfolio project — no warranty.