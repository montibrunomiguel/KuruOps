<p align="center">
  <img src="docs/logo.png" alt="ArgusOps" width="420" />
</p>

# ArgusOps

SOC/SIEM alert & incident management — Go + PostgreSQL backend, React frontend, desenhado a partir
do handoff em `design_handoff_argusops/`. Ver `backend/README.md`, `frontend/README.md` e
`db/README.md` para detalhes de cada parte; este README cobre como subir tudo e o que esperar
depois que sobe.

## Requisitos

- [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`)
- Docker + Docker Compose (Postgres, API, ingest, worker e o frontend rodam em container)
- Go 1.25+, Node 20+ (só necessários se for rodar `backend`/`frontend` fora de container)

## Deploy local (um comando)

```bash
task deploy:up
```

Isso faz, nessa ordem: sobe o Postgres e espera ficar saudável, aplica as migrations
(`db/migrations`), cria o role `argusops_app` (least-privilege — é o que faz a row-level security
valer alguma coisa, ver `db/README.md`), builda as imagens e sobe `api` + `ingest` + `worker` +
`frontend`. No final, imprime:

- **Web UI:** http://localhost:3000 (o frontend, servido por nginx, já com proxy de `/api` e
  `/auth` pro backend — é por aqui que você entra)
- **API:** http://localhost:8080 (`AUTH_MODE=dev` por padrão — ver `backend/README.md` antes de
  expor isso fora da sua máquina)
- **Ingest (webhooks):** http://localhost:8081

```bash
task deploy:logs   # acompanhar logs de tudo
task deploy:down   # derrubar (mantém o volume do Postgres)
```

Para desenvolvimento de UI com hot reload (sem rebuildar a imagem Docker a cada mudança):

```bash
task frontend:dev   # :5173, faz proxy de /api e /auth pro :8080
```

### Primeiro login

Todo deploy novo já vem com um admin: **`admin@argusops.local` / `ChangeMe123!`**. A senha é
pública (está neste repo) de propósito — o primeiro login força a troca antes de liberar qualquer
outra coisa, tanto na tela quanto no backend (nenhuma rota além de trocar senha responde enquanto
`mustChangePassword` estiver ativo). Troque assim que entrar.

ArgusOps é pensado pra rodar como **uma instância só** — não tem conceito de "empresa"/tenant no
login. "Empresa" existe apenas como tag em alertas/incidentes, usada para restringir o que cada
usuário enxerga (`allowedTags`); um mesmo usuário pode ter acesso a alertas de várias empresas ao
mesmo tempo.

> Como `AUTH_MODE=dev` gera um par de chaves JWT novo a cada vez que o container `api` sobe/reinicia
> (ver `backend/README.md`), qualquer sessão logada antes de um `task deploy:up` seguinte é
> invalidada — é só logar de novo, não é bug.

### O que tem depois de logado

- **Dashboard** — três abas (Alertas / Incidentes / Follow-up), com KPIs computados no backend
  (não somando listas no navegador): alertas abertos/críticos, incidentes ativos, SLA estourado, e
  **MTTA/MTTR** de alertas e incidentes, além de volume de alertas e de incidentes por dia,
  distribuição por severidade/status/prioridade/fase e por analista/commander responsável. As
  médias e os gráficos de volume vêm de materialized views (`mv_alert_daily_stats`,
  `mv_incident_kpis`, `mv_incident_daily_stats`) recalculadas a cada 1 minuto pelo `worker` — se
  você acabou de fechar um alerta/incidente, o número pode levar até um minuto para refletir, é o
  trade-off deliberado de não recalcular isso a cada request. Contadores "ao vivo" (abertos,
  críticos) e a lista/atividade recente atualizam via SSE, sem precisar recarregar a página. O
  filtro de período aceita tanto um preset (24h/7d/30d/90d) quanto um intervalo customizado com
  data e hora exatas.
- **Alertas / Incidentes** — listagem com filtros e paginação (`Carregar mais`), detalhe com
  timeline de eventos, comentários, vínculo alerta↔incidente, e análise por IA (manual via botão,
  ou automática na ingestão se houver um provedor LLM configurado). Um alerta recebido via webhook
  pode carregar metadados customizados (canal do Slack, link de playbook externo, ambiente, ou
  qualquer chave/valor que a fonte quiser mandar), renderizados num painel dedicado no detalhe.
- **Papéis da Equipe** (no detalhe do incidente) — Commander, Technical Lead, Incident Handler(s),
  Communications Lead e Privacy Officer (NIST 800-61), cada um atribuível a um usuário.
- **Histórico de Fases** (no detalhe do incidente) — cada fase NIST 800-61 registra quando foi
  entrada; o horário original nunca é sobrescrito. Uma correção exige motivo, fica registrada com
  autor, e gera um evento no log de auditoria (append-only) — ver `db/migrations/0005_incidents.up.sql`.
  Pular uma fase (ex.: New → Eradication direto) não é bloqueado, mas fica marcado com um evento de
  aviso na timeline, para não mascarar processo mal seguido em métricas de MTTR.
- **Playbooks** — biblioteca de procedimentos por categoria/fase, com sugestão automática no
  detalhe do alerta.
- **Settings** (admin) — Webhook Endpoints (token com política de expiração/rotação — 90 dias por
  padrão, configurável na criação/regeneração), AI Integration (LLM providers), MCP Servers
  (com painel de aprovações pendentes para tools de efeito colateral que a IA propõe usar),
  Integração de Armazenamento (S3/GCS, para evidências anexadas), SMTP (reset de senha por email),
  Users & Roles, Identity Providers (LDAP/SAML — configurar, atualizar e remover), Tags, Escala de
  Atendimento, SLAs de Incidentes, Escalonamento de Plantão (PagerDuty/Slack/webhook genérico),
  Exportação de Auditoria (CEF) e Banco de Dados Externo (migração assistida do Postgres embutido
  para um Postgres gerenciado pelo cliente).

## Testes

```bash
task test         # build + vet + gofmt + go test + tsc + vite build — não precisa de deploy
task test:smoke   # sobe o stack (task deploy:up) e roda scripts/smoke-test.sh contra ele
```

`test:smoke` é o teste que realmente prova que a cadeia inteira funciona — Postgres real, RLS real,
JWT real, HTTP real —, não só que o código compila. Ele loga como o admin padrão semeado pela
migration, testa a troca de senha obrigatória, cria um webhook endpoint, ingere um alerta por ele e
confere isolamento/RLS. Idempotente — pode rodar de novo contra um deploy que já rodou antes, mas
**troca a senha do admin padrão** como parte do fluxo — se você quer manter `ChangeMe123!` válido
para explorar a UI manualmente depois, não rode `test:smoke` nesse mesmo deploy (ou resete com
`docker compose down -v && task deploy:up` depois). Não é suíte de testes completa — não toca
LDAP/SAML nem MCP —, é o smoke test que pega regressão de "a stack nem sobe".

## Todas as tasks

```bash
task --list
```

## Estrutura do repositório

```
backend/    Go: cmd/api, cmd/ingest, cmd/worker + internal/ (domain, repository, service, httpserver, auth)
frontend/   React + Vite + TS
db/         migrations (golang-migrate) + init scripts (role least-privilege)
docs/       assets estáticos (logo)
Taskfile.yml, docker-compose.yml   orquestração local
```

## Estado do projeto e Governança Open-Source

O projeto conta com suítes de testes unitários no backend (`go test ./...`) e no frontend (`npm test` via Vitest), além de verificação estática de tipos (`tsc`) e validação de formatação (`gofmt`). 

O `task test:smoke` executa a validação end-to-end sobre a stack completa em Docker Compose (Postgres + RLS + JWT + HTTP).

Consulte os arquivos de governança open-source:
- [LICENSE](LICENSE) (Apache 2.0)
- [SECURITY.md](SECURITY.md) (Política de Segurança e Vuln Disclosure)
- [CONTRIBUTING.md](CONTRIBUTING.md) (Guia de Contribuição e Workflow)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) (Código de Conduta)

