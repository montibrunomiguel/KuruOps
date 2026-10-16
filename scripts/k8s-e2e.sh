#!/usr/bin/env bash
# End-to-end test of deploy/k8s on a disposable kind cluster.
#
# `kubeconform` (the Kubernetes Manifests CI job) proves the YAML is well formed. It cannot
# prove the pods START, that the images run as the user the manifests require, that the
# NetworkPolicies allow what the app needs and deny what they promise to, or that the stack
# serves a request. Two real defects of exactly that kind sat in the manifests undetected --
# no pod could start under `runAsNonRoot` because the images declared a named user, and the
# uploads volume was not writable -- because nothing ever deployed them. This does.
#
# What it does, in order:
#   1. creates a kind cluster with Calico (kind's default CNI does not enforce NetworkPolicy),
#      ingress-nginx and metrics-server;
#   2. builds the five images and loads them into the cluster;
#   3. deploys a throwaway Postgres (deploy/k8s/tests/postgres.yaml), a fake LLM, the secrets,
#      then the REAL manifests: migration Job, roles Job (optional/postgres-roles-job.yaml),
#      api, ingest, analyzer, worker, frontend, NetworkPolicies, PDBs/HPAs, Ingress with TLS;
#   4. asserts: every Deployment becomes ready; the repo's own smoke test passes through the
#      frontend; the webhook -> ingest -> analyzer -> model pipeline completes across pods;
#      an attachment uploaded through one api replica downloads through the other; the live
#      event stream reaches a client through the ingress unbuffered; HTTPS works through the
#      ingress; NetworkPolicy blocks a stray pod and allows the frontend; PDBs exist and the
#      HPA reads real metrics.
#
# Usage:   bash scripts/k8s-e2e.sh
# Needs:   docker, kubectl, kind (>= 0.24), openssl, curl, node (to read JSON), and ~6 GB of RAM
#          for Docker. Takes roughly 10-15 minutes, most of it building images.
# Env:     E2E_KEEP=1        keep the cluster afterwards (default: delete it)
#          E2E_CLUSTER=name  cluster name (default kuruops-e2e)
#          E2E_SKIP_BUILD=1  reuse kuruops-*:e2e images already built locally
#          E2E_REUSE=1       reuse an existing cluster (its namespace is recreated) -- for iterating
#          KIND=path         the kind binary (default: kind on PATH)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER="${E2E_CLUSTER:-kuruops-e2e}"
KIND="${KIND:-kind}"
NS=kuruops
HOST=kuruops.e2e.test
HTTP_PORT="${E2E_HTTP_PORT:-18080}"
HTTPS_PORT="${E2E_HTTPS_PORT:-18443}"
# On Git Bash for Windows, shell paths (/c/Users/.. or /tmp/..) mean nothing to docker, kind, kubectl
# or openssl, which are native programs; cygpath -m turns them into C:/... , which both sides
# understand. A no-op elsewhere. The scratch directory is made native up front for that reason.
native() { cygpath -m "$1" 2>/dev/null || printf %s "$1"; }
WORK="$(native "$(mktemp -d)")"
K="kubectl --context kind-${CLUSTER}"
KN="$K -n $NS"
export MSYS_NO_PATHCONV=1

pass=0; fail=0
ok()   { echo "  ok   - $1"; pass=$((pass + 1)); }
bad()  { echo "  FAIL - $1"; fail=$((fail + 1)); }
check() { local desc="$1"; shift; if "$@" >/dev/null 2>&1; then ok "$desc"; else bad "$desc"; fi; }
step() { echo; echo "==> $*"; }

PIDS=()
cleanup() {
  local code=$?
  for p in "${PIDS[@]:-}"; do [[ -n "$p" ]] && kill "$p" 2>/dev/null || true; done
  if [[ $code -ne 0 || $fail -ne 0 ]] && $K get ns "$NS" >/dev/null 2>&1; then
    echo; echo "==> diagnostics"
    $KN get pods -o wide 2>&1 | head -40 || true
    for d in api ingest analyzer worker frontend; do
      echo "--- logs: $d"; $KN logs "deploy/$d" --tail=15 2>&1 | head -20 || true
    done
    $KN get events --sort-by=.lastTimestamp 2>&1 | tail -15 || true
  fi
  if [[ "${E2E_KEEP:-}" != "1" ]]; then
    "$KIND" delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  else
    echo "cluster '$CLUSTER' kept (E2E_KEEP=1)"
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

for c in docker kubectl openssl curl node "$KIND"; do
  command -v "$c" >/dev/null 2>&1 || { echo "missing required tool: $c" >&2; exit 2; }
done

# ---------------------------------------------------------------- 1. cluster
step "creating the kind cluster (Calico, ingress-nginx, metrics-server)"
cat > "$WORK/kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  disableDefaultCNI: true
  podSubnet: 192.168.0.0/16
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
    extraPortMappings:
      - { containerPort: 80,  hostPort: ${HTTP_PORT},  protocol: TCP }
      - { containerPort: 443, hostPort: ${HTTPS_PORT}, protocol: TCP }
EOF
if [[ "${E2E_REUSE:-}" == "1" ]] && $K get nodes >/dev/null 2>&1; then
  echo "reusing cluster ${CLUSTER}; recreating namespace ${NS}"
  $K delete namespace "$NS" --ignore-not-found --wait=true >/dev/null
else
"$KIND" delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
# No --wait: with the default CNI disabled the node cannot become Ready until Calico is applied.
"$KIND" create cluster --name "$CLUSTER" --config "$(native "$WORK/kind.yaml")" >/dev/null
$K apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/calico.yaml >/dev/null
$K -n kube-system rollout status ds/calico-node --timeout=400s >/dev/null
$K wait --for=condition=Ready node --all --timeout=240s >/dev/null
$K apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.12.0/deploy/static/provider/kind/deploy.yaml >/dev/null
$K apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml >/dev/null
$K -n kube-system patch deploy metrics-server --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null
fi
ok "cluster up"

# ---------------------------------------------------------------- 2. images
step "building and loading the images"
build() { # name, dockerfile, target
  if [[ "${E2E_SKIP_BUILD:-}" == "1" ]] && docker image inspect "kuruops-$1:e2e" >/dev/null 2>&1; then return; fi
  local args=(-f "$(native "$ROOT")/$2" -t "kuruops-$1:e2e")
  [[ -n "${3:-}" ]] && args+=(--target "$3")
  docker build -q "${args[@]}" "$(native "$ROOT")" >/dev/null
}
build api backend/Dockerfile api
build ingest backend/Dockerfile ingest
build analyzer backend/Dockerfile analyzer
build worker backend/Dockerfile worker
build frontend frontend/Dockerfile
docker save -o "$(native "$WORK/images.tar")" kuruops-api:e2e kuruops-ingest:e2e kuruops-analyzer:e2e kuruops-worker:e2e kuruops-frontend:e2e
# Docker Desktop's containerd image store breaks `kind load docker-image`; the archive route works everywhere.
"$KIND" load image-archive "$(native "$WORK/images.tar")" --name "$CLUSTER" >/dev/null
ok "images loaded"

# ------------------------------------------------- 3. manifests to deploy
step "preparing manifests (images -> :e2e, never pulled; host -> $HOST)"
mkdir -p "$WORK/k8s"
for f in "$ROOT"/deploy/k8s/*.yaml; do cp "$f" "$WORK/k8s/"; done
cp "$ROOT"/deploy/k8s/tests/postgres.yaml "$ROOT"/deploy/k8s/tests/mock-llm.yaml "$ROOT"/deploy/k8s/optional/postgres-roles-job.yaml "$WORK/k8s/"
# Same images, locally built tags, and never pulled.
sed -i -E 's#^( *)image: kuruops-([a-z]+):latest$#\1image: kuruops-\2:e2e\n\1imagePullPolicy: Never#' "$WORK"/k8s/*.yaml
sed -i "s#kuruops.example.com#${HOST}#g" "$WORK/k8s/12-ingress.yaml" "$WORK/k8s/02-configmap.yaml"
# The fake LLM lives on a private address; the SSRF guard must be told that is fine here.
sed -i 's#ALLOW_PRIVATE_NETWORK_TARGETS: ""#ALLOW_PRIVATE_NETWORK_TARGETS: "true"#' "$WORK/k8s/02-configmap.yaml"
grep -q 'ALLOW_PRIVATE_NETWORK_TARGETS: "true"' "$WORK/k8s/02-configmap.yaml" || \
  printf '  ALLOW_PRIVATE_NETWORK_TARGETS: "true"\n' >> "$WORK/k8s/02-configmap.yaml"
# No real OAuth apps here; the placeholders would show up as a configured integration.
sed -i 's#GOOGLE_OAUTH_CLIENT_ID: "CHANGEME"#GOOGLE_OAUTH_CLIENT_ID: ""#; s#SLACK_CLIENT_ID: "CHANGEME"#SLACK_CLIENT_ID: ""#' "$WORK/k8s/02-configmap.yaml"
# Run quickly: analyzer polls fast; the ingress trusts the controller pod range for client IPs.
sed -i 's#AI_ANALYSIS_POLL_INTERVAL: "5s"#AI_ANALYSIS_POLL_INTERVAL: "2s"#' "$WORK/k8s/02-configmap.yaml"
ok "manifests prepared"

step "secrets"
rnd() { openssl rand -base64 36 | tr -d '/+=\n' | cut -c1-32; }
PG_SUPER=$(rnd); APP_PW=$(rnd); WORKER_PW=$(rnd)
WORK_N="$WORK"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$WORK_N/jwt_private.pem" 2>/dev/null
openssl rsa -in "$WORK_N/jwt_private.pem" -pubout -out "$WORK_N/jwt_public.pem" 2>/dev/null
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -keyout "$WORK_N/tls.key" -out "$WORK_N/tls.crt" \
  -subj "/CN=${HOST}" -addext "subjectAltName=DNS:${HOST}" >/dev/null 2>&1
$K apply -f "$WORK/k8s/00-namespace.yaml" >/dev/null
$KN create secret generic kuruops-pg-superuser --from-literal=password="$PG_SUPER" >/dev/null
$KN create secret generic kuruops-pg-roles --from-literal=app_password="$APP_PW" --from-literal=worker_password="$WORKER_PW" >/dev/null
$KN create secret generic kuruops-secrets \
  --from-literal=DATABASE_URL_APP="postgres://kuruops_app:${APP_PW}@kuruops-pg-rw:5432/kuruops?sslmode=disable" \
  --from-literal=DATABASE_URL_WORKER="postgres://kuruops_worker:${WORKER_PW}@kuruops-pg-rw:5432/kuruops?sslmode=disable" \
  --from-literal=DATABASE_URL_MIGRATE="postgres://postgres:${PG_SUPER}@kuruops-pg-rw:5432/kuruops?sslmode=disable" \
  --from-literal=SECRETS_ENCRYPTION_KEY="$(openssl rand -base64 32)" \
  --from-file=JWT_PRIVATE_KEY="$(native "$WORK/jwt_private.pem")" \
  --from-file=JWT_PUBLIC_KEY="$(native "$WORK/jwt_public.pem")" \
  --from-literal=GOOGLE_OAUTH_CLIENT_SECRET= --from-literal=SLACK_CLIENT_SECRET= --from-literal=SLACK_SIGNING_SECRET= \
  --from-literal=BACKUP_AWS_ACCESS_KEY_ID= --from-literal=BACKUP_AWS_SECRET_ACCESS_KEY= \
  --from-literal=BACKUP_S3_BUCKET= --from-literal=BACKUP_AWS_REGION=us-east-1 >/dev/null
$KN create secret tls kuruops-tls --cert="$(native "$WORK/tls.crt")" --key="$(native "$WORK/tls.key")" >/dev/null
$KN create configmap kuruops-db-init --from-file="$(native "$ROOT/db/init")" >/dev/null
ok "secrets created"

# ---------------------------------------------------------------- 4. deploy
step "database: Postgres, migrations, roles"
$KN apply -f "$WORK/k8s/postgres.yaml" >/dev/null
$KN rollout status deploy/kuruops-pg --timeout=180s >/dev/null
$KN apply -f "$WORK/k8s/02-configmap.yaml" >/dev/null
$KN apply -f "$WORK/k8s/03-migration-job.yaml" >/dev/null
check "migration Job completes" $KN wait --for=condition=complete job/kuruops-migrate --timeout=240s
$KN apply -f "$WORK/k8s/postgres-roles-job.yaml" >/dev/null
check "roles Job (db/init scripts) completes" $KN wait --for=condition=complete job/kuruops-pg-roles --timeout=120s

step "the stack"
$KN apply -f "$WORK/k8s/mock-llm.yaml" >/dev/null
for f in 04-api 05-ingest 06-worker 06a-analyzer 07-frontend; do $KN apply -f "$WORK/k8s/$f.yaml" >/dev/null; done
$KN apply -f "$WORK/k8s/10-networkpolicy.yaml" >/dev/null
$KN apply -f "$WORK/k8s/11-availability.yaml" >/dev/null
$KN apply -f "$WORK/k8s/12-ingress.yaml" >/dev/null
$K -n ingress-nginx wait --for=condition=ready pod -l app.kubernetes.io/component=controller --timeout=240s >/dev/null
for d in api ingest analyzer worker frontend mock-llm; do
  check "Deployment $d becomes ready" $KN rollout status "deploy/$d" --timeout=240s
done

# --------------------------------------------------------------- 5. exercise
step "through the frontend: the repository's own smoke test"
$KN port-forward svc/frontend 28999:8080 >/dev/null 2>&1 & PIDS+=($!)
$KN port-forward svc/ingest 28998:8080 >/dev/null 2>&1 & PIDS+=($!)
for _ in $(seq 1 30); do curl -sf http://localhost:28999/ >/dev/null && break; sleep 1; done
API_URL=http://localhost:28999 INGEST_URL=http://localhost:28998 bash "$ROOT/scripts/smoke-test.sh" >"$WORK/smoke.log" 2>&1 \
  && ok "scripts/smoke-test.sh passes ($(grep -c '  ok  ' "$WORK/smoke.log") checks)" \
  || { bad "scripts/smoke-test.sh"; tail -15 "$WORK/smoke.log"; }

step "authenticated flows"
A=http://localhost:28999
jf() { node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{const v=JSON.parse(s);console.log(String(eval("v."+process.argv[1])??""))}catch(e){console.log("")}})' "$1"; }
TOK=$(curl -s -X POST $A/auth/login -H 'Content-Type: application/json' -d '{"email":"admin@kuruops.local","password":"SmokeTest-Rotated-Pw1!"}' | jf token)
[[ -n "$TOK" ]] && ok "admin login (jwt mode) returns a token" || bad "admin login"
AUTH=(-H "Authorization: Bearer $TOK")

# A provider that points at the in-cluster fake LLM, auto-analysing every alert.
PID=$(curl -s -X POST $A/api/v1/settings/llm-providers "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"mock","kind":"openai_compatible","baseUrl":"http://mock-llm:9000","model":"mock","apiKey":"k","autoAnalyzeAllAlerts":true}' | jf id)
curl -s -X POST "$A/api/v1/settings/llm-providers/$PID/default" "${AUTH[@]}" >/dev/null
WH=$(curl -s -X POST $A/api/v1/settings/webhooks "${AUTH[@]}" -H 'Content-Type: application/json' -d '{"name":"e2e","source":"e2e"}')
HOOK_TOKEN=$(echo "$WH" | jf token)

# Open the live event stream through the INGRESS before the alert arrives, so what it
# receives proves the stream is not buffered by either proxy.
curl -sk --max-time 25 -N --resolve "${HOST}:${HTTPS_PORT}:127.0.0.1" "https://${HOST}:${HTTPS_PORT}/api/v1/events/stream" "${AUTH[@]}" >"$WORK/sse.txt" 2>/dev/null & PIDS+=($!)
sleep 2
curl -s -X POST http://localhost:28998/hooks -H "X-Webhook-Token: $HOOK_TOKEN" -d '{"title":"e2e analysed alert","severity":"high"}' >/dev/null

ALERT_ID=""
for _ in $(seq 1 20); do
  ALERT_ID=$(curl -s "$A/api/v1/alerts" "${AUTH[@]}" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const a=JSON.parse(s).find(x=>x.title==="e2e analysed alert");console.log(a?a.id:"")})')
  [[ -n "$ALERT_ID" ]] && break; sleep 1
done
[[ -n "$ALERT_ID" ]] && ok "webhook through ingest created an alert" || bad "webhook through ingest created an alert"

STATUS=""
for _ in $(seq 1 40); do
  STATUS=$(curl -s "$A/api/v1/alerts/$ALERT_ID/analyze/messages" "${AUTH[@]}" | jf status)
  [[ "$STATUS" == "completed" ]] && break; sleep 1
done
[[ "$STATUS" == "completed" ]] && ok "the analyzer (a different pod) analysed it via the fake model" || bad "analysis did not complete (status '$STATUS')"

sleep 1
if grep -q "event: alert" "$WORK/sse.txt" 2>/dev/null; then ok "the live event stream delivered the alert through the ingress, unbuffered"; else bad "no event reached the stream through the ingress"; fi

# Attachment: upload through whichever api replica answers, then read it back many times so
# both replicas serve it -- only possible because attachments live in Postgres, not a volume.
printf '\x89PNG\r\n\x1a\n' > "$WORK/pix.png"; head -c 64 /dev/urandom >> "$WORK/pix.png"
UP=$(curl -s -X POST $A/api/v1/uploads/images "${AUTH[@]}" -F "file=@$(native "$WORK/pix.png");type=image/png" -F kind=alert -F "id=$ALERT_ID")
URL=$(echo "$UP" | jf url)
if [[ -n "$URL" ]]; then
  served=0; for _ in $(seq 1 12); do [[ $(curl -s -o "$WORK/discard" -w '%{http_code}' "$A$URL" "${AUTH[@]}" || true) == 200 ]] && served=$((served + 1)); done
  [[ $served -eq 12 ]] && ok "an uploaded attachment is served on all 12 reads (both api replicas)" || bad "attachment served only $served/12 times"
else
  bad "attachment upload failed: $UP"
fi

step "ingress and TLS"
code=$(curl -sk -o "$WORK/discard" -w '%{http_code}' --resolve "${HOST}:${HTTPS_PORT}:127.0.0.1" "https://${HOST}:${HTTPS_PORT}/" || true)
[[ "$code" == "200" ]] && ok "HTTPS serves the UI through the ingress" || bad "HTTPS UI returned $code"
code=$(curl -s -o "$WORK/discard" -w '%{http_code}' --resolve "${HOST}:${HTTP_PORT}:127.0.0.1" "http://${HOST}:${HTTP_PORT}/" || true)
[[ "$code" == "308" || "$code" == "301" || "$code" == "302" ]] && ok "plain HTTP redirects to HTTPS" || bad "plain HTTP returned $code (wanted a redirect)"
code=$(curl -sk -o "$WORK/discard" -w '%{http_code}' -X POST --resolve "${HOST}:${HTTPS_PORT}:127.0.0.1" "https://${HOST}:${HTTPS_PORT}/hooks" -d '{}' || true)
[[ "$code" == "401" ]] && ok "/hooks reaches ingest (401 without a token)" || bad "/hooks returned $code"
code=$(curl -sk -o "$WORK/discard" -w '%{http_code}' --resolve "${HOST}:${HTTPS_PORT}:127.0.0.1" "https://${HOST}:${HTTPS_PORT}/api/v1/alerts" || true)
[[ "$code" == "401" ]] && ok "/api reaches the api through the frontend (401 without a token)" || bad "/api returned $code"

step "NetworkPolicy"
$KN run probe --image=kuruops-frontend:e2e --image-pull-policy=Never --restart=Never --labels=app=probe --command -- sleep 600 >/dev/null
$KN wait --for=condition=Ready pod/probe --timeout=90s >/dev/null
if $KN exec probe -- wget -q -T 4 -O- http://api:8080/livez >/dev/null 2>&1; then bad "a stray pod reached the api"; else ok "a stray pod cannot reach the api"; fi
if $KN exec probe -- wget -q -T 4 -O- http://kuruops-pg-rw:5432 >/dev/null 2>&1; then :; fi   # TCP, not HTTP: no assertion
FE=$($KN get pod -l app=frontend -o name | head -1)
$KN exec "${FE#pod/}" -- wget -q -T 5 -O- http://api.kuruops.svc.cluster.local:8080/livez >/dev/null 2>&1 \
  && ok "the frontend reaches the api (FQDN, resolver from kube-dns)" || bad "the frontend cannot reach the api"
if $KN exec "${FE#pod/}" -- wget -q -T 4 -O- http://1.1.1.1/ >/dev/null 2>&1; then bad "the frontend reached the internet"; else ok "the frontend has no route to the internet"; fi

step "availability objects"
check "PodDisruptionBudgets exist (api, ingest, analyzer, worker, frontend)" bash -c "[ \$($K -n $NS get pdb -o name | wc -l) -ge 5 ]"
for _ in $(seq 1 30); do
  out=$($KN get hpa frontend -o jsonpath='{.status.currentMetrics[0].resource.current.averageUtilization}' 2>/dev/null || true)
  [[ -n "$out" ]] && break; sleep 5
done
[[ -n "${out:-}" ]] && ok "the frontend HPA reads real metrics (${out}% CPU)" || bad "the frontend HPA has no metrics"
check "api runs 2 replicas" bash -c "[ \"\$($K -n $NS get deploy api -o jsonpath='{.status.readyReplicas}')\" -ge 2 ]"
check "worker runs 2 replicas (hot standby)" bash -c "[ \"\$($K -n $NS get deploy worker -o jsonpath='{.status.readyReplicas}')\" -ge 2 ]"
check "analyzer runs 2 replicas" bash -c "[ \"\$($K -n $NS get deploy analyzer -o jsonpath='{.status.readyReplicas}')\" -ge 2 ]"

echo
echo "==> ${pass} passed, ${fail} failed"
[[ $fail -eq 0 ]]
