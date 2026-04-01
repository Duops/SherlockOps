package middleware

import (
	"net/http"

	"github.com/Duops/SherlockOps/internal/domain"
	"github.com/google/uuid"
)

// RequestID returns middleware that generates a UUID for each request,
// sets it as the X-Request-ID response header, and adds it to the request context.
// If the incoming request already has an X-Request-ID header, it is reused.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = uuid.New().String()
			}

			w.Header().Set("X-Request-ID", id)

			ctx := domain.ContextWithRequestID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
