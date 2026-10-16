<p align="right"><a href="OPERATIONS.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Operação

Runbook para quem está de olho no KuruOps rodando (produção assistida): o que os healthchecks
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
kubectl rollout history deployment/kuruops-api -n kuruops
kubectl rollout undo deployment/kuruops-api -n kuruops
# repita pra kuruops-ingest / kuruops-worker / kuruops-frontend se o deploy ruim tocou nelas também
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
   s3://$BACKUP_S3_BUCKET/kuruops/<arquivo>.dump .`).
2. **Nunca restaure direto por cima do banco de produção sem antes validar o dump** — rode
   `task db:backup:restore-test` localmente primeiro (aponta pro dump mais recente em `backups/`,
   restaura num banco descartável `kuruops_backup_verify`, roda uma contagem de sanidade em
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

- **Taxa de erro por serviço**: `sum by (job) (rate(kuruops_http_requests_5xx_total[5m])) / sum by (job) (rate(kuruops_http_requests_total[5m]))`
- **Latência p99 por serviço**: `histogram_quantile(0.99, sum by (le, job) (rate(kuruops_http_request_duration_seconds_bucket[5m])))`
- **Saturação do pool de conexões**: `kuruops_db_pool_acquired_conns / kuruops_db_pool_max_conns`
- **Conexões SSE ativas** (deve variar com analistas logados, não crescer sem limite): `kuruops_sse_active_connections`
- **Há quanto tempo cada sweep do worker rodou com sucesso pela última vez** (em minutos), entre as duas réplicas do worker -- só a réplica que vence um tick atualiza a própria série, então use o máximo: `(time() - max by (sweep_job) (kuruops_worker_last_sweep_success_timestamp)) / 60`
- **Scrape targets fora do ar**: `up{job=~"kuruops-.*"} == 0`
- **Fila de análise automática**: `kuruops_ai_analysis_jobs` (profundidade por status) e `kuruops_ai_analysis_oldest_pending_seconds` (há quanto tempo o job vencido mais antigo espera), servidos pelo analyzer
- **Mais de um build ativo** (deploy parcial): `count(count by (build) (kuruops_build_info)) > 1`
- **Runtime Go e processo** (goroutines, GC, memória, fds): `go_*` e `process_*`, exportados por todo processo

## Processos, escala e a fila

| Processo | Papel | Réplicas | Escale por |
|---|---|---|---|
| `api` | REST da UI, tokens de sessão | 2+ (HPA 2–6 no `deploy/k8s`) | CPU. Stateless: anexos ficam no Postgres (`UPLOAD_BACKEND=database`) ou S3/GCS, atualizações ao vivo passam pelo `LISTEN/NOTIFY` do Postgres. Com `UPLOAD_BACKEND=local` fica preso a uma réplica. |
| `ingest` | recebimento de webhooks; registra cada alerta e seu job de análise | 2+ (HPA 2–6) | taxa de alertas recebidos. Sem LLM nem cofre de segredos. |
| `analyzer` | trabalha a fila de análises (chamadas a LLM + MCP) | 2+ | o rate limit do provedor de LLM, não CPU: `AI_ANALYSIS_CONCURRENCY` por réplica × réplicas. |
| `worker` | varreduras periódicas | 2 (standby quente) | de jeito nenhum: cada tick pega um advisory lock do Postgres, então uma réplica executa e a outra pula; mais de duas não adiciona nada. |

**A fila de análises está travada** (`KuruOpsAIAnalysisQueueStalled`, ou alertas ficam sem análise): em
ordem -- (1) há algum `analyzer` rodando e saudável (`/healthz`)? Sem um, os jobs esperam para sempre; o
ingest não os consome. (2) `kuruops_ai_analysis_jobs{status="failed"}` e os logs do analyzer por "ai
analysis queue": uma API key rejeitada, uma Base URL errada ou um modelo aposentado não é repetido e
aparece como `failed` com a causa em `ai_analysis_jobs.last_error`; corrija em Settings → Integração de
IA. (3) Uma queda do provedor é atravessada sozinha com backoff (1m, 5m, 15m, 1h, até cinco tentativas);
se durou mais, os jobs terminaram `failed` e podem voltar à fila com
`update ai_analysis_jobs set status='pending', attempts=0, next_attempt_at=now() where status='failed'`.

O **Postgres** é o único componente que o KuruOps não torna altamente disponível sozinho: use uma
instância gerenciada multi-AZ ou o `deploy/k8s/optional` (três instâncias, failover automático). Todo
processo do KuruOps reconecta após um reinício ou failover do banco sem ser reiniciado -- mas uma
réplica não é backup, então mantenha também os backups acima.

As regras de alerta em `deploy/k8s/09-monitoring.yaml` são validadas e testadas no CI
(`scripts/check-prometheus-rules.sh`); rode isso antes de editar uma.

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
próprio KuruOps usa pra escalar incidentes de segurança dos tenants, ou um canal separado pra
incidentes da própria plataforma)._
