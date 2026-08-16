// Command embed indexa los documentos de la cooperativa: genera el embedding
// de cada documento sin vector y lo persiste en Postgres (columna vector).
//
// Idempotente por diseño: solo procesa documentos con embedding NULL, así que
// correrlo dos veces no re-embebbe nada. Uso:
//
//	AGRO_DATABASE_URL=... GEMINI_API_KEY=... go run ./cmd/embed
//
// Variables de entorno:
//
//	AGRO_DATABASE_URL  DSN de Postgres (default local del dev).
//	GEMINI_API_KEY     clave del proveedor (REQUERIDA).
//	GEMINI_EMBED_MODEL modelo de embeddings (default gemini-embedding-2).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/embedding"
	pg2 "github.com/agro-agent/agro-agent/internal/store/pg"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := context.Background()

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Error("GEMINI_API_KEY es requerida")
		os.Exit(1)
	}
	dsn := os.Getenv("AGRO_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/agro"
	}

	// Pool de conexiones (igual que cmd/api): el adapter de documentos espera
	// un *pgxpool.Pool compartido, thread-safe, en vez de una única *pgx.Conn.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("conectar a Postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	docsStore := pg2.NewDocumentoStore(pool)
	emb, err := embedding.NewGemini(ctx, geminiKey, os.Getenv("GEMINI_EMBED_MODEL"))
	if err != nil {
		log.Error("crear proveedor de embeddings", "err", err)
		os.Exit(1)
	}

	// Indexamos por tenant: el RAG es multi-tenant, cada cooperativa indexa
	// SOLO sus documentos.
	tenantIDs, err := listTenantIDs(ctx, pool)
	if err != nil {
		log.Error("listar tenants", "err", err)
		os.Exit(1)
	}

	total := 0
	for _, tid := range tenantIDs {
		docs, err := docsStore.ListSinEmbedding(ctx, domain.TenantID(tid))
		if err != nil {
			log.Error("listar documentos sin embedding", "tenant", tid, "err", err)
			os.Exit(1)
		}
		for _, d := range docs {
			vec, err := emb.Embed(ctx, d.Content)
			if err != nil {
				log.Error("generar embedding", "doc", d.Filename, "err", err)
				os.Exit(1)
			}
			if len(vec) != embedding.Dimension {
				log.Error("dimensión inesperada del modelo",
					"doc", d.Filename, "got", len(vec), "want", embedding.Dimension)
				os.Exit(1)
			}
			if err := docsStore.GuardarEmbedding(ctx, domain.TenantID(tid), d.ID, vec); err != nil {
				log.Error("guardar embedding", "doc", d.Filename, "err", err)
				os.Exit(1)
			}
			log.Info("documento indexado", "tenant", tid, "doc", d.Filename, "dims", len(vec))
			total++
		}
	}
	log.Info("indexación completa", "tenants", len(tenantIDs), "documentos_indexados", total)
}

// listTenantIDs devuelve los tenants que tienen al menos un documento.
func listTenantIDs(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT tenant_id FROM documentos ORDER BY tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("listar tenants: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iteración tenants: %w", err)
	}
	return out, nil
}
