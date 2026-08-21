<p align="right"><a href="THREAT_MODEL.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Threat Model

Este documento descreve os limites de confiança do ArgusOps, o que cada camada de defesa garante
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
   │ role argusops_app, sem BYPASSRLS
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
  (`argusops_app`, ver `db/init/argusops_app_role.sql`) — sem `BYPASSRLS`, sem ownership de tabela.
  O role `postgres` (superuser, usado só por migrations/`task db:*`) contorna RLS por completo,
  de propósito — nunca é o role que uma requisição de usuário usa.

## O que a Row-Level Security garante — e o que não garante

RLS filtra toda query pelo `tenant_id` da sessão (`set_config('app.tenant_id', ...)`, ver
`db.Pool.WithTenant`). Hoje o ArgusOps roda como instância única — não existe conceito de
"empresa"/tenant no login (`README.md` raiz) — então na prática atual, RLS por `tenant_id` é
defesa em profundidade contra um bug de query que "esqueceu" o filtro certo, não o mecanismo de
isolamento que separa usuários entre si no dia a dia. Ela existe pronta para SaaS multi-tenant
real no futuro sem reescrever schema.

**RLS não garante**:
- **Autorização fina dentro do tenant** — isso é `role`/`resourceAccess`/`allowedTags`, aplicado na
  camada de serviço/handler (`middleware.RequireRole`, `middleware.RequireResourceAccess`,
  filtro `tags && $allowedTags` na query), não na política de RLS em si. Um bug nessa camada não é
  coberto por RLS.
- **Isolamento contra o role `postgres`** — intencional; esse role só é usado para setup
  administrativo, nunca por uma requisição HTTP.
- **Consistência dos sub-recursos de incidente** — comentários, vínculos com alertas e timeline
  ainda não repetem a checagem de `allowedTags` que `Get`/`ChangeStatus`/`Close`/`ChangePhase`/
  `SetSeverityAndPriority`/`UpdateDescription` já fazem (ver `backend/README.md`, seção
  "Autorização") — dependem só do isolamento por tenant via RLS. Num ambiente single-tenant isso
  não vaza nada entre tenants diferentes, mas significa que um usuário com acesso ao recurso
  `incidents` (mas sem a tag específica de um incidente) pode, hoje, comentar/ver sub-recursos de
  um incidente fora do seu `allowedTags` se souber o ID. Lacuna conhecida, não um achado novo desta
  revisão.

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
(senha do Postgres, das roles `argusops_app`/`argusops_worker`, a chave de criptografia do
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
   - `ARGUSOPS_APP_PASSWORD` / `ARGUSOPS_WORKER_PASSWORD`: qualquer senha forte — só precisam bater
     com o que `db/init/argusops_app_role.sql` / `argusops_worker_role.sql` configuram na criação
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
