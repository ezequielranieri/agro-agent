// Command mktoken emite un JWT de PRUEBA con el mismo formato que agro-iam.
//
// SOLO para desarrollo local: en producción los tokens los emite agro-iam
// (el servicio de identidad) y agro-agent únicamente los verifica con el
// secret compartido. Este binario NO debe desplegarse.
//
// Uso:
//
//	go run ./cmd/mktoken -secret "$JWT_SECRET" -tenant 1 -user 42 -role admin
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	secret := flag.String("secret", "", "JWT_SECRET compartido con agro-iam")
	tenant := flag.String("tenant", "1", "TenantID (claim tenant_id)")
	user := flag.String("user", "42", "UserID (claim sub)")
	role := flag.String("role", "admin", "Rol (claim role)")
	flag.Parse()

	if *secret == "" {
		fmt.Fprintln(os.Stderr, "uso: mktoken -secret <JWT_SECRET> -tenant <id> -user <id> -role <rol>")
		os.Exit(1)
	}

	// Formato byte-compatible con agro-iam: sub/tenant_id/role/iat/exp.
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":       *user,
		"tenant_id": *tenant,
		"role":      *role,
		"iat":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(*secret))
	if err != nil {
		fmt.Fprintf(os.Stderr, "firmar token: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(s)
}
