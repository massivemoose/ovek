package main

import (
	"crypto/subtle"
	"net/http"
)

func apiKeyMiddleware(expectedAPIKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedAPIKey := r.Header.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(providedAPIKey), []byte(expectedAPIKey)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
