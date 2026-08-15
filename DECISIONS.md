# DECISIONS.md — agro-agent

Architecture decisions & project constitution. Every entry answers: what we
chose, why, and what we traded away. Written for the next engineer (or the
interviewer) who reads this repository cold.

> [Español](./DECISIONS.es.md)

## AD-001 · Hexagonal architecture, ports & adapters

**Status:** accepted · **Scope:** whole repo

**Decision:** `internal/domain` (entities + sentinel errors) and
`internal/tenant`/`internal/identity` (context carriers) import nothing but
the standard library. Use cases (`internal/agent`, `internal/approval`,
`internal/eval`) depend only on ports (interfaces in `internal/store`,
`internal/llm`, `internal/embedding`). Adapters live in `internal/store/pg`,
`internal/approval/pg`, `internal/llm/gemini.go`.

**Why:** the orchestrator must not know Gemini exists; the HITL service must
not know pgx exists. Tests use fakes everywhere, which is what makes 60+
tests run with `go test ./...` and no testcontainers.

**Trade-off:** more files and indirection than a thin service. The payoff is
each slice (HTTP, HITL, RAG, evals) landed without touching the domain.

## AD-002 · Shared-schema tenancy (tenant_id column), not RLS

**Status:** accepted · **Scope:** database

**Decision:** every business table carries `tenant_id`; composite foreign keys
`(tenant_id, id)`; `UNIQUE (tenant_id, id)` on referenced tables. The tenant
is carried in the **context** (from the JWT claim) and injected by the
middleware — the LLM never provides it, and tools fail closed if it's absent.

**Why:** RLS (as in agro-iam) is the gold standard, but agro-agent's isolation
boundary is the *application*: the agent's tools query through the store
layer, which always filters by the context tenant. We chose the simpler model
and enforced it with composite FKs so a cross-tenant insert is impossible at
the constraint level.

**Trade-off:** a raw SQL session without RLS is not tenant-isolated. For this
project's threat model (the LLM is the attacker, not a compromised DBA) the
context-carried tenant plus constraint-level protection is the right size.

## AD-003 · Local JWT verification, byte-compatible with agro-iam

**Status:** accepted · **Scope:** auth

**Decision:** agro-agent *validates* HS256 JWTs issued by agro-iam
(`internal/auth/verifier.go`), expecting the same claims (`sub`,
`tenant_id`, `role`, `iat`, `exp`, 15 min TTL). It **never mints tokens** —
`cmd/mktoken` is dev-only. The verifier rejects empty `sub`/`tenant_id` and an
empty secret at boot.

**Why:** agro-iam is a separate module whose internals are not importable
(they live in `internal/`). Validating locally with a shared `JWT_SECRET` is
the real microservice pattern: zero coupling, no auth network call per
request.

**Trade-off:** a compromised `JWT_SECRET` breaks both services; rotation must
be coordinated. That's normal for HS256 shared-secret setups.

## AD-004 · Gemini model pinning + the thought_signature dance

**Status:** accepted · **Scope:** internal/llm

**Decision:** default chat model `gemini-3.6-flash`, temperature 0.2, pinned
(not "latest") for deterministic evals. When re-sending a `functionCall` to
Gemini, the adapter must preserve `Part.ThoughtSignature` — the helper
`resp.FunctionCalls()` **drops it**, so `gemini.go` iterates
`Candidates[0].Content.Parts` directly. Tool results go in role `"user"` as
`functionResponse` (Gemini has no `"tool"` role).

**Why:** models get retired for new keys (2.0/2.5-flash disappeared); pinning
avoids surprise breakage. The thought_signature requirement is an API contract
detail that silently 400s if missed.

**Trade-off:** pinned models eventually go stale; upgrading is a one-line
change plus an eval re-run.

## AD-005 · Embeddings: gemini-embedding-2, 768 dims via output dimensionality

**Status:** accepted · **Scope:** internal/embedding, db/schema.sql (v3)

**Decision:** `text-embedding-004` is not available for new keys. The current
model is `gemini-embedding-2` (3072 dims native) configured with
`OutputDimensionality: 768` so the column stays `vector(768)` and the HNSW
index stays small. The dimension constant lives in
`internal/embedding/gemini.go` and must match the migration.

**Why:** 768 dims is plenty for a handful of technical documents; 3072 would
quadruple storage and index size for no retrieval gain at this scale.

**Trade-off:** if the model changes, column + index + constant must change
together — documented in the migration and the adapter.

## AD-006 · RAG only for documents, never structured data

**Status:** accepted · **Scope:** internal/tools/buscar_documentos.go

**Decision:** the RAG tool searches only `documentos` (manuals, protocols,
reports). Structured data (lots, applications, yields) is served exclusively
by typed tools. `buscar_documentos` embeds the query **server-side** and
returns top-k by cosine similarity within the tenant, exposing
`filename`/`content`/`score` so the model can cite the source.

**Why:** two retrieval paths with overlapping data would create ambiguity in
the tool-calling decision and duplicate truth. Documents are prose — vectors
are the right tool; numbers are rows — SQL is the right tool.

**Trade-off:** a question answerable from both paths will only use one. The
eval harness's `protocolo-herbicidas` case pins which.

## AD-007 · HITL: opaque token, hash-only storage, full re-validation on approve

**Status:** accepted · **Scope:** internal/approval

**Decision:** write tools never mutate directly. `programar_aplicacion`
creates a `PENDIENTE` approval request with a token (32 random bytes, hex)
whose **SHA-256 hash** is persisted. Approve/reject requires the token
(timing-safe compare), a pending non-expired request (24h TTL), and RBAC
(`admin`/`agronomo`). On approve the service **re-validates** the context:
payload re-parsed with `DisallowUnknownFields`, lot/product/campaign resolved
inside the tenant, then insert (`planificada`) and mark `ejecutado`. Audit is
fail-open.

**Why:** the token is the "proof of human intent" — knowing it proves you saw
the request. Hash-only storage means a DB leak does not leak usable tokens.
Re-validation closes the time-of-check/time-of-use gap: the world may have
changed between creation and approval, so we check again at materialization.

**Trade-off:** no "approved but not executed" state yet — approve == execute.
A deferred worker is future work (state `aprobado` exists in the enum but is
unused).

## AD-008 · Evals: subsequence tool check + anti-hallucination assertions

**Status:** accepted · **Scope:** internal/eval

**Decision:** golden cases assert (1) expected tools appear **in order** as a
subsequence of the trace (exploration allowed), (2) required substrings
present (`MustContain` / `MustContainAny`), (3) banned substrings absent
(`MustNotContain`) to catch hallucinated numbers. Write cases are skipped by
default so eval runs are read-only. Tests use a scripted fake provider, so the
harness itself is deterministic; live runs measure routing accuracy.

**Why:** exact tool-match would punish legitimate exploration
(consult-lots-before-deciding). The anti-hallucination assertions encode the
product's core promise: grounded answers only.

**Trade-off:** substring checks are blunt — a verbose answer can fail on
formatting. The corpus is small and hand-picked from live-verified stories.

## AD-009 · Fail-closed parsing everywhere, fail-open audit

**Status:** accepted · **Scope:** all tools + approval service

**Decision:** every tool decodes JSON with `DisallowUnknownFields`; unknown
fields (e.g. a prompt-injected `tenant_id`) are rejected, never ignored. The
auditor, by contrast, is fail-open: if writing the audit row fails, the flow
continues with a WARN.

**Why:** the LLM's tool arguments are untrusted input — failing closed on
anything outside the contract is the cheap, correct default. Audit is
observability, not a business gate; it must never take the flow down.

**Trade-off:** fail-open audit means an audit outage is silent by design
(only a log line). Acceptable for this scale.

## AD-010 · One agent per request (OnEvent), immutable shared agent

**Status:** accepted · **Scope:** internal/agent, internal/httpapi

**Decision:** the composition root builds one `agent.Agent` with getters
(`Provider()`, `Registry()`, `MaxIterations()`) and no mutable event state.
The HTTP handler constructs a fresh agent per request with its own `OnEvent`
closure for SSE streaming.

**Why:** concurrent requests must not share mutable streaming state; an
immutable agent with per-request events is race-free by construction.

**Trade-off:** one extra allocation per request — negligible.

## Non-decisions (explicitly deferred)

- **Conversation persistence** (messages table exists in schema but no chat
  history is stored yet) — next slice.
- **`aprobado` (approved-but-not-executed) state** — a deferred worker would
  consume it.
- **Deployment** — render.com like agro-iam; the Dockerfile/compose is ready,
  the free-tier Gemini daily quota is the constraint.
- **Deployable demo frontend** — agro-iam already demonstrates the SPA
  pattern; agro-agent stays API-first.