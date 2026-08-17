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
//	go run ./cmd/mktoken -tenant 1 -user 2 -role agronomo -exp 24h   # dev local
//
// Token agro-iam-style (tenant/sub UUID + rol en inglés). -uuid usa los UUID
// fijos del seed demo: el middleware los resuelve vía tenants.uuid/users.uuid.
// -tenant/-user explícitos ganan sobre los defaults de -uuid:
//
//	go run ./cmd/mktoken -uuid -role agronomist -exp 24h
//	go run ./cmd/mktoken -uuid -role agronomist -user 22222222-2222-4222-8222-222222222222
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
	// La expiración corta por defecto es la segura para un emisor de prueba:
	// un token largo olvidado en un shell o log es una ventana de abuso. El
	// dev local explícitamente puede pedir más (p.ej. -exp 24h) cuando sabe
	// que el token va a vivir en el .env.local del frontend un día entero.
	exp := flag.Duration("exp", 15*time.Minute, "duración del token (p.ej. 24h para dev local)")
	// -uuid usa los UUID fijos del seed demo: tenant 11111111-... y user
	// 22222222-... (los claims ya son strings, así que nada bloquea UUIDs).
	// Solo cambia los DEFAULTS: si el dev pasa -tenant/-user explícitos, esos
	// valores ganan.
	uuidDemo := flag.Bool("uuid", false, "usar los UUID fijos del seed demo (tenant 11111111-1111-4111-8111-111111111111, user 22222222-2222-4222-8222-222222222222) en vez de los ids enteros por defecto")
	flag.Parse()

	if *uuidDemo {
		// flag.Visit solo visita los flags que el usuario seteó explícitamente:
		// así -uuid no pisa un -tenant/-user escrito a mano.
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if !set["tenant"] {
			*tenant = "11111111-1111-4111-8111-111111111111"
		}
		if !set["user"] {
			*user = "22222222-2222-4222-8222-222222222222"
		}
	}

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
			ExpiresAt: jwt.NewNumericDate(now.Add(*exp)),
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
