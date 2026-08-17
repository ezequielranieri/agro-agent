# DECISIONS.es.md — agro-agent

Decisiones de arquitectura y constitución del proyecto. Cada entrada responde:
qué elegimos, por qué, y qué sacrificamos. Escrito para la próxima ingeniera
(o el entrevistador) que lea este repositorio en frío.

> [English](./DECISIONS.md)

## AD-001 · Arquitectura hexagonal, puertos y adapters

**Estado:** aceptada · **Alcance:** todo el repo

**Decisión:** `internal/domain` (entidades + errores centinela) e
`internal/tenant`/`internal/identity` (portadores de contexto) no importan
nada más que la biblioteca estándar. Los casos de uso (`internal/agent`,
`internal/approval`, `internal/eval`) dependen solo de puertos (interfaces en
`internal/store`, `internal/llm`, `internal/embedding`). Los adapters viven en
`internal/store/pg`, `internal/approval/pg`, `internal/llm/gemini.go`.

**Por qué:** el orquestador no debe saber que Gemini existe; el servicio HITL
no debe saber que pgx existe. Los tests usan fakes en todos lados, que es lo
que permite correr 60+ tests con `go test ./...` y sin testcontainers.

**Trade-off:** más archivos e indirección que un servicio fino. La ganancia:
cada slice (HTTP, HITL, RAG, evals) aterrizó sin tocar el dominio.

## AD-002 · Tenancy por schema compartido (columna tenant_id), no RLS

**Estado:** aceptada · **Alcance:** base de datos

**Decisión:** todas las tablas de negocio llevan `tenant_id`; foreign keys
compuestas `(tenant_id, id)`; `UNIQUE (tenant_id, id)` en las tablas
referenciadas. El tenant viaja en el **contexto** (del claim JWT) y lo inyecta
el middleware — el LLM nunca lo provee, y las tools fallan cerrado si falta.

**Por qué:** RLS (como en agro-iam) es el estándar de oro, pero la frontera de
aislamiento de agro-agent es la *aplicación*: las tools del agente consultan a
través de la capa de store, que siempre filtra por el tenant del contexto.
Elegimos el modelo más simple y lo reforzamos con FKs compuestas para que un
insert cross-tenant sea imposible a nivel de constraint.

**Trade-off:** una sesión SQL cruda sin RLS no está aislada por tenant. Para el
modelo de amenazas de este proyecto (el atacante es el LLM, no un DBA
comprometido), el tenant en contexto + protección a nivel de constraint es el
tamaño correcto.

## AD-003 · Verificación JWT local — misma forma HS256/JWT, vocabulario de identidad accept-both

**Estado:** aceptada · **Alcance:** auth + middleware + actor de approvals

**Decisión:** agro-agent *valida* JWTs HS256 localmente contra un `JWT_SECRET`
compartido (`internal/auth/verifier.go`): `sub`, `tenant_id`, `role`, `iat`,
`exp`, TTL 15 min. `sub`/`tenant_id` vacíos, un método de firma distinto de
HS256 y un secret vacío al boot se rechazan todos. **Nunca emite tokens** —
`cmd/mktoken` es solo dev. Desde [AD-015](#ad-015--ingesta-accept-both-de-jwt-tenant-entero--uuid-normalización-de-roles),
la ingesta es **accept-both**: un `tenant_id` entero (demo `mktoken`) se usa
directo, mientras que un `tenant_id` UUID (agro-iam) se resuelve al id interno
vía `tenants.uuid`; los códigos de rol en inglés
(`agronomist`/`producer`/...) se normalizan al vocabulario local
(`agronomo`/`productor`/...), y un `sub` UUID se resuelve vía `users.uuid`
acotado al tenant del request.

**Por qué:** agro-iam es un módulo aparte cuyos internos no son importables
(viven en `internal/`). Validar localmente con un `JWT_SECRET` compartido es
el patrón real de microservicios: cero acoplamiento, sin llamada de auth por
request.

**Trade-off:** un `JWT_SECRET` comprometido rompe ambos servicios; la rotación
debe coordinarse. Normal en setups de secret compartido HS256. La DB mantiene
un `id` BIGINT interno más una columna de mapeo `uuid` — dos identidades que
deben mantenerse en sincronía (ver AD-015).

## AD-015 · Ingesta accept-both de JWT (tenant entero + UUID, normalización de roles)

**Estado:** aceptada · **Alcance:** internal/store, internal/httpapi, internal/approval, db

**Decisión:** la frontera de ingesta del JWT acepta ambos vocabularios de
identidad. Un `tenant_id` entero (demo `cmd/mktoken`) se usa directo; un
`tenant_id` UUID (agro-iam) se resuelve por un puerto nuevo —
`store.TenantStore` — implementado por `pg.TenantStore` contra la columna
`tenants.uuid` (`ResolveTenantByUUID`). Los roles se normalizan una vez en la
ingesta (`agronomist`→`agronomo`, `producer`→`productor`, `admin`→`admin`;
`auditor`/`hauler` se mapean a vacío para que `requireRole` los rechace con
403). Un `sub` UUID se resuelve al actor interno vía `approval.UserResolver` →
`pg.TenantStore.ResolveUserByUUID`, siempre acotado al tenant del request. Las
columnas de mapeo `tenants.uuid`/`users.uuid` viven en la migración
`003_uuid_identity.sql` (y en `schema.sql` para bases nuevas), con valores demo
fijos en `seed.sql`. Todos los fallos quedan fail-closed bajo el contrato
uniforme previo (401 para todo fallo de token/tenant, 500 para un actor
irresoluble); el modelo de seguridad del token (HS256 fijado, tokens de
aprobación solo-hash, comparación en tiempo constante) queda intacto.

**Por qué:** los tokens reales de agro-iam ahora autentican de punta a punta
mientras la demo con enteros sigue corriendo sin cambios — una ingesta, dos
vocabularios, y sin migrar `domain.TenantID` (las 13 columnas `tenant_id
BIGINT` se mantienen).

**Trade-off:** dos mundos de vocabulario (entero + UUID) deben mantenerse en
sincronía, y la base de la demo y la de agro-iam son separadas: los UUIDs de un
token real deben existir en las tablas `tenants`/`users` de agro-agent, si no
el request recibe 401. El dominio sigue viendo un `TenantID` int64; las
columnas UUID son la única adición.

## AD-004 · Modelo Gemini fijado + la danza de thought_signature

**Estado:** aceptada · **Alcance:** internal/llm

**Decisión:** modelo de chat default `gemini-3.6-flash`, temperatura 0.2,
fijado (no "latest") para evals deterministas. Al reenviar un `functionCall` a
Gemini, el adapter debe preservar `Part.ThoughtSignature` — el helper
`resp.FunctionCalls()` **la pierde**, así que `gemini.go` itera
`Candidates[0].Content.Parts` directamente. Los resultados de tools van en rol
`"user"` como `functionResponse` (Gemini no tiene rol `"tool"`).

**Por qué:** los modelos se retiran para keys nuevas (2.0/2.5-flash
desaparecieron); fijar evita romper por sorpresa. El requisito de
thought_signature es un detalle del contrato de la API que da 400 en silencio
si se omite.

**Trade-off:** los modelos fijados envejecen; actualizar es un cambio de una
línea más una corrida de evals.

## AD-005 · Embeddings: gemini-embedding-2, 768 dims vía dimensionalidad de salida

**Estado:** aceptada · **Alcance:** internal/embedding, db/schema.sql (v3)

**Decisión:** `text-embedding-004` no está disponible para keys nuevas. El
modelo actual es `gemini-embedding-2` (3072 dims nativas) configurado con
`OutputDimensionality: 768` para que la columna siga siendo `vector(768)` y el
índice HNSW siga chico. La constante de dimensión vive en
`internal/embedding/gemini.go` y debe coincidir con la migración.

**Por qué:** 768 dims alcanza para unos pocos documentos técnicos; 3072
cuadruplicaría almacenamiento e índice sin ganancia de retrieval a esta
escala.

**Trade-off:** si cambia el modelo, columna + índice + constante deben cambiar
juntos — documentado en la migración y el adapter.

## AD-006 · RAG solo para documentos, nunca datos estructurados

**Estado:** aceptada · **Alcance:** internal/tools/buscar_documentos.go

**Decisión:** la tool RAG busca solo en `documentos` (manuales, protocolos,
informes). Los datos estructurados (lotes, aplicaciones, rendimientos) se
sirven exclusivamente por tools tipadas. `buscar_documentos` embeddee la
consulta **del lado del servidor** y devuelve el top-k por similitud cosena
dentro del tenant, exponiendo `filename`/`content`/`score` para que el modelo
cite la fuente.

**Por qué:** dos caminos de retrieval con datos superpuestos crearían
ambigüedad en la decisión de tool calling y verdad duplicada. Los documentos
son prosa — los vectores son la herramienta correcta; los números son filas —
SQL es la herramienta correcta.

**Trade-off:** una pregunta respondible por ambos caminos usará solo uno. El
caso `protocolo-herbicidas` del harness de evals fija cuál.

## AD-007 · HITL: token opaco, almacenamiento solo-hash, re-validación completa al aprobar

**Estado:** aceptada · **Alcance:** internal/approval

**Decisión:** las tools de escritura nunca mutan directo. `programar_aplicacion`
crea una solicitud `PENDIENTE` con un token (32 bytes aleatorios, hex) cuyo
**hash SHA-256** se persiste. Aprobar/rechazar requiere el token (comparación
timing-safe), una solicitud pendiente no vencida (TTL 24 h) y RBAC
(`admin`/`agronomo`). Al aprobar el servicio **re-valida** el contexto: payload
re-parseado con `DisallowUnknownFields`, lote/producto/campaña resueltos
dentro del tenant, recién entonces inserta (`planificada`) y marca `ejecutado`.
La auditoría es fail-open.

**Por qué:** el token es la "prueba de intención humana" — conocerlo demuestra
que viste la solicitud. El almacenamiento solo-hash hace que una fuga de DB no
fugue tokens utilizables. La re-validación cierra la brecha
time-of-check/time-of-use: el mundo pudo cambiar entre la creación y la
aprobación, así que se vuelve a verificar en la materialización.

**Trade-off:** no existe aún el estado "aprobado pero no ejecutado" — aprobar
== ejecutar. Un worker diferido es trabajo futuro (el estado `aprobado` existe
en el enum pero no se usa).

## AD-008 · Evals: chequeo de tools por subsecuencia + aserciones anti-alucinación

**Estado:** aceptada · **Alcance:** internal/eval

**Decisión:** los casos golden verifican (1) que las tools esperadas aparezcan
**en orden** como subsecuencia del trace (se permite explorar), (2) substrings
requeridas presentes (`MustContain` / `MustContainAny`), (3) substrings
prohibidas ausentes (`MustNotContain`) para cazar números alucinados. Los
casos de escritura se saltean por defecto para que las corridas sean
read-only. Los tests usan un provider fake con guion, así que el harness en sí
es determinista; las corridas live miden precisión de routing.

**Por qué:** el match exacto de tools castigaría exploración legítima
(consultar-lotes-antes-de-decidir). Las aserciones anti-alucinación codifican
la promesa central del producto: solo respuestas ancladas.

**Trade-off:** los substring checks son toscos — una respuesta verbosa puede
fallar por formato. El corpus es chico y elegido a mano de historias
verificadas en vivo.

## AD-009 · Parsing fail-closed en todos lados, auditoría fail-open

**Estado:** aceptada · **Alcance:** todas las tools + servicio de aprobación

**Decisión:** cada tool decodifica JSON con `DisallowUnknownFields`; los
campos desconocidos (p. ej. un `tenant_id` inyectado por prompt) se rechazan,
nunca se ignoran. El auditor, por el contrario, es fail-open: si escribir la
fila de auditoría falla, el flujo continúa con un WARN.

**Por qué:** los argumentos de tool del LLM son input no confiable — fallar
cerrado ante cualquier cosa fuera del contrato es el default correcto y
barato. La auditoría es observabilidad, no una compuerta de negocio; jamás
debe tumbar el flujo.

**Trade-off:** auditoría fail-open significa que una caída de auditoría es
silenciosa por diseño (solo una línea de log). Aceptable a esta escala.

## AD-010 · Un agente por request (OnEvent), agente compartido inmutable

**Estado:** aceptada · **Alcance:** internal/agent, internal/httpapi

**Decisión:** la raíz de composición construye un `agent.Agent` con getters
(`Provider()`, `Registry()`, `MaxIterations()`) y sin estado de eventos
mutable. El handler HTTP construye un agente fresco por request con su propio
closure `OnEvent` para el streaming SSE.

**Por qué:** las requests concurrentes no deben compartir estado mutable de
streaming; un agente inmutable con eventos por request es race-free por
construcción.

**Trade-off:** una asignación extra por request — despreciable.

## AD-011 · Materialización HITL atómica (approve a prueba de TOCTOU)

**Estado:** aceptada · **Alcance:** internal/approval

**Decisión:** aprobar/rechazar corren la re-validación + un
`UPDATE ... WHERE status='pendiente'` condicional + el INSERT de la aplicación
en UNA sola `pgx.Tx`. El perdedor de un approve concurrente obtiene 0 filas
afectadas → `ErrNotPending` → HTTP 409; la transacción hace rollback y no se
escribe ninguna fila de aplicación duplicada.

**Por qué:** dos approves concurrentes con el mismo token válido antes pasaban
ambos los checks de lectura y duplicaban el insert — corrupción de datos en la
feature insignia.

**Trade-off:** una sección crítica un poco más grande; el puerto del applier
(`approval.Applier`) agrega una capa pequeña de indirección. (Archivos:
`internal/approval/pg/writer.go`, `internal/approval/service.go`,
`internal/approval/approval.go`; test `TestConcurrentApprove_SoloUnaGana`.)

## AD-012 · Resiliencia acotada del proveedor (timeouts + un solo retry)

**Estado:** aceptada · **Alcance:** cmd/api, internal/agent, internal/llm

**Decisión:** `http.Server` recibe `ReadHeaderTimeout` 10s / `IdleTimeout` 60s
(NO hay `ReadTimeout` global — el chat SSE necesita conexiones de larga
duración). El loop del agente envuelve cada `provider.Chat` en un contexto de
60s por iteración y chequea `ctx.Err()` entre iteraciones. El adapter Gemini
hace UN retry acotado ante errores transitorios 429/5xx usando el delay del
`RetryInfo` del proveedor, con tope de 5s.

**Por qué:** una llamada colgada al proveedor antes fijaba una goroutine + una
conexión a la DB por tiempo indefinido, y un 429 fallaba el chat al instante.

**Trade-off:** la latencia en el peor caso sube por el delay acotado del retry;
un proveedor en 429 permanente sigue fallando cerrado tras un solo retry.

## AD-013 · Límite de tasa por IP en el chat

**Estado:** aceptada · **Alcance:** internal/httpapi

**Decisión:** un token bucket en memoria en la ruta del chat (default 10
req/min/IP, env `CHAT_RATE_LIMIT`); las requests que superan el límite reciben
un 429 con `{"error":"rate limit exceeded"}`.

**Por qué:** el chat llama a un LLM pago hasta 5× por request sin protección;
un loop podría quemar la cuota o la factura.

**Trade-off:** el estado en memoria es por instancia (suficiente para un demo
de un solo proceso; habría que usar un store compartido para multi-instancia).

## AD-014 · Rechazar el JWT_SECRET por defecto al boot

**Estado:** aceptada · **Alcance:** cmd/api, internal/auth

**Decisión:** el startup falla de forma ruidosa si `JWT_SECRET` está vacío O es
el literal `"change-me"` (espejando agro-iam).

**Por qué:** `.env.example` trae `change-me`; aceptarlo corre HS256 con una
clave conocida públicamente.

**Trade-off:** un dev que copie el archivo env al pie de la letra debe elegir
un secret real — ese es el punto.

## AD-016 · Failover de LLM multi-proveedor (Gemini → Groq)

**Estado:** aceptada · **Alcance:** internal/llm, cmd/api, internal/embedding

**Decisión:** `llm.FallbackProvider` envuelve el puerto `llm.Provider`: intenta
el primario y, SOLO ante un error transitorio — `429` (cuota/rate limit), `5xx`
o falla de red, clasificados por `llm.IsTransient` — cae al proveedor
secundario. Todo lo demás falla cerrado: un error NO transitorio del primario
se devuelve tal cual, jamás enmascarado. `internal/llm/groq.go` es un segundo
adapter del puerto `llm.Provider` para la API compatible-OpenAI de Groq
(`POST /openai/v1/chat/completions`, `Authorization: Bearer`); comparte el
mismo system prompt y temperatura (0.2) que Gemini, mapea los resultados de
tools con el mismo envelope `{"result": ...}` y produce
`ToolCall.ThoughtSignature: nil` (específica de Gemini — segura: el
orquestador solo la copia). `cmd/api/main.go` compone: ambas keys →
`FallbackProvider(Gemini → Groq)`; solo `GEMINI_API_KEY` → Gemini solo
(comportamiento histórico); solo `GROQ_API_KEY` → Groq como único proveedor de
chat (los embeddings del RAG — solo-Gemini — degradan a un
`embedding.Unavailable` descriptivo); ninguna → fatal.

**Por qué:** el free tier de Gemini (20 req/día, 5 req/min) vuelve el chat demo
poco confiable — un día de demos agota la cuota y cada request da 429. El free
tier de Groq es generoso, así que un failover automático convierte el demo de
"funciona hasta 20 chats" en una experiencia con sensación de producto, a costo
cero.

**Trade-off:** dos modelos pueden responder distinto (un eval puede pasar en
Gemini y fallar en Groq); una conversación mixta que vuelva a Gemini a mitad
de camino podría dar 400 porque Gemini exige `thought_signature` al reenviar un
`functionCall` — en la práctica el proveedor agotado sigue fallando, así que la
conversación completa queda en Groq; una key de entorno más por administrar
(`GROQ_API_KEY`, opcional).

## AD-017 · Postgres externo en Neon en vez de la base manejada por Render

**Status:** accepted · **Scope:** render.yaml, READMEs, deploy

**Decision:** el blueprint de Render NO provisiona base de datos.
`AGRO_DATABASE_URL` es un secret `sync: false` que el usuario completa con una
connection string de Neon; `schema.sql`/`seed.sql` se aplican una vez desde la
máquina del desarrollador (`psql -f`). El blueprint es un único web service
(`runtime: docker`, `plan: free`, healthcheck `/healthz`).

**Por qué:** Render permite solo UNA base Postgres free activa por cuenta
("cannot have more than one active free tier database"), lo que bloquea el
blueprint para usuarios que ya corren cualquier Postgres free en Render —
exactamente la audiencia free-tier que este demo apunta. Un Postgres
serverless externo (Neon: 0.5 GB, `pgvector` nativo, sin expiración de 30 días
como el plan free de Render) mantiene todo el stack en $0 y desacopla la base
del proveedor de hosting (portable: apuntá `AGRO_DATABASE_URL` a donde sea).

**Trade-off:** una pieza móvil más (cuenta externa de base); el free tier de
Neon se pausa tras inactividad (cold start en la base, de segundos) y tiene
límites de compute-hours; la connection string cruza el prompt de Render a mano
en vez de auto-conectarse vía `fromDatabase`.

## No-decisiones (diferidas explícitamente)

- **Persistencia de conversación** (la tabla messages existe en el schema pero
  todavía no se guarda historial) — próximo slice.
- **Estado `aprobado` (aprobado pero no ejecutado)** — un worker diferido lo
  consumiría.
- **Deploy** — render.com como agro-iam; el Dockerfile/compose está listo, la
  cuota diaria del free tier de Gemini es la restricción.
- **Frontend de demo desplegable** — agro-iam ya demuestra el patrón SPA;
  agro-agent se mantiene API-first.