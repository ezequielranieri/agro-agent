package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/agro-agent/agro-agent/internal/domain"
	"github.com/agro-agent/agro-agent/internal/store"
	"github.com/agro-agent/agro-agent/internal/tenant"
)

// fakeDocStore implementa store.DocumentoStore con datos controlados.
type fakeDocStore struct {
	sinEmbedding []store.Documento
	similares    []store.DocumentoSimilar
	guardadoID   int64
	guardadoVec  []float32
}

func (f *fakeDocStore) ListSinEmbedding(ctx context.Context, tid domain.TenantID) ([]store.Documento, error) {
	return f.sinEmbedding, nil
}

func (f *fakeDocStore) GuardarEmbedding(ctx context.Context, tid domain.TenantID, docID int64, vec []float32) error {
	f.guardadoID = docID
	f.guardadoVec = vec
	return nil
}

func (f *fakeDocStore) BuscarSimilares(ctx context.Context, tid domain.TenantID, vec []float32, limit int) ([]store.DocumentoSimilar, error) {
	return f.similares, nil
}

// fakeEmbedder devuelve un vector fijo y registra la consulta.
type fakeEmbedder struct {
	query   string
	vec     []float32
	err     error
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.query = text
	return f.vec, f.err
}

func ctxWithTenant(t *testing.T, tid domain.TenantID) context.Context {
	t.Helper()
	return tenant.WithID(context.Background(), tid)
}

func TestBuscarDocumentosParams(t *testing.T) {
	tool := BuscarDocumentos(&fakeDocStore{}, &fakeEmbedder{vec: []float32{0.1, 0.2}})
	ctx := ctxWithTenant(t, 1)

	tests := []struct {
		name string
		raw  string
		want string // substring esperado del error (o "" = ok)
	}{
		{"query vacía", `{"query":""}`, "query es requerida"},
		{"query faltante", `{"limite":2}`, "query es requerida"},
		{"limite negativo", `{"query":"x","limite":-1}`, "limite inválido"},
		{"limite 6", `{"query":"x","limite":6}`, "limite inválido"},
		{"campo desconocido", `{"query":"x","tenant_id":2}`, "params inválidos"},
		{"json inválido", `{query`, "params inválidos"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Run(ctx, json.RawMessage(tc.raw))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("esperaba ok, got %v", err)
				}
				return
			}
			if err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("esperaba error con %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuscarDocumentosOk(t *testing.T) {
	docs := &fakeDocStore{similares: []store.DocumentoSimilar{
		{Documento: store.Documento{ID: 2, TenantID: 1, Filename: "protocolo-herbicidas-trigo.txt", Content: "Glifosato 48%: 3 L/ha en barbecho."}, Score: 0.9},
	}}
	emb := &fakeEmbedder{vec: []float32{0.3, 0.4}}
	tool := BuscarDocumentos(docs, emb)

	res, err := tool.Run(ctxWithTenant(t, 1), json.RawMessage(`{"query":"herbicidas en trigo","limite":1}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// El embedder recibió la query del LLM.
	if emb.query != "herbicidas en trigo" {
		t.Fatalf("embedder recibió %q, esperaba la query", emb.query)
	}
	data, ok := res.Data.([]DocumentoResult)
	if !ok {
		t.Fatalf("Data de tipo %T, esperaba []DocumentoResult", res.Data)
	}
	if len(data) != 1 || data[0].Filename != "protocolo-herbicidas-trigo.txt" {
		t.Fatalf("resultado inesperado: %+v", data)
	}
	// La proyección no filtra el score (el LLM puede citar la fuente).
	if data[0].Score != 0.9 {
		t.Fatalf("score esperado 0.9, got %v", data[0].Score)
	}
}

func TestBuscarDocumentosErrorEmbedder(t *testing.T) {
	docs := &fakeDocStore{}
	emb := &fakeEmbedder{err: errors.New("embedding: Gemini: boom")}
	tool := BuscarDocumentos(docs, emb)

	_, err := tool.Run(ctxWithTenant(t, 1), json.RawMessage(`{"query":"protocolo"}`))
	if err == nil || !contains(err.Error(), "embedding") {
		t.Fatalf("esperaba error del embedder propagado, got %v", err)
	}
}

func TestBuscarDocumentosRequiereTenant(t *testing.T) {
	tool := BuscarDocumentos(&fakeDocStore{}, &fakeEmbedder{vec: []float32{1}})
	// Sin tenant en el contexto: debe fallar fail-closed.
	_, err := tool.Run(context.Background(), json.RawMessage(`{"query":"x"}`))
	if err == nil {
		t.Fatal("esperaba error sin tenant en contexto")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}