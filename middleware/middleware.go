package middleware

// ErrorResponse is the standard JSON body for error responses.
type ErrorResponse struct {
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}
