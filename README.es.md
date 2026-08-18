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

> Frontend: [agro-web](https://github.com/ezequielranieri/agro-web) — UI de chat, lotes y aprobaciones HITL en Next.js (ver la [ingesta accept-both](#integración-con-agro-iam) para cómo este ecosistema reutiliza la identidad de agro-iam)

## Capturas

<p align="center">
  <img src="docs/screenshots/dashboard.jpeg" alt="Vista general del dashboard" width="720"/>
  <img src="docs/screenshots/chat.jpeg" alt="Chat con el agente" width="720"/>
</p>

> Agregá más capturas en `docs/screenshots/` (por ejemplo `approvals.png`,
> `rag.png`) y referencialas acá.

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

## Router de discernimiento

El agente no le entrega al LLM todas las tools en cada iteración. Un
clasificador determinista (`internal/router`, reglas de keywords sobre la
consulta normalizada — minúsculas, sin acentos, match por palabra completa)
decide el **dominio** de la pregunta — `datos` (DB) vs `documentos` (RAG) —
y el orquestador expone **solo las tools de esos dominios**. Cada tool
declara su dominio (`tools.Dominio`); las descripciones refuerzan la misma
frontera ("usá X, NO Y"). Es un **sesgo, no una barrera**: las consultas
inciertas reciben todas las tools y un fallo del router degrada al
comportamiento actual. Resultado: menos llamadas mal enrutadas, menos ruido
en el contexto del LLM, menor costo — y una garantía medible vía el
`ForbiddenTools` del eval (no llamar al RAG para preguntas de datos, y
viceversa).

## Evals

`cmd/eval` corre un golden set (`internal/eval/cases.go`): cada caso verifica
que la tool esperada aparezca **en orden** (subsecuencia, permite
exploración) o — para preguntas híbridas — que **todas las tools requeridas**
aparezcan en cualquier orden, que la respuesta contenga los datos reales
requeridos, que **nunca se llamen tools prohibidas** (discernimiento: las
preguntas de datos no deben disparar el RAG, las documentales no deben
disparar tools de datos) y — lo crítico — **no contenga números alucinados**.
Los casos que escriben (HITL) se saltean por defecto para que las corridas de
eval sean read-only. El harness es determinista en tests (provider fake con
guion) y mide precisión de routing + anti-alucinación + discernimiento contra
el LLM real.

## Modelo de seguridad

| Preocupación | Mecanismo |
|---|---|
| Aislamiento de tenant | `tenant_id` en todas las tablas + FKs compuestas `(tenant_id, id)`; el tenant sale del **contexto** (claim JWT), nunca del input del LLM |
| Inyección de prompt | Cada tool parsea params con `json.Decoder` + `DisallowUnknownFields` — campos desconocidos fallan cerrado |
| Autoridad de escritura | Tokens HITL: 32 bytes aleatorios opacos, guardados solo como SHA-256, comparación timing-safe |
| Re-validación de contexto | Aprobar re-parsea el payload y re-resuelve los IDs dentro del tenant antes de insertar |
| Comportamiento del modelo | Temperatura 0.2, prompt de sistema que prohíbe inventar datos, harness de evals que lo impone |
| Auth | JWT HS256 verificado localmente (`sub`/`tenant_id`/`role`, TTL 15 min); **ingesta accept-both**: el `tenant_id` entero (demo `cmd/mktoken`) se usa directo, el `tenant_id` UUID (agro-iam) se resuelve vía `tenants.uuid`, los códigos de rol en inglés se normalizan (`agronomist`→`agronomo`, `producer`→`productor`; `auditor`/`hauler` no tienen rol local de escritura → 403) y un `sub` UUID se resuelve vía `users.uuid` acotado al tenant — ver [Integración con agro-iam](#integración-con-agro-iam). agro-agent nunca emite tokens por sí solo |

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
curl http://localhost:8080/healthz   # -> ok
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
| `GET` | `/healthz` | ninguna | liveness (devuelve `ok`) |
| `POST` | `/api/v1/chat` | Bearer JWT | chat (JSON, o SSE con `Accept: text/event-stream`); con límite de tasa por IP (default 10 req/min, `CHAT_RATE_LIMIT`) |
| `GET` | `/api/v1/approvals?status=` | Bearer JWT | listar solicitudes |
| `POST` | `/api/v1/approvals/{id}/approve` | Bearer JWT, admin/agronomo | aprobar con token |
| `POST` | `/api/v1/approvals/{id}/reject` | Bearer JWT, admin/agronomo | rechazar |
| `GET` | `/api/v1/lotes` | Bearer JWT | listar lotes |
| `GET` | `/api/v1/aplicaciones` | Bearer JWT | listar aplicaciones |

Token de desarrollo (nunca en producción): `JWT_SECRET=... go run ./cmd/mktoken -tenant 1 -user 2 -role agronomo`
(el tool se auto-verifica el token contra el verifier real del backend antes de imprimirlo).
Token agro-iam-style (tenant UUID + rol en inglés, con los UUIDs demo fijos del
seed): `JWT_SECRET=... go run ./cmd/mktoken -uuid -role agronomist -exp 24h`.

### Integración con agro-iam

**Implementada — ingesta accept-both de JWT** (ver [AD-015](./DECISIONS.es.md)):
la capa de auth acepta la misma forma HS256/JWT que emite agro-iam
(`sub`/`tenant_id`/`role`, TTL 15 min) en **ambos** vocabularios de identidad:

- **`tenant_id` entero** (demo `cmd/mktoken`) se usa directo como `TenantID`
  interno.
- **`tenant_id` UUID** (agro-iam) se resuelve al id interno vía la columna
  `tenants.uuid` (`ResolveTenantByUUID`). Un UUID que no exista en la tabla
  `tenants` de agro-agent se rechaza con el mismo **401** uniforme que un
  entero mal formado.
- **Roles en inglés** se normalizan una vez en la ingesta: `agronomist`→
  `agronomo`, `producer`→`productor`; `admin` queda `admin`. `auditor`/`hauler`
  no tienen equivalente local de escritura y se mapean a rol vacío, así
  `requireRole` (aprobar/rechazar) los rechaza con **403** — quedan solo
  lectura.
- **`sub` UUID** (usuario de agro-iam) se resuelve al actor interno vía la
  columna `users.uuid` **acotado al tenant del request**
  (`ResolveUserByUUID`): un usuario de agro-iam de otra cooperativa jamás
  resuelve acá.

La demo sigue funcionando sin cambios: `cmd/mktoken -tenant 1 -user 2 -role
agronomo` sigue emitiendo tokens enteros con rol en español que evitan los
resolvers por completo.

**Salvedad — bases separadas:** agro-iam y agro-agent tienen cada uno su propia
base. Un token real de agro-iam solo funciona si los UUIDs de sus claims
existen en las tablas `tenants`/`users` de agro-agent — el seed fija los demo
(tenant `11111111-1111-4111-8111-111111111111`, user
`22222222-2222-4222-8222-222222222222`) para que los tokens de `mktoken -uuid`
resuelvan. Un token para un tenant de agro-iam que **no tiene fila** en
agro-agent recibe 401. Migraciones: `db/migrations/003_uuid_identity.sql`
agrega las columnas y fija los UUIDs demo en bases existentes; las bases nuevas
los reciben vía `schema.sql` + `seed.sql`.

### Cuota del LLM

El free tier de Gemini es de 5 requests por minuto y 20 por día. El agente puede
llamar al LLM hasta 5 veces por request (loop de tool calling) y el endpoint de
chat tiene límite de tasa por IP (default 10 req/min, `CHAT_RATE_LIMIT`), pero
en picos la cuota diaria puede agotarse igual — esperar errores
`429`/`RESOURCE_EXHAUSTED` que la capa de proveedor reintenta una vez
(acotada) antes de propagarlos.

### Failover del LLM (Gemini → Groq)

Configurá `GROQ_API_KEY` para que el chat sobreviva al agotamiento de la cuota
del free tier de Gemini: `cmd/api/main.go` compone un `llm.FallbackProvider`
(Gemini primario, Groq respaldo). Ante un error **transitorio** — `429`
(cuota/rate limit), `5xx` o falla de red (`llm.IsTransient`) — el request cae
a Groq automáticamente; cualquier otro error falla cerrado (el fallback nunca
enmascara un bug del primario). Sin `GROQ_API_KEY` el sistema funciona
exactamente igual que antes. `GROQ_MODEL` elige el modelo de Groq (default
`llama-3.3-70b-versatile`). Si solo está `GROQ_API_KEY`, Groq queda como único
proveedor de chat (el RAG de documentos queda indisponible: necesita
embeddings de Gemini).

## Deploy

**Estado: actualmente NO está desplegado en un host público.** Este proyecto de
portfolio se mantiene en tiers gratuitos: el free tier de Render ya está
ocupado por [agro-iam](https://github.com/ezequielranieri/agro-iam) (un web
service y una Postgres free por cuenta, más el tope de 750 h/mes de instancia)
y un plan pago queda fuera de alcance — así que el backend corre localmente
(Docker Compose + Neon). Las instrucciones de abajo son el camino a seguir si
alguna vez lo desplegás (por ejemplo con un plan pago de Render u otro host de
contenedores).

Desplegá en [Render](https://render.com) con el blueprint
[`render.yaml`](./render.yaml) incluido, y alojá el Postgres en
[Neon](https://neon.tech) (Postgres serverless free, `pgvector` incluido):

1. Subí este repo a GitHub/GitLab y creá una **Blueprint Instance**
   (Dashboard → New → Blueprint Instance → conectá el repo). Render detecta
   `render.yaml` solo y compila la API desde [`./Dockerfile`](./Dockerfile)
   (multi-stage: binario Go estático sobre `alpine:3.20`, usuario no-root,
   certs de CA para las llamadas HTTPS a Gemini).
2. **Creá la base en Neon** (free tier): New Project → copiá la connection
   string (`postgresql://...neon.tech/...`).
3. Durante la creación del Blueprint Render pide una vez los secretos,
   incluido `AGRO_DATABASE_URL` — pegá ahí el DSN de Neon.
   (`AGRO_DATABASE_URL` es a propósito **no** `fromDatabase`: Render permite
   solo UNA base Postgres free por cuenta, y una base externa mantiene este
   deploy gratis e independiente del proveedor.)
4. **Sembrá la base UNA vez.** El Blueprint no tiene tipo de servicio "job
   one-off" y el contenedor web no trae `psql`, así que aplicá el schema+seed
   una vez desde tu máquina contra el DSN de Neon:

   ```bash
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f db/schema.sql -f db/seed.sql
   ```

   `schema.sql` necesita la extensión `pgvector`, que Neon soporta
   nativamente. `seed.sql` **no es idempotente** (ids demo fijos) — corrélo
   exactamente una vez.
5. Health check: Render sondea `GET /healthz` (público, sin auth).

Variables de entorno que usa `cmd/api/main.go`:

| Variable | Propósito |
|---|---|
| `AGRO_DATABASE_URL` | DSN pgx (de Neon; pegala como secret) |
| `GEMINI_API_KEY` | chat (primario) + embeddings; requerida salvo que `GROQ_API_KEY` esté presente |
| `GROQ_API_KEY` | opcional — habilita failover automático a Groq ante `429`/`5xx` de Gemini |
| `GROQ_MODEL` | opcional — modelo de Groq (default `llama-3.3-70b-versatile`) |
| `JWT_SECRET` | requerida al boot; el valor `change-me` se rechaza |
| `PORT` | la inyecta Render; default en el código `8080` |
| `CHAT_RATE_LIMIT` | opcional, límite de chat por IP (default 10 req/min) |

Advertencias honestas:

- **El free tier de Render duerme** después de ~15 min sin tráfico; el primer
  request después de dormir sufre un cold start lento. Usá un plan pago para
  cualquier uso real.
- **El free tier de Gemini es ajustado para producción** (5 req/min, 20/día; el
  agente puede llamar al LLM hasta 5 veces por request). Esperá `429` /
  `RESOURCE_EXHAUSTED` y el reintento acotado incluido. Configurar
  `GROQ_API_KEY` convierte esa falla en un respaldo automático a Groq.
- **El free tier de Neon** se pausa tras inactividad (cold start en la base,
  segundos no minutos) y tiene límites de compute-hours; sirve para demos,
  upgradéalo para durabilidad.

## Testing

`go test ./...` simple — sin testcontainers, sin testify. Los unit tests usan
fakes (proveedor LLM con guion, stores falsos) y corren siempre. Los tests de
integración (`internal/store/pg`, `internal/approval`) están **compuertados**
detrás de `AGRO_TEST_DB` y se saltean por defecto — corren solo cuando la
variable apunta a la base de compose:

```bash
AGRO_TEST_DB="postgres://postgres:postgres@localhost:5432/agro" go test ./internal/store/pg ./internal/approval -v
```

Todo verde hoy: build + vet + 60+ tests.

## Configuración (env)

| Variable | Default | Propósito |
|---|---|---|
| `AGRO_DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/agro` | DSN pgx |
| `GEMINI_API_KEY` | — (requerida salvo que `GROQ_API_KEY` esté presente) | clave API Gemini (chat + embeddings) |
| `GROQ_API_KEY` | — (opcional) | clave API de Groq — habilita el failover automático cuando Gemini agota cuota / el proveedor cae |
| `GROQ_MODEL` | `llama-3.3-70b-versatile` | modelo de Groq del proveedor de failover |
| `GEMINI_EMBED_MODEL` | `gemini-embedding-2` | modelo de embeddings (768 dims vía dimensionalidad de salida) |
| `JWT_SECRET` | — (requerida) | clave HS256 para el JWT demo (`cmd/mktoken`); el valor `change-me` se rechaza al boot |
| `PORT` | `8080` | puerto HTTP |

## Roadmap

- [x] Schema v1 + seed — 13 tablas, multi-tenant, historias demo
- [x] 5 tools de lectura + puertos/adapters + orquestador + adapter Gemini
- [x] HTTP API — auth JWT, aislamiento de tenant, chat JSON + streaming SSE
- [x] HITL — solicitudes de aprobación, tokens opacos, RBAC, re-validación, auditoría
- [x] RAG — pgvector, `buscar_documentos`, `cmd/embed`
- [x] Router de discernimiento — clasificador determinista de dominio + exposición filtrada de tools
- [x] Evals — golden set, harness de routing + anti-alucinación + discernimiento
- [x] Fallback multi-proveedor LLM — Gemini → Groq ante límites de cuota (AD-016)
- [x] Postgres externo — Neon, sembrado, corre vía Docker Compose (AD-017)
- [x] Conexión live con agro-iam — ingesta accept-both: tenant UUID vía `tenants.uuid`, normalización de roles en inglés, `sub` UUID vía `users.uuid` acotado al tenant (AD-015)
- [x] Docs — README bilingüe con capturas reales; decisión de deploy documentada
- [ ] Corrida live del eval (cuota diaria free tier)
- [ ] Deploy — decidido **no** hostear en público: el free tier de Render ya lo
      usa agro-iam y no hay plan pago; el backend corre localmente (Docker
      Compose + Neon). Ver [Deploy](#deploy)

## Licencia

Proyecto de aprendizaje/portfolio — sin garantía.