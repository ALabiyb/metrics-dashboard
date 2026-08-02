package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com
// ---------------------------------------------------------------------------

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---- user store -------------------------------------------------------------
// Users are configured via the DASHBOARD_USERS env var (mounted from a k8s
// Secret) as a JSON array of {username, password_hash, role}. Roles are
// "admin" or "viewer". Generate password_hash values with:
//
//	go run . hash-password '<plaintext password>'

type authUser struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"` // "admin" | "viewer"
}

const (
	roleAdmin  = "admin"
	roleViewer = "viewer"
)

const (
	sessionCookieName = "dashboard_session"
	sessionTTL        = 12 * time.Hour
)

const (
	loginPath    = "/login"
	loginErrPath = "/login?error=1"
)

// dataSourceStatus is implemented by both *Simulator and *RealSource (see
// simulator.go / real_source.go). It lets the login page report where the
// dashboard's live data is coming from and whether that source is currently
// healthy, without auth.go needing to know anything about Prometheus.
type dataSourceStatus interface {
	SourceLabel() string
	Healthy() bool
}

// Authenticator validates credentials and issues/verifies signed session
// cookies. Sessions are stateless (HMAC-signed, not server-stored), so any
// number of replicas can validate them as long as they share SESSION_SECRET.
type Authenticator struct {
	users     map[string]authUser
	secret    []byte
	dummyHash []byte // used to keep failed-login timing constant for unknown usernames

	// oidc is non-nil when Keycloak SSO is configured (see oidc.go). The
	// login page only renders the "Continue with Keycloak SSO" button when
	// this is set — see handleLoginPage.
	oidc *OIDCAuthenticator

	// dataSource reports whether the dashboard is backed by Prometheus or
	// the built-in simulator, and whether it's healthy. Set from main.go
	// once the data source is constructed. Used for the login page's
	// DATA SOURCE / STATUS fields.
	dataSource dataSourceStatus
}

func NewAuthenticator() (*Authenticator, error) {
	raw := os.Getenv("DASHBOARD_USERS")
	if raw == "" {
		return nil, errors.New("DASHBOARD_USERS is not set (see README.md § Authentication for the expected JSON format)")
	}
	var list []authUser
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("parsing DASHBOARD_USERS: %w", err)
	}
	users := make(map[string]authUser, len(list))
	for _, u := range list {
		if u.Username == "" || u.PasswordHash == "" {
			return nil, fmt.Errorf("DASHBOARD_USERS: entry %q is missing username or password_hash", u.Username)
		}
		if u.Role != roleAdmin && u.Role != roleViewer {
			return nil, fmt.Errorf("DASHBOARD_USERS: user %q has invalid role %q (must be %q or %q)", u.Username, u.Role, roleAdmin, roleViewer)
		}
		users[u.Username] = u
	}
	if len(users) == 0 {
		return nil, errors.New("DASHBOARD_USERS contains no users")
	}

	secret := os.Getenv("SESSION_SECRET")
	if len(secret) < 16 {
		return nil, errors.New("SESSION_SECRET is not set (or too short — need at least 16 bytes); see README.md § Authentication")
	}

	dummyHash, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	return &Authenticator{users: users, secret: []byte(secret), dummyHash: dummyHash}, nil
}

// authenticate checks a username/password pair and reports the user's role on
// success. It always runs a bcrypt comparison — even for unknown usernames —
// so response timing doesn't leak whether an account exists.
func (a *Authenticator) authenticate(username, password string) (role string, ok bool) {
	u, found := a.users[username]
	hash := a.dummyHash
	if found {
		hash = []byte(u.PasswordHash)
	}
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if !found || err != nil {
		return "", false
	}
	return u.Role, true
}

// ---- session cookies ---------------------------------------------------------

type session struct {
	Username string    `json:"u"`
	Role     string    `json:"r"`
	Expires  time.Time `json:"e"`
}

func (a *Authenticator) sign(payload []byte) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) issueCookie(w http.ResponseWriter, username, role string) {
	s := session{Username: username, Role: role, Expires: time.Now().Add(sessionTTL)}
	payload, err := json.Marshal(s)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	cookie := encoded + "." + a.sign([]byte(encoded))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cookie,
		Path:     "/",
		Expires:  s.Expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure should be true in production (HTTPS via the Gateway API
		// HTTPRoute's TLS termination). Left unset here so `go run .` works
		// over plain http://localhost too — see README for enabling it
		// behind TLS.
	})
}

func (a *Authenticator) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
	})
}

func (a *Authenticator) sessionFromRequest(r *http.Request) (*session, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}
	encoded, sig, found := strings.Cut(c.Value, ".")
	if !found {
		return nil, errors.New("malformed session cookie")
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign([]byte(encoded)))) != 1 {
		return nil, errors.New("invalid session signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var s session
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, err
	}
	if time.Now().After(s.Expires) {
		return nil, errors.New("session expired")
	}
	return &s, nil
}

// ---- OIDC state cookies --------------------------------------------------------
// The OIDC Authorization Code flow (see oidc.go) needs to round-trip a
// `state` (CSRF guard) and `nonce` (ID-token replay guard) between the
// /login/oidc redirect and the /login/oidc/callback request. Rather than
// server-side storage, both values are stashed in a short-lived cookie
// signed with the same HMAC scheme as session cookies.

const (
	oidcStateCookieName = "oidc_state"
	oidcStateTTL        = 5 * time.Minute
)

// setOIDCStateCookie stashes a signed "state.nonce" pair for the duration of
// the SSO redirect round-trip.
func (a *Authenticator) setOIDCStateCookie(w http.ResponseWriter, state, nonce string) {
	payload := state + "." + nonce
	cookie := payload + "." + a.sign([]byte(payload))
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    cookie,
		Path:     "/login/oidc",
		MaxAge:   int(oidcStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// oidcStateFromRequest recovers and verifies the state/nonce pair stashed by
// setOIDCStateCookie. An error means the callback can't be trusted (missing,
// expired, or tampered cookie) and the login attempt should be aborted.
func (a *Authenticator) oidcStateFromRequest(r *http.Request) (state, nonce string, err error) {
	c, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(c.Value, ".", 3)
	if len(parts) != 3 {
		return "", "", errors.New("malformed oidc state cookie")
	}
	state, nonce, sig := parts[0], parts[1], parts[2]
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign([]byte(state+"."+nonce)))) != 1 {
		return "", "", errors.New("invalid oidc state signature")
	}
	return state, nonce, nil
}

// clearOIDCStateCookie removes the state cookie once the callback has
// consumed it (success or failure).
func (a *Authenticator) clearOIDCStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: "", Path: "/login/oidc", MaxAge: -1, HttpOnly: true,
	})
}

// ---- request context ---------------------------------------------------------

type ctxKey int

const sessionCtxKey ctxKey = 0

func sessionFromContext(ctx context.Context) *session {
	s, _ := ctx.Value(sessionCtxKey).(*session)
	return s
}

// ---- middleware ---------------------------------------------------------------

// requireAuth verifies the session cookie. Browser navigations without a
// valid session are redirected to /login; API requests get a 401 so the
// frontend can react (see shared.jsx's fetchJSON, which redirects on 401).
func (a *Authenticator) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := a.sessionFromRequest(r)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, loginPath, http.StatusFound)
			}
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey, s)))
	}
}

// requireRole gates a handler to a single role. It must sit behind
// requireAuth (which populates the session in the request context).
// Authenticated users with the wrong role get a 403 — distinct from the 401
// an unauthenticated request gets, so the frontend can tell "log in" apart
// from "you're logged in but not allowed to see this".
func requireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := sessionFromContext(r.Context())
		if s == nil || s.Role != role {
			http.Error(w, "forbidden: requires "+role+" role", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// ---- handlers: /login, /logout -------------------------------------------------

var loginPage = template.Must(template.New("login").Parse(loginPageHTML))

// loginPageData drives the login page template: whether the local-credential
// form should show an error, whether Keycloak SSO is configured (the SSO
// button + divider are omitted entirely when it isn't), and where the
// dashboard's live data currently comes from.
type loginPageData struct {
	Error           bool
	OIDCEnabled     bool
	DataSourceLabel string
	Healthy         bool
	AppEnv          string // "Development" or "Production" — shown in the eyebrow badge
}

func (a *Authenticator) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Already signed in? Skip the form.
	if _, err := a.sessionFromRequest(r); err == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = "Development"
	}
	data := loginPageData{
		Error:       r.URL.Query().Get("error") != "",
		OIDCEnabled: a.oidc != nil,
		AppEnv:      appEnv,
	}
	if a.dataSource != nil {
		data.DataSourceLabel = a.dataSource.SourceLabel()
		data.Healthy = a.dataSource.Healthy()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginPage.Execute(w, data)
}

func (a *Authenticator) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	role, ok := a.authenticate(username, password)
	if !ok {
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}
	a.issueCookie(w, username, role)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *Authenticator) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.handleLoginPage(w, r)
	case http.MethodPost:
		a.handleLoginSubmit(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *Authenticator) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.clearCookie(w)
	http.Redirect(w, r, loginPath, http.StatusFound)
}

// handleEmbed validates a static embed token and issues a viewer session so
// cross-origin iframes (e.g. a TV wall display) stay authenticated without
// exposing user credentials.
//
// HTTP (TV NodePort): SameSite=Lax, no Secure — works because the TV page and
// this service share the same host IP (same-site, different port).
// HTTPS (gateway): SameSite=None + Secure — required for cross-domain iframes.
func (a *Authenticator) handleEmbed(embedToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if embedToken == "" {
			http.NotFound(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(embedToken)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		https := r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil
		sameSite := http.SameSiteLaxMode
		if https {
			sameSite = http.SameSiteNoneMode
		}
		s := session{Username: "kiosk", Role: roleViewer, Expires: time.Now().Add(sessionTTL)}
		payload, err := json.Marshal(s)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		encoded := base64.RawURLEncoding.EncodeToString(payload)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    encoded + "." + a.sign([]byte(encoded)),
			Path:     "/",
			Expires:  s.Expires,
			HttpOnly: true,
			Secure:   https,
			SameSite: sameSite,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// handleMe reports who the caller is authenticated as, so the frontend (which
// can't read the HttpOnly session cookie) knows what to render — e.g. whether
// to show admin-only panels. Must sit behind requireAuth.
func handleMe(w http.ResponseWriter, r *http.Request) {
	s := sessionFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"username": s.Username, "role": s.Role})
}

// loginPageHTML is a split-screen login page: a branding/status panel on the
// left (what cluster this is, where its data comes from, and whether that
// source is healthy — all driven by loginPageData) and a sign-in panel on
// the right (Keycloak SSO when configured, plus local username/password).
const loginPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>Sign in — K8s + Ceph Dashboard</title>
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@500;600;700&family=IBM+Plex+Sans:wght@400;500;600;700&display=swap" rel="stylesheet" />
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; min-height: 100%; }
  body {
    font-family: "IBM Plex Sans", system-ui, sans-serif; background: #0a0e14; color: #e6edf3;
    display: flex; min-height: 100vh;
  }

  /* ---- left: branding / cluster status panel ---- */
  .brand {
    flex: 1 1 50%; display: flex; flex-direction: column; justify-content: space-between;
    gap: 32px; padding: 48px;
    background:
      radial-gradient(circle at 28% 22%, rgba(34,211,238,0.10), transparent 55%),
      #0d121a;
    border-right: 1px solid rgba(255,255,255,0.06);
  }
  .brand-top { display: flex; flex-direction: column; gap: 22px; }
  .logo-glow {
    width: 60px; height: 60px; border-radius: 50%; flex: 0 0 auto;
    display: flex; align-items: center; justify-content: center;
    background: linear-gradient(135deg, #22d3ee, #0e7490);
    color: #04222a; font: 700 26px/1 "IBM Plex Mono", monospace;
    animation: noc-login-pulse 2.6s ease-in-out infinite;
  }
  @keyframes noc-login-pulse {
    0%, 100% { box-shadow: 0 0 0 0 rgba(34,211,238,0), 0 0 22px 2px rgba(34,211,238,0.25); }
    50%      { box-shadow: 0 0 0 12px rgba(34,211,238,0.06), 0 0 34px 8px rgba(34,211,238,0.4); }
  }
  .eyebrow {
    display: inline-block; align-self: flex-start;
    font: 600 10.5px/1 "IBM Plex Sans", sans-serif; letter-spacing: 0.18em; text-transform: uppercase;
    color: #22d3ee; background: rgba(34,211,238,0.1); border: 1px solid rgba(34,211,238,0.25);
    border-radius: 999px; padding: 6px 12px; margin: 4px 0 14px;
  }
  .headline { font: 600 30px/1.25 "IBM Plex Sans", sans-serif; color: #e6edf3; max-width: 380px; margin: 0; }
  .tagline { font: 500 13px/1.6 "IBM Plex Sans", sans-serif; color: #7d8a9c; max-width: 380px; margin: 10px 0 0; }

  .info-card {
    background: #10151e; border: 1px solid rgba(255,255,255,0.07); border-radius: 10px;
    padding: 16px 18px; display: flex; flex-direction: column; gap: 13px; max-width: 380px;
  }
  .info-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .info-row .k {
    font: 600 10px/1 "IBM Plex Sans", sans-serif; letter-spacing: 0.14em; text-transform: uppercase; color: #7d8a9c;
  }
  .info-row .v {
    font: 600 12px/1 "IBM Plex Mono", monospace; color: #e6edf3; display: flex; align-items: center; gap: 7px;
  }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex: 0 0 auto; }
  .dot.ok  { background: #34d399; box-shadow: 0 0 6px #34d399; }
  .dot.bad { background: #fb7185; box-shadow: 0 0 6px #fb7185; }

  .brand-foot { font: 500 11px/1.6 "IBM Plex Sans", sans-serif; color: #576373; max-width: 380px; margin: 0; }

  /* ---- right: sign-in panel ---- */
  .signin { flex: 1 1 50%; display: flex; align-items: center; justify-content: center; padding: 48px; }
  .signin-card { width: 100%; max-width: 360px; display: flex; flex-direction: column; gap: 18px; }
  .signin-card h1 { font: 600 26px/1 "IBM Plex Sans", sans-serif; margin: 0; }
  .signin-card .subtitle { font: 500 13px/1.5 "IBM Plex Sans", sans-serif; color: #7d8a9c; margin: 8px 0 0; }

  .error {
    font: 500 12px/1.4 "IBM Plex Sans", sans-serif; color: #fb7185; background: rgba(251,113,133,0.1);
    border: 1px solid rgba(251,113,133,0.25); border-radius: 6px; padding: 9px 11px;
  }

  .sso-btn {
    display: flex; align-items: center; justify-content: center; gap: 9px; width: 100%;
    background: #10151e; border: 1px solid rgba(255,255,255,0.12); border-radius: 6px;
    padding: 12px; font: 600 13px "IBM Plex Sans", sans-serif; color: #e6edf3;
    text-decoration: none; cursor: pointer; transition: border-color .15s, background .15s;
  }
  .sso-btn:hover { border-color: rgba(34,211,238,0.4); background: #141b27; }

  .divider {
    display: flex; align-items: center; gap: 12px;
    font: 600 10px/1 "IBM Plex Sans", sans-serif; letter-spacing: 0.14em; text-transform: uppercase; color: #576373;
  }
  .divider::before, .divider::after { content: ''; flex: 1 1 auto; height: 1px; background: rgba(255,255,255,0.08); }

  form { display: flex; flex-direction: column; gap: 14px; }
  label {
    font: 600 10.5px/1 "IBM Plex Sans", sans-serif; letter-spacing: 0.12em; text-transform: uppercase;
    color: #7d8a9c; display: block; margin-bottom: 6px;
  }
  input {
    width: 100%; background: #0d121a; border: 1px solid rgba(255,255,255,0.07); border-radius: 6px;
    padding: 10px 12px; color: #e6edf3; font: 500 13px "IBM Plex Mono", monospace; outline: none;
  }
  input::placeholder { color: #576373; }
  input:focus { border-color: rgba(34,211,238,0.4); }
  button {
    background: #22d3ee; color: #04222a; border: none; border-radius: 6px; padding: 11px;
    font: 600 13px "IBM Plex Sans", sans-serif; letter-spacing: 0.04em; cursor: pointer;
  }
  button:hover { background: #67e8f9; }

  .helper-note { font: 500 11px/1.6 "IBM Plex Sans", sans-serif; color: #576373; margin: 0; }

  @media (max-width: 880px) {
    body { flex-direction: column; }
    .brand { border-right: none; border-bottom: 1px solid rgba(255,255,255,0.06); padding: 32px; }
    .signin { padding: 32px; }
  }
</style>
</head>
<body>
  <div class="brand">
    <div class="brand-top">
      <div class="logo-glow">⎈</div>
      <div>
        <span class="eyebrow">{{.AppEnv}} Environment</span>
        <div class="headline">Kubernetes Cluster Observability</div>
        <div class="tagline">Real-time visibility into node health, workloads, and Ceph storage.</div>
      </div>
      <div class="info-card">
        <div class="info-row"><span class="k">Dashboard</span><span class="v">K8s + Ceph Cluster Dashboard</span></div>
        <div class="info-row"><span class="k">Data Source</span><span class="v">{{.DataSourceLabel}}</span></div>
        <div class="info-row"><span class="k">Status</span><span class="v"><span class="dot {{if .Healthy}}ok{{else}}bad{{end}}"></span>{{if .Healthy}}Operational{{else}}Degraded{{end}}</span></div>
      </div>
    </div>
    <p class="brand-foot">Authorized personnel only · All access is logged &amp; audited</p>
  </div>

  <div class="signin">
    <div class="signin-card">
      <div>
        <h1>Sign in</h1>
        <p class="subtitle">Authenticate to view the live cluster dashboard.</p>
      </div>
      {{if .Error}}<div class="error">Invalid username or password.</div>{{end}}
      {{if .OIDCEnabled}}
      <a class="sso-btn" href="/login/oidc">🔒 Continue with Keycloak SSO</a>
      <div class="divider">or use local credentials</div>
      {{end}}
      <form method="post" action="/login">
        <div>
          <label for="username">Username</label>
          <input id="username" name="username" type="text" placeholder="you@cluster" autocomplete="username" autofocus required />
        </div>
        <div>
          <label for="password">Password</label>
          <input id="password" name="password" type="password" autocomplete="current-password" required />
        </div>
        <button type="submit">Sign in to dashboard</button>
      </form>
      <p class="helper-note">{{if .OIDCEnabled}}Use your Keycloak SSO account if you have one, or sign in with local credentials provided by your administrator.{{else}}Sign in with local credentials provided by your administrator.{{end}}</p>
    </div>
  </div>
</body>
</html>`
