package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signTest firma un token con el formato de agro-iam (sub/tenant_id/role/
// iat/exp). Se usa para fabricar tokens tanto válidos como hostiles.
func signTest(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return s
}

func TestVerify_TokenValido(t *testing.T) {
	v, err := NewVerifier("secret-de-prueba")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := signTest(t, "secret-de-prueba", map[string]any{
		"sub":       "42",
		"tenant_id": "1",
		"role":      "admin",
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(15 * time.Minute).Unix(),
	})

	claims, err := v.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID != "42" || claims.TenantID != "1" || claims.Role != "admin" {
		t.Errorf("claims incorrectos: %+v", claims)
	}
}

func TestVerify_OtroSecret(t *testing.T) {
	v, _ := NewVerifier("secret-correcto")
	token := signTest(t, "secret-INCORRECTO", map[string]any{
		"sub": "42", "tenant_id": "1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(token); err == nil || err.Error() != "auth: token inválido" {
		t.Fatalf("esperaba error uniforme, obtuve: %v", err)
	}
}

func TestVerify_AlgHS512(t *testing.T) {
	v, _ := NewVerifier("secret-de-prueba")
	// HS512 con el MISMO secret: si el keyfunc no filtrara el método, firmaría.
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"sub": "42", "tenant_id": "1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := token.SignedString([]byte("secret-de-prueba"))
	if err != nil {
		t.Fatalf("firmar: %v", err)
	}
	if _, err := v.Verify(s); err == nil {
		t.Fatal("esperaba rechazo por método HS512")
	}
}

func TestVerify_TokenMalformado(t *testing.T) {
	v, _ := NewVerifier("secret")
	for _, tk := range []string{"", "no-es-un-jwt", "a.b.c"} {
		if _, err := v.Verify(tk); err == nil || err.Error() != "auth: token inválido" {
			t.Errorf("token %q: esperaba error uniforme, obtuve %v", tk, err)
		}
	}
}

func TestVerify_SubVacio(t *testing.T) {
	v, _ := NewVerifier("secret")
	token := signTest(t, "secret", map[string]any{
		"sub": "", "tenant_id": "1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(token); err == nil || err.Error() != "auth: token inválido" {
		t.Fatalf("esperaba error uniforme, obtuve: %v", err)
	}
}

func TestVerify_TenantIDVacio(t *testing.T) {
	v, _ := NewVerifier("secret")
	token := signTest(t, "secret", map[string]any{
		"sub": "42", "tenant_id": "", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(token); err == nil || err.Error() != "auth: token inválido" {
		t.Fatalf("esperaba error uniforme, obtuve: %v", err)
	}
}

func TestVerify_Expirado(t *testing.T) {
	v, _ := NewVerifier("secret")
	token := signTest(t, "secret", map[string]any{
		"sub": "42", "tenant_id": "1",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := v.Verify(token); err == nil {
		t.Fatal("esperaba rechazo por token expirado")
	}
}

func TestVerify_RoleOpcional(t *testing.T) {
	v, _ := NewVerifier("secret")
	token := signTest(t, "secret", map[string]any{
		"sub": "42", "tenant_id": "1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	claims, err := v.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Role != "" {
		t.Errorf("role debería estar vacío, obtuve %q", claims.Role)
	}
}

func TestNewVerifier_SecretVacio(t *testing.T) {
	_, err := NewVerifier("")
	if err == nil {
		t.Fatal("esperaba error por secret vacío")
	}
	if !strings.Contains(err.Error(), "vacío") {
		t.Errorf("error poco descriptivo: %v", err)
	}
}
