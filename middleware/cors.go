package middleware

import (
	"log"
	"net/http"
	"net/url"
	"strings"
)

func CheckOrigin(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	allowedMap := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowedMap[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin == "" {
				referer := r.Header.Get("Referer")
				if referer != "" {
					if u, err := url.Parse(referer); err == nil {
						origin = u.Scheme + "://" + u.Host
					}
				}
			}

			// Browsers send "null" Origin for privacy-sensitive contexts
			// (file://, sandboxed iframes, redirects from HTTPS to HTTP).
			// Treat it as missing — allow safe methods, block state changes.
			if origin == "null" {
				origin = ""
			}

			if origin == "" {
				switch r.Method {
				case http.MethodGet, http.MethodHead, http.MethodOptions:
					// Even without an Origin header, set Vary so caches know
					// the response differs when Origin IS present (CORS
					// headers are only added for known origins below).
					w.Header().Add("Vary", "Origin")
				default:
					log.Printf("警告: 拒绝了缺少 Origin 头的状态变更请求: %s %s", r.Method, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"error":"缺少 Origin 头 (Missing Origin header)"}`))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			origin = strings.TrimRight(origin, "/")

			if _, ok := allowedMap[origin]; !ok {
				log.Printf("警告: 拒绝了来自非法 Origin 的请求: %s", origin)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"非法请求来源 (Invalid Origin)"}`))
				return
			}

			// Set CORS headers for allowed origins.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.Header().Add("Vary", "Origin")

			// Handle preflight requests.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
