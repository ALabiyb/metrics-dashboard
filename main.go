package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: saidlabiybm@gmail.com
// ---------------------------------------------------------------------------

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed static
var staticFiles embed.FS

// dataSource is anything that can produce a point-in-time Snapshot. The
// simulator implements it; a Prometheus/Ceph-mgr-backed source would too —
// see README.md § "Connecting it to a real cluster" for how to build one and
// swap it in below.
type dataSource interface {
	Snapshot() Snapshot
}

func main() {
	// `go run . hash-password '<password>'` prints a bcrypt hash suitable for
	// DASHBOARD_USERS — it's not part of the server, just an operator helper.
	if len(os.Args) >= 2 && os.Args[1] == "hash-password" {
		runHashPassword(os.Args[2:])
		return
	}

	auth, err := NewAuthenticator()
	if err != nil {
		log.Fatalf("auth: %v (run `go run . hash-password <password>` to generate password_hash values)", err)
	}

	// Keycloak SSO is optional — see oidc.go. NewOIDCAuthenticator returns
	// (nil, nil) if OIDC_ISSUER_URL/OIDC_CLIENT_ID/OIDC_CLIENT_SECRET/
	// OIDC_REDIRECT_URL aren't all set, in which case the login page simply
	// won't show the "Continue with Keycloak SSO" button.
	oidcAuth, err := NewOIDCAuthenticator(context.Background(), auth)
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}
	auth.oidc = oidcAuth
	if oidcAuth != nil {
		log.Printf("oidc: Keycloak SSO enabled (issuer %s)", os.Getenv("OIDC_ISSUER_URL"))
	} else {
		log.Printf("oidc: Keycloak SSO disabled (set OIDC_ISSUER_URL/OIDC_CLIENT_ID/OIDC_CLIENT_SECRET/OIDC_REDIRECT_URL to enable)")
	}

	startedAt := time.Now()

	var source dataSource
	if promURL := os.Getenv("PROMETHEUS_URL"); promURL != "" {
		rs, err := NewRealSource(promURL)
		if err != nil {
			log.Fatalf("real source: %v", err)
		}
		go rs.Run()
		source = rs
		log.Printf("data source: real cluster via Prometheus (%s)", promURL)
	} else {
		sim := NewSimulator()
		go sim.Run()
		source = sim
		log.Printf("data source: simulator (set PROMETHEUS_URL to use a real cluster)")
	}
	// Expose which data source is active (and whether it's healthy) to the
	// login page's DATA SOURCE / STATUS fields. Both *Simulator and
	// *RealSource implement dataSourceStatus (see simulator.go / real_source.go).
	auth.dataSource = source.(dataSourceStatus)

	var statsSource simStatsIface
	if ss, ok := source.(simStatsIface); ok {
		statsSource = ss
	} else {
		statsSource = &noopStats{startedAt: startedAt}
	}

	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Read index.html from the embedded FS and substitute {{APP_COMPANY_NAME}}
	// so the footer and page title reflect the deployment's company name without
	// requiring a rebuild. Set APP_COMPANY_NAME in the ConfigMap or env.
	idxRaw, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		log.Fatal("reading embedded static/index.html:", err)
	}
	companyName := os.Getenv("APP_COMPANY_NAME")
	if companyName == "" {
		companyName = "DevSecOps Platform"
	}
	idxHTML := bytes.ReplaceAll(idxRaw, []byte("{{APP_COMPANY_NAME}}"), []byte(companyName))

	// serveIndex intercepts root/index.html requests to serve the substituted
	// bytes; all other paths fall through to the embedded static file server.
	serveIndex := func(base http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "" || r.URL.Path == "/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(idxHTML)
				return
			}
			base.ServeHTTP(w, r)
		}
	}

	staticFS := http.FileServer(http.FS(staticRoot))
	mux := http.NewServeMux()

	// -- public routes --------------------------------------------------
	mux.HandleFunc("/login", auth.handleLogin)
	mux.HandleFunc("/logout", auth.handleLogout)
	mux.HandleFunc("/embed", auth.handleEmbed(os.Getenv("EMBED_TOKEN")))
	if oidcAuth != nil {
		mux.HandleFunc("/login/oidc", oidcAuth.handleOIDCLogin)
		mux.HandleFunc("/login/oidc/callback", oidcAuth.handleOIDCCallback)
	}

	// -- authenticated routes (any role) ---------------------------------
	mux.HandleFunc("/api/me", auth.requireAuth(handleMe))
	mux.HandleFunc("/api/snapshot", auth.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(source.Snapshot()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	mux.Handle("/", auth.requireAuth(serveIndex(staticFS)))

	// -- TV kiosk mode (no auth) -----------------------------------------
	// Public read-only endpoints for the kiosk display. No login, no cookie,
	// no third-party-cookie issues in iframes. Safe to expose on a trusted
	// internal LAN — gate by source IP if exposing externally.
	// The SPA (index.html + jsx) is served from /tv/* via the same static FS;
	// the JS detects window.location.pathname.startsWith('/tv') and switches
	// its fetches from /api/* to /tv/* automatically.
	mux.HandleFunc("/tv/me", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"username": "tv",
			"role":     "viewer",
		})
	})
	mux.HandleFunc("/tv/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(source.Snapshot()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.Handle("/tv/", http.StripPrefix("/tv", serveIndex(staticFS)))
	mux.HandleFunc("/tv", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tv/", http.StatusMovedPermanently)
	})

	// -- admin-only routes ------------------------------------------------
	// Authorization in action: any signed-in user can view the dashboard,
	// but operational/debug info is restricted to the "admin" role. A 401
	// here means "log in"; a 403 means "you're logged in but not allowed".
	mux.HandleFunc("/api/admin/status", auth.requireAuth(requireRole(roleAdmin, handleAdminStatus(statsSource, startedAt))))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}
	addr := ":" + port
	log.Printf("K8s + Ceph dashboard listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

type simStatsIface interface{ Stats() (int, time.Time) }

// noopStats is used when the real source is active (no simulation ticks).
type noopStats struct{ startedAt time.Time }

func (n *noopStats) Stats() (int, time.Time) { return 0, n.startedAt }

// handleAdminStatus exposes operational counters gated to the "admin" role.
func handleAdminStatus(stats simStatsIface, startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := sessionFromContext(r.Context())
		ticks, dataStarted := stats.Stats()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"requestedBy":     s.Username,
			"role":            s.Role,
			"serverStartedAt": startedAt,
			"serverUptime":    time.Since(startedAt).Round(time.Second).String(),
			"dataStartedAt":   dataStarted,
			"simTicks":        ticks,
			"goVersion":       runtime.Version(),
			"goroutines":      runtime.NumGoroutine(),
		})
	}
}

func runHashPassword(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: go run . hash-password '<plaintext password>'")
		os.Exit(2)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(args[0]), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(hash))
}
