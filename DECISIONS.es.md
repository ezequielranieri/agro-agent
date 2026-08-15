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

## AD-003 · Verificación JWT local, byte-compatible con agro-iam

**Estado:** aceptada · **Alcance:** auth

**Decisión:** agro-agent *valida* JWTs HS256 emitidos por agro-iam
(`internal/auth/verifier.go`), esperando los mismos claims (`sub`,
`tenant_id`, `role`, `iat`, `exp`, TTL 15 min). **Nunca emite tokens** —
`cmd/mktoken` es solo dev. El verifier rechaza `sub`/`tenant_id` vacíos y un
secret vacío al boot.

**Por qué:** agro-iam es un módulo aparte cuyos internos no son importables
(viven en `internal/`). Validar localmente con un `JWT_SECRET` compartido es
el patrón real de microservicios: cero acoplamiento, sin llamada de auth por
request.

**Trade-off:** un `JWT_SECRET` comprometido rompe ambos servicios; la rotación
debe coordinarse. Normal en setups de secret compartido HS256.

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

## No-decisiones (diferidas explícitamente)

- **Persistencia de conversación** (la tabla messages existe en el schema pero
  todavía no se guarda historial) — próximo slice.
- **Estado `aprobado` (aprobado pero no ejecutado)** — un worker diferido lo
  consumiría.
- **Deploy** — render.com como agro-iam; el Dockerfile/compose está listo, la
  cuota diaria del free tier de Gemini es la restricción.
- **Frontend de demo desplegable** — agro-iam ya demuestra el patrón SPA;
  agro-agent se mantiene API-first.