<p align="right"><a href="CHANGELOG.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/). Datas em AAAA-MM-DD.

Como este projeto ainda não versiona releases (`v0.x`), as seções abaixo agrupam por marco de
desenvolvimento em vez de tag — a partir de agora, toda mudança relevante (feature, fix de
segurança, mudança de comportamento visível) deve ganhar uma entrada aqui no mesmo PR que a
introduz, não como arqueologia posterior. Ver o item correspondente no checklist de
`.github/PULL_REQUEST_TEMPLATE.md`.

## [Não lançado]

### Added

- Mudança de status em massa nas listagens de Alertas/Incidentes: uma coluna de checkbox +
  "selecionar todos desta página" em ambas as listagens, com uma barra de ações que aparece assim
  que ≥1 linha é selecionada para mudar o status de todo alerta selecionado
  (`POST /api/v1/alerts/bulk/status`) ou a fase NIST de todo incidente selecionado
  (`POST /api/v1/incidents/bulk/phase`) em uma única ação. Implementado como um loop sobre o já
  existente `ChangeStatus`/`ChangePhase` de ID único (não um novo UPDATE multi-linha), então todo
  invariante já existente (visibilidade por tag, a trava de "fechado é terminal", um evento de
  auditoria por item afetado) continua funcionando sem alteração; uma falha parcial (ex.: uma
  linha que não é mais visível ao chamador) reporta resultados por ID em vez de falhar a
  requisição inteira. Deliberadamente restrito a status/fase apenas -- fechamento em massa não é
  suportado (transições diretas para "closed"/"post_incident" são rejeitadas), fechar um alerta ou
  incidente ainda exige o fluxo existente de classificação/Fechamento por item.
- Busca por texto completo em Alertas e Incidentes: um filtro `q` em ambas as listagens compara
  contra título, origem, ID de regra e ativo para alertas, e título/descrição para incidentes --
  baseado em uma coluna `tsvector` gerada pelo Postgres e um índice GIN por tabela
  (`db/migrations/0008_fulltext_search`), não em um scan `ILIKE` mais lento. A caixa de busca é
  debounced (300ms) antes de refazer a consulta, igual a todo outro filtro dessas listagens que
  reseta para a página 1 ao mudar.
- Configurações → Dados & Auditoria → Log de Auditoria: um registro somente-inserção de toda
  mudança de Configurações que qualquer admin fez -- área, ação, autor, e um diff antes/depois
  (tabela `admin_audit_events`, `AdminAuditEventRepository.InsertEvent`). Cobre todas as ~15 áreas
  de Configurações (Webhooks, Templates de Mapeamento de Campos, Integração de IA, Servidores MCP,
  Usuários, Papéis, Provedores de Identidade, Tags, Integração de Armazenamento, SMTP, Slack,
  Escala de Atendimento, SLAs de Incidentes, Escala de Acionamento, Retenção) -- todo método que
  muda estado em cada um desses services agora grava um evento na mesma transação da mudança que
  registra, então uma falha ao gravar o evento desfaz a mudança também. Distinto da Exportação de
  Auditoria já existente (`AuditExportService`), que cobre o histórico de alertas/incidentes para
  exportação a um SIEM, não mudanças de Configurações. Lido via `GET /api/v1/settings/audit-log`,
  paginado por keyset, mais recente primeiro.
- Configurações → Dados & Auditoria → Retenção: retenção de dados configurável para alertas/
  incidentes fechados (padrão de 18 meses, configurável separadamente por tipo de recurso). Um
  alerta/incidente fechado é excluído permanentemente pelo job `sweepDataRetention` (roda a cada
  hora em `backend/cmd/worker`) assim que seu prazo de retenção vence a partir do fechamento — um
  aberto nunca é tocado, não importa a idade. Evidências anexadas a um alerta/incidente excluído
  (imagens em S3/GCS/Google Drive/disco local) nunca são removidas -- só o registro do ArgusOps
  sobre o alerta/incidente, já que nada neste código jamais implementou exclusão do armazenamento
  de blobs.
- Configurações → Conectores → Slack: conectar/desconectar um workspace do Slack via OAuth de bot
  token (`internal/slackclient`, `SlackConfigService`). Apenas a fundação -- ainda sem sincronia de
  mensagens/threads/canais; um `SlackConfigService.Get` não-nulo é o gate que futuros recursos do
  Slack vão checar antes de oferecer sua UI. Ver `docs/SLACK_APP_SETUP.pt-BR.md` para o Manifesto
  de App do Slack necessário para configurar `SLACK_CLIENT_ID`/`SLACK_CLIENT_SECRET`. Introduz uma
  tabela compartilhada `oauth_states`/`OAuthStateService` (token CSRF de uso único, persistido no
  banco) reaproveitada pelo fluxo OAuth do Google Drive abaixo.
- Google Drive como um terceiro provedor de Integração de Armazenamento (além de S3/GCS), com a
  opção de colar uma chave de service account ou um fluxo completo de "Conectar sua conta Google"
  via OAuth (`blobstore.GDriveStore`, `StorageConfigService.SaveGDriveServiceAccount`/
  `HandleGDriveOAuthCallback`).
- Exportação em JSON (`GET /api/v1/settings/audit-export/json`, newline-delimited) além da
  exportação CEF já existente, compartilhando a mesma lógica de cursor com paginação keyset.
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
- Uma busca de configurações acima dos grupos de navegação de Settings (`SettingsLayout.tsx`) --
  filtra os itens pelo label traduzido conforme você digita, recolhendo um grupo inteiro quando
  nenhum de seus itens combina, em vez de rolar uma nav de ~18 itens pra achar um que você já sabe
  o nome.
- Dois hooks compartilhados substituindo boilerplate reimplementado do zero em 6+ painéis de
  Settings: `useSavedFlag` (o booleano de "mostrar mensagem de sucesso após salvar") e
  `useAdminSingletonConfig` (a forma "GET um objeto de config ou null, editar estado de formulário
  local, PUT/DELETE pra salvar, reload após sucesso"), adotados pelos painéis de
  Retenção/SMTP/Storage/Slack/Provedores de Identidade (LDAP+SAML) e Perfil/SLA de Incidente
  respectivamente.
- Cobertura de teste de backend pra código anteriormente sem testes: `tagsVisible`/
  `latestAnalysisFields`/`orEmptySlice` de `internal/service/access.go`, `S3Store`/`GCSStore` de
  `internal/blobstore` (mockando o cliente de cada SDK do mesmo jeito que `gdrive_test.go` já faz
  pro Google Drive), e um teste no nível de handler pra guarda de status-já-resolvido dos endpoints
  de aprovar/rejeitar de `analysis_tool_calls.go` (antes coberta só na camada de serviço, não
  através do handler HTTP).
- Um componente `Modal` compartilhado (`frontend/src/components/Modal.tsx`) substituindo 7 cópias
  independentes do mesmo markup `modal-overlay`/`modal` (`CloseAlertModal`, `AnalysisChat`, o
  formulário de criação de incidente do `IncidentsListPage`, `PlaybookViewModal`, o popover de
  override do `OnCallTimeline`, os dois modais do `WebhooksPanel`) — nenhum deles tinha
  `role="dialog"`/`aria-modal`, fechar com Escape, ou qualquer gerenciamento de foco. O novo
  componente adiciona tudo isso mais um focus trap de verdade, modelado no dialog que
  `CommandPalette.tsx` já tinha.
- Um segundo `ErrorBoundary` em volta das rotas de painel de Settings (`SettingsLayout.tsx`) — um
  painel de configurações quebrado agora mostra uma mensagem escopada de "este painel falhou ao
  carregar" em vez de derrubar o app inteiro (o resto da navegação de Settings continua funcional,
  já que fica fora desse novo boundary).

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
- `SlackIntegrationPanel.tsx` e seus painéis-irmãos de config (Retenção/SMTP/Storage/Provedores de
  Identidade LDAP+SAML) derivavam "ainda não configurado" da truthiness pura do objeto de config
  buscado, mas um GET que falha colapsa pro mesmo valor falsy de "genuinamente não configurado" --
  uma falha de carregamento mostrava a CTA de "Conectar"/"Configurar" do estado vazio (mais, no caso
  específico do Slack, um botão de Connect via OAuth totalmente clicável) como se nada estivesse
  configurado, com só um banner de erro fácil de não notar como sinal real de que algo deu errado.
  Corrigido pelo novo campo `configured` do `useAdminSingletonConfig`, só verdadeiro quando loading e
  error já resolveram limpo; o botão Connect do Slack agora é totalmente substituído por uma
  mensagem de "não foi possível carregar o status" numa falha de carregamento, já que clicar nele
  nesse estado poderia iniciar um fluxo OAuth novo em cima de um estado existente desconhecido.
- Removido `frontend/src/components/PersonFilter.tsx` (zero imports, confirmado morto).
- **Segurança**: o `Destination` de webhook de uma política de escalonamento, o `endpoint` de um
  servidor MCP e o `base_url` de um provedor LLM self-hosted/compatível com OpenAI eram todos
  acessados com um `http.Client` simples — qualquer um com acesso de Settings a essas três áreas
  podia apontar um deles pra `http://169.254.169.254/...` (endpoint de metadata de nuvem) ou um
  serviço interno e fazer o ArgusOps mandar essa requisição por ele (SSRF). As três agora acessam
  via um novo `internal/httpguard.NewClient`, que recusa conectar num endereço
  loopback/link-local/privado (checado contra o IP resolvido, não só a string do hostname, então
  não é contornável por DNS rebinding); um deploy genuinamente on-prem pode sair dessa proteção com
  `ALLOW_PRIVATE_NETWORK_TARGETS=true`.
- **Segurança**: `secrets.NewFromConfig` agora recusa iniciar com o valor exato de
  `SECRETS_ENCRYPTION_KEY` commitado no `.env.example`, a menos que `AUTH_MODE` seja
  `dev`/`dev-headers` — essa chave é real e funcional (por conveniência de dev local), o que a
  tornava uma chave conhecida e compartilhada se algum dia fosse copiada e colada direto num
  deploy real em vez de gerada do zero.
- **Segurança**: os handlers de callback OAuth do Google Drive e do Slack colocavam o texto bruto
  do erro Go (que pode carregar detalhe interno — um erro de banco, uma falha ao buscar o state)
  direto na query string da URL de redirect em caso de falha. O erro real agora é logado só no
  servidor; o redirect recebe um código genérico fixo (`?gdrive_error=connection_failed`) no lugar.
- A política de senha (`AuthService.ChangePassword`, `PasswordResetService.ConfirmReset`) era só
  de tamanho, aceitando `"11111111"`/`"aaaaaaaa"` — agora também exige pelo menos uma letra e um
  número (deliberadamente sem exigir símbolo/regra de complexidade, que na maioria das vezes só
  empurra as pessoas pra substituições previsíveis).
- O botão de desvincular do `LinkedAlertsPanel` e os steppers +/- de analistas concorrentes do
  `ScheduleForm` tinham `aria-label`s fixos e não traduzidos (`"unlink"`, `"-"`, `"+"`) em vez de
  passar por i18n como todo outro label deste app.

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
