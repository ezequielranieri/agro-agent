// Package auth verifica tokens JWT emitidos por agro-iam (el servicio de
// identidad del ecosistema). agro-agent NUNCA emite tokens: solo los valida
// contra el secret compartido, manteniendo un único punto de emisión.
//
// Regla de seguridad: fallo cerrado y uniforme. Cualquier token inválido
// (firma, expiración, método, claims) devuelve el MISMO error genérico, para
// que un atacante no pueda distinguir por qué falló un token.
package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

// Claims son los datos del usuario autenticado que el middleware expone a los
// handlers. UserID y Role alimentan el audit log; TenantID aísla la consulta.
type Claims struct {
	UserID   string
	TenantID string
	Role     string
}

// errInvalid es el error UNIFORME para todo fallo de verificación. Nunca se
// filtra el detalle (p.ej. "firma inválida" vs "expirado") al llamador.
var errInvalid = errors.New("auth: token inválido")

// Verifier valida tokens HS256 contra un secret compartido.
type Verifier struct {
	secret []byte
}

// NewVerifier construye el verifier. Un secret vacío es inaceptable: quien
// emite el token sería cualquiera con acceso al servicio. Por eso se rechaza
// en el arranque, no en cada request.
func NewVerifier(secret string) (*Verifier, error) {
	if secret == "" {
		return nil, errors.New("auth: el secret JWT no puede estar vacío")
	}
	return &Verifier{secret: []byte(secret)}, nil
}

// Verify valida la firma y los claims de un token. Formato byte-compatible
// con agro-iam: sub (UserID), tenant_id (TenantID), role (Role), iat y exp.
func (v *Verifier) Verify(token string) (Claims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		// Rechaza TODO método que no sea HS256: bloquea "alg=none" y la
		// confusión de algoritmos (HS512/RS256 con el mismo secret).
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("auth: método de firma no permitido")
		}
		return v.secret, nil
	})
	if err != nil {
		// jwt valida exp/iat/nbf automáticamente sobre MapClaims; cualquier
		// fallo de parse o claims cae acá.
		return Claims{}, errInvalid
	}

	sub, _ := claims["sub"].(string)
	tid, _ := claims["tenant_id"].(string)
	role, _ := claims["role"].(string)
	// sub y tenant_id son obligatorios: sin ellos no hay usuario ni dato a
	// aislar. role puede faltar (claims legacy), se deja vacío.
	if sub == "" || tid == "" {
		return Claims{}, errInvalid
	}
	return Claims{UserID: sub, TenantID: tid, Role: role}, nil
}
