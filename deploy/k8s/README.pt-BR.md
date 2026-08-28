<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# KuruOps no Kubernetes

Manifests básicos para rodar a stack real (api, ingest, worker, frontend) em
um cluster. Deliberadamente minimalista: sem Helm chart, sem
HorizontalPodAutoscaler, sem ingress controller ou integração com
cert-manager. Isso coloca uma implantação funcional em um cluster; adicionar
ingress/TLS/autoscaling por cima fica a cargo do que sua organização já usa
para isso, em vez deste projeto escolher uma opção por você.

## O que NÃO está incluído

- **Postgres.** Todo manifest aqui assume uma instância Postgres 16 já
  existente e acessível (uma instância RDS/Cloud SQL gerenciada, um
  StatefulSet que você gerencia separadamente etc.) — a mesma premissa de
  "banco de dados fornecido pelo cliente" que a própria funcionalidade de
  migração para banco de dados externo em Settings do app já assume. As
  roles `kuruops_app`/`kuruops_worker` e as políticas de RLS já precisam
  existir lá (veja `db/init/*.sql` e `db/README.md`) antes de apontar o
  `01-secret.example.yaml` para ela.
- **Ingress/TLS.** O Service do `frontend` é `ClusterIP` — alcançá-lo de fora
  do cluster (um ingress controller, um Service LoadBalancer de nuvem,
  `kubectl port-forward` para uma olhada rápida) fica por sua conta.
- **Autoscaling.** Sem HorizontalPodAutoscaler — cada contagem de réplicas
  abaixo é um número fixo definido manualmente. `ingest` sai com
  `replicas: 2` por padrão (veja o comentário no próprio manifest): ele não
  tem dependência de disco local, e seu rate limiter/infraestrutura
  relacionada a SSE já são apoiados em Postgres exatamente para isso. `api`
  sai com `replicas: 1` — seu PVC `uploads` é `ReadWriteOnce`, então ir além
  de 1 réplica exige uma StorageClass ReadWriteMany ou trocar Settings ->
  Storage para o backend S3/GCS primeiro (veja o comentário no
  `04-api.yaml` para os passos exatos). `worker` permanece em 1 por design
  (seus jobs de sweep usam um advisory lock do Postgres justamente para que
  rodar mais de uma réplica seja seguro, mas não útil no momento — apenas
  uma estaria de fato trabalhando por vez).
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
```

Reaplicar todo o conjunto após um novo release é seguro: o Job de migração é
idempotente (o golang-migrate só aplica versões que ainda não registrou,
veja o comentário no `03-migration-job.yaml`), e cada apply de
Deployment/Service/ConfigMap é uma simples atualização declarativa. Ajuste a
tag `image:` em `04-api.yaml`/`05-ingest.yaml`/`06-worker.yaml`/`07-frontend.yaml`
para o que o seu build publicou (`:latest` aqui é um placeholder para testar
os manifests, não uma estratégia real de fixação de versão de release).

## Imagens

Construídas da mesma forma que o `docker-compose.yml` as constrói localmente
(os targets `api`/`ingest`/`worker` do `backend/Dockerfile`,
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
