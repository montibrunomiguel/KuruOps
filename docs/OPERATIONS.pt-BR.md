<p align="right"><a href="OPERATIONS.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Operação

Runbook para quem está de olho no ArgusOps rodando (produção assistida): o que os healthchecks
significam, como ler os logs, como reverter um deploy ruim, como restaurar de um backup. Não
confundir com `TROUBLESHOOTING.md` (gotchas de desenvolvimento/CI) nem com `THREAT_MODEL.md`
(rotação de segredos e modelo de ameaças) — este aqui é o "o que fazer quando algo pisca no
painel", os outros dois são sobre outra coisa.

## Healthchecks

Três endpoints, cada um com um propósito diferente — ver `backend/internal/httpserver/healthcheck.go`:

- **`GET /healthz`** — pinga o Postgres com timeout de 2s; 503 se não conseguir. Usado como
  `readinessProbe` (ver `deploy/k8s/04-api.yaml` etc.): um pod que falha aqui é retirado do
  Service até voltar a responder, sem ser reiniciado.
- **`GET /livez`** — sempre 200, sem checar nada. Usado como `livenessProbe` — de propósito
  independente do Postgres, pra uma instabilidade passageira do banco não derrubar todas as
  réplicas de uma vez (kubelet mataria e reiniciaria todo mundo ao mesmo tempo se a liveness
  também dependesse do banco).
- **`GET /metrics`** — formato de exposição do Prometheus, ver seção seguinte.

**Se `/healthz` está falhando**: primeiro suspeito é o Postgres em si (rede, credenciais,
`max_connections` esgotado) — não o pod. `kubectl logs` do pod não vai mostrar muito além de "não
consegui pingar"; olhe o lado do banco (métricas do RDS/Cloud SQL, ou `docker compose logs
postgres` localmente).

**Se `/livez` está falhando** (o pod nem responde 200 nisso): o processo em si travou ou morreu —
aí sim é hora de olhar `kubectl logs`/`kubectl describe pod` daquela réplica específica.

## Logs

JSON estruturado via `log/slog` (não texto solto) — cada linha tem um campo `request_id` que
correlaciona todas as linhas de uma mesma requisição, incluindo através de handlers diferentes
(ver `backend/internal/httpserver/middleware/logging.go`). Pra investigar um erro específico:

1. Ache a linha do erro, pegue o `request_id`.
2. Filtre todas as linhas com esse mesmo `request_id` (em qualquer agregador de log —
   `kubectl logs | grep` funciona pra uma checagem rápida local).
3. Isso reconstrói a requisição inteira, não só a linha que falhou.

## Reverter um deploy ruim

Não há blue-green nem canary aqui — é `kubectl rollout undo` de verdade:

```bash
kubectl rollout history deployment/argusops-api -n argusops
kubectl rollout undo deployment/argusops-api -n argusops
# repita pra argusops-ingest / argusops-worker / argusops-frontend se o deploy ruim tocou nelas também
```

Uma migration nova (`deploy/k8s/03-migration-job.yaml`) **não** é revertida automaticamente por
isso — `kubectl rollout undo` só volta a imagem do container, não o schema do banco. Se o deploy
ruim incluiu uma migration incompatível com a versão anterior do app, reverter o Deployment sem
também reverter a migration pode deixar o app antigo rodando contra um schema que ele não
entende. Confirme que a migration em questão era aditiva (nova coluna/tabela, não uma renomeação/
remoção) antes de confiar só no rollback do Deployment.

## Restaurar de um backup

`deploy/k8s/08-backup-cronjob.yaml` roda `pg_dump` uma vez por dia e sobe pro S3 — RPO de ~24h
(ver o próprio comentário do manifest). Pra restaurar:

1. Baixe o dump mais recente do bucket S3 configurado (`aws s3 cp
   s3://$BACKUP_S3_BUCKET/argusops/<arquivo>.dump .`).
2. **Nunca restaure direto por cima do banco de produção sem antes validar o dump** — rode
   `task db:backup:restore-test` localmente primeiro (aponta pro dump mais recente em `backups/`,
   restaura num banco descartável `argusops_backup_verify`, roda uma contagem de sanidade em
   `tenants`/`alerts`/`incidents`, depois derruba o banco descartável). Isso não toca no banco
   real — é seguro rodar a qualquer momento pra confirmar que um dump é restaurável de verdade.
3. Só depois de validado, restaure no banco real:
   ```bash
   pg_restore --no-owner --no-privileges -d <DATABASE_URL real> <arquivo>.dump
   ```
4. Isso é uma restauração completa (substitui o estado atual) — não incremental. Qualquer escrita
   feita depois do backup diário mais recente é perdida; é exatamente isso que "RPO de ~24h"
   significa. Se isso for inaceitável para o seu caso, WAL archiving/PITR (não implementado ainda)
   é o próximo passo, não este runbook.

## Queries de referência (Prometheus)

`deploy/k8s/09-monitoring.yaml` sobe um Prometheus mínimo (sem Grafana, sem
armazenamento persistente — ver o próprio manifest) já com as regras de
alerta que disparam sozinhas. As queries abaixo são pra investigação manual
durante a janela assistida — cole em `http://<prometheus>:9090/graph`:

- **Taxa de erro por serviço**: `sum by (job) (rate(argusops_http_requests_5xx_total[5m])) / sum by (job) (rate(argusops_http_requests_total[5m]))`
- **Latência p99 por serviço**: `histogram_quantile(0.99, sum by (le, job) (rate(argusops_http_request_duration_seconds_bucket[5m])))`
- **Saturação do pool de conexões**: `argusops_db_pool_acquired_conns / argusops_db_pool_max_conns`
- **Conexões SSE ativas** (deve variar com analistas logados, não crescer sem limite): `argusops_sse_active_connections`
- **Há quanto tempo cada sweep do worker rodou com sucesso pela última vez** (em minutos): `(time() - argusops_worker_last_sweep_success_timestamp) / 60`
- **Scrape targets fora do ar**: `up{job=~"argusops-.*"} == 0`

## Baseline de capacidade

Primeira execução de `task perf:smoke` (5 VUs, 30s) contra o stack local via
docker-compose (hardware de desenvolvimento, não um ambiente de produção
real — usar como referência de forma relativa, não como número absoluto):

- **`GET /api/v1/alerts` e `GET /api/v1/dashboard/stats`**: 100% de sucesso,
  ~9.5 req/s combinados, latência p90 18.7ms / p95 24.9ms / média 16.3ms.
  Praticamente sem gargalo visível nesse volume.
- **`POST /hooks` (ingest de webhook)**: achado real do teste de carga, não
  um bug — em rajada, ~59% das requisições voltaram 429 porque o rate
  limiter `webhook_ip` (`cmd/ingest/main.go`, 60 requisições/minuto por IP de
  origem) entrou em ação de propósito. Isso é o limite real de throughput
  pra uma única fonte (um único SIEM/IP) enviando alertas: **~1 req/s
  sustentado por IP de origem**. Se uma integração real precisar de mais
  que isso de uma fonte só, o limite está hardcoded (não configurável via
  env hoje) — mudar isso é uma decisão de segurança à parte, não algo pra
  ajustar de passagem.

Reexecutar `task perf:load` (mais pesado, VUS/DURATION configuráveis) contra
o ambiente real antes do lançamento assistido e comparar os números acima.

## Escalonamento / contatos

_Preencher com os contatos reais do time antes de liberar produção assistida — quem é acionado
quando um alerta do Alertmanager dispara, e por qual canal (o mesmo PagerDuty/Slack/webhook que o
próprio ArgusOps usa pra escalar incidentes de segurança dos tenants, ou um canal separado pra
incidentes da própria plataforma)._
