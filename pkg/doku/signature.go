package doku

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// SignRequest builds the Non-SNAP request signature.
//
// Algorithm:
//  1. If body is non-empty: SHA-256(body) → base64 → Digest
//  2. Build stringToSign with \n separators:
//     Client-Id:<clientID>
//     Request-Id:<requestID>
//     Request-Timestamp:<timestamp>
//     Request-Target:<requestTarget>
//     Digest:<digest>          ← omitted for GET/no-body requests
//  3. HMAC-SHA256(stringToSign, secretKey) → base64
//
// Returns the Signature header value: "HMACSHA256=<base64>"
func SignRequest(clientID, requestID, timestamp, requestTarget, secretKey string, body []byte) string {
	parts := []string{
		"Client-Id:" + clientID,
		"Request-Id:" + requestID,
		"Request-Timestamp:" + timestamp,
		"Request-Target:" + requestTarget,
	}

	if len(body) > 0 {
		digest := bodyDigest(body)
		parts = append(parts, "Digest:"+digest)
	}

	stringToSign := strings.Join(parts, "\n")

	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(stringToSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return "HMACSHA256=" + sig
}

// SignSNAPRequest builds the SNAP symmetric request signature.
//
// Algorithm:
//  1. SHA-256(minify(body)) → lowercase hex
//  2. stringToSign = HTTPMethod:endpointURL:accessToken:hexHash:timestamp
//  3. HMAC-SHA512(stringToSign, clientSecret) → base64
//
// Returns the raw base64 value for the X-Signature header.
func SignSNAPRequest(httpMethod, endpointURL, accessToken, timestamp, clientSecret string, body []byte) string {
	h := sha256.New()
	h.Write(body)
	hexHash := hex.EncodeToString(h.Sum(nil)) // already lowercase

	stringToSign := fmt.Sprintf("%s:%s:%s:%s:%s",
		strings.ToUpper(httpMethod),
		endpointURL,
		accessToken,
		hexHash,
		timestamp,
	)

	mac := hmac.New(sha512.New, []byte(clientSecret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// bodyDigest returns the base64-encoded SHA-256 hash of body.
// Used as the Digest component in Non-SNAP signatures.
func bodyDigest(body []byte) string {
	h := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(h[:])
}
