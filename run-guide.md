# Run Guide — K8s + Ceph Dashboard

Quick reference for running this locally and on Kubernetes. PowerShell syntax
(Windows). Full background: see `README.md`.

---

## 1. Run locally with `go run .`

**Step 1 — Generate password hashes**
```powershell
go run . hash-password 'admin-password-here'
go run . hash-password 'viewer-password-here'
```
Each prints a `$2a$10$...` bcrypt hash.

**Step 2 — Set env vars (per session)**
```powershell
$env:DASHBOARD_USERS = '[{"username":"admin","password_hash":"$2a$10$<hash1>","role":"admin"},{"username":"viewer","password_hash":"$2a$10$<hash2>","role":"viewer"}]'
$env:SESSION_SECRET = [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Max 256 }))
```

**Step 3 — Run**
```powershell
go run .
```
Open **http://localhost:8090** and sign in.

**Optional env vars**
| Var | Purpose | Default |
|---|---|---|
| `PORT` | HTTP port | `8090` |
| `PROMETHEUS_URL` | switch from simulator to live cluster data, e.g. `http://192.168.200.15:30909` | unset (uses simulator) |

---

## 2. Run the prebuilt binary

Same env vars as above, then:
```powershell
.\dashboard.exe
```

To rebuild it after code changes:
```powershell
go build -o dashboard.exe .
```

---

## 3. Run via Docker

```powershell
docker build -t k8s-ceph-dashboard:latest .

# generate hashes using the image (no local Go needed)
docker run --rm k8s-ceph-dashboard:latest hash-password 'admin-password-here'
docker run --rm k8s-ceph-dashboard:latest hash-password 'viewer-password-here'

docker run --rm -p 8090:8090 `
  -e DASHBOARD_USERS='[{"username":"admin","password_hash":"$2a$10$...","role":"admin"}]' `
  -e SESSION_SECRET="<random-32-byte-base64>" `
  k8s-ceph-dashboard:latest
```
Open **http://localhost:8090**.

---

## 4. Run on Kubernetes

Cluster context used: `kubernetes-admin@kubernetes`.

**Step 1 — Build & make the image available to the cluster**
```powershell
docker build -t k8s-ceph-dashboard:latest .
```
If cluster nodes can't pull from your local Docker daemon, push to a registry
the cluster can reach and update `image:` in `k8s/deployment.yaml`
accordingly.

**Step 2 — Namespace**
```powershell
kubectl apply -f k8s/namespace.yaml
```

**Step 3 — Auth Secret** (imperative — never commit real creds / don't apply
`secret.example.yaml` directly)
```powershell
$HASH_ADMIN = docker run --rm k8s-ceph-dashboard:latest hash-password 'pick-a-real-admin-password'
$HASH_VIEWER = docker run --rm k8s-ceph-dashboard:latest hash-password 'pick-a-real-viewer-password'
$SESSION_SECRET = [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Max 256 }))

kubectl create secret generic dashboard-auth -n dashboard `
  --from-literal=DASHBOARD_USERS="[{\"username\":\"admin\",\"password_hash\":\"$HASH_ADMIN\",\"role\":\"admin\"},{\"username\":\"viewer\",\"password_hash\":\"$HASH_VIEWER\",\"role\":\"viewer\"}]" `
  --from-literal=SESSION_SECRET="$SESSION_SECRET"
```

**Step 4 — Config (`k8s/config.yaml`)**

`PORT` and `PROMETHEUS_URL` come from the `dashboard-config` ConfigMap.
Currently:
```yaml
PROMETHEUS_URL: "http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090"
```
Adjust to match your in-cluster Prometheus Service (namespace + Helm release
name), or use the external NodePort URL e.g. `http://192.168.200.15:30909`.
Then apply it:
```powershell
kubectl apply -f k8s/config.yaml
```

**Step 5 — Deploy**
```powershell
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
```

**Step 6 — Access**

Quick local check:
```powershell
kubectl port-forward -n dashboard svc/dashboard 8090:80
```
→ http://localhost:8090

Or via your Gateway API HTTPRoute: copy `k8s/httproute.example.yaml` →
`k8s/httproute.yaml`, edit `hostnames`/`parentRefs` for your cluster's
Gateway, then:
```powershell
kubectl apply -f k8s/httproute.yaml
```
TLS terminates at the Gateway's HTTPS listener — once served over HTTPS,
flip the `Secure` cookie flag on in `auth.go`'s `issueCookie`.

---

## 5. Sanity checks

```powershell
kubectl get pods -n dashboard
kubectl logs -n dashboard -l app=dashboard
kubectl get svc -n dashboard
```

---

## 6. Enable Keycloak SSO (optional)

Adds a "Continue with Keycloak SSO" button to `/login`, alongside the local
username/password form. It stays hidden unless all four required vars below
are set — see `README.md` § Authentication for details on role mapping.

```powershell
$env:OIDC_ISSUER_URL    = "https://keycloak.example.com/realms/myrealm"
$env:OIDC_CLIENT_ID     = "k8s-dashboard"
$env:OIDC_CLIENT_SECRET = "<client secret from Keycloak>"
$env:OIDC_REDIRECT_URL  = "http://localhost:8090/login/oidc/callback"
$env:OIDC_ADMIN_ROLE    = "dashboard-admin"   # optional, this is the default
```

In Keycloak, the client's **Valid Redirect URIs** must include
`OIDC_REDIRECT_URL` exactly (scheme + host + port + path). Any signed-in SSO
user gets the `viewer` role; users whose token includes `OIDC_ADMIN_ROLE` in
`realm_access.roles` get `admin`.

On Kubernetes, add these as extra `env:` entries on the `dashboard` container
in `k8s/deployment.yaml` (put `OIDC_CLIENT_SECRET` in the `dashboard-auth`
Secret alongside `SESSION_SECRET` — see the commented-out example there and
in `k8s/secret.example.yaml`).

---

## Notes

- Sessions are stateless HMAC-signed cookies (`SESSION_SECRET`) — safe to run
  multiple replicas, no sticky sessions needed.
- Rotating `SESSION_SECRET` invalidates all existing sessions.
- `/api/admin/status` requires `role: admin`.
- Without `PROMETHEUS_URL`, the app falls back to the built-in simulator
  (`simulator.go`) — a fake but realistic 10-node cluster + Ceph data.
