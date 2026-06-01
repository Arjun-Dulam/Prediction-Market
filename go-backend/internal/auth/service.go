package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	db     *sql.DB
	secret []byte
	ttl    time.Duration
}

type Claims struct {
	Subject string `json:"sub"`
	Issuer  string `json:"iss"`
	Expiry  int64  `json:"exp"`
}

type contextKey struct{}

func New(db *sql.DB, secret string) (*Service, error) {
	if len(secret) < 16 {
		return nil, errors.New("JWT_SECRET must contain at least 16 characters")
	}
	return &Service{db: db, secret: []byte(secret), ttl: 24 * time.Hour}, nil
}

func (s *Service) Register(ctx context.Context, email, username, password string) (string, string, error) {
	email, username = strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(username)
	if email == "" || username == "" || len(password) < 8 {
		return "", "", errors.New("email, username, and an 8-character password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	id := uuid.NewString()
	if _, err = s.db.ExecContext(ctx, `INSERT INTO users(id,email,username,password_hash) VALUES($1,$2,$3,$4)`, id, email, username, hash); err != nil {
		return "", "", errors.New("email or username already registered")
	}
	token, err := s.sign(id)
	return id, token, err
}

func (s *Service) Login(ctx context.Context, email, password string) (string, string, error) {
	var id string
	var hash []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,password_hash FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))).Scan(&id, &hash)
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		return "", "", errors.New("invalid credentials")
	}
	token, err := s.sign(id)
	return id, token, err
}

func (s *Service) sign(userID string) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, err := json.Marshal(Claims{Subject: userID, Issuer: "prediction-market-exchange", Expiry: time.Now().Add(s.ttl).Unix()})
	if err != nil {
		return "", err
	}
	unsigned := encode(header) + "." + encode(payload)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + encode(mac.Sum(nil)), nil
}

func (s *Service) Parse(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return Claims{}, errors.New("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("invalid token payload")
	}
	var claims Claims
	if json.Unmarshal(payload, &claims) != nil || claims.Subject == "" || claims.Issuer != "prediction-market-exchange" || time.Now().Unix() >= claims.Expiry {
		return Claims{}, errors.New("expired or invalid token")
	}
	return claims, nil
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := s.Parse(strings.TrimPrefix(authorization, "Bearer "))
		if err != nil {
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, claims.Subject)))
	})
}

func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(contextKey{}).(string)
	return id, ok && id != ""
}

func RequireOwner(ctx context.Context, requested string) (string, error) {
	id, authenticated := UserID(ctx)
	if !authenticated {
		return requested, nil
	}
	if requested != "" && requested != id {
		return "", fmt.Errorf("resource belongs to another user")
	}
	return id, nil
}

func encode(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
