package repository

import (
	"encoding/json"

	"github.com/google/uuid"
)

// nilIfEmpty returns nil when s is empty, so nullable TEXT columns store NULL rather than "".
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// marshalJSON marshals v to JSON bytes.
// Returns (nil, false) when v is nil, so callers can distinguish NULL from "{}".
func marshalJSON(v any) ([]byte, bool) {
	if v == nil {
		return nil, false
	}
	b, _ := json.Marshal(v)
	return b, true
}

// unmarshalJSON unmarshals src into dst; ignores errors (NULL/empty is safe).
func unmarshalJSON(src []byte, dst any) error {
	if len(src) == 0 {
		return nil
	}
	return json.Unmarshal(src, dst)
}

// jsonParam returns the JSON bytes as a string (for $N::jsonb params), or nil when invalid.
func jsonParam(b []byte, valid bool) any {
	if !valid || len(b) == 0 {
		return nil
	}
	return string(b)
}

// mustParseUUID parses a UUID string; returns uuid.Nil on error (should never happen with DB data).
func mustParseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

// parseOptionalUUID parses a nullable UUID string pointer.
func parseOptionalUUID(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &id
}
