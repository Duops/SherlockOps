package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery returns middleware that recovers from panics in HTTP handlers,
// logs the stack trace, and returns a 500 Internal Server Error response.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					stack := debug.Stack()
					logger.Error("panic recovered",
						"panic", rv,
						"method", r.Method,
						"path", r.URL.Path,
						"stack", string(stack),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
