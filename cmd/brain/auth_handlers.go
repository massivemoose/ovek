package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const headerAPIKey = "X-API-Key"
const headerReauthToken = "X-Ovek-Reauth-Token"

type authContextKey struct{}

func authMiddleware(cfg config, db *sql.DB, next http.Handler) http.Handler {
	if cfg.AuthMode == authModeProd {
		return dbAPIKeyMiddleware(db, next)
	}

	return apiKeyMiddleware(cfg.BrainAPIKey, next)
}

func dbAPIKeyMiddleware(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := validateAPIKey(r.Context(), db, r.Header.Get(headerAPIKey))
		if errors.Is(err, errInvalidAPIKey) {
			writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, principal)))
	})
}

func authPrincipalFromContext(ctx context.Context) (authPrincipal, bool) {
	principal, ok := ctx.Value(authContextKey{}).(authPrincipal)
	return principal, ok
}

func handleBootstrapAuth(cfg config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.AuthMode != authModeProd {
			writeJSONError(w, http.StatusForbidden, errorCodeAuthBootstrapDisabled, "auth bootstrap disabled")
			return
		}
		defer r.Body.Close()

		var request brainapi.BootstrapAuthRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}
		request.Username = strings.TrimSpace(request.Username)
		request.Password = strings.TrimSpace(request.Password)
		if request.Username == "" || request.Password == "" {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		apiKey, err := bootstrapAdminUser(db, request.Username, request.Password)
		if errors.Is(err, errAuthBootstrapDisabled) {
			writeJSONError(w, http.StatusForbidden, errorCodeAuthBootstrapDisabled, "auth bootstrap disabled")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeAuthBootstrapFailed, "failed to bootstrap auth")
			return
		}

		writeJSON(w, http.StatusCreated, brainapi.BootstrapAuthResponse{
			Username: request.Username,
			APIKey:   apiKey,
		})
	}
}

func handleReauth(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		principal, ok := authPrincipalFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
			return
		}

		var request brainapi.ReauthRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		reauthToken, expiresAt, err := issueReauthToken(r.Context(), db, principal, request.Password)
		if errors.Is(err, errInvalidPassword) {
			writeJSONError(w, http.StatusUnauthorized, errorCodeAuthReauthFailed, "reauth failed")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeAuthReauthFailed, "reauth failed")
			return
		}

		writeJSON(w, http.StatusOK, brainapi.ReauthResponse{
			ReauthToken: reauthToken,
			ExpiresAt:   expiresAt,
		})
	}
}

func requireCriticalReauth(cfg config, db *sql.DB, eventType string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.AuthMode != authModeProd {
			next.ServeHTTP(w, r)
			return
		}

		principal, ok := authPrincipalFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, errorCodeUnauthorized, "unauthorized")
			return
		}

		if err := requireReauthToken(r.Context(), db, principal, r.Header.Get(headerReauthToken), reauthScopeCriticalMutation); errors.Is(err, errReauthRequired) {
			writeJSONError(w, http.StatusUnauthorized, errorCodeReauthRequired, "reauth required")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeReauthRequired, "reauth required")
			return
		}

		projectName := strings.TrimSpace(r.PathValue("projectName"))
		_ = insertAuditLog(r.Context(), db, auditLogRecord{
			UserID:      sql.NullString{String: principal.UserID, Valid: true},
			APIKeyID:    sql.NullString{String: principal.APIKeyID, Valid: true},
			EventType:   eventType,
			ProjectName: sql.NullString{String: projectName, Valid: projectName != ""},
			DetailsJSON: mustDetailsJSON(map[string]string{"scope": reauthScopeCriticalMutation}),
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		})

		next.ServeHTTP(w, r)
	}
}
