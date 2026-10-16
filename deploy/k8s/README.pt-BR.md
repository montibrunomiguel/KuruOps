<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# KuruOps no Kubernetes

Manifests para rodar a stack real (api, ingest, analyzer, worker, frontend) em um cluster. Sem Helm
chart: YAML simples que você pode ler, comparar e aplicar. Ele é exercitado de ponta a ponta --
migrations, roles, as cinco imagens, NetworkPolicy, PodDisruptionBudgets, HorizontalPodAutoscalers,
um Ingress com TLS e o pipeline webhook → ingest → analyzer → modelo entre pods -- num cluster kind
descartável pelo `scripts/k8s-e2e.sh`, que o CI roda a cada mudança.

## O que NÃO está incluído

- **Postgres.** Todo manifest aqui assume uma instância Postgres 16 já existente e acessível (um
  RDS/Cloud SQL multi-AZ gerenciado é a melhor escolha, ou o cluster opcional de 3 instâncias
  CloudNativePG em [`optional/`](optional/README.md), que tem failover automático). As roles
  `kuruops_app`/`kuruops_worker` e as políticas de RLS já precisam existir lá (veja `db/init/*.sql` e
  `db/README.md`) antes de apontar o `01-secret.example.yaml` para ela. O Postgres é o único componente
  que estes manifests não tornam altamente disponível por conta própria.
- **Certificado ou DNS.** O `12-ingress.yaml` é o ponto de entrada público (hooks → ingest, o resto →
  frontend, TLS terminado ali), mas precisa de um ingress controller no cluster (ingress-nginx
  assumido), do seu hostname no lugar de `kuruops.example.com` e de um Secret TLS chamado
  `kuruops-tls` -- emitido pelo cert-manager (há um emissor de exemplo em
  [`optional/cert-manager-issuer.yaml`](optional/cert-manager-issuer.yaml)) ou criado por você.
  Defina `TRUSTED_PROXY_CIDR` no frontend (`07-frontend.yaml`) com a faixa de pods do controller para
  que o rate limit de login enxergue os endereços reais dos clientes.
- **O autoscaling depende de métricas.** O `11-availability.yaml` adiciona HorizontalPodAutoscalers
  para `api` (2–6), `ingest` (2–6) e `frontend` (2–4) e PodDisruptionBudgets para todo Deployment; os
  HPAs precisam do metrics-server no cluster e, sem ele, não fazem nada e valem as contagens de
  réplicas de cada manifest. O `api` pode escalar porque os anexos ficam no Postgres
  (`UPLOAD_BACKEND=database` no `02-configmap.yaml`), não em um volume; se você mantiver um volume
  ReadWriteOnce com `UPLOAD_BACKEND=local`, o api volta a uma réplica. O `analyzer` roda duas réplicas que
  compartilham a fila de análises (dimensione contra o rate limit do provedor de LLM, não contra CPU);
  o `worker` roda duas como standby quente (um advisory lock do Postgres por tick deixa uma executar e a
  outra pular), não por vazão.
- **Gerenciamento de secrets.** `01-secret.example.yaml` é um template com
  valores de placeholder, não algo para dar `kubectl apply` e esquecer —
  veja os comentários no próprio arquivo.

## Ordem de aplicação

```bash
kubectl apply -f deploy/k8s/00-namespace.yaml

# Copie 01-secret.example.yaml, preencha cada CHANGEME, NÃO faça commit da
# cópia -- em seguida aplique o seu arquivo real em vez do arquivo de exemplo.
kubectl apply -f deploy/k8s/02-configmap.yaml

# Espera o Job de fato terminar antes de continuar -- api/ingest/worker
# assumem que o schema já existe na primeira inicialização.
kubectl apply -f deploy/k8s/03-migration-job.yaml
kubectl wait --for=condition=complete --timeout=120s -n kuruops job/kuruops-migrate

kubectl apply -f deploy/k8s/04-api.yaml
kubectl apply -f deploy/k8s/05-ingest.yaml
kubectl apply -f deploy/k8s/06-worker.yaml
kubectl apply -f deploy/k8s/06a-analyzer.yaml
kubectl apply -f deploy/k8s/07-frontend.yaml

# Opcional, mas fortemente recomendado antes que dados reais comecem a
# entrar -- veja "Backups" abaixo e os comentários no próprio
# 08-backup-cronjob.yaml para os pré-requisitos de bucket S3/IAM que isso
# exige primeiro.
kubectl apply -f deploy/k8s/08-backup-cronjob.yaml

# Opcional, mas fortemente recomendado para um lançamento assistido em
# produção -- veja "Monitoring" abaixo. Sem pré-requisitos além do próprio
# cluster; o receiver do Alertmanager sai como uma rota nula de placeholder
# até você editar o alertmanager-config em 09-monitoring.yaml para apontar
# para um destino real.
kubectl apply -f deploy/k8s/09-monitoring.yaml

# Recomendado: nega todo ingresso dentro do namespace por padrão e reabre só os
# caminhos que a stack usa (frontend e ingest de webhooks vindos de fora, a API
# só a partir do frontend, métricas só a partir do Prometheus). Precisa de um
# CNI que aplique NetworkPolicy (Calico, Cilium, ...) -- em um que não aplique
# fica inerte sem avisar, então confirme com o teste do pod descartável no
# cabeçalho de 10-networkpolicy.yaml.
kubectl apply -f deploy/k8s/10-networkpolicy.yaml

# PodDisruptionBudgets (um drain de nó nunca derruba o último pod de ingest/frontend)
# e HorizontalPodAutoscalers de ingest e frontend. Os HPAs precisam do metrics-server.
kubectl apply -f deploy/k8s/11-availability.yaml

# O ponto de entrada público. Precisa de um ingress controller, do seu hostname e do Secret
# kuruops-tls (veja "O que NÃO está incluído" acima e o cabeçalho do 12-ingress.yaml).
kubectl apply -f deploy/k8s/12-ingress.yaml
```

O nginx do frontend encontra a api por `API_UPSTREAM` (definido no
`07-frontend.yaml` com o FQDN do Service) e resolve a cada requisição, então um
pod da api recriado nunca deixa o frontend discando para um endereço morto. Troque
`cluster.local` nesse valor se o seu cluster usa outro domínio. As três imagens do
backend imprimem e exportam o build de que foram compiladas (`kuruops_build_info`);
o `KuruOpsBuildSkew` em `09-monitoring.yaml` dispara quando mais de um build fica
ativo por 20 minutos, ou seja, um deploy parcial.

Reaplicar todo o conjunto após um novo release é seguro: o Job de migração é
idempotente (o golang-migrate só aplica versões que ainda não registrou,
veja o comentário no `03-migration-job.yaml`), e cada apply de
Deployment/Service/ConfigMap é uma simples atualização declarativa. Ajuste a
tag `image:` em `04-api.yaml`/`05-ingest.yaml`/`06-worker.yaml`/`06a-analyzer.yaml`/`07-frontend.yaml`
para o que o seu build publicou (`:latest` aqui é um placeholder para testar
os manifests, não uma estratégia real de fixação de versão de release).

## Testando estes manifests

`bash scripts/k8s-e2e.sh` implanta tudo isto num cluster kind descartável (com Calico, para que a
NetworkPolicy seja de fato aplicada) e confere: todo Deployment fica pronto, o smoke test do repositório
passa pelo frontend, um webhook chega ao analyzer em outro pod e é analisado, um anexo enviado por uma
réplica do api é servido pela outra, o stream de eventos chega a um cliente pelo Ingress sem buffer, um
pod estranho não alcança o api e o HPA lê métricas reais. Precisa de docker, kind, kubectl e openssl e
leva uns dez minutos. As regras de alerta em `09-monitoring.yaml` são validadas e testadas por
`bash scripts/check-prometheus-rules.sh`.

## Imagens

Construídas da mesma forma que o `docker-compose.yml` as constrói localmente
(os targets `api`/`ingest`/`analyzer`/`worker` do `backend/Dockerfile`,
`frontend/Dockerfile`) — envie-as para o registry que o seu cluster consiga
puxar e atualize os campos `image:` de acordo; esses manifests referenciam
os mesmos nomes locais sem tag que o `docker compose build` produz
(`kuruops-api:latest`, etc.) como placeholder.

## Health checks e limites de recursos

`api`/`ingest`/`worker` expõem `/healthz` (verifica o banco de dados, 503 em
caso de falha — veja `internal/httpserver.HealthCheck`), `/livez` (200
incondicional, sem dependência downstream — veja
`internal/httpserver.Livez`), e `/metrics` (formato texto do Prometheus,
veja `internal/httpserver/metrics.go`). O `readinessProbe` usa `/healthz` e
o `livenessProbe` usa `/livez` — deliberadamente diferentes: uma liveness
probe apoiada na mesma verificação de banco de dados que a readiness faria
o kubelet matar e reiniciar todas as réplicas de uma vez em uma instabilidade
transitória do Postgres, transformando um soluço breve do banco em uma
tempestade de reinícios sincronizados em vez de simplesmente deixar a
readiness contornar o problema. `resources.requests`/`limits` espelham o
`mem_limit`/`cpus` do `docker-compose.yml` para os mesmos serviços — mesma
ressalva se aplica: dimensionado para experimentar, não uma recomendação de
dimensionamento de produção. Observe as séries reais `kuruops_db_pool_*` /
`kuruops_http_request_duration_seconds` em `/metrics` depois de implantado
e ajuste a partir daí.

## Monitoring

`09-monitoring.yaml` roda um Prometheus + Alertmanager mínimo autogerenciado
— fazendo scrape do `/metrics` já existente de api/ingest/worker (sem
necessidade de mudanças de código, ele já está lá) e alertando sobre o
básico: um alvo de scrape ficando indisponível, uma taxa elevada de 5xx,
latência p99 alta, saturação do pool de conexões, e um job de sweep do
worker ficando obsoleto (veja os comentários no próprio ConfigMap para os
limiares exatos — pontos de partida, ainda não ajustados contra tráfego
real). Sem armazenamento persistente e sem Grafana, deliberadamente: isso
responde "algo está pegando fogo agora", não "me mostre um dashboard dos
últimos 30 dias". Veja o comentário de cabeçalho do próprio manifest para
saber como apontar um provedor gerenciado (Grafana Cloud, Datadog etc.) para
essas mesmas métricas depois. O receiver do Alertmanager sai como uma rota
nula de placeholder — os alertas disparam e aparecem na própria UI do
Prometheus/Alertmanager, mas não notificam ninguém até você editar o
`alertmanager-config` para apontar para um destino real.

## Backups

`08-backup-cronjob.yaml` roda `pg_dump` uma vez por dia e envia o dump para
o S3 — veja os comentários no próprio arquivo para os pré-requisitos exatos
(bucket, credenciais IAM, as chaves `BACKUP_*` que ele espera em
`01-secret.example.yaml` e `02-configmap.yaml`). O RPO com o agendamento
diário padrão é de ~24h; isso é um ponto de partida documentado para um
lançamento assistido em produção, não um substituto para WAL archiving/
point-in-time recovery caso você precise de um RPO mais apertado depois.
Para restaurar, veja o runbook de restauração em `docs/OPERATIONS.md` — o
mesmo caminho de `pg_restore` é exercitado localmente por
`task db:backup:restore-test` contra um banco de dados descartável, então
essa também é a forma mais rápida de confirmar que um determinado dump é de
fato restaurável antes de você precisar contar com ele de verdade.
