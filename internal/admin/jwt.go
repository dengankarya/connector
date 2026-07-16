package admin

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type adminClaims struct {
	jwt.RegisteredClaims
}

// IssueToken signs a 24-hour JWT for adminID using HS256.
func IssueToken(adminID uuid.UUID, secret string) (string, error) {
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   adminID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// VerifyToken parses and validates a JWT, returning the admin UUID from the Subject claim.
func VerifyToken(tokenStr, secret string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &adminClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return uuid.Nil, ErrTokenInvalid
	}
	claims, ok := token.Claims.(*adminClaims)
	if !ok {
		return uuid.Nil, ErrTokenInvalid
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrTokenInvalid
	}
	return id, nil
}
