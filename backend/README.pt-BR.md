<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# KuruOps backend

Implementação Go do backend descrito no handoff de design (`design_handoff_kuruops/`) e no
review de arquitetura. Três binários, um módulo:

| Comando | Faz o quê | Por quê é separado |
|---|---|---|
| `cmd/api` | REST para o frontend: alertas, incidentes, playbooks, settings | escala/falha independente da ingestão |
| `cmd/ingest` | Recebe webhooks de SIEM/XDR (`POST /hooks`, autenticado por token) | perfil de carga/rate-limit diferente do `api` |
| `cmd/worker` | Jobs de fundo: refresh das materialized views de KPI (`mv_alert_daily_stats`, `mv_incident_kpis`, `mv_incident_daily_stats`), sweep de `incidents.sla_breached`, sweep de escalonamento on-call | não deixa uma chamada de LLM lenta bloquear o CRUD; a análise por IA em si dispara numa goroutine dentro de `cmd/ingest` (na ingestão do alerta), não passa por este worker |

## Rodando local

O caminho mais rápido é `task deploy:up` na raiz do repo (ver `README.md` raiz) — sobe Postgres via
Docker, aplica migrations, cria o role `kuruops_app` e builda/roda os três binários em container.

Pra rodar os binários direto na máquina (sem Docker para o Go, só para o Postgres):

```bash
cp .env.example .env   # ajuste DATABASE_URL depois de criar o role kuruops_app (ver db/README.md)
export $(cat .env | xargs)
make run-api      # :8080
make run-ingest   # reusa HTTP_ADDR -- rode em processos/terminais separados com portas diferentes
make run-worker
```

Todo primeiro deploy já vem com um admin padrão (`db/migrations/0002_seed_default_admin.up.sql`) —
`admin@kuruops.local` / `ChangeMe123!`, com `must_change_password=true`. Teste do login local
(`AUTH_MODE=dev` já basta — não precisa gerar chaves JWT nem trocar pra `dev-headers`; login é
autenticação real mesmo em modo dev, só a geração de chave é que vira efêmera):

```bash
curl -X POST http://localhost:8080/auth/login \
  -d '{"email":"admin@kuruops.local","password":"ChangeMe123!"}'
# -> {"token":"...", "user": {"mustChangePassword": true, ...}}

# com mustChangePassword=true, TODO outro endpoint em /api/v1 responde 403
# (middleware.RequirePasswordChanged) exceto este:
curl -X POST http://localhost:8080/api/v1/account/change-password \
  -H "Authorization: Bearer <token>" \
  -d '{"currentPassword":"ChangeMe123!","newPassword":"<sua senha>"}'
# -> {"token":"..."}  (novo token, já sem mustChangePassword)

curl -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/alerts
```

Pra criar outros usuários locais (não há endpoint de signup — ver `internal/repository/user_repository.go`),
gere o `password_hash` com `go run ./cmd/hashpw '<senha>'`, nunca escreva o hash à mão, e insira via
SQL contra o tenant único (`select id from tenants` — sempre uma linha, ver
`TenantRepository.GetDefault`).

KuruOps é software single-instance (ver review de arquitetura): não existe conceito de
"empresa"/tenant no login — `tenant_id` continua em todo lugar por baixo dos panos (pra permitir
SaaS multi-tenant real no futuro sem reescrever schema), mas a API sempre resolve o único tenant
automaticamente. "Empresa" só existe como tag em alertas/incidentes (`allowedTags` do usuário), não
como boundary de login.

Pra curlar `/api/v1` sem passar por login (atalho, não é "autenticação de mentira" de propósito
geral — troca a verificação de JWT por confiar em headers crus, então um token de login não
funciona nesse modo):

```bash
AUTH_MODE=dev-headers make run-api
curl -H "X-Tenant-ID: <uuid>" -H "X-User-ID: <uuid>" http://localhost:8080/api/v1/alerts
```

Teste do endpoint de ingestão (crie um `webhook_endpoints` de teste primeiro, hash do token com
sha256):

```bash
curl -X POST http://localhost:8081/hooks \
  -H "X-Webhook-Token: <token>" \
  -d '{"title":"Multiple Failed SSH Login Attempts","severity":"critical"}'
```

## O que está implementado vs. ainda é design

**Implementado**: schema completo (`db/migrations`) com RLS por tenant, e o CRUD completo com
regras de negócio para:
- **Alertas** — ciclo de vida (open → investigating/escalated → closed, classificação só no
  fechamento), auditoria append-only (`alert_events`), metadados customizados aceitos no payload do
  webhook (lista arbitrária de chave/valor — canal do Slack, link de playbook externo, etc.,
  renderizada num painel dedicado no detalhe), análise por IA disparada automaticamente na
  ingestão quando há provedor LLM configurado (mesmo resultado do botão manual "Analisar com IA")
- **Incidentes** — fases NIST com salto livre + detecção de "fase pulada", matriz
  severidade×prioridade, correção auditável de timestamp (nunca sobrescreve o valor original),
  Team Notes, alertas correlacionados, papéis de equipe NIST 800-61 (Commander, Technical Lead,
  Incident Handler(s), Communications Lead, Privacy Officer — `domain.Incident.Roles`, substituiu
  o painel genérico de "responsáveis" na tela de detalhe)
- **Playbooks** — CRUD + auto-match por keyword (com fallback "General Security Event")
- **Settings**: endpoints de webhook (token com hash + rotação), provedores de LLM por tenant
  (`kind=openai_compatible` genérico, chave nunca persistida em claro — ver
  `internal/secrets/store.go`; validado ao vivo contra o endpoint OpenAI-compatible real do Gemini,
  `generativelanguage.googleapis.com/v1beta`, sem precisar de nenhum adapter dedicado — "Analisar
  com IA" funciona ponta a ponta com um provedor real, não só mockado), servidores MCP (allow-list de tools + lista de tools com efeito
  colateral que sempre exigem aprovação — ver `service.EvaluateToolInvocation`), usuários/roles e
  mapeamento de grupo LDAP/SAML → role/tags (com botão de remover configuração, além de
  criar/atualizar), integração de armazenamento de evidências (S3/GCS/Google Drive — `S3Store`
  validado ao vivo contra um bucket real: upload de uma imagem via `POST /api/v1/uploads/images`,
  depois `GET` de volta confirmando o mesmo conteúdo, latência consistente com uma chamada de rede
  real à AWS, não disco local; `GCSStore`/`GDriveStore` ainda não validados contra uma conta
  GCP/Drive real — os testes unitários do próprio `GDriveStore` mockam a API REST do Drive v3, ver
  `internal/blobstore/gdrive_test.go`), uma fundação de conectar/desconectar um workspace do Slack
  (`internal/slackclient`, OAuth de bot token — ainda não validado contra um App/workspace do Slack
  real, ver `docs/SLACK_APP_SETUP.pt-BR.md`), SMTP (reset de senha por email), tags, escalas de
  plantão, SLAs de incidente por severidade×prioridade, políticas de escalonamento
  (PagerDuty/Slack/webhook genérico), exportação de auditoria em CEF ou JSON, e migração assistida
  para um Postgres externo (Settings → Banco de Dados Externo)
- Ingestão de webhook com normalização genérica, cálculo de MTTA/MTTR como materialized view em
  vez de client-side
- **Autenticação**: login local (argon2id + JWT RS256), bind LDAP (`internal/authn/ldap.go`, com
  o padrão de dois binds: service account para achar o DN, depois bind como o próprio usuário para
  checar a senha), e SSO SAML (`internal/authn/saml.go`, SP via `crewjam/saml`, com proteção contra
  replay via cookie de `InResponseTo`). Os três convergem em `AuthService`/`ProvisionFederated`,
  que aplica `auth_group_mappings` (grupo do IdP → role/resource_access/allowed_tags) e emite o
  mesmo JWT. `internal/httpserver/middleware.JWTAuth` verifica esse token em `/api/v1/**`.
- **Autorização**: o acesso é totalmente delegado a um `Role` nomeado (Settings → Roles), atribuído
  a um usuário via `roleId` — não fica no próprio registro do usuário. `isAdmin`/`resourceAccess`/
  `allowedTags` são espelhados desse Role pro JWT no momento do login (`authn.Claims`) e aplicados
  em dois pontos — `middleware.RequireAdmin()` bloqueia todo `/settings/**` para quem não é admin, e
  `middleware.RequireResourceAccess` bloqueia `/alerts` ou `/incidents` por inteiro conforme o
  `resourceAccess` do usuário. Dentro de cada recurso, `allowedTags` filtra a listagem na query SQL
  (`tags && $allowedTags`) e é checado de novo em `Get`/`ChangeStatus`/`Close`/`ChangePhase`/
  `SetSeverityAndPriority`/`UpdateDescription` — ver `service/access.go`. Os sub-recursos
  (comentários, links, IOCs, timeline, histórico de status, aprovação de tool call) repetem a mesma
  checagem carregando o pai via `loadVisible` antes; a proteção estrutural que impede um
  sub-recurso novo de esquecer o gate é o `TestSubResourceRoutesRejectTagRestrictedCaller`, que
  percorre as rotas chi registradas em vez de uma lista mantida à mão. Como o JWT só espelha o Role
  no momento do login, editar um Role (ou reatribuir um usuário a outro) só tem efeito no próximo login/refresh
  de token desse usuário, não imediatamente — mesmo trade-off de defasagem que o próprio comentário
  de `authn.Claims` descreve para `mustChangePassword`.

## Autenticação

O fluxo funciona ponta a ponta (local/LDAP/SAML → JWT → `JWTAuth` middleware).

Resolvidos desde a última revisão deste documento: o `ServeACS` não responde mais ao POST do IdP com
um token de sessão. Ele devolvia um como JSON direto, o que colocava uma credencial no histórico do
navegador, em qualquer log de proxy no caminho e na própria página renderizada caso o redirect não
acontecesse. A correção documentada era a SPA trocar um código de uso único — mas o cookie de
refresh já *é* esse código, e melhor (HttpOnly, SameSite=Strict, escopo `/auth`, rotativo a cada uso,
revogável), então o ACS grava esse cookie e redireciona para `/login/saml`, onde a SPA o
troca por um access token pelo `POST /auth/refresh` de sempre. Nenhum token trafega numa URL nem num
corpo que algum script consiga ler; o refresh token não volta mais em nenhum corpo
de resposta — ele é entregue como cookie `kuruops_refresh` (`HttpOnly`, `SameSite=Strict`,
`Path=/auth`, `Secure` quando o `APP_BASE_URL` é https; ver `internal/sessioncookie`), e
`/auth/refresh`/`/auth/logout` o leem só de lá, ignorando um campo `refreshToken` no corpo para que
um valor obtido em outro lugar não possa ser reapresentado; revogação de sessão (refresh tokens em
`refresh_tokens`, ver `AuthService.Refresh`/`RevokeSessions` e o botão "Revoke sessions" em
Settings → Usuários), cache de metadata SAML (`SAMLAuthService`'s `resolveIDPMetadata`, TTL de 1h,
invalidado ao salvar config), rate limiting por conta no login (`AuthHandlers.loginAttempts`, além
do limite por IP já existente), normalizers dedicados para Wazuh/CrowdStrike/GuardDuty
(`internal/ingest/normalize_*.go`, roteados por `webhook_endpoints.source` em `Handler.normalizerFor`
— fontes sem adapter dedicado continuam caindo no `genericNormalizer`; nenhum dos três foi validado
contra tráfego real do respectivo vendor, tratar como ponto de partida), e um backend de
`secrets.Store` real além do `PersistentEnvStore` padrão (encriptado com `SECRETS_ENCRYPTION_KEY`,
persistido na tabela `secret_store` — sobrevive a um restart do processo, ao contrário do antigo
`EnvStore` em memória puro, que ainda existe só para uso em testes): `SECRETS_BACKEND=vault`
(`VaultStore`, engine KV v2 via HTTP direto, sem o SDK oficial) ou `SECRETS_BACKEND=kms`
(`AWSKMSStore`, Encrypt/Decrypt puro, sem Secrets Manager) — ver `secrets.NewFromConfig` para o
factory switch e as variáveis de cada backend. `VaultStore` já foi validado contra um servidor
Vault real em modo dev (`internal/secrets/vault_store_live_test.go`, `task backend:test:vault`) —
um ciclo Put→Resolve de verdade, não só o mock em `vault_store_test.go`.

## Client MCP

`internal/mcpclient` fala Model Context Protocol de verdade com um servidor MCP registrado —
handshake (`initialize` + `notifications/initialized`), `tools/list` (com paginação) e
`tools/call`, sobre o transporte "Streamable HTTP" (POST JSON-RPC 2.0, com suporte a resposta
`text/event-stream` de um único evento). Validado contra o servidor de referência oficial
(`@modelcontextprotocol/server-everything`, `internal/mcpclient/live_test.go`, `task backend:test:mcp`)
— handshake, listagem e chamada de tool passam contra uma implementação MCP real e independente,
não só contra os mocks internos deste repo. `stdio`/`sse` como transporte continuam não
implementados (retornam erro claro em vez de tentar e falhar confuso).

`service.MCPToolService` é a fronteira entre esse client e a política de acesso:
- `DiscoverTools` conecta no servidor ao vivo e devolve o catálogo real de `tools/list` — é o que
  o botão "Discover tools" em Settings → MCP Servers chama, pra trocar o preenchimento manual de
  nomes de tool por uma lista de verdade com checkboxes.
- `ProposeToolCall` sempre passa por `EvaluateToolInvocation` primeiro (allow-list). Tool sem
  efeito colateral executa na hora; tool marcada em `side_effecting_tools` fica em
  `ai_tool_calls.status = 'proposed'` até `ApproveToolCall`/`RejectToolCall` — o agente nunca
  executa uma tool de efeito colateral sozinho (ver review de arquitetura, "IA sugere vs IA
  executa"). Endpoints: `GET/POST /api/v1/settings/mcp-servers/tool-calls[/{id}/approve|reject]`.

**Resolvido desde a última revisão deste documento**: `AIAnalysisService.runAgentAnalysis` agora
roda um loop agêntico de verdade (até `maxAgenticTurns = 5` idas e vindas com a LLM) e chama
`ProposeToolCall` a cada tool que o modelo pedir. Uma tool sem efeito colateral executa na hora e o
resultado volta pro próximo turno; uma tool marcada `side_effecting_tools` pausa o run inteiro
(persistido em `ai_analysis_runs` com status `paused`) até um analista aprovar/rejeitar em
Settings → Servidores MCP → Aprovações Pendentes (painel novo, `MCPServersPanel.tsx`) —
`MCPToolService.SetOnToolCallResolved` retoma o run de onde parou via `ResumeAnalysisRun`. O
"Analisar com IA" do detalhe de alerta/incidente aciona esse loop de ponta a ponta, e a ingestão de
um alerta também dispara a mesma análise automaticamente quando há provedor LLM configurado.

Uma tool call pausada pode ser resolvida de duas formas, ambas chamando no fim o mesmo
`MCPToolService.ApproveToolCall`/`RejectToolCall`: em Settings → Servidores MCP → Aprovações
Pendentes (qualquer admin, vê toda call pendente do tenant), ou direto no painel `AnalysisChat` do
próprio alerta/incidente (`POST /api/v1/{alerts,incidents}/{id}/analyze/tool-calls/{callId}/approve|reject`,
`internal/httpserver/handlers/analysis_tool_calls.go`'s `resolveAnalysisToolCall`) — sem precisar de
acesso admin, já que a checagem aqui é confirmar que a call realmente pertence ao alerta/incidente
ao qual quem chamou já tem acesso, não uma permissão separada.

### Configurando LDAP/SAML de um tenant

O frontend (`frontend/src/pages/settings/IdentityProvidersPanel.tsx`) já cobre isso — Settings →
Identity Providers. Pra testar direto na API sem subir o frontend:

```bash
# LDAP
curl -X PUT http://localhost:8080/api/v1/settings/identity-providers/ldap \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"host":"ldap.acme.local","port":636,"useTls":true,"bindDn":"cn=svc,dc=acme,dc=local",
       "bindPassword":"...","userBaseDn":"ou=people,dc=acme,dc=local","userFilter":"(mail=%s)",
       "groupAttribute":"memberOf"}'

# SAML -- gera a keypair da SP na primeira chamada; baixe a metadata depois em
# GET /auth/saml/metadata e registre no IdP
curl -X PUT http://localhost:8080/api/v1/settings/identity-providers/saml \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"idpMetadataUrl":"https://idp.acme.com/metadata","acsUrl":"https://kuruops.acme.com/auth/saml/acs",
       "spEntityId":"https://kuruops.acme.com/auth/saml","groupAttribute":"groups"}'
```

## Padrão de código

Cada recurso de domínio segue: `internal/domain` (struct + regras de transição documentadas em
comentário) → `internal/repository` (SQL puro via pgx, sempre dentro de `db.Pool.WithTenant`) →
`internal/service` (única camada que pode escrever, valida transições antes de tocar o repo) →
`internal/httpserver/handlers` (decodifica request, chama o service, serializa resposta). Ver
`alert_service.go` / `alert_repository.go` / `handlers/alerts.go` como referência ao adicionar
incidentes/playbooks/settings.
