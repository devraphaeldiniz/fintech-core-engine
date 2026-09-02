package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const ClientContextKey contextKey = "client_info"

type ClientInfo struct {
	ClientName string
	Scopes     []string
}

func APIKeyAuth(db *pgxpool.Pool, requiredScope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
			if rawKey == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Header X-API-Key obrigatório"})
				return
			}

			hash := sha256.Sum256([]byte(rawKey))
			keyHash := hex.EncodeToString(hash[:])

			var clientName string
			var rawScopes string
			var isActive bool

			query := `SELECT client_name, scopes, is_active FROM api_keys WHERE key_hash = $1`
			err := db.QueryRow(r.Context(), query, keyHash).Scan(&clientName, &rawScopes, &isActive)
			if err != nil {
				log.Printf("[AUTH FAILED] Hash recebido: %s | Erro: %v\n", keyHash, err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Chave de API inválida ou revogada"})
				return
			}

			if !isActive {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Chave de API inativa"})
				return
			}

			scopesList := strings.Split(rawScopes, ",")
			hasScope := false
			for _, s := range scopesList {
				s = strings.TrimSpace(s)
				if s == requiredScope || s == "admin" {
					hasScope = true
					break
				}
			}

			if !hasScope {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Permissão insuficiente para esta operação"})
				return
			}

			ctx := context.WithValue(r.Context(), ClientContextKey, &ClientInfo{
				ClientName: clientName,
				Scopes:     scopesList,
			})

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
