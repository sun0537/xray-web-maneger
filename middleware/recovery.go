package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("!!! HTTP Handler Panic: %v", err)
				log.Printf("!!! Stacktrace:\n%s", debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				body, _ := json.Marshal(ErrorResponse{
					Error:     "Internal Server Error",
					ErrorType: "server",
				})
				if body == nil {
					body = []byte(`{"error":"Internal Server Error","error_type":"server"}`)
				}
				_, _ = w.Write(body)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
