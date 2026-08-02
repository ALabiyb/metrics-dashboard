package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com
// ---------------------------------------------------------------------------

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCAuthenticator implements "Continue with Keycloak SSO": the standard
// OAuth2 Authorization Code flow with OpenID Connect on top, against
// whichever realm/client is configured via the OIDC_* environment variables.
//
// It's optional — see NewOIDCAuthenticator. When it isn't configured, the
// dashboard works exactly as before with only DASHBOARD_USERS-based local
// logins, and the login page hides the SSO button entirely.
type OIDCAuthenticator struct {
	provider           *oidc.Provider
	verifier           *oidc.IDTokenVerifier
	oauth2Cfg          oauth2.Config
	adminRole          string
	auth               *Authenticator // for issuing session cookies + signing state cookies
	httpClient         *http.Client   // custom client for token exchange (same TLS config as discovery)
	endSessionEndpoint string         // Keycloak end_session_endpoint from discovery doc
}

// oidcClaims is the subset of ID token claims this app cares about. Keycloak
// (and most OIDC providers) put the username in preferred_username and realm
// roles under realm_access.roles.
type oidcClaims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// NewOIDCAuthenticator builds an OIDCAuthenticator from the OIDC_* env vars:
//
//	OIDC_ISSUER_URL    - e.g. https://keycloak.example.com/realms/myrealm
//	OIDC_CLIENT_ID     - a confidential client configured in that realm
//	OIDC_CLIENT_SECRET - the client's secret
//	OIDC_REDIRECT_URL  - must exactly match the client's "Valid Redirect URI",
//	                     e.g. http://localhost:8090/login/oidc/callback
//	OIDC_ADMIN_ROLE    - realm role mapped to the dashboard's "admin" role
//	                     (default "dashboard-admin"); everyone else who signs
//	                     in via SSO gets "viewer"
//
// If any of the first four are unset, OIDC is disabled: NewOIDCAuthenticator
// returns (nil, nil) and the login page simply won't show the SSO button.
func NewOIDCAuthenticator(ctx context.Context, auth *Authenticator) (*OIDCAuthenticator, error) {
	issuer := os.Getenv("OIDC_ISSUER_URL")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	clientSecret := os.Getenv("OIDC_CLIENT_SECRET")
	redirectURL := os.Getenv("OIDC_REDIRECT_URL")
	if issuer == "" || clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil, nil
	}

	// Allow skipping TLS verification for internal Keycloak instances that use
	// a self-signed or internal CA (set OIDC_TLS_SKIP_VERIFY=true).
	httpClient := &http.Client{}
	if os.Getenv("OIDC_TLS_SKIP_VERIFY") == "true" {
		log.Println("oidc: TLS verification disabled — only use with trusted internal CAs")
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	ctx = oidc.ClientContext(ctx, httpClient)

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovering issuer %s: %w", issuer, err)
	}

	adminRole := os.Getenv("OIDC_ADMIN_ROLE")
	if adminRole == "" {
		adminRole = "dashboard-admin"
	}

	// Extract end_session_endpoint from the OIDC discovery document so logout
	// can terminate the Keycloak SSO session, not just the local cookie.
	var disc struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	endSessionEndpoint := ""
	if err := provider.Claims(&disc); err != nil {
		log.Printf("oidc: cannot read end_session_endpoint — full OIDC logout disabled: %v", err)
	} else if disc.EndSessionEndpoint != "" {
		endSessionEndpoint = disc.EndSessionEndpoint
		log.Printf("oidc: full OIDC logout enabled via %s", endSessionEndpoint)
	}

	return &OIDCAuthenticator{
		provider:   provider,
		verifier:   provider.Verifier(&oidc.Config{ClientID: clientID}),
		httpClient: httpClient,
		oauth2Cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		adminRole:          adminRole,
		auth:               auth,
		endSessionEndpoint: endSessionEndpoint,
	}, nil
}

// logoutURL builds the Keycloak end_session URL with id_token_hint so Keycloak
// can terminate the SSO session without prompting the user to confirm logout.
// Returns "" if end_session_endpoint was not in the discovery document — the
// caller should fall back to a plain /login redirect.
func (o *OIDCAuthenticator) logoutURL(idToken string) string {
	if o.endSessionEndpoint == "" || idToken == "" {
		return ""
	}
	// Derive post_logout_redirect_uri from the configured callback URL:
	// strip "/login/oidc/callback" → append "/login".
	// This URI must be listed in the Keycloak client's "Valid post logout redirect URIs".
	postLogoutURI := strings.TrimSuffix(o.oauth2Cfg.RedirectURL, "/login/oidc/callback") + loginPath
	v := url.Values{}
	v.Set("id_token_hint", idToken)
	v.Set("post_logout_redirect_uri", postLogoutURI)
	return o.endSessionEndpoint + "?" + v.Encode()
}

// handleOIDCLogin starts the Authorization Code flow: it generates a random
// state (CSRF guard) and nonce (ID-token replay guard), stashes both in a
// short-lived signed cookie, and redirects the browser to Keycloak's login
// page.
func (o *OIDCAuthenticator) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	o.auth.setOIDCStateCookie(w, state, nonce)
	http.Redirect(w, r, o.oauth2Cfg.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

// handleOIDCCallback completes the Authorization Code flow: it validates the
// state cookie, exchanges the authorization code for tokens, verifies the ID
// token (issuer, audience, nonce), maps the user's Keycloak realm roles to a
// dashboard role, and issues a normal session cookie — same as a
// local-credentials login (see Authenticator.issueCookie).
func (o *OIDCAuthenticator) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	state, nonce, err := o.auth.oidcStateFromRequest(r)
	o.auth.clearOIDCStateCookie(w)
	if err != nil || r.URL.Query().Get("state") != state {
		log.Printf("oidc callback: invalid or missing state: %v", err)
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		log.Printf("oidc callback: provider returned error: %s", errParam)
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	token, err := o.oauth2Cfg.Exchange(oidc.ClientContext(r.Context(), o.httpClient), r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("oidc callback: code exchange failed: %v", err)
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		log.Printf("oidc callback: token response had no id_token")
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	idToken, err := o.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		log.Printf("oidc callback: id_token verification failed: %v", err)
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}
	if idToken.Nonce != nonce {
		log.Printf("oidc callback: nonce mismatch")
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("oidc callback: decoding claims failed: %v", err)
		http.Redirect(w, r, loginErrPath, http.StatusFound)
		return
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Subject
	}

	role := roleViewer
	for _, rr := range claims.RealmAccess.Roles {
		if rr == o.adminRole {
			role = roleAdmin
			break
		}
	}

	o.auth.issueCookie(w, username, role, rawIDToken)
	http.Redirect(w, r, "/", http.StatusFound)
}

// randomToken returns a URL-safe random string suitable for OAuth2 state and
// nonce values.
func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
