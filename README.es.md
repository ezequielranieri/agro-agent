# agro-agent

Backend de AI multi-tenant para cooperativas agropecuarias: un **agente con
tool calling** sobre datos reales de PostgreSQL, con **human-in-the-loop**
para acciones de escritura, **RAG sobre documentos técnicos** (pgvector) y un
**harness de evals** con golden set. Proyecto de aprendizaje/portfolio que
demuestra **Arquitectura Limpia / Hexagonal**, controles de inyección de
prompt en profundidad y comportamiento de agente determinista y verificable en
Go idiomático.

> [English](./README.md)

> Documentación: [DECISIONS.es.md](./DECISIONS.es.md) — decisiones de arquitectura y constitución del proyecto

## El problema

Los agrónomos de una cooperativa deberían poder preguntar en lenguaje natural
— "¿qué lotes tienen aplicaciones con retraso?", "resumí los últimos 30 días",
"¿cuál es el protocolo para aplicar herbicidas en trigo?" — y obtener
respuestas ancladas en **datos reales**, jamás en la memoria del modelo.

Pero el agente también quiere *escribir*: "programá una aplicación de
glifosato en el lote 4". Una AI que muta datos de producción directamente es
una responsabilidad. Agro-agent lo resuelve con un flujo durable de
**human-in-the-loop**: el agente crea una solicitud de aprobación pendiente
con un token opaco; un agrónomo la aprueba vía HTTP presentando el token;
recién entonces se inserta la aplicación — tras re-validar el contexto por
completo.

## Arquitectura

```
┌────────────────────────────────────────────────────────────────────┐
│                    Capa HTTP (stdlib net/http)                     │
│  server.go · middleware.go (JWT, tenant, rol, SSE) · handlers      │
└───────────────▲─────────────────────────────────▲──────────────────┘
                │                                 │
                │                llm.Provider     │
                │               (adapter Gemini)  │
┌───────────────┴─────────────────────────────────┴──────────────────┐
│                    Capa de aplicación (casos de uso)               │
│  internal/agent    — orquestador: loop de tool calling, max-iter   │
│  internal/approval — servicio HITL: crear/aprobar/rechazar, TTL,   │
│                      token (solo hash), re-validación de contexto  │
│  internal/embedding— RAG: puerto Embedder (Gemini)                 │
│  internal/eval     — runner del golden set + resumen               │
└───────────────▲─────────────────────────────────▲──────────────────┘
                │                                 │
┌───────────────┴─────────────────────────────────┴──────────────────┐
│                  Capa de infraestructura (adapters)                │
│  store/pg     — lotes, aplicaciones, rendimientos, documentos      │
│                (búsqueda cosena HNSW con pgvector)                 │
│  approval/pg  — store de solicitudes, resolvers, writer de         │
│                aplicaciones, auditor (fail-open)                   │
│  llm          — chat + embeddings Gemini (thought_signature,       │
│                dimensionalidad de salida)                          │
└────────────────────────────────────────────────────────────────────┘

Capa de dominio (internal/domain, internal/tenant, internal/identity):
entidades puras + tenant/actor en el contexto, CERO dependencias.
```

```
cmd/
├── api/     raíz de composición: backend HTTP (JWT + SSE + approvals)
├── demo/    demo CLI de un solo uso contra Postgres + Gemini reales
├── embed/   indexador idempotente de embeddings de documentos
├── eval/    corre el golden set y reporta PASS/FAIL
└── mktoken/ emisor de JWT solo para desarrollo (agro-agent nunca emite tokens)
internal/    puertos (interfaces) + adapters, hexagonal
db/          schema.sql (v1+v2+v3) · seed.sql · migrations/
```

## Tools (el producto)

| Tool | Tipo | Propósito |
|---|---|---|
| `consultar_lotes` | lectura | lotes, opcional por campaña/cultivo |
| `consultar_aplicaciones` | lectura | aplicaciones por lote/campaña/período/estado |
| `consultar_rendimientos` | lectura | rendimientos por lote-campaña |
| `resumir_aplicaciones` | lectura | resumen de 30 días |
| `detectar_retrasos` | lectura | planificadas vencidas |
| `buscar_documentos` | lectura (RAG) | búsqueda cosena pgvector en documentos técnicos |
| `programar_aplicacion` | **escritura (HITL)** | crea una solicitud pendiente — nunca inserta directo |
| `consultar_aprobaciones` | lectura | estados de las solicitudes |

## Human-in-the-loop (el diferenciador)

1. El agente llama `programar_aplicacion` → crea una solicitud `PENDIENTE`
   con un **token opaco** (32 bytes aleatorios; solo se persiste su hash
   SHA-256).
2. Un `admin`/`agronomo` aprueba vía `POST /api/v1/approvals/{id}/approve`
   presentando el token. Un `productor` recibe **403**.
3. Al aprobar, el servicio **re-valida** todo: la solicitud sigue pendiente,
   no venció (TTL 24 h), el payload se re-parsea (fail-closed),
   lote/producto/campaña se resuelven **dentro del tenant** — recién entonces
   inserta la aplicación (`planificada`) y marca la solicitud `ejecutado`.
4. Cada paso queda auditado (`approval.crear` / `approval.aprobar`) con un
   auditor fail-open: auditar jamás debe tumbar el flujo.

El loop del agente no cambia: el humano decide la **materialización**, la AI
propone. Es el patrón "la AI propone, el humano dispone".

## RAG

Los documentos (manuales, protocolos, informes de campaña) se indexan en
`documentos.embedding vector(768)` vía `cmd/embed` (idempotente). La tool
`buscar_documentos` embeddee la consulta del usuario **del lado del servidor**
y devuelve el top-k por similitud cosena **dentro del tenant** — el LLM puede
citar el archivo de origen, y los datos estructurados nunca se filtran a la
búsqueda documental (ni viceversa).

## Evals

`cmd/eval` corre un golden set (`internal/eval/cases.go`): cada caso verifica
que la tool esperada aparezca **en orden** (subsecuencia, permite
exploración), que la respuesta contenga los datos reales requeridos y — lo
crítico — **no contenga números alucinados**. Los casos que escriben (HITL) se
saltean por defecto para que las corridas de eval sean read-only. El harness
es determinista en tests (provider fake con guion) y mide precisión de routing
+ anti-alucinación contra el LLM real.

## Modelo de seguridad

| Preocupación | Mecanismo |
|---|---|
| Aislamiento de tenant | `tenant_id` en todas las tablas + FKs compuestas `(tenant_id, id)`; el tenant sale del **contexto** (claim JWT), nunca del input del LLM |
| Inyección de prompt | Cada tool parsea params con `json.Decoder` + `DisallowUnknownFields` — campos desconocidos fallan cerrado |
| Autoridad de escritura | Tokens HITL: 32 bytes aleatorios opacos, guardados solo como SHA-256, comparación timing-safe |
| Re-validación de contexto | Aprobar re-parsea el payload y re-resuelve los IDs dentro del tenant antes de insertar |
| Comportamiento del modelo | Temperatura 0.2, prompt de sistema que prohíbe inventar datos, harness de evals que lo impone |
| Auth | JWT HS256 verificado localmente, byte-compatible con los claims de agro-iam (`sub`/`tenant_id`/`role`, TTL 15 min); agro-agent nunca emite tokens |

## Inicio rápido

Requiere: Go 1.26+, Docker con Compose.

```bash
# 1. arrancar PostgreSQL (pgvector) — schema + seed se aplican solos
docker compose up -d

# 2. configurar
cp .env.example .env   # completar GEMINI_API_KEY, JWT_SECRET

# 3. indexar documentos para el RAG (idempotente)
go run ./cmd/embed

# 4. correr la API
go run ./cmd/api

# sanity check
curl http://localhost:8080/healthz   # -> {"status":"ok"}
```

### Demo CLI de un solo uso

```bash
GEMINI_API_KEY=... go run ./cmd/demo "¿Hay lotes con retraso en las aplicaciones planificadas?"
GEMINI_API_KEY=... go run ./cmd/demo "¿Cuál es el protocolo recomendado para aplicar herbicidas en trigo?"
```

### Harness de evals

```bash
GEMINI_API_KEY=... go run ./cmd/eval            # read-only (saltea casos HITL)
GEMINI_API_KEY=... go run ./cmd/eval --writes   # incluir casos de escritura
```

### API

| Método | Ruta | Auth | Propósito |
|---|---|---|---|
| `GET` | `/healthz` | ninguna | liveness |
| `POST` | `/api/v1/chat` | Bearer JWT | chat (JSON, o SSE con `Accept: text/event-stream`) |
| `GET` | `/api/v1/approvals?status=` | Bearer JWT | listar solicitudes |
| `POST` | `/api/v1/approvals/{id}/approve` | Bearer JWT, admin/agronomo | aprobar con token |
| `POST` | `/api/v1/approvals/{id}/reject` | Bearer JWT, admin/agronomo | rechazar |

Token de desarrollo (nunca en producción): `JWT_SECRET=... go run ./cmd/mktoken -tenant 1 -user 2 -role agronomo`
(el tool se auto-verifica el token contra el verifier real del backend antes de imprimirlo).

## Testing

`go test ./...` simple — sin testcontainers, sin testify. Los unit tests usan
fakes (proveedor LLM con guion, stores falsos). Los tests de integración
(`internal/store/pg`, `internal/approval`) corren contra la base de compose
cuando está levantada, con el DSN por defecto. Todo verde hoy: build + vet +
60+ tests.

## Configuración (env)

| Variable | Default | Propósito |
|---|---|---|
| `AGRO_DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/agro` | DSN pgx |
| `GEMINI_API_KEY` | — (requerida) | clave API Gemini (chat + embeddings) |
| `GEMINI_EMBED_MODEL` | `gemini-embedding-2` | modelo de embeddings (768 dims vía dimensionalidad de salida) |
| `JWT_SECRET` | — (requerida) | clave HS256 compartida con agro-iam |
| `PORT` | `8080` | puerto HTTP |

## Roadmap

- [x] Schema v1 + seed — 13 tablas, multi-tenant, historias demo
- [x] 5 tools de lectura + puertos/adapters + orquestador + adapter Gemini
- [x] HTTP API — auth JWT, aislamiento de tenant, chat JSON + streaming SSE
- [x] HITL — solicitudes de aprobación, tokens opacos, RBAC, re-validación, auditoría
- [x] RAG — pgvector, `buscar_documentos`, `cmd/embed`
- [x] Evals — golden set, harness de routing + anti-alucinación
- [ ] Corrida live del eval (cuota diaria free tier) + prueba de discernimiento
- [ ] Deploy (render.com, como agro-iam)

## Licencia

Proyecto de aprendizaje/portfolio — sin garantía.