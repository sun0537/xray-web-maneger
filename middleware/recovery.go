package middleware

import (
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
				WriteJSONError(w, "Internal Server Error", http.StatusInternalServerError, "server")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
