package admin

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AdminClaims are the JWT payload fields for platform admin tokens.
type AdminClaims struct {
	jwt.RegisteredClaims
	Email        string   `json:"email"`
	IsSuperAdmin bool     `json:"is_super_admin"`
	Permissions  []string `json:"perms,omitempty"` // format: "resource:ACTION"
}

// IssueToken signs a 24-hour JWT for adminID using HS256.
// Super admins receive an empty Permissions slice — IsSuperAdmin: true grants all access.
func IssueToken(adminID uuid.UUID, email string, isSuperAdmin bool, perms []string, secret string) (string, error) {
	claims := AdminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   adminID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:        email,
		IsSuperAdmin: isSuperAdmin,
		Permissions:  perms,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// VerifyToken parses and validates a JWT, returning the full admin claims.
func VerifyToken(tokenStr, secret string) (*AdminClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &AdminClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, ErrTokenInvalid
	}
	claims, ok := token.Claims.(*AdminClaims)
	if !ok {
		return nil, ErrTokenInvalid
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}
