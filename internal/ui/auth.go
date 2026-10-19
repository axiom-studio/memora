package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "memora_session"
	// secureSessionCookieName is used whenever the cookie is Secure. Browsers
	// accept a __Host- cookie only if it is Secure, host-only and Path=/, so
	// a sibling subdomain cannot plant or overwrite it (#6033).
	secureSessionCookieName = "__Host-memora_session"
	sessionMaxAge           = 24 * time.Hour
	hstsHeaderValue         = "max-age=31536000"
)

// Values for AuthConfig.CookieSecure (server.ui_cookie_secure).
const (
	// CookieSecureAuto marks the cookie Secure only when the request itself
	// arrived over TLS. Behind a TLS-terminating proxy that is never true,
	// so it suits loopback and plain-HTTP consoles only.
	CookieSecureAuto = "auto"
	// CookieSecureAlways marks it Secure on every response; the right
	// setting whenever users reach the console over HTTPS, including
	// through a proxy or Ingress.
	CookieSecureAlways = "always"
	// CookieSecureNever never marks it Secure.
	CookieSecureNever = "never"
)

type AuthConfig struct {
	APIKey    string
	SecretKey []byte // HMAC signing key; derived from APIKey if not set
	// CookieSecure is one of the CookieSecure* values; "" means auto.
	CookieSecure string
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

// secureCookies reports whether this response's session cookie is Secure.
func (h *Handler) secureCookies(r *http.Request) bool {
	switch h.authCfg.CookieSecure {
	case CookieSecureAlways:
		return true
	case CookieSecureNever:
		return false
	default:
		return r.TLS != nil
	}
}

func (h *Handler) sessionCookieNameFor(r *http.Request) string {
	if h.secureCookies(r) {
		return secureSessionCookieName
	}
	return sessionCookieName
}

func (h *Handler) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	c := &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/ui/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if h.secureCookies(r) {
		c.Name = secureSessionCookieName
		c.Path = "/" // required by the __Host- prefix
		c.Secure = true
	}
	return c
}

// hsts wraps every console route. Browsers honour HSTS only on HTTPS
// responses, so sending it whenever the console is treated as HTTPS is safe,
// and it stops them from ever making the plaintext request that would leak
// the session cookie.
func (h *Handler) hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.secureCookies(r) {
			w.Header().Set("Strict-Transport-Security", hstsHeaderValue)
		}
		next.ServeHTTP(w, r)
	})
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

		cookie, err := r.Cookie(h.sessionCookieNameFor(r))
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
	cookie := h.sessionCookie(r, token, int(sessionMaxAge.Seconds()))
	if cookie.Secure && r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		// Only a hint for operators; the header is never trusted for security.
		h.plainHTTPWarn.Do(func() {
			slog.Default().Warn("console session cookie is Secure but this login arrived over plain HTTP; " +
				"browsers will drop it unless users reach the console over HTTPS. " +
				"For a plain-HTTP console set server.ui_cookie_secure = \"auto\"")
		})
	}
	//nolint:gosec // G124: Secure is set by server.ui_cookie_secure (default always on non-loopback binds, #6033); HttpOnly and SameSite are always set
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/ui/", http.StatusSeeOther)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // G124: same attributes as the login cookie; this one only expires the session
	http.SetCookie(w, h.sessionCookie(r, "", -1))
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}
