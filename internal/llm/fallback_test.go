package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// stubProvider es un fake del puerto Provider: devuelve lo configurado y
// cuenta las llamadas.
type stubProvider struct {
	resp   Response
	err    error
	called int
}

func (s *stubProvider) Chat(_ context.Context, _ []Message, _ []ToolSchema) (Response, error) {
	s.called++
	return s.resp, s.err
}

func TestFallback_primarioOk_NoCaeAlRespaldo(t *testing.T) {
	primary := &stubProvider{resp: Response{Text: "ok"}}
	fallback := &stubProvider{resp: Response{Text: "respaldo"}}
	f := NewFallbackProvider(primary, fallback)

	resp, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("Text = %q, esperaba la del primario", resp.Text)
	}
	if fallback.called != 0 {
		t.Errorf("respaldo llamado %d veces, esperaba 0", fallback.called)
	}
}

func TestFallback_primarioTransitorio_DevuelveRespaldo(t *testing.T) {
	primary := &stubProvider{err: &groqError{Status: http.StatusTooManyRequests}}
	fallback := &stubProvider{resp: Response{Text: "respaldo", Usage: Usage{PromptTokens: 1, CompletionTokens: 2}}}
	f := NewFallbackProvider(primary, fallback)

	resp, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "respaldo" {
		t.Errorf("Text = %q, esperaba la del respaldo", resp.Text)
	}
	if resp.Usage.PromptTokens != 1 || resp.Usage.CompletionTokens != 2 {
		t.Errorf("Usage = %+v, esperaba la del respaldo", resp.Usage)
	}
	if fallback.called != 1 {
		t.Errorf("respaldo llamado %d veces, esperaba 1", fallback.called)
	}
}

func TestFallback_ambosFallan_DevuelveErrorDelRespaldo(t *testing.T) {
	primary := &stubProvider{err: &groqError{Status: http.StatusServiceUnavailable}}
	fallbackErr := errors.New("groq down")
	fallback := &stubProvider{err: fallbackErr}
	f := NewFallbackProvider(primary, fallback)

	_, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
	if err == nil {
		t.Fatal("esperaba error")
	}
	if !errors.Is(err, fallbackErr) {
		t.Errorf("error no envuelve el del respaldo: %v", err)
	}
	if !strings.Contains(err.Error(), "primario y respaldo fallaron") {
		t.Errorf("error sin contexto del failover: %v", err)
	}
}

func TestFallback_primarioNoTransitorio_NoLlamaRespaldo(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"400 contrato", &groqError{Status: http.StatusBadRequest}},
		{"error genérico", errors.New("boom")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			primary := &stubProvider{err: c.err}
			fallback := &stubProvider{resp: Response{Text: "respaldo"}}
			f := NewFallbackProvider(primary, fallback)

			_, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
			if !errors.Is(err, c.err) {
				t.Errorf("error = %v, esperaba el del primario tal cual", err)
			}
			if fallback.called != 0 {
				t.Errorf("respaldo llamado %d veces, esperaba 0 (fail-closed)", fallback.called)
			}
		})
	}
}

func TestFallback_sinRespaldo_SeComportaComoPrimario(t *testing.T) {
	t.Run("éxito", func(t *testing.T) {
		primary := &stubProvider{resp: Response{Text: "ok"}}
		f := NewFallbackProvider(primary, nil)
		resp, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
		if err != nil || resp.Text != "ok" {
			t.Errorf("resp = %q, err = %v", resp.Text, err)
		}
	})
	t.Run("error transitorio devuelto tal cual", func(t *testing.T) {
		primaryErr := &groqError{Status: http.StatusTooManyRequests}
		primary := &stubProvider{err: primaryErr}
		f := NewFallbackProvider(primary, nil)
		_, err := f.Chat(context.Background(), []Message{{Role: RoleUser, Text: "x"}}, nil)
		if err != primaryErr {
			t.Errorf("error = %v, esperaba el del primario sin envolver", err)
		}
	})
}
