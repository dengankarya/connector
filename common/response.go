package common

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// ErrorDetail is a structured error payload carrying a machine-readable code
// and a human-readable message. Use it as the Error field on Response.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Err returns a Response with a structured ErrorDetail.
func Err(httpStatusText, code, message string) Response {
	return Response{
		Status: httpStatusText,
		Error:  ErrorDetail{Code: code, Message: message},
	}
}

// PaginationResponse is the standard envelope for cursor-paginated list endpoints.
type PaginationResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}
