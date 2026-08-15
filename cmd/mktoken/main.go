// Command mktoken emite un JWT de PRUEBA con el mismo formato que agro-iam.
//
// SOLO para desarrollo local: en producción los tokens los emite agro-iam
// (el servicio de identidad) y agro-agent únicamente los verifica con el
// secret compartido. Este binario NO debe desplegarse.
//
// Uso:
//
//	JWT_SECRET=... go run ./cmd/mktoken -tenant 1 -user 2 -role agronomo
//	go run ./cmd/mktoken -secret "$JWT_SECRET" -tenant 1 -user 42 -role admin
//
// El secret sale de JWT_SECRET (env) o del flag -secret. El flag es el
// fallback para shells sin env configurado; la env evita que el secret quede
// en el historial del shell.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agro-agent/agro-agent/internal/auth"
)

// claims es el formato byte-compatible con agro-iam: sub (UserID),
// tenant_id (TenantID), role (Role), iat y exp. RegisteredClaims embebido
// implementa jwt.Claims (aud/exp/iat/iss/nbf/sub) con serialización estándar;
// solo agregamos los claims propios del ecosistema. Tipado en vez de
// MapClaims para que un typo en el nombre de un claim falle en compilación,
// no en auth.
type claims struct {
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func main() {
	secret := flag.String("secret", "", "JWT_SECRET compartido con agro-iam (fallback: env)")
	tenant := flag.String("tenant", "1", "TenantID (claim tenant_id)")
	user := flag.String("user", "42", "UserID (claim sub)")
	role := flag.String("role", "admin", "Rol (claim role)")
	flag.Parse()

	// El secret sale de la env primero (no ensucia el historial del shell);
	// el flag es el fallback explícito.
	secretValue := os.Getenv("JWT_SECRET")
	if secretValue == "" {
		secretValue = *secret
	}
	if secretValue == "" {
		fmt.Fprintln(os.Stderr, "uso: JWT_SECRET=... go run ./cmd/mktoken -tenant <id> -user <id> -role <rol>")
		os.Exit(1)
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		TenantID: *tenant,
		Role:     *role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   *user,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	})
	signed, err := token.SignedString([]byte(secretValue))
	if err != nil {
		fmt.Fprintf(os.Stderr, "firmar token: %v\n", err)
		os.Exit(1)
	}

	// Auto-verificación: el token se valida con el verifier REAL del proyecto
	// antes de imprimirlo. Si el dev emite algo que el backend rechazaría,
	// este paso lo descubre acá y no en una request.
	verifier, err := auth.NewVerifier(secretValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verifier: %v\n", err)
		os.Exit(1)
	}
	parsed, err := verifier.Verify(signed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "el token no pasa la verificación del backend: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(signed)
	fmt.Fprintf(os.Stderr, "tenant=%s user=%s role=%s (verificado)\n", parsed.TenantID, parsed.UserID, parsed.Role)
}