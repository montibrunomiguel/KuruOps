<p align="right"><a href="CHANGELOG.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Changelog

Formato baseado em [Keep a Changelog](https://keepachangelog.com/). Datas em AAAA-MM-DD.

Como este projeto ainda não versiona releases (`v0.x`), as seções abaixo agrupam por marco de
desenvolvimento em vez de tag — a partir de agora, toda mudança relevante (feature, fix de
segurança, mudança de comportamento visível) deve ganhar uma entrada aqui no mesmo PR que a
introduz, não como arqueologia posterior. Ver o item correspondente no checklist de
`.github/PULL_REQUEST_TEMPLATE.md`.

## [Não lançado]

### Security

- **Breaking (só para clientes de API).** O refresh token não volta mais em nenhum corpo de
  resposta. Todo caminho de login -- local, LDAP, o segundo passo do MFA e o ACS do SAML -- agora o
  entrega como cookie `kuruops_refresh` com `HttpOnly`, `SameSite=Strict`, `Path=/auth` e `Max-Age`
  igual ao TTL de 30 dias do próprio token. O `Secure` segue o scheme do `APP_BASE_URL`, então um
  deploy em https o recebe e um laptop em `http://localhost` (a única origem em texto claro que os
  navegadores ainda consideram confiável) continua funcionando. `/auth/refresh` e `/auth/logout`
  leem o token só desse cookie: um campo `refreshToken` no corpo é ignorado, e é isso que impede
  que um valor obtido em outro lugar seja reapresentado.

  O access token passa a viver só em memória -- não é mais escrito no `localStorage`, então um
  reload recupera a sessão trocando o cookie, e não lendo uma credencial armazenada. O storage do
  navegador agora guarda apenas dados de exibição (nome, e-mail, rótulo do papel). Um navegador já
  logado atravessa a atualização: o usuário é lido do registro antigo
  `{ token, refreshToken, user }` e os tokens dele são descartados.

  Vale dizer com precisão o que isso compra. Não torna XSS inofensivo -- script na página ainda
  consegue chamar `/auth/refresh` e usar o access token que vier. O que acaba é a exfiltração de
  uma credencial de 30 dias para um host controlado pelo atacante, que é a diferença entre dano
  limitado ao tempo de vida do script injetado e uma sessão portátil de um mês.

### Added

- Chamadas externas de LLM e MCP passam por um circuit breaker
  (`backend/internal/circuitbreaker`), com chave por host para que um provedor morto não derrube
  chamadas a um saudável. Após `EXTERNAL_CALL_BREAKER_THRESHOLD` falhas consecutivas (padrão 5) as
  chamadas falham imediatamente por `EXTERNAL_CALL_BREAKER_COOLDOWN` (padrão 30s); depois uma sonda
  passa, e uma sonda que falha reabre o circuito sem recontar. Qualquer um dos dois em 0 restaura o
  comportamento anterior.

  Só erros de transporte, 5xx e 429 contam como falha. Um 4xx deliberadamente não conta: uma API
  key errada devolve 401 em toda chamada, então contá-la abriria o breaker permanentemente e
  trocaria a única mensagem de erro que diz ao operador o que corrigir por um genérico "circuit
  breaker is open".

### Changed

- O `docs/THREAT_MODEL.md` listava o escopo por tag nos sub-recursos de incidente como lacuna
  aberta. Não é: comentários, vínculos, IOCs, timeline, histórico de status e aprovação de tool
  call carregam o pai via `loadVisible(ctx, tx, id, allowedTags)` antes, e o
  `TestSubResourceRoutesRejectTagRestrictedCaller` comprova percorrendo as rotas chi registradas
  (42 delas) em vez de uma lista mantida à mão. A doc e o parágrafo correspondente no
  `backend/README.md` agora descrevem o que o código faz. Sem mudança de comportamento -- esta
  entrada existe porque uma "lacuna conhecida" desatualizada num threat model é pior que nenhuma.

### Changed

- Bumps de dependência major agora exigem revisão, em três camadas. O `.github/dependabot.yml`
  declara a política e para de repropor o major do `golangci-lint-action`, que está travado na
  migração do `.golangci.yml` para o schema v2 -- com uma nota dizendo o que o destrava, de modo
  que fica adiado em vez de fechado-e-esquecido. O `.github/CODEOWNERS` faz toda PR solicitar
  revisão e destaca os caminhos onde uma mudança não revisada é menos visível e mais danosa:
  definições de CI, a própria automação de dependências, manifestos de deploy e os pacotes de
  segredos e de proteção contra SSRF. O `CONTRIBUTING.md` registra a política junto com as
  perguntas que valem para um major -- esse pacote tem um par que precisa andar junto, ele deixa de
  fornecer algo que fornecia transitivamente, ele muda um formato de configuração do qual este
  repositório tem um arquivo.

  Nenhuma das camadas *bloqueia* um merge sozinha. A imposição é branch protection exigindo
  revisão, que precisa de repositório público ou plano pago -- num repo privado gratuito a API a
  recusa de saída, que é exatamente como quinze PRs foram mergeadas sem revisão. Habilitá-la é um
  passo a dar quando o repositório for tornado público.


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

- Alertas podem ser fechados em lote. Fechar deliberadamente não podia usar a mudança de status em
  lote existente -- o repositório recusa transição direta para `closed` para que uma classificação
  seja sempre registrada -- o que deixava triar uma rajada de falsos positivos quase idênticos como
  um trabalho de um diálogo por vez. Escolher "Fechado" na barra de ações em lote da lista de
  alertas agora revela um seletor de classificação e uma nota opcional compartilhada, e
  `POST /api/v1/alerts/bulk/close` os aplica a todos os alertas selecionados. Os resultados por
  alerta são reportados do mesmo jeito que a mudança de status em lote reporta, então um alerta já
  fechado ou oculto por tag não interrompe os demais; uma classificação inválida é recusada uma vez
  como requisição inválida em vez de falhar todos os alertas do lote com o mesmo erro. Anexos
  deliberadamente não são aceitos em lote -- um anexo é evidência sobre um alerta específico, e
  grampear o mesmo arquivo em cinquenta deles faria o registro dizer algo que ninguém quis dizer.
- `.gitleaks.toml` e um job `secret-scan` no CI, para que a auditoria de segredos anterior à
  publicação seja repetível por qualquer pessoa em vez de um teste avulso. A varredura lê o
  histórico completo, não apenas a árvore de trabalho, já que um segredo commitado e removido
  depois continua sendo um segredo. A allowlist nomeia cada correspondência sabidamente inofensiva
  individualmente -- a chave de cifragem de dev documentada, o token root do Vault em modo dev,
  placeholders PEM escritos como `CHANGEME` e fixtures de teste contendo a palavra literal `fake`
  -- de modo que um segredo NOVO nesses mesmos arquivos ainda reprova a varredura.


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

### Added

- A primeira análise que um alerta ou incidente recebe agora é uma triagem estruturada completa em
  vez de alguns parágrafos livres: quatro fases (coletar, correlacionar, classificar, escalar),
  uma disposição explícita (verdadeiro positivo / verdadeiro positivo benigno / falso positivo),
  prioridade P1-P4 baseada em risco contextual e não na severidade com que o alerta chegou, e um
  layout fixo de relatório (resumo, entidades afetadas, tabela de decisão, evidências, correlação,
  ações recomendadas, tuning). Reanálises e turnos de conversa seguintes mantêm o prompt geral mais
  curto -- o relatório de triagem já está na timeline a essa altura. A metodologia é adaptada da
  skill `alert-triage` do UnitOneAI/SecuritySkills (MIT), que se apoia em MITRE ATT&CK e NIST SP
  800-61 Rev 2. O prompt também carrega restrições que passam a importar quando há ferramentas MCP
  ligadas: recomendar contenção mas nunca executá-la, nunca executar nada encontrado num payload, e
  tratar instruções embutidas no conteúdo do alerta como dado a reportar e não como diretiva a
  seguir -- payloads de alerta chegam por webhook e são influenciáveis por atacante por definição.
- Provedores de IA agora podem ser editados depois de criados. O backend já tinha o endpoint; o
  painel de Configurações simplesmente nunca o chamava, então corrigir uma base URL ou um modelo
  digitado errado exigia apagar o provedor e reinserir a chave de API. Deixar o campo da chave em
  branco preserva a que está guardada -- ela nunca volta para o navegador, então não há com o que
  preenchê-lo.

### Fixed

- `main` reparada de novo depois que mais dez PRs do Dependabot foram mergeadas sem revisão
  (#101-#110), várias delas majors. Três jobs de CI falharam, por duas causas.
- **O pin do toolchain Go dessincronizou do módulo.** O Dependabot subiu a diretiva do `go.mod`
  para 1.26; os workflows ainda instalavam 1.25, então `Backend (Go)`, `dbmigrate` e
  `Go Vulnerability Check` morreram em `go.mod requires go >= 1.26.0 (running go 1.25.14)`. Agora
  `go-version: '1.26.x'` nos três lugares -- deliberadamente não `go-version-file: backend/go.mod`,
  que parece a correção mais elegante e é pior: a diretiva é um piso, não uma recomendação, então a
  action instalaria exatamente a 1.26.0, cuja biblioteca padrão carrega cinco vulnerabilidades
  conhecidas corrigidas na 1.26.6. O `govulncheck` reporta cinco na 1.26.0 e zero na 1.26.6.
- **O TypeScript 7 ainda não é utilizável aqui.** A versão mais nova do `typescript-eslint` ainda
  declara `typescript >=4.8.4 <6.1.0`, então o TS 7 faz o `npm ci` falhar num peer range
  insatisfazível -- o que derrubou o job do frontend e o build da imagem. Revertido para a linha
  5.9 e o major do TypeScript adicionado à lista de `ignore` do `.github/dependabot.yml`, com o
  comando que diz quando é seguro removê-lo (`npm view typescript-eslint@latest peerDependencies`).
- O Vitest 5 usa por padrão o pool `forks` -- um processo filho por arquivo de teste, cada um
  construindo o próprio jsdom. Isso serve numa máquina de desenvolvimento e não num runner de CI de
  dois núcleos: os 71 arquivos morreram com `[vitest-pool]: Failed to start forks worker`, e a
  execução reportou "no tests" em vez de uma falha legível. Trocado por um pool `threads` com teto,
  que preserva o isolamento por arquivo, abre mão apenas da fronteira de processo (nada aqui
  depende dela) e roda a suíte em 46s em vez de 86s.
- O `gitleaks-action` v3 escreve os achados de volta na pull request, então sob o padrão somente
  leitura do workflow ele falhava com `Resource not accessible by integration` (403) *depois* de
  escanear -- o que se lê como falha de varredura e não de permissão. O job agora concede
  `pull-requests: write` só para si.
- A versão do Node no CI dessincronizou do toolchain do mesmo jeito que a do Go. O Dependabot moveu
  o `frontend/Dockerfile` para `node:26` mas o workflow ficou no Node 20, e o Vitest 5 exige
  `^22.12.0 || ^24.0.0 || >=26.0.0` -- então todo arquivo de teste falhava ao subir um worker e a
  execução reportava "no tests". A mensagem nomeia o pool, não a versão do Node, que é o que a fez
  parecer um problema de pool. O CI agora roda Node 26, igual à imagem.


- `main` reparada depois que quinze pull requests do Dependabot foram mergeadas de uma vez, várias
  delas de major. A automação de dependências adicionada na mudança anterior funcionou como
  projetada -- majors chegaram como PRs separadas em vez de agrupadas -- mas mergeá-las sem revisão
  quebrou o build de seis formas distintas, cada uma com causa própria: `react` foi para 19 e
  `react-dom` ficou em 18, partindo o par do runtime num major; `@types/react` e `@types/react-dom`
  se partiram do mesmo jeito; o ESLint 10 deixou de trazer `@eslint/js` transitivamente e deixou o
  `eslint-plugin-react-hooks@5` incapaz de satisfazer o peer range; o `golangci-lint-action` v9
  aciona o golangci-lint v2, cujo schema de configuração é uma reescrita que o `.golangci.yml` v1
  do repositório não atende; o `arduino/setup-task` sem token esgotou o orçamento compartilhado de
  API do GitHub do runner; e o `alpine:3.24` trazia `libcrypto3` 3.5.7-r0, com uma negação de
  serviço do OpenSSL (CVE-2026-14456).
- A imagem do backend agora roda `apk upgrade` antes de instalar pacotes. Uma tag de imagem base é
  reconstruída no ritmo dela, então entre a correção de uma CVE de pacote de SO e a republicação da
  tag, todo build embarca a versão vulnerável. A imagem do frontend já fazia isso; o backend agora
  acompanha. As quatro imagens passam limpas depois disso.
- O `golangci-lint-action` fica fixado em v6 com uma nota de que uma PR do Dependabot que tente
  subi-lo deve ser fechada, não mergeada, até o `.golangci.yml` migrar para o schema v2 -- essa
  migração é trabalho próprio, não efeito colateral de um bump de versão.

### Changed

- O `eslint-plugin-react-hooks` v7 introduz `set-state-in-effect`, `purity` e `refs`, que não
  existiam na v5 e apontam 18 lugares em 14 arquivos -- majoritariamente o padrão de "carregar a
  configuração existente no estado do formulário na montagem". Elas ficam em `warn` em vez de
  adotadas ou desligadas: o sinal continua visível e código novo segue sendo apontado conforme é
  escrito, sem prender o build a um refactor que ninguém agendou. Voltem para `error` quando as
  ocorrências atuais forem resolvidas.


- `google.golang.org/grpc` atualizado de 1.82.1 para 1.83.1 por conta da CVE-2026-84304 (HIGH),
  apontada pelo scan de imagem nas três imagens Go. É dependência indireta, alcançada pelo cliente
  da API do Google; o `govulncheck` não reporta nada no código chamado, mas ela vai junto nas
  imagens.

- **Todo restart da API deixava o frontend servindo `502` até ele também ser reiniciado.** O nginx
  resolve um hostname literal em `proxy_pass` uma única vez, na carga da configuração, e o cacheia
  pelo resto da vida do processo -- então quando o container da api voltava em outro endereço
  depois de um deploy, um crash ou uma mudança de escala, o nginx continuava discando o antigo e
  nada se recuperava sozinho. Observado diretamente: nginx conectando em `172.20.0.6` enquanto a
  api estava saudável em `172.20.0.5`, o que para o usuário parecia a aplicação inteira fora do ar.
  O upstream agora é nomeado por uma variável com `resolver` explícito, o que força resolução a
  cada requisição. Verificado estacionando um container no endereço antigo da api para forçá-la a
  outro, e então logando com sucesso por um frontend que nunca foi reiniciado.
- Falhas de gateway chegavam ao usuário como texto cru de status HTTP -- uma tela de login
  exibindo "Bad Gateway", que não diz nem o que aconteceu nem o que fazer. `502`, `503` e `504` não
  trazem corpo `{error}` porque vêm de um proxy e não da API, então agora viram uma mensagem
  traduzida de "servidor temporariamente indisponível" em vez da linha de status.
- `PUT /alerts/{id}/assignee` desatribuía o alerta em silêncio quando o corpo usava o nome errado
  de campo. `analystId` é um ponteiro para que um `null` explícito signifique "desatribuir", o que
  tornava um corpo sem nenhuma chave reconhecida indistinguível de uma desatribuição intencional:
  um cliente com um erro de digitação recebia `204` e o responsável apagado. A chave agora precisa
  estar presente; `{"analystId": null}` continua desatribuindo.

### Added

- `.github/dependabot.yml` acompanhando módulos Go, npm, GitHub Actions e os dois Dockerfiles. Nada
  acompanhava dependências antes, que é como o `golang.org/x/crypto` ficou vinte dias atrás de uma
  correção publicada até um scan de container perceber -- e isso pesa mais com o projeto público.
  Atualizações de patch e minor são agrupadas em um PR por ecossistema; as major chegam sozinhas
  para serem lidas com atenção.


- Regras de Template de Mapeamento de Campos não conseguiam endereçar um elemento de array. Um
  caminho como `detect.behaviors.0.tactic` -- o formato que payloads de CrowdStrike, CloudTrail e
  Wazuh usam -- não resolvia nada e era ignorado em silêncio, indistinguível de um campo que o
  payload nunca trouxe, então o admin que montava o template não tinha como saber qual dos dois
  aconteceu. Um segmento composto só de dígitos agora indexa um array. Um dígito contra um objeto
  continua sendo lido como chave primeiro, então um payload com um campo literal `"0"` segue
  funcionando; índices fora de faixa e negativos não resolvem nada em vez de dar a volta. O
  agrupamento de deduplicação de alertas compartilha o mesmo resolver e ganha isso também.
- Um roster de plantão que não dividia exato pela quantidade de vagas simultâneas deixava períodos
  com metade da equipe. Com cinco respondentes e duas vagas, a rotação produzia grupos de
  `[2, 2, 1]`: um período em cada três rodava com um único analista, numa escala configurada para
  dois, e nada avisava. O último grupo agora dá a volta ao início do roster, então todo período
  fica com a equipe completa. O custo -- alguém cobre dois períodos seguidos a cada ciclo -- passa
  a ser dito no editor da escala sempre que o roster não divide exato, em vez de ser descoberto
  pelo calendário. A rotação em TypeScript usada na pré-visualização foi alterada junto, e as
  fixtures compartilhadas em `docs/oncall-rotation-fixtures.json` mantêm as duas honestas.
- `ALLOW_PRIVATE_NETWORK_TARGETS` não podia ser definida num deploy real. A própria mensagem de
  recusa do `httpguard` manda o operador defini-la, e os dois `.env.example` e o
  `docs/THREAT_MODEL.md` a documentam -- mas o `docker-compose.yml` nunca a referenciava, nenhum
  serviço a declarava e não existe `env_file:`, então um valor no `.env` jamais chegava a um
  container. O mesmo nos manifestos do Kubernetes. Agora é declarada pelos serviços api, ingest e
  worker e no ConfigMap, vazia por padrão.
- O corpo inteiro da resposta de um destino de webhook que falha era lido com um `io.ReadAll` sem
  limite e ecoado na resposta da API e nos logs. O destino é configurável pelo admin e a resposta é
  controlada por quem o opera, então um único passo com falha podia gerar um erro arbitrariamente
  grande. O corpo agora é limitado a 4 KiB e marcado como truncado.
- Um limite de página acima do máximo devolvia menos linhas que o máximo. `limit=500` caía no
  default de 50 do repositório em vez de ser limitado ao teto de 200, então pedir mais entregava
  menos, sem nada na resposta indicando que um teto fora aplicado. Valores acima do teto agora são
  limitados a ele; um limite não numérico continua caindo no default, já que não há número
  sensato a inferir de lixo.

### Added

- `GET /api/v1/settings/on-call-schedules/current` responde "quem está de plantão agora" para cada
  escala, no fuso do tenant. Isso antes não tinha resposta possível: a resolução de plantão só
  existia dentro da entrega de acionamento, onde ela sorteia um analista para notificar, então
  nenhum analista, página de status ou integração de paging conseguia simplesmente perguntar -- e o
  timeline em Configurações precisava reimplementar a rotação em TypeScript para desenhar o
  calendário. Devolve o conjunto completo em vez de um sorteio, já que com vagas simultâneas há de
  fato várias pessoas de plantão, e mantém escalas em que ninguém está de plantão em vez de
  descartá-las: "ninguém" é exatamente o que um chamador precisa poder ver.

### Changed

- O default do rate limit de ingestão sobe de 60 para 600 requisições por minuto, e a chave dele
  passa a ser documentada onde é configurado. Ele é chaveado pelo **IP de origem**, não pelo
  endpoint nem pelo token, então toda integração vinda de um mesmo endereço divide um único
  orçamento -- com 60, um cliente cujo forwarder de SIEM ou NAT concentra cinco fontes começava a
  receber `429` a doze alertas por fonte por minuto. O novo default ainda limita um flood; reduza-o
  se a ingestão ficar numa rede não confiável.


- **Controle de acesso**: os gráficos de tendência de alertas/incidentes do dashboard e as médias
  de MTTA/MTTR ignoravam o escopo por tags. Todos os cards ao lado deles estavam corretamente
  escopados, mas a série temporal não: dois analistas restritos a tags diferentes e sem
  interseção recebiam tendências e médias byte a byte idênticas, cobrindo o tenant inteiro. As
  tendências liam materialized views chaveadas só por `tenant_id`, sem dimensão de tag para
  filtrar, então um chamador restrito agora segue um caminho ao vivo sobre as tabelas base que
  reproduz exatamente a definição de cada view. Um chamador irrestrito mantém o caminho rápido
  pré-agregado. O que vazava era metadado agregado, não conteúdo -- contagens e médias, nunca um
  título ou payload -- mas para um tenant que usa tags para separar clientes ou unidades, é
  exatamente o que a segregação por tags existe para impedir.
- Alertas que chegavam por webhook sem nenhuma tag eram invisíveis para todo analista restrito
  por tag. A visibilidade por tag é uma interseção, então um registro sem tags não intersecta com
  nada: monte um SOC com o Tier 1 restrito e os alertas recém-ingeridos ficavam visíveis para
  ninguém que devesse triá-los, sem erro e sem nenhum indicador de fila vazia. Um alerta cuja
  origem não envia tags agora recebe o nome do próprio endpoint como tag, o que preserva a regra
  fail-closed e ao mesmo tempo dá significado à tag (ela identifica a origem) e permite concedê-la
  a um papel.
- As keywords de playbook eram coletadas no editor, guardadas e nunca consultadas. O match testava
  só `alert_name_pattern` e `is_default`, então um playbook podia listar todas as keywords
  imagináveis e ainda assim não casar com nada -- uma funcionalidade que parecia funcionar e
  silenciosamente não funcionava. Keywords agora participam, casadas como substring sem diferenciar
  maiúsculas, ranqueadas abaixo de um pattern explícito (a declaração de intenção mais específica)
  e acima do playbook padrão.
- O normalizador genérico de webhook comparava a severidade de forma exata e sensível a
  maiúsculas, então uma origem enviando `High` -- o que a maioria dos SIEM e EDR faz -- tinha
  todo alerta rejeitado com 400 logo na porta. A severidade agora é reconhecida sem diferenciar
  maiúsculas nem espaços, com as grafias que outros produtos realmente emitem mapeadas para o
  vocabulário daqui (`info`, `warn`, `error`, `crit`, `sev1`-`sev4` e companhia). Um valor
  desconhecido continua sendo rejeitado em vez de adivinhado, e o erro agora diz quais são aceitos.
- A API aceitava passos de playbook na fase `new`, que a UI deixou de renderizar. Um passo assim
  era guardado, contado e exibido em lugar nenhum -- invisível e não editável no produto. Criação
  e edição agora recusam.
- Um destino de escalação que nunca poderia funcionar salvava normalmente e só falhava ao ser
  disparado, ou seja, o operador descobria durante o incidente para o qual ele fora configurado.
  Destinos de webhook agora são checados no salvamento. A checagem é consultiva por design -- a
  guarda em tempo de conexão do `httpguard` continua sendo o controle de verdade, já que só ela
  resolve o nome no instante da requisição e portanto não pode ser burlada por DNS rebinding -- e
  respeita `ALLOW_PRIVATE_NETWORK_TARGETS`, então um deploy on-prem legítimo ainda pode apontar um
  passo para um endereço privado.
- Um tipo de IOC inválido era rejeitado com uma mensagem que só ecoava o valor errado. São quinze
  tipos válidos e não havia como descobri-los pelo erro; agora ele os nomeia.
- Uma escala de plantão sem participantes passa a ser sinalizada na lista. Continua sendo uma
  configuração válida -- passos de webhook, PagerDuty e Slack disparam para um destino
  independentemente de quem está de plantão -- mas ela não coloca ninguém de plantão e deixa
  nome/e-mail/telefone do analista em branco em toda notificação que alimenta, o que merece ser
  dito em vez de deduzido de uma contagem de zero.
- O card de MTTA/MTTR do dashboard podia exibir uma média acima de uma legenda dizendo que ela
  fora calculada a partir de nada ("32h / 4m" sobre "baseado em 0 reconhecidos, 0 fechados"),
  porque o número e o próprio denominador vinham de consultas diferentes. Agora mostra um traço
  quando a amostra está vazia.


- O primeiro provedor de IA de um tenant agora vira padrão sozinho. Enquanto nada estiver marcado
  como padrão, toda análise falha com "no LLM provider configured" -- ou seja, terminar o cadastro
  e mesmo assim nada funcionar era a experiência normal de primeira vez. A regra usa "não existe
  padrão" em vez de "não existem provedores", porque apagar o padrão não promove ninguém: sem isso,
  um tenant podia ficar com vários provedores e nenhum utilizável.
- Resposta vazia de um provedor de LLM era registrada como análise bem-sucedida. O resultado era
  uma análise marcada como "completed" e sem nada dentro: nenhuma saída para o analista, nenhum
  erro para o operador, e nada em lugar nenhum indicando que o provedor tinha voltado em branco.
  Respostas vazias agora falham a execução com o `finish_reason` do próprio provedor na mensagem,
  o que separa resposta truncada (`length` -- típico de modelo de raciocínio que gasta todo o
  orçamento de saída pensando) de resposta suprimida (`content_filter`) de endpoint que
  simplesmente não devolve conteúdo onde um chamador compatível com OpenAI espera. Encontrado na
  prática contra o endpoint de compatibilidade OpenAI do Gemini. Um turno vazio que carrega uma
  chamada de ferramenta continua válido, já que a chamada é a saída daquele turno.

- **Integridade de dados**: escalar um alerta para incidente eram três transações independentes
  (criar o incidente, vincular o alerta a ele, mudar o alerta para `escalated`). Qualquer falha
  depois da primeira deixava um estado parcial real — falha no vínculo deixava um incidente
  criado e preso a nada, falha no status deixava o alerta parecendo não-escalado já carregando um
  incidente — e, como a criação rodava incondicionalmente, o retry que naturalmente segue uma
  requisição que falhou criava *outro* incidente para o mesmo alerta. Duplo clique no botão fazia
  o mesmo. A escalação agora roda inteira em uma única transação (as três escritas commitam ou
  nenhuma), e uma segunda tentativa num alerta já escalado retorna `409 Conflict` em vez de um
  incidente duplicado. A checagem usa o vínculo de incidente que o próprio alerta já tem como
  fonte da verdade, então vale independente de como a requisição chegou — a UI esconder o botão
  sempre foi só uma proteção de cliente, nunca imposta pela API.
- Export de PDF de incidente e postmortem liam o incidente e seus quatro sub-recursos (histórico
  de status, comentários, alertas vinculados, IOCs) em cinco transações separadas — cinco
  snapshots, então um comentário postado no meio da geração podia entrar num documento cujo
  histórico de status era anterior a ele, além de cinco checagens de permissão redundantes pra
  mesma linha. As cinco leituras agora compartilham uma transação.
- **Acessibilidade**: diálogos não tinham nome acessível. O `Modal` renderizava um `role="dialog"`
  pelado, então o leitor de tela anunciava qualquer um deles como só "diálogo", sem indicar o que
  tinha aberto. O `Modal` agora *exige* uma prop `label` e a aplica como `aria-label` —
  obrigatória em vez de opcional de propósito, pra que o compilador pegue o próximo diálogo sem
  rótulo em vez de ele passar em silêncio.
- `golang.org/x/crypto` atualizado de 0.54.0 para 0.55.0 por conta da CVE-2026-56854 (bypass de
  autenticação no `x/crypto/ssh` por restrições de endereço de origem não aplicadas), marcada como
  CRITICAL pelo scan de imagem nas três imagens Go. O KuruOps não usa `x/crypto/ssh` em lugar
  nenhum -- só argon2id pra hash de senha -- então nada aqui era explorável, mas o módulo estava
  no grafo de dependências e ia junto nas imagens.
- **Acessibilidade**: a lista de Playbooks era inalcançável por teclado. Cada linha era um
  `<div onClick={navigate}>` sem `tabindex` e sem `role`, e a linha era a *única* forma de abrir um
  playbook — então um usuário só de teclado ou de leitor de tela conseguia criar e buscar
  playbooks, mas nunca abrir um (WCAG 2.1.1 Teclado, Nível A). A linha agora envolve o título num
  `<Link>` de verdade, usando o mesmo padrão `row-link-stretch` que as tabelas de
  Alertas/Incidentes já usam, mantendo a linha inteira clicável. A mesma correção vale para as
  linhas de alerta vinculado de um incidente, que tinham o padrão idêntico.
- **Acessibilidade**: quatro controles de formulário não tinham nome acessível nenhum — os campos
  de início/fim de horário de funcionamento de uma escala de plantão (dois `<input type="time">`
  adjacentes, indistinguíveis num leitor de tela), o select de adicionar respondente, o select de
  adicionar responsável, e o textarea de descrição do incidente. Todos os quatro agora têm
  `aria-label`.
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
