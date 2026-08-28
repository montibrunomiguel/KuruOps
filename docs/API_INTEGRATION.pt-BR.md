<p align="right"><a href="API_INTEGRATION.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Guia de Integração — API & Webhooks

O KuruOps expõe duas superfícies HTTP separadas, em dois serviços separados, para dois públicos separados:

| Superfície | Serviço | Porta (local) | Público | Especificação |
|---|---|---|---|---|
| API REST (`/api/v1/**`, `/auth/**`) | `cmd/api` | `:8080` | O frontend, e qualquer ferramenta de admin que você construir sobre o próprio KuruOps | [`docs/openapi.yaml`](openapi.yaml) — o contrato completo e autoritativo |
| Ingestão via webhook (`/hooks`) | `cmd/ingest` | `:8081` | Suas fontes de SIEM/XDR/alerta, enviando alertas *para dentro* do KuruOps | Este documento |

Este documento cobre a segunda: **como um sistema externo envia um alerta para o KuruOps**. É deliberadamente narrativo — o `openapi.yaml` não cobre o `/hooks` de jeito nenhum, porque o formato da requisição não é um schema fixo único (ver "Formato do payload por fonte" abaixo), o que não se encaixa bem no modelo de schema único por rota do OpenAPI. Tudo sobre a API REST em si (listar/atualizar alertas, gerenciar settings, autenticação) está totalmente especificado no `openapi.yaml`; não duplicamos isso aqui.

## 1. Criar um endpoint de webhook

Antes de qualquer alerta poder ser ingerido, um admin cadastra um endpoint de webhook em **Settings → Webhook Endpoints** (ou via `POST /api/v1/settings/webhooks` na API REST, mesmo efeito). Dois campos importam para o que acontece depois:

- **`name`** — um rótulo, só para sua própria referência na listagem de Settings.
- **`source`** — escolhe qual normalizador de payload o `/hooks` usa para alertas enviados por esse endpoint (ver §3). Não diferencia maiúsculas/minúsculas; qualquer valor não reconhecido cai no normalizador genérico.

A resposta inclui um **token** bearer, mostrado exatamente uma vez:

```json
{
  "endpoint": { "id": "...", "name": "Wazuh Prod", "source": "wazuh", "tokenLast4": "a1b2", "status": "active", "expiresAt": "2026-11-19T00:00:00Z" },
  "token": "whk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

Guarde esse token na configuração do seu SIEM/sistema emissor agora — o KuruOps nunca mostra ele de novo (só o `tokenLast4` do endpoint, para reconhecer qual token é qual na UI). Se perder, regenere um novo pela Settings (`POST /api/v1/settings/webhooks/{id}/regenerate`) — o antigo para de funcionar no exato momento em que você faz isso.

Por padrão, um token expira 90 dias depois de emitido ou regenerado pela última vez; defina `expiresInDays` explicitamente na criação (0 ou negativo = nunca expira) se esse padrão não servir pra sua política de rotação.

## 2. Enviar um alerta

```
POST http://<host-do-ingest>:8081/hooks
X-Webhook-Token: whk_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{ ... payload, formato depende do source configurado no endpoint, ver §3 ... }
```

- **Autenticação**: o header `X-Webhook-Token`, com o token exato da criação do endpoint. Nada mais autentica esse endpoint — é um segredo compartilhado único por endpoint, não assinatura por requisição.
- **Limite de tamanho do body**: 1 MiB. Bodies maiores são rejeitados diretamente.
- **Rate limit**: 60 requisições/minuto por IP de origem por padrão (`WEBHOOK_RATE_LIMIT_PER_MINUTE`), aplicado independentemente de qual endpoint/token está sendo usado a partir daquele IP. Acima do limite: `429 Too Many Requests`.

### Respostas

| Status | Significado |
|---|---|
| `201 Created` | Novo alerta ingerido. Body: `{"id": "<uuid-do-alerta>"}`. |
| `200 OK` | Deduplicado contra um alerta já existente (ver §5) — nenhuma linha nova foi criada. Body: `{"id": "<uuid-do-alerta-original>"}`, mesmo formato nos dois casos, então um emissor que não se importa com dedup pode tratar os dois igual. |
| `400 Bad Request` | JSON malformado, ou um campo obrigatório está faltando/inválido (ver §3 por fonte) — o body é a mensagem de erro em texto puro. |
| `401 Unauthorized` | Token ausente/inválido/expirado. |
| `403 Forbidden` | O endpoint existe mas está desabilitado (Settings → Webhook Endpoints → Disable). |
| `429 Too Many Requests` | Rate limit excedido (ver acima). |
| `500 Internal Server Error` | Algo falhou do lado do KuruOps — o alerta não foi ingerido; seguro tentar de novo. |

## 3. Formato do payload por fonte

Toda variante de fonte é normalizada para o mesmo formato interno antes de um alerta ser criado — `title`, `severity` (um de `critical`/`high`/`medium`/`low`/`informational`), e alguns campos opcionais. Qual normalizador roda é escolhido pelo campo `source` do endpoint, definido na criação — não por nada na própria requisição.

### `generic` (o fallback — qualquer valor de `source` não reconhecido também cai aqui)

```json
{
  "title": "Login suspeito de dispositivo não reconhecido",
  "severity": "high",
  "external_id": "evt-88213",
  "rule_id": "auth-anomaly-42",
  "asset": "vpn-gateway-01",
  "src_ip": "203.0.113.7",
  "tags": ["vpn", "fora-do-horario"]
}
```

`title` e `severity` são obrigatórios (severity precisa ser exatamente um dos cinco valores acima, sensível a maiúsculas/minúsculas). Todo o resto é opcional. Use esse formato para qualquer fonte sem um adaptador dedicado abaixo — configure seu emissor pra mandar esse envelope diretamente, sem precisar esperar o KuruOps ganhar suporte nativo pra sua ferramenta específica.

### `wazuh`

```json
{
  "id": "1234567890.123456",
  "rule": { "level": 10, "description": "Múltiplas falhas de autenticação", "id": "5710", "groups": ["authentication_failures", "pci_dss_10.2.4"] },
  "agent": { "name": "web-01", "ip": "10.0.1.15" },
  "data": { "srcip": "203.0.113.7" }
}
```

`rule.level` (a escala própria do Wazuh, 0–15+) mapeia pra severidade do KuruOps: `>=12` critical, `>=9` high, `>=6` medium, `>=3` low, caso contrário informational. `rule.groups` vira as tags do alerta (auto-criadas se novas — ver §4). `rule.description` vira o título.

### `crowdstrike`

```json
{
  "event": {
    "DetectId": "ldt:abc123",
    "DetectName": "Process Injection",
    "Severity": 80,
    "SeverityName": "High",
    "ComputerName": "WKSTN-042",
    "LocalIP": "10.0.5.20",
    "Tactic": "Defense Evasion",
    "Technique": "Process Injection"
  }
}
```

`SeverityName` (o rótulo próprio do CrowdStrike — Critical/High/Medium/Low/Informational) é usado quando presente; o score numérico `Severity` (0–100) é o fallback, mapeado por faixas.

### `guardduty`

```json
{
  "id": "8ab5f8...",
  "type": "UnauthorizedAccess:EC2/SSHBruteForce",
  "title": "Tentativas de força bruta SSH contra i-0abcd1234",
  "severity": 8.5,
  "resource": { "instanceDetails": { "instanceId": "i-0abcd1234", "networkInterfaces": [{ "publicIp": "203.0.113.50" }] } },
  "service": { "action": { "networkConnectionAction": { "remoteIpDetails": { "ipAddressV4": "198.51.100.9" } } } }
}
```

`severity` é o score float próprio do GuardDuty (0.1–8.9), dividido em faixas nos cinco níveis do KuruOps.

Adicionar um normalizador dedicado novo (em vez de depender do `generic`) é uma mudança de código no backend — ver `backend/internal/ingest/normalize_*.go` para o padrão (interface `Normalizer`, um arquivo adaptador por fonte) se você for contribuir com um.

## 4. Tags

Qualquer tag que seu payload mandar (`rule.groups` no Wazuh, `tags` no genérico — CrowdStrike/GuardDuty hoje não mapeiam nenhum campo para tags) é auto-criada no catálogo de tags do tenant no momento em que é vista pela primeira vez — nada precisa estar pré-cadastrado em **Settings → Tags** antes. Uma tag nova aparece lá imediatamente depois, totalmente gerenciável (cor, exclusão) como qualquer outra. Ver `TagService.EnsureExist` se quiser os detalhes exatos; resumindo, isso nunca pode vazar um alerta para um analista que não deveria ver — um papel restrito por tag só ganha visibilidade quando um admin adiciona explicitamente a tag nova à allow-list daquele papel, igual a qualquer tag pré-existente.

## 5. Deduplicação

Um endpoint pode ser configurado (Settings → Webhook Endpoints, ou `groupByFields`/`dedupWindowMinutes` na criação) com uma lista de nomes de campo em JSON-path — notação com ponto, ex.: `host.name`, batendo com a estrutura do payload do próprio emissor, não com o formato normalizado. Quando configurado:

- Um alerta novo cujos valores em todos esses caminhos batem com os de um alerta já existente, ingerido dentro de `dedupWindowMinutes` (padrão 30) do original, não cria uma linha nova — em vez disso incrementa o `duplicateCount` do alerta existente, e a resposta é `200 OK` com o id do alerta *original*.
- Um payload sem um dos campos configurados nunca dedupica contra nada — ausência nunca bate com ausência.
- Deixe `groupByFields` vazio (o padrão) para desabilitar dedup completamente nesse endpoint — todo payload ingerido vira seu próprio alerta.

## 6. Metadata

Qualquer objeto `"metadata"` no nível raiz do payload é armazenado e mostrado literalmente na tela de detalhe do alerta, num painel próprio — um canal do Slack, um link de runbook, uma tag de ambiente, qualquer par chave/valor que sua fonte quiser anexar. Metadata malformado (não é um objeto, ou o campo simplesmente não existe) é ignorado silenciosamente, nunca vira `400` — é um enriquecimento best-effort, não um campo obrigatório.

```json
{ "title": "...", "severity": "high", "metadata": { "slackChannel": "#incident-response", "environment": "production" } }
```

Um endpoint também pode ser configurado com um **Field Mapping Template** (Settings → Field Mapping Templates), que extrai valores adicionais via JSON-path do payload bruto e mescla no mesmo objeto de metadata, sob rótulos que um admin define — útil pra expor um campo que sua fonte manda mas que não está já em `metadata` no nível raiz. As próprias chaves `metadata.*` do emissor vencem em caso de conflito de rótulo.

## 7. O que acontece depois da ingestão

- **Análise por IA**: se o provedor de LLM padrão do tenant tiver optado por "Analisar automaticamente todos os alertas recebidos" (Settings → AI Integration), todo alerta ingerido aqui é analisado automaticamente em background — caso contrário, a análise só acontece quando um analista clica em "Analisar com IA" na UI. De qualquer forma, a ingestão em si nunca espera por isso; a resposta volta imediatamente.
- **Auto-atribuição de plantão**: se uma escala de plantão estiver configurada e bater com o alerta, ele é auto-atribuído a quem estiver de plantão no momento.
- **Atualização ao vivo**: qualquer aba do navegador conectada (Dashboard, listagem de Alertas) reflete o alerta novo em instantes via Server-Sent Events — sem polling, sem precisar recarregar manualmente do lado de quem está vendo.

## 8. Todo o resto

Ler alertas de volta, atualizar status, fechar/classificar, comentar, escalar para incidente, e toda operação de admin/Settings passam pela API REST em `cmd/api` (`:8080`), totalmente especificada em [`docs/openapi.yaml`](openapi.yaml). Essa API exige uma sessão real (login local/LDAP/SAML, ou um token pessoal de API em **Profile → API Tokens**) — é uma fronteira de confiança separada do token de webhook acima, que só consegue criar alertas, nada mais.
