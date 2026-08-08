# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/). Datas em AAAA-MM-DD.

Como este projeto ainda não versiona releases (`v0.x`), as seções abaixo agrupam por marco de
desenvolvimento em vez de tag — a partir de agora, toda mudança relevante (feature, fix de
segurança, mudança de comportamento visível) deve ganhar uma entrada aqui no mesmo PR que a
introduz, não como arqueologia posterior. Ver o item correspondente no checklist de
`.github/PULL_REQUEST_TEMPLATE.md`.

## [Não lançado]

### Added

- Diagrama de arquitetura (mermaid) no README raiz, `CHANGELOG.md`, `docs/TROUBLESHOOTING.md` e
  spec OpenAPI para `/api/v1/**` (documentação que estava só em prosa, ou faltando).
- `golangci-lint` (backend) e ESLint (frontend) configurados do zero, com gate próprio no
  `task test`; `govulncheck`/`npm audit` também gated no `task test`; script de gate de regressão
  de cobertura (`backend/scripts/check-coverage-baseline.sh`).
- Servidor LDAP fake reutilizável (`internal/testutil/fake_ldap.go`) e helper de metadata SAML
  (`internal/testutil/fake_saml.go`) para testar `AuthenticateLDAP`/`SAMLAuthService` sem depender
  de um diretório/IdP externo de verdade.
- Testes de integração reais (gated por env var, não rodam no `task test` padrão) contra
  infraestrutura de verdade: `task backend:test:vault` (HashiCorp Vault dev-mode) e
  `task backend:test:mcp` (servidor de referência oficial `@modelcontextprotocol/server-everything`).
- `AUTH_MODE=dev` agora persiste o par de chaves JWT gerado em `.dev-keys/` (volume Docker
  nomeado) em vez de gerar um novo a cada restart do container `api` — sessões sobrevivem a um
  `docker compose restart`/redeploy normal.

### Fixed

- **Segurança**: `middleware.NewRateLimiter` (usado no rate limit de login) confiava em
  `X-Forwarded-For`, um header que o próprio cliente controla — um atacante podia contornar o
  limite de tentativas de login só variando esse header a cada request. Agora confia
  exclusivamente em `X-Real-IP`, que o `nginx.conf` sobrescreve incondicionalmente em todo request
  proxiado.
- **Segurança**: dependência `goxmldsig` (assinatura/validação XML do fluxo SAML) atualizada para
  corrigir uma vulnerabilidade de bypass de assinatura por captura de variável de loop
  (GO-2026-4753). `chi`, `pgx` e o toolchain Go também atualizados para fechar o restante dos
  achados do `govulncheck`.
- **Segurança**: `MCPToolService.RejectToolCall` não conferia se a tool call já tinha sido
  resolvida antes de sobrescrever o status — permitia reverter uma tool call já aprovada/executada
  de volta para "rejected", corrompendo a trilha de auditoria. Agora usa a mesma guarda de
  existência/status que `ApproveToolCall` já tinha.
- Cookie de correlação do fluxo SAML (`SameSite=Lax`) nunca era enviado no POST cross-site que todo
  IdP real usa para devolver a asserção — o ID do `AuthnRequest` agora viaja pelo `RelayState`
  (ecoado por qualquer IdP compatível com o spec) como canal primário, com o cookie como fallback
  same-site.
- `secrets.EnvStore` perdia todas as credenciais a cada restart do processo enquanto as linhas no
  banco continuavam referenciando o ref antigo — substituído por `PersistentEnvStore` (AES-256-GCM,
  persistido na tabela `secret_store`) como backend padrão.
- `CommandPalette.tsx` usava chaves de i18n que não existiam (`nav.dashboard` em vez de
  `sidebar.nav.dashboard`), então sempre mostrava a chave crua em vez do texto traduzido; o item
  "perfil" também linkava para uma página só-admin em vez de `/profile`.
- `WebhooksPanel.tsx`: regenerar um token de webhook revelava o valor em texto puro e, na sequência,
  disparava um reload que desmontava a linha antes do usuário conseguir ver o token — o próprio
  propósito da revelação era anulado silenciosamente.

## [2026-08-07] — `78f41f5`

### Added

- Backend de secret store real (`PersistentEnvStore` como padrão; `VaultStore`/`AWSKMSStore` como
  alternativas via `SECRETS_BACKEND`).
- Métricas de dashboard: MTTA/MTTR, volume de alertas/incidentes por dia (materialized views
  recalculadas pelo `worker`), filtros por severidade/status/prioridade/fase/analista/commander/tag
  e período (preset ou intervalo customizado).
- Papéis da equipe NIST 800-61 no incidente (Commander, Technical Lead, Incident Handler(s),
  Communications Lead, Privacy Officer) e histórico de fases com correção auditável.
- Loop agêntico de IA de verdade (`AIAnalysisService.runAgentAnalysis`): a análise pode propor
  tool calls via MCP, pausar em tools de efeito colateral até aprovação de um analista, e retomar
  de onde parou.
- Normalizers dedicados de ingestão para Wazuh, CrowdStrike e GuardDuty.
- Notificações em tempo real via SSE (`internal/events`, `EventsHandlers.Stream`).
- Escalonamento de plantão (PagerDuty/Slack/webhook genérico) e exportação de auditoria em CEF.
- Cobertura de testes extensiva em backend e frontend.

## [2026-08-06] — `e235d44`

- Implementação inicial: alertas, incidentes, playbooks, autenticação local, ingestão via webhook,
  Settings básico.
