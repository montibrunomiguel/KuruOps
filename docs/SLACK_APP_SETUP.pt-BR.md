<p align="right"><a href="SLACK_APP_SETUP.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Configuração do App do Slack

Este guia mostra como criar o Slack App ao qual a integração Settings → Conectores → Slack do ArgusOps se conecta. Ele cobre o que existe **hoje** — conectar/desconectar um workspace via OAuth de bot token — além dos escopos reservados para a integração completa com o Slack (abrir incidentes pelo Slack, sincronizar mensagens e arquivos de threads em alertas e incidentes, vincular canais de incidente) planejada para atualizações futuras. Você só precisa fazer isso uma vez por implantação do ArgusOps.

## 1. Crie o app a partir de um manifesto

Manifestos de App do Slack permitem declarar escopos e configurações de OAuth em uma única etapa, em vez de passar por várias telas. Acesse [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From an app manifest**, escolha seu workspace e cole o YAML abaixo — substituindo `your-argusops-domain.example.com` pelo `APP_BASE_URL` real da sua implantação:

```yaml
display_information:
  name: ArgusOps
  description: Incident response and SOC operations, connected to Slack.
  background_color: "#1a1a2e"

oauth_config:
  redirect_urls:
    - https://your-argusops-domain.example.com/auth/oauth/slack/callback
  scopes:
    bot:
      - chat:write
      - channels:read
      - channels:manage
      - channels:history
      - groups:read
      - groups:write
      - groups:history
      - files:read
      - im:write
      - commands

settings:
  org_deploy_enabled: false
  socket_mode_enabled: false
  token_rotation_enabled: false
```

**Por que todos os escopos são pedidos de uma vez, mesmo sendo uma versão "só a fundação":** o Slack exige que um workspace reautorize o app sempre que a lista de escopos muda — conectando novamente, reaprovando na tela de consentimento. Declarar de uma vez o conjunto completo que esta integração vai eventualmente precisar, em vez de adicionar escopos PR por PR, significa conectar o ArgusOps ao Slack uma única vez, sem repetir essa etapa (e recompartilhar o bot token resultante) a cada nova capacidade lançada. Nem todo escopo acima é usado pelo código que existe hoje -- veja a tabela abaixo.

**Repare no que foi deliberadamente omitido:** não há bloco `event_subscriptions` no manifesto. O Slack exige uma URL ativa capaz de responder a um desafio de verificação no instante em que `event_subscriptions` é configurado com uma Request URL -- e esta versão não tem nenhum receptor para responder a isso. Esse bloco será adicionado (e este documento, atualizado) na versão futura que trouxer suporte à Events API do Slack (mensagens recebidas, respostas em thread, slash commands).

| Escopo | Usado por (hoje / planejado) |
|---|---|
| `chat:write` | Planejado — postar mensagens do ArgusOps em um canal vinculado |
| `channels:read` | Planejado — ler metadados de canais públicos |
| `channels:manage` | Planejado — criar um canal quando um admin vincula um a um incidente |
| `channels:history` | Planejado — buscar o histórico de mensagens de um canal público |
| `groups:read` | Planejado — mesmo que `channels:read`, para canais privados |
| `groups:write` | Planejado — mesmo que `channels:manage`, para canais privados |
| `groups:history` | Planejado — mesmo que `channels:history`, para canais privados |
| `files:read` | Planejado — buscar arquivos anexados a uma thread sincronizada |
| `im:write` | Planejado — enviar mensagem direta a um analista |
| `commands` | Planejado — um futuro slash command `/argusops` |

Nenhum desses é chamado pelo código do ArgusOps ainda — a versão atual só troca o código OAuth por um bot token e o armazena. Esta tabela existe para que uma futura atualização deste documento (quando um escopo realmente entrar em uso) tenha um "antes" claro para comparar.

⚠️ **Confira esta lista contra o catálogo de escopos atual do Slack antes de instalar.** A nomenclatura de escopos granulares do Slack mudou ao longo do tempo; se algum dos nomes acima for rejeitado ou aparecer como descontinuado ao colar o manifesto, consulte [api.slack.com/scopes](https://api.slack.com/scopes) pelo equivalente atual e ajuste.

## 2. Instale o app no seu workspace

Na página **OAuth & Permissions** do app, clique em **Install to Workspace** e aprove os escopos solicitados. Isso cria a instalação do app no seu workspace -- o ArgusOps em si ainda não foi informado sobre ela; isso acontece na próxima etapa, de dentro do ArgusOps.

## 3. Copie as credenciais para a configuração do ArgusOps

Na página **Basic Information** do app, copie:

- **Client ID** → `SLACK_CLIENT_ID`
- **Client Secret** → `SLACK_CLIENT_SECRET`
- **Signing Secret** → `SLACK_SIGNING_SECRET`

Defina essas variáveis de ambiente no serviço `api` (veja `docker-compose.yml` / a configuração de ambiente da sua implantação) e reinicie-o. `SLACK_SIGNING_SECRET` ainda não é usado por nada -- não há requisição recebida do Slack para verificar nesta versão -- mas o Slack fixa esse valor no momento da criação do app de qualquer forma, então capturá-lo agora evita reabrir esta página depois.

## 4. Conecte o workspace a partir do ArgusOps

No ArgusOps, acesse **Configurações → Conectores → Slack** e clique em **Conectar ao Slack**. Você será redirecionado para a tela de consentimento do Slack e, em seguida, de volta ao ArgusOps, mostrando o nome do workspace conectado e quem o conectou. **Desconectar** na mesma página remove o bot token armazenado e desativa a integração -- todo recurso que depende do Slack (incluindo os marcados como "Planejado" acima, quando existirem) fica oculto até que um workspace seja conectado novamente.

## O que vem a seguir

Esta versão cobre apenas conectar/desconectar. Sincronização de mensagens/threads/arquivos, abertura de incidentes pelo Slack e vinculação de canal de incidente estão planejadas para atualizações futuras -- cada uma vai estender este mesmo Slack App (reaproveitando os escopos já declarados acima) em vez de exigir um novo.
