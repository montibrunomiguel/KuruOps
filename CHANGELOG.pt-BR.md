<p align="right"><a href="CHANGELOG.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/). Datas em AAAA-MM-DD.

Como este projeto ainda não versiona releases (`v0.x`), as seções abaixo agrupam por marco de
desenvolvimento em vez de tag — a partir de agora, toda mudança relevante (feature, fix de
segurança, mudança de comportamento visível) deve ganhar uma entrada aqui no mesmo PR que a
introduz, não como arqueologia posterior. Ver o item correspondente no checklist de
`.github/PULL_REQUEST_TEMPLATE.md`.

## [Não lançado]

### Changed

- Playbooks não oferecem mais uma seção de fase "Novo" em "Passos por Fase" -- quando um incidente
  ainda está em Novo/Identificação, o analista ainda não triou o alerta, então nunca havia nada
  pra um playbook de resposta prescrever ali (passos só fazem sentido a partir de Detecção &
  Análise em diante). Vale tanto para o editor (`PlaybookDetailPage.tsx`) quanto pro popup
  somente-leitura de acionamento (`PlaybookViewModal.tsx`); o backend em si não foi tocado (ainda
  aceita genericamente qualquer fase NIST pra um passo de playbook) -- é um recorte só de UI do
  que é oferecido/exibido, não uma mudança de modelo de dados. Nenhum playbook existente tinha
  passos na fase "Novo" pra começo de conversa, então nada foi migrado ou perdido.
- `AlertsListPage.tsx`/`IncidentsListPage.tsx` tinham desenvolvido independentemente a mesma
  máquina de estado de seleção de linhas de ~25 linhas (um `Set` de ids selecionados, selecionar
  tudo com o estado indeterminate do checkbox do cabeçalho, reset ao mudar filtro/página) e o
  mesmo estado de aplicar/erro/resumo de ação em massa. Extraído para hooks compartilhados
  `useRowSelection`/`useBulkAction` -- sem mudança de comportamento, os testes já existentes de
  ambas as páginas passam sem alteração.
- `useSidebarCounts.ts` (os contadores "Alertas"/"Incidentes" da barra lateral) era o último hook
  de busca de dados feito à mão com `useEffect`+`fetch`+flag de cancelado que restava no app, sem
  nenhuma assinatura de atualização ao vivo -- os contadores só mudavam numa navegação de página
  completa, ao contrário dos contadores das próprias páginas de listagem. Reconstruído sobre
  `useList` (react-query) + `useEventStream`, o mesmo padrão que toda outra página de listagem/
  detalhe já usa; os contadores agora atualizam ao vivo via SSE no instante em que a mudança de
  outro analista chega, igual às páginas de listagem. `useList` ganhou uma flag `options.enabled`
  (padrão `true`, nenhum call site existente é afetado) pra suportar isso -- pula a busca
  inteiramente pra uma capacidade que o chamador não tem, em vez de disparar uma requisição
  garantidamente 403.

- **Projeto renomeado de ArgusOps para KuruOps** -- inspirado no Curupira, personagem do folclore
  brasileiro que protege a floresta e avisa os animais com seu grito característico. A renomeação
  cobre a base de código inteira: o caminho do módulo Go (`github.com/argusops/argusops` →
  `github.com/kuruops/kuruops`), todo nome de imagem/container/volume/rede Docker, o nome do banco
  Postgres e dos roles (`argusops`/`argusops_app`/`argusops_worker` →
  `kuruops`/`kuruops_app`/`kuruops_worker`), as env vars `ARGUSOPS_APP_PASSWORD`/
  `ARGUSOPS_WORKER_PASSWORD` (agora `KURUOPS_*`), o email do admin padrão semeado
  (`admin@argusops.local` → `admin@kuruops.local`), prefixos de nome de métrica do Prometheus,
  nomes de recurso Kubernetes/Terraform, a marca no frontend (título da página, sidebar, login,
  logo), e toda a documentação. Sem mudança funcional -- puramente uma renomeação, verificada com
  as suites de teste completas de backend/frontend, um `docker compose up` do zero, e um fluxo real
  de login → criar webhook → ingerir alerta → dashboard. Deploys locais existentes ganham volumes/
  banco Docker novos sob o novo nome de projeto (os antigos `argusops_*` não são migrados
  automaticamente) -- ver a documentação de deploy se precisar levar os dados adiante em vez de
  começar do zero.
- Os grupos do menu de Configurações (Integrações, Conectores, Identidade & Acesso, Operações,
  Dados & Auditoria) agora comprimem e expandem -- clique no rótulo de um grupo para alternar,
  em vez de sempre rolar por todas as opções de cada categoria. O grupo que contém a página de
  configuração em que você está sempre fica expandido, e buscar no menu continua trazendo à tona
  um item de um grupo que estava recolhido. O estado de recolhido/expandido de cada grupo persiste
  entre recarregamentos (`localStorage`).

### Added

- Indicadores de Comprometimento (IOCs) em incidentes: um botão "IOCs" na página de detalhe do
  incidente abre um popup listando todo IOC já cadastrado, com um formulário inline pra adicionar
  um novo -- tipo (uma lista cobrindo os próprios exemplos de IOC da NIST SP 800-61r3 -- endereço
  IP, nome de domínio, URL, hash de arquivo, endereço/assunto de email -- mais os tipos de
  observável STIX 2.1 que a NIST SP 800-150 aponta pra troca estruturada: chave de registro, mutex,
  nome de processo, user-agent, CVE, fingerprint de certificado, e um "outro" genérico), o valor do
  indicador, uma descrição opcional, e a data em que foi identificado. Somente-inserção (sem
  editar/excluir, igual às notas da equipe) -- `GET`/`POST /api/v1/incidents/{id}/iocs`.
  Automaticamente puxado tanto pro postmortem em Markdown quanto pro relatório em PDF, numa nova
  seção "Indicadores de Comprometimento (IOCs)" -- nada mais é necessário pra um IOC recém-cadastrado
  aparecer em qualquer um dos dois documentos.
- Relatório de incidente exportável em PDF: um botão "Baixar Relatório (PDF)" na página de detalhe
  do incidente (`GET /api/v1/incidents/{id}/report.pdf`) -- título, severidade/prioridade, fase
  atual, a timeline completa de histórico de fases com durações, papéis da equipe, descrição, tags,
  alertas vinculados e notas da equipe. Disponível em qualquer fase, diferente do postmortem em
  Markdown (que só aparece quando o incidente chega em Pós-Incidente) -- este é um export factual
  de um momento específico, não um documento narrativo de encerramento. Geração de PDF em Go puro
  (`github.com/jung-kurt/gofpdf`), sem adicionar dependência de navegador headless/Chromium. Texto
  não-ASCII (títulos/descrições/notas em pt-BR com acento, tags, nomes) passa por
  `pdf.UnicodeTranslatorFromDescriptor` antes de renderizar -- a fonte "Arial" embutida do gofpdf
  exige cp1252, e bytes UTF-8 crus passados direto pra ela saem como caracteres corrompidos.
- Autenticação em dois fatores (TOTP): autoatendimento, opcional por usuário, ativada em
  Configurações → Perfil → Autenticação em Dois Fatores (escaneie um QR code ou digite o segredo
  manualmente em um aplicativo autenticador, depois confirme com um código de 6 dígitos). Um login
  local para uma conta com 2FA ativado para antes de gerar uma sessão, logo após a verificação de
  senha, e retorna um token pendente de curta duração em vez disso; a segunda etapa do formulário de
  login troca esse token mais um código atual pela sessão real (`POST /auth/mfa/verify`). Desativar
  exige reinserir a senha atual. Usa `github.com/pquerna/otp` (backend) e `qrcode.react` (frontend)
  -- nenhum segredo ou imagem de QR code sai do navegador, exceto através da própria requisição de
  ativação do usuário.
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
  (imagens em S3/GCS/Google Drive/disco local) nunca são removidas -- só o registro do KuruOps
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

- **Segurança**: a visibilidade por tag (`Role.allowedTags`) era aplicada só nas rotas "principais"
  de alerta e incidente — toda rota de sub-recurso consultava por ID apenas sob o RLS de tenant.
  Nada obriga um cliente HTTP a chamar `GET /incidents/{id}` antes de
  `GET /incidents/{id}/comments`, então um analista restrito a um conjunto de tags conseguia ler as
  notas da equipe, IOCs, timeline, histórico de status e alertas vinculados de um alerta/incidente
  que não pode ver, escrever novos comentários e IOCs nele, e aprovar suas tool calls MCP pausadas
  — esta última *executa* uma automação real com efeito colateral. Todas as 42 rotas de
  alerta/incidente com `{id}` agora rechecam visibilidade, respondendo 404 igual ao `Get` (então
  "não existe" e "existe mas está oculto para você" continuam indistinguíveis). Um novo
  `TestSubResourceRoutesRejectTagRestrictedCaller` percorre as rotas chi registradas em vez de uma
  lista escrita à mão, então um sub-recurso futuro que esquecer o gate quebra o CI assim que for
  registrado.
- **Segurança**: uma falha de `RevokeSessions` ao desativar um usuário era descartada
  silenciosamente. Não fazer rollback da desativação continua sendo a decisão certa, mas com TTL de
  30 dias no refresh token uma falha engolida podia deixar uma conta desativada (possivelmente
  comprometida) com sessão renovável por um mês, enquanto o admin via um 204 limpo. Agora é logado.
- Os fluxos de callback OAuth do Slack e do Google faziam chamadas de saída sem timeout algum
  (`http.DefaultClient` / o padrão do oauth2), numa rota montada fora do timeout de request do
  grupo da API — então uma conexão travada com qualquer um dos provedores prendia uma goroutine e
  sua conexão indefinidamente. Ambos agora usam um cliente limitado a 15s.
- Cinco formulários de lista reordenável/removível usavam o índice do array como key
  (os passos de escalonamento do `EscalationEditForm.tsx`, os intervalos de horário de
  funcionamento do `ScheduleForm.tsx`, os passos de playbook do `PlaybookDetailPage.tsx`, as
  regras de mapeamento do `FieldMappingTemplatesPanel.tsx`, os campos de dedup do
  `GroupByFieldsEditor.tsx`) -- remover ou reordenar uma linha desloca o índice de toda linha
  posterior, então o React reaproveita o nó DOM de cada linha deslocada (e qualquer foco/estado
  que ele estivesse segurando) pra uma linha lógica *diferente* da que ele estava de fato
  renderizando um instante antes. Toda lista agora usa uma key estável em vez disso: um
  `crypto.randomUUID()` gerado uma vez por linha (removido de novo antes do payload de salvar,
  junto com a lista somente-leitura de passos de playbook, que já tinha um id real do backend
  disponível o tempo todo e agora usa ele) pros quatro casos de array de objeto, e um pequeno
  array paralelo de ids pro `string[]` puro do prop do `GroupByFieldsEditor`, que não tem objeto
  nenhum pra pendurar um id.
- `LinkSearchPicker.tsx` (a busca de correlação alerta-a-alerta / incidente-a-alerta, usada no
  painel Alertas Vinculados do AlertDetailPage e no painel de alertas correlacionados do
  IncidentDetailPage) renderizava seus resultados como `<div onClick>` simples -- totalmente
  inacessível por teclado, sem jeito de dar Tab até um resultado ou vincular sem usar o mouse.
  Reconstruído com o mesmo padrão `role="combobox"`/`aria-activedescendant`/setas do teclado já
  comprovado em `CommandPalette.tsx`: Seta pra cima/baixo move o resultado ativo (com wrap), Enter
  vincula, Escape limpa a busca.
- As barras coloridas de plantão do `OnCallTimeline.tsx` fixavam texto branco -- falha o mínimo de
  contraste 4,5:1 da WCAG AA pra texto pequeno contra 9 das 10 cores da paleta (chegando a 1,59:1
  no amarelo). `lib/personColor.ts` ganhou `personTextColor`, que escolhe preto ou branco por
  amostra (o que realmente atinge 4,5:1 contra aquele fundo específico), em vez de uma cor
  presumida segura pra todas.
- Duas ações destrutivas pulavam o próprio padrão de confirmação inline do app (`useConfirm`,
  escolhido nos outros lugares justamente porque `window.confirm()` some sozinho silenciosamente
  em alguns contextos de browser embutido) e apagavam de primeira, com um clique só: o botão de
  desvincular do `LinkedAlertsPanel.tsx` e a remoção de substituição do `OnCallTimeline.tsx`.
  Ambos agora pedem confirmação antes, igual a toda outra ação destrutiva em Configurações.
- A migração de banco externo (Configurações → Dados & Auditoria, `internal/dbmigrate`) falhava
  ao copiar qualquer tabela com uma coluna de busca full-text `generated always as (...) stored`
  (`alerts.search_vector`, `incidents.search_vector`) com `"row field count is N, expected N-1"`
  — `COPY ... TO` inclui o valor calculado de uma coluna gerada por padrão, `COPY ... FROM` não
  aceita um, então um `COPY tablename` sem lista explícita de colunas enviava um campo a mais por
  linha do que o lado de destino esperava ler. Corrigido resolvendo as colunas não-geradas de cada
  tabela uma vez e usando essa lista exata explicitamente nos dois lados. Encontrado ao conectar a
  própria suíte de testes de integração do `internal/dbmigrate` ao CI pela primeira vez (veja
  abaixo) — ela nunca rodava automaticamente antes, então isso não tinha cobertura de teste na
  prática.
- Nenhuma goroutine de background desacoplada (execuções de análise de IA, envio de notificação de
  escalonamento manual, o stream de cópia de linhas da migração de banco externo, todo loop de
  background de longa duração) tinha recuperação de panic — `chi.Recoverer` só protege o caminho
  síncrono de request HTTP, então um panic não tratado em qualquer uma delas derrubava o processo
  inteiro. O novo `internal/safego.Go` envolve todas elas agora, recuperando e logando um panic em
  vez de derrubar o processo.
- Três pontos descartavam um erro real silenciosamente
  (`AIAnalysisService.finishSimpleRun`/`failRun`, `MCPToolService.recordFailure`) — uma escrita
  falha deixava uma execução de análise de IA travada mostrando "running" para sempre, ou o status
  no banco de uma tool call permanentemente dessincronizado do resultado real, sem nenhum rastro
  de log de por quê. Agora logado via `slog.Error`.
- Uma rajada de alertas chegando mais rápido do que uma chamada de LLM completa podia disparar um
  número ilimitado de goroutines concorrentes de auto-análise, uma por alerta — agora limitado a 5
  análises concorrentes em andamento via um semáforo contador.
- 62 pontos de handler em 24 arquivos escreviam uma string de erro Go crua (`err.Error()`) direto
  no corpo de uma resposta 500 — um nome de constraint/coluna do Postgres, um erro de nível de
  driver, ou um caminho de arquivo interno vazando rotineiramente em uma resposta da API. Um novo
  helper `writeInternalError` agora loga o erro real no servidor e retorna uma mensagem genérica
  ao cliente em vez disso.
- `docs/openapi.yaml` documentava as respostas 401/429 de `/auth/login` como `text/plain` — na
  verdade são `application/json` (`{"error": "..."}`), igual a todo outro erro no nível de handler
  nesta API; só as respostas compartilhadas `Unauthorized`/`Forbidden`, genuinamente texto puro
  (emitidas pelo middleware de auth, não por um handler), devem ser diferentes.
- Removido `RoleService.Get` — nenhuma rota HTTP ou outro serviço nunca o chamava, só seu próprio
  arquivo de teste, como helper de conveniência para assertions, agora substituído por uma leitura
  direta do repositório.
- **Segurança**: `AuthenticateLDAP` refazia o bind como o DN do usuário resolvido com qualquer
  senha enviada pelo chamador, inclusive uma vazia — a RFC 4513 §5.1.2 define que um bind com um
  DN válido e senha de tamanho zero é um "unauthenticated bind" que muitos diretórios (inclusive o
  OpenLDAP com config padrão) tratam como bem-sucedido sem checar nada, então qualquer usuário
  provisionado via LDAP podia ser logado só sabendo o e-mail dele. Uma senha vazia/só espaços
  agora é rejeitada antes mesmo de conectar ao diretório.
- **Segurança**: refresh tokens nunca eram revogados no logout (não existia endpoint de logout) ou
  na troca/reset de senha — um refresh token roubado continuava funcionando pelo TTL inteiro de 30
  dias mesmo depois do usuário legítimo trocar a senha. Adicionado `POST /auth/logout` (revoga
  exatamente o token apresentado) e `ChangePassword`/confirmação de reset de senha agora revogam
  todo refresh token pendente do usuário, igual à ação admin "Revogar sessões" já existente.
- **Segurança**: `users.mfa_totp_secret` guardava o segredo TOTP de cada usuário em texto puro — o
  único segredo de classe credencial neste schema que nunca passava pelo `secrets.Store`, ao
  contrário de toda credencial LLM/MCP/SAML/SMTP. Agora guarda uma referência opaca do
  `secrets.Store` em vez disso; uma migration limpa qualquer valor em texto puro pré-existente
  (o projeto ainda não tem deploy em produção, então o único impacto é reinscrição de MFA em
  ambientes de dev/staging).
- **Segurança**: os pods api/ingest/worker do `deploy/k8s` só definiam `runAsNonRoot` — adicionado
  `seccompProfile: RuntimeDefault`, `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false` e
  `capabilities: {drop: [ALL]}`, fechando técnicas de fuga de container que uma futura CVE em
  alguma dependência poderia explorar (nenhum dos três binários escreve fora dos volumes que já
  monta, então root somente leitura não precisou de volume novo).
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
  serviço interno e fazer o KuruOps mandar essa requisição por ele (SSRF). As três agora acessam
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
- **Segurança**: o `Destination` do canal Slack de uma política de escalonamento on-call (uma URL
  de "Incoming Webhook" do Slack colada pelo admin) era discado com o `http.Client` puro, não com o
  cliente protegido contra SSRF (`internal/httpguard`) que o canal de webhook genérico já usava
  para esse mesmo tipo de destino informado pelo admin — o destino do Slack nunca era de fato
  validado como uma URL real do `hooks.slack.com`, então era tão falsificável quanto para um alvo
  interno/loopback/link-local. Agora usa o mesmo cliente protegido.
- **Segurança**: a URL de metadados de um provedor de identidade SAML (Configurações → Provedores
  de Identidade) era buscada com `http.DefaultClient` em vez do cliente protegido contra SSRF —
  mesma categoria de "URL configurada pelo admin" que a proteção de webhook/MCP/LLM já cobre, só
  que ficou de fora quando essa proteção foi adicionada.
- **Segurança**: o STARTTLS de saída do SMTP (`mailer.SMTPSender`) montava seu `tls.Config` sem
  `MinVersion`, permitindo negociar até TLS 1.0 contra um relay de e-mail permissivo ou mal
  configurado. Fixado em TLS 1.2.

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
