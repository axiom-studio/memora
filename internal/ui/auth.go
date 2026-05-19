package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "memora_session"
	sessionMaxAge     = 24 * time.Hour
)

type AuthConfig struct {
	APIKey    string
	SecretKey []byte // HMAC signing key; derived from APIKey if not set
}

func deriveSecret(apiKey string) []byte {
	h := sha256.Sum256([]byte("memora-ui-session:" + apiKey))
	return h[:]
}

func (h *Handler) SetAuth(cfg AuthConfig) {
	h.authCfg = cfg
	if len(cfg.SecretKey) == 0 {
		h.authCfg.SecretKey = deriveSecret(cfg.APIKey)
	}
}

func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authCfg.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		if path == "/ui/login" || strings.HasPrefix(path, "/ui/static/") {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !h.validSession(cookie.Value) {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) validSession(value string) bool {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, h.authCfg.SecretKey)
	mac.Write(payload)
	expected := mac.Sum(nil)
	if !hmac.Equal(sig, expected) {
		return false
	}

	// payload format: "exp:<unix>"
	var exp int64
	if _, err := fmt.Sscanf(string(payload), "exp:%d", &exp); err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

func (h *Handler) createSession() string {
	exp := time.Now().Add(sessionMaxAge).Unix()
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	payload := fmt.Sprintf("exp:%d,n:%s", exp, hex.EncodeToString(nonce))
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	mac := hmac.New(sha256.New, h.authCfg.SecretKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.loginTmpl.Execute(w, map[string]any{"Error": ""}); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	key := r.FormValue("api_key")
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.authCfg.APIKey)) != 1 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = h.loginTmpl.Execute(w, map[string]any{"Error": "Invalid API key."})
		return
	}

	token := h.createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/ui/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	http.Redirect(w, r, "/ui/", http.StatusSeeOther)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/ui/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}
