package embedding

import (
	"context"
	"testing"
)

// TestUnavailable_DevuelveErrorDescriptivo: en modo solo-Groq (sin
// GEMINI_API_KEY) el RAG no puede embeddear; el placeholder debe fallar con
// un error claro, nunca silenciarlo ni paniquear.
func TestUnavailable_DevuelveErrorDescriptivo(t *testing.T) {
	vec, err := (Unavailable{}).Embed(context.Background(), "¿qué protocolo aplico al trigo?")
	if err == nil {
		t.Fatal("esperaba error descriptivo")
	}
	if vec != nil {
		t.Errorf("vec = %v, esperaba nil", vec)
	}
}
