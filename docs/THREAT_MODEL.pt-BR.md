<p align="right"><a href="THREAT_MODEL.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Threat Model

Este documento descreve os limites de confiança do KuruOps, o que cada camada de defesa garante
(e o que explicitamente não garante), e como girar um segredo sem downtime. É um complemento ao
código, não um substituto — onde os dois divergirem, o código está certo e este documento está
desatualizado; atualize-o no mesmo PR que mudar o comportamento descrito aqui (ver checklist do
`.github/PULL_REQUEST_TEMPLATE.md`).

## Limites de confiança

```
Navegador (não confiável)
   │ HTTPS (produção) / HTTP (dev local)
   ▼
nginx (frontend) -- serve a SPA, faz proxy de /api e /auth
   │ rede interna do Docker Compose
   ▼
api / ingest (Go, confiável) -- autenticação e autorização são checadas aqui
   │ role kuruops_app, sem BYPASSRLS
   ▼
Postgres -- RLS por tenant_id
```

- **Navegador → nginx**: fronteira não confiável de verdade. Qualquer coisa vinda daqui —
  header, cookie, body — é tratada como potencialmente hostil pelo backend.
- **nginx → api/ingest**: nginx é a única coisa que fala com o navegador; ele reescreve
  `X-Real-IP` incondicionalmente antes de repassar (`proxy_set_header X-Real-IP $remote_addr`),
  então esse é o único header de IP que o backend confia (ver
  `middleware.getClientIP`/`ClientIPFromHeader` — nunca `X-Forwarded-For`, que o cliente controla
  diretamente).
- **`ingest` é superfície separada de `api`**: só ele recebe tráfego de webhook de terceiros
  (vendors SIEM/XDR), autenticado por um token por-endpoint com hash SHA-256 armazenado
  (`webhook_endpoints.token_hash`), não por JWT de usuário. Isolar esse caminho limita o raio de
  um endpoint de ingestão comprometido/vazado a esse único endpoint (rotação de token disponível em
  Settings → Webhook Endpoints), sem tocar em nada do resto da API.
- **`worker` não tem superfície HTTP nenhuma** — só roda cron interno (refresh de materialized
  view, sweep de SLA/escalonamento), então não é um alvo de rede.
- **api/ingest/worker → Postgres**: os três conectam como o mesmo role least-privilege
  (`kuruops_app`, ver `db/init/kuruops_app_role.sql`) — sem `BYPASSRLS`, sem ownership de tabela.
  O role `postgres` (superuser, usado só por migrations/`task db:*`) contorna RLS por completo,
  de propósito — nunca é o role que uma requisição de usuário usa.

## Limite de confiança do redirect/callback OAuth

Google Drive e Slack são conectados via um fluxo OAuth padrão de authorization-code
(`internal/service/storage_config_service.go`'s `HandleGDriveOAuthCallback`,
`internal/service/slack_config_service.go`'s `HandleOAuthCallback`), ambos caindo em
`OAuthCallbackHandlers` (`internal/httpserver/handlers/oauth_callback.go`) — a única família de
rotas top-level deliberadamente não autenticada deste app (`/auth/oauth/...`, fora de `/api/v1`),
já que o redirect de um provedor é um GET simples do navegador, sem JWT pra anexar.

- **`state` é toda a âncora de confiança.** Não há sessão nesse ponto — o callback não prova nada
  sobre quem está fazendo a requisição, exceto o que a linha correspondente em `oauth_states`
  (`internal/repository/oauth_state_repository.go`) diz: um token de uso único, persistido no
  banco, com TTL, gerado quando o fluxo foi iniciado a partir de uma página de Settings
  autenticada. Quem conseguir adivinhar ou interceptar um valor de `state` válido antes de ele ser
  consumido poderia completar o fluxo no lugar da vítima; ele é opaco, gerado com `crypto/rand`, e
  consumido exatamente uma vez pra fechar essa janela.
- **A troca de `code` por token acontece só no servidor** — o callback nunca confia no que o
  navegador afirma sobre o resultado, só no que o próprio endpoint de token do Google/Slack
  retorna para o `code` que este handler recebeu diretamente.
- **Erros de qualquer um dos provedores são ecoados verbatim** (`?gdrive_error=access_denied`,
  etc) — esse é texto fornecido pelo provedor descrevendo um resultado visível ao usuário (consent
  recusado, code expirado), não o estado interno deste app. Uma falha *dentro* de
  `HandleGDriveOAuthCallback`/`HandleOAuthCallback` (um erro de banco, uma falha ao buscar o
  state, uma resposta de token malformada) é logada só no servidor e redireciona com um código
  genérico fixo (`?gdrive_error=connection_failed`) — o texto bruto do erro Go nunca vai parar numa
  URL de redirect, que histórico do navegador, headers `Referer` e qualquer log de acesso de proxy
  no caminho poderiam capturar.
- **O tenant é sempre resolvido da mesma forma** (`AuthService.ResolveDefaultTenant`), igual a
  todo outro ponto de entrada não autenticado deste app single-tenant (o endpoint ACS do SAML, o
  admin padrão semeado) — não há ambiguidade de tenant pra um atacante explorar nesse limite.

## O que a Row-Level Security garante — e o que não garante

RLS filtra toda query pelo `tenant_id` da sessão (`set_config('app.tenant_id', ...)`, ver
`db.Pool.WithTenant`). Hoje o KuruOps roda como instância única — não existe conceito de
"empresa"/tenant no login (`README.md` raiz) — então na prática atual, RLS por `tenant_id` é
defesa em profundidade contra um bug de query que "esqueceu" o filtro certo, não o mecanismo de
isolamento que separa usuários entre si no dia a dia. Ela existe pronta para SaaS multi-tenant
real no futuro sem reescrever schema.

**RLS não garante**:
- **Autorização fina dentro do tenant** — isso é o `isAdmin`/`resourceAccess`/`allowedTags` do Role
  atribuído, aplicado na camada de serviço/handler (`middleware.RequireAdmin`,
  `middleware.RequireResourceAccess`, filtro `tags && $allowedTags` na query), não na política de
  RLS em si. Um bug nessa camada não é
  coberto por RLS.
- **Isolamento contra o role `postgres`** — intencional; esse role só é usado para setup
  administrativo, nunca por uma requisição HTTP.
- **Consistência dos sub-recursos de incidente** — *fechada*. Comentários, vínculos com alertas,
  IOCs, timeline, histórico de status e as rotas de aprovar/rejeitar tool call do MCP consultavam
  por ID apenas sob o RLS do tenant, sem repetir a checagem de `allowedTags` que
  `Get`/`ChangeStatus`/`Close`/`ChangePhase`/`SetSeverityAndPriority`/`UpdateDescription` fazem.
  Isso era diretamente explorável: nada obriga um cliente a chamar `GET /incidents/{id}` antes de
  `GET /incidents/{id}/comments`, então um analista restrito por tag que soubesse um ID fora do seu
  escopo conseguia ler as Team Notes e os IOCs, escrever novos e aprovar tool calls com efeito
  colateral. Todo sub-recurso agora carrega o pai via `loadVisible(ctx, tx, id, allowedTags)` antes.

  A proteção contra a regressão é estrutural, não uma lista que alguém precisa lembrar de
  atualizar: `TestSubResourceRoutesRejectTagRestrictedCaller` percorre as rotas chi *realmente
  registradas* e afirma que nenhuma rota com `{id}` responde 2xx para um chamador restrito por tag
  (42 rotas no momento em que isto foi escrito). Um sub-recurso novo passa a ser coberto no momento
  em que é registrado; um que esqueça o gate quebra o teste. As únicas exceções são `/` e
  `/bulk/*`, que não têm entidade prévia para checar — ver `routesExemptFromTagScoping`.

## O que a varredura de retenção de dados garante — e o que não garante

Settings → Dados & Auditoria → Retenção configura por quanto tempo um alerta/incidente **fechado**
fica no KuruOps antes de ser permanentemente excluído pelo job horário `sweepDataRetention` do
`cmd/worker` (padrão 18 meses, configurável separadamente por tipo de recurso —
`internal/service/retention_config_service.go`).

- **A exclusão é definitiva, não é arquivamento reversível.** Não existe desfazer, lixeira, nem
  passo de exportação automática antes de excluir — uma linha que passou do prazo configurado some
  assim que a varredura roda. Um admin que quiser uma cópia do que será excluído precisa exportar
  antes (Settings → Dados & Auditoria → Exportar Auditoria), antes de reduzir um prazo de retenção
  abaixo da idade de um registro já existente.
- **Só `closed_at` determina elegibilidade** — um alerta/incidente aberto nunca é tocado
  independente da idade, e um incidente reaberto depois de fechado (que limpa `closed_at`) é
  excluído da varredura mesmo que estivesse elegível momentos antes: a varredura seleciona e
  exclui cada tipo de recurso numa única instrução atômica `WITH ... FOR UPDATE ... DELETE`, então
  o Postgres revalida a elegibilidade contra o estado atual committado de cada linha, não um
  snapshot tirado antes da exclusão.
- **Evidências em blob storage nunca são tocadas.** Anexos de comentários de alerta/incidente
  ficam no backend de storage configurado pelo tenant (S3/GCS/Google Drive/disco local),
  referenciados por URL/chave — `internal/blobstore.Store` não tem método `Delete` em lugar nenhum
  deste código, então uma exclusão aqui só pode remover o registro do próprio KuruOps no banco,
  nunca o arquivo subjacente. Essa é uma limitação de escopo deliberada e conhecida, não um
  descuido: limpar blob storage órfão não está implementado.
- **Quem pode configurar**: o mesmo controle admin-only de qualquer outro painel de Settings
  (`middleware.RequireAdmin()`) — reduzir um prazo de retenção é, na prática, uma ação de
  destruição de dados disponível pra qualquer um com um Role admin, por isso o frontend exige
  confirmação inline explícita ao *reduzir* um valor (aumentar um valor, ou salvar pela primeira
  vez, não exige confirmação já que nenhum dos dois pode excluir algo que já não seria excluído de
  qualquer forma).
- **O raio de impacto por ciclo é limitado** (`retentionSweepBatchLimit`, 5000 linhas por tipo de
  recurso por ciclo horário) — um backlog grande no primeiro deploy dessa feature é processado aos
  poucos ao longo dos ciclos em vez de numa única transação sem limite competindo com tráfego real
  pelas mesmas tabelas.

## Segredos: como nunca trafegam em claro para o Postgres

`secrets.Store` (`Put`/`Resolve`) é a única forma pela qual código de aplicação lida com uma
credencial de terceiro (senha de bind LDAP, chave privada SP do SAML, API key de LLM, credencial
S3/GCS). O backend padrão, `PersistentEnvStore`, criptografa com AES-256-GCM
(`SECRETS_ENCRYPTION_KEY`, uma variável de ambiente — nunca fica no Postgres) antes de gravar na
tabela `secret_store`; um dump ou vazamento do banco sozinho não expõe nada em claro, só com a
chave de criptografia também comprometida. `VaultStore`/`AWSKMSStore` (`SECRETS_BACKEND=vault|kms`)
vão além — o segredo nem chega a existir no Postgres, criptografado ou não.

O que isso **não** cobre: o valor em claro passa pela memória do processo Go no momento de
`Put`/`Resolve` (inevitável — é preciso pra de fato bindar no LDAP, chamar a API do LLM, etc.), e
trafega do navegador pro `api` em texto no momento de salvar em Settings (HTTPS em produção
protege esse trecho; em dev local via `task deploy:up` é HTTP puro, aceitável só numa máquina
local).

**Tradeoff deliberado**: o `EnvStore` original (puramente em memória) nunca tocava disco, mas
perdia toda credencial a cada restart do processo enquanto as linhas no banco continuavam
referenciando o ref antigo — um bug de disponibilidade real, descoberto durante teste ao vivo de
LDAP/SAML nesta sessão. Trocar para `PersistentEnvStore` troca "nunca toca disco" por "sobrevive a
um restart", mitigado pela criptografia AES-256-GCM antes da gravação — não é uma correção sem
custo, é uma escolha de durabilidade vs. superfície, documentada aqui de propósito.

**A chave padrão do `.env.example` é recusada fora do modo dev.** O `.env.example` vem com um
`SECRETS_ENCRYPTION_KEY` real, funcional, pra que `docker compose up` funcione de primeira em dev
local — a mesma conveniência faz dela uma chave conhecida e publicamente visível se algum dia for
copiada e colada direto num deploy real em vez de gerada do zero. `secrets.NewFromConfig` falha
ao iniciar se a chave configurada ainda for exatamente esse valor e `AUTH_MODE` não for
`dev`/`dev-headers`.

## Requisições de saída para URLs configuradas por admin: proteção contra SSRF

Três configurações aceitam uma URL que este código então acessa em nome do tenant: o
`Destination` do webhook de uma política de escalonamento (`internal/notifier/webhook.go`), o
`endpoint` de um servidor MCP (`internal/mcpclient/jsonrpc.go`), e o `base_url` de um provedor LLM
pros tipos `openai_compatible`/`azure_openai`/`self_hosted` (`internal/llmclient/llmclient.go`) —
o `api.anthropic.com` fixo do tipo anthropic não é configurável pelo usuário, então não entra
nesse escopo. Qualquer um com acesso a essas três áreas de Settings pode, de outra forma, apontar
pra `http://169.254.169.254/...` (endpoint de metadata de nuvem) ou `http://localhost:5432/...`
(um serviço interno que confia em requisições vindas deste processo) e fazer o
kuruops-api/kuruops-worker mandar essa requisição por ele — um pivô clássico de SSRF, de "pode
editar config" pra "pode alcançar rede interna".

As três agora acessam via `internal/httpguard.NewClient`, cujo `Transport.DialContext` resolve o
host alvo e recusa conectar se qualquer IP resolvido for loopback, link-local ou privado
(RFC1918/RFC4193) — checado contra o IP realmente sendo conectado, não só a string do hostname da
URL, então não é contornável por DNS rebinding (um nome que resolve pra um IP público quando a
config é salva mas um privado quando a requisição é de fato feita) nem digitando um IP privado
direto. Um deploy genuinamente on-prem, onde um receptor de webhook ou servidor MCP ou endpoint
LLM self-hosted legitimamente mora em espaço de endereço privado, define
`ALLOW_PRIVATE_NETWORK_TARGETS=true` pra tirar o processo inteiro dessa proteção.

## Cookie `SameSite` do SAML: por que o `RelayState` é o canal primário

`RedirectToIDP` originalmente dependia de um cookie (`SameSite=Lax`) pra amarrar a resposta do IdP
de volta ao `AuthnRequest` original (proteção contra replay/CSRF). Isso quebrava contra qualquer
IdP real (Okta, Azure AD, mocksaml.com) — todo IdP compatível com o spec devolve a asserção via
POST cross-site (binding HTTP-POST), e um cookie `SameSite=Lax` nunca é enviado numa requisição
POST cross-site por design do navegador. O fix: o ID do `AuthnRequest` viaja no `RelayState` (que
todo IdP compatível com o spec ecoa de volta verbatim) como canal primário; o cookie continua como
fallback só pro caso same-site. Ver `internal/authn/saml.go`'s comentário em
`samlRequestIDCookie` para os detalhes de wire format.

## Headers de segurança HTTP

`middleware.SecurityHeaders` (aplicado a toda resposta de `api`/`ingest`, `internal/httpserver/router.go`)
usa uma CSP maximamente restrita (`default-src 'none'; frame-ancestors 'none'`) porque essas
respostas são sempre JSON ou texto puro, nunca HTML — não há razão legítima pra carregar
script/style/frame a partir de uma resposta de API. `frontend/nginx.conf` define uma CSP separada,
mais permissiva, só na resposta que serve o `index.html` (o único lugar que serve HTML de fato) —
`style-src 'unsafe-inline'` é necessário porque a árvore React usa `style={{...}}` extensivamente
(gráficos principalmente), não porque sobrou de um exemplo copiado; não há
`dangerouslySetInnerHTML` nem `<script>` inline em lugar nenhum do app, então `script-src` continua
estrito sem exceção correspondente. `X-XSS-Protection` deliberadamente não é setado — deprecated,
o próprio Auditor de XSS do Chrome foi removido em 2019 depois de o auditor em si introduzir bugs;
a orientação atual da OWASP é omitir o header, não setar um valor.

## Runbook: rotação de segredo sem downtime

Nenhuma rotação abaixo derruba sessões já ativas — o JWT de sessão não depende de o
LDAP/SAML/LLM continuar configurado do jeito que estava no momento do login.

### Senha de bind do LDAP

1. Gere/atualize a nova senha do lado do diretório LDAP primeiro (a conta de serviço, não a de um
   usuário comum).
2. Settings → Identity Providers → LDAP → preencha o campo de senha com o novo valor e salve.
   Deixar o campo em branco mantém a credencial antiga (`SaveLDAPConfig`'s "re-save sem nova senha
   mantém a atual") — **é preciso digitar a senha nova explicitamente** para rotacionar de fato.
3. Login LDAP a partir daqui usa a senha nova; nada mais precisa reiniciar.

### Keypair de assinatura da SP (SAML)

Não existe um botão dedicado de "rotacionar" — a única forma suportada é remover e reconfigurar,
já que `SaveSAMLConfig` só gera um keypair novo quando não existe nenhuma config anterior (ver seu
comentário: reconfigurar sem apagar mantém o keypair, de propósito, porque o IdP já confia
naquele certificado).

1. Settings → Identity Providers → SAML → "Remover configuração".
2. Reconfigure do zero (mesma URL/XML de metadata do IdP) e salve — isso gera um par novo.
3. Baixe a metadata nova em `GET /auth/saml/metadata` e registre no IdP no lugar da antiga.
4. **Login SAML fica indisponível entre o passo 1 e o IdP confiar no certificado novo do passo 3**
   — planeje uma janela curta se este for o único método de login em uso; local/LDAP continuam
   funcionando durante a janela.

### Chave de API de um provedor LLM

1. Settings → AI Integration → edite o provedor → preencha o campo de API key com o valor novo e
   salve. Mesma regra do LDAP: deixar em branco mantém a chave antiga.
2. Nenhuma sessão de análise em andamento é afetada — a chave só é resolvida no momento de cada
   chamada à LLM, não mantida aberta entre requisições.

## Segredos de infraestrutura: credenciais padrão do `docker-compose.yml`

As seções acima cobrem segredos de nível de aplicação (LDAP, SAML, LLM/MCP) — resolvidos via
`secrets.Store` e rotacionáveis pela UI de Settings sem reiniciar nada. `docker-compose.yml` tem
uma categoria diferente e mais básica de segredo: as credenciais que colocam a própria stack de pé
(senha do Postgres, das roles `kuruops_app`/`kuruops_worker`, a chave de criptografia do
`secrets.Store`). Essas **não** passam pela UI — são lidas direto de variáveis de ambiente no
momento em que cada container sobe.

Todo esse conjunto vem com um valor padrão funcional embutido no próprio `docker-compose.yml`
(propositalmente — `docker compose up` sem nenhuma configuração adicional já sobe a stack inteira,
sem exigir que quem está só experimentando localmente gere segredos primeiro). O efeito colateral é
que esses valores padrão são **públicos**: aparecem em texto puro neste repositório, tanto no
`docker-compose.yml` quanto no `.env.example` (ver raiz do repositório). São aceitáveis para rodar
a stack no laptop de um único desenvolvedor. **Não são aceitáveis** para qualquer ambiente
alcançável por outra pessoa — uma máquina de staging compartilhada, um ambiente de demonstração,
qualquer coisa exposta a um IP que não seja `localhost`.

### Rotação antes de qualquer uso compartilhado

1. Copie `.env.example` (raiz do repositório) para `.env`.
2. Gere valores novos para cada credencial:
   - `POSTGRES_PASSWORD` / `POSTGRES_USER`: qualquer senha forte; trocar `POSTGRES_USER` exige
     também atualizar as chamadas `docker compose exec postgres psql -U postgres ...` hardcoded no
     `Taskfile.yml` (`db:up`, `db:test:up`, `db:backup`, etc. — elas não leem a variável, assumem
     literalmente `postgres`).
   - `KURUOPS_APP_PASSWORD` / `KURUOPS_WORKER_PASSWORD`: qualquer senha forte — só precisam bater
     com o que `db/init/kuruops_app_role.sql` / `kuruops_worker_role.sql` configuram na criação
     das roles (rodar `task db:reset` depois de trocar, para recriar as roles com a senha nova).
   - `SECRETS_ENCRYPTION_KEY`: `openssl rand -base64 32`. **Atenção**: trocar essa chave depois que
     já existem segredos gravados no Postgres (`secret_store`) os torna ilegíveis — gere a chave
     definitiva antes do primeiro `docker compose up`, não depois.
3. `docker compose up -d` novamente para os serviços pegarem os valores novos (`api`/`ingest`/
   `worker` seguem `${VAR:-default}`, então uma vez setado no `.env` o valor override já é o que
   sobe).
4. `.env` está no `.gitignore` do repositório — nunca force `git add -f` nele.

Exceção deliberada: `VAULT_DEV_ROOT_TOKEN_ID` (serviço `vault-dev`, perfil `tools`) fica hardcoded
no `docker-compose.yml`, sem variável de override. É o token de root de uma instância Vault em modo
dev — armazenamento em memória, descartado a cada restart (ver o próprio comentário do serviço) —
então não existe nada durável ali para rotacionar.
