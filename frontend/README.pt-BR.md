<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# KuruOps frontend

React + Vite + TypeScript. Cobre **Dashboard, Alertas, Incidentes, Playbooks e Settings**
(gerenciamento completo de alertas e resposta a incidentes de SOC/SIEM), com internacionalização
(pt/en), tema claro/escuro e atualização ao vivo via SSE (`src/api/eventStream.ts`).

## Rodando local

```bash
npm install
npm run dev   # :5173, faz proxy de /api e /auth para http://localhost:8080 (cmd/api)
```

Precisa do `cmd/api` do backend rodando (ver `backend/README.md`). Login: `admin@kuruops.local` /
`ChangeMe123!` — todo deploy novo já vem com esse admin, semeado pela migration
`db/migrations/0002_seed_default_admin.up.sql`. Não há campo de "empresa" no login (KuruOps é single-instance,
"empresa" existe só como tag em alertas/incidentes) e não há tela de signup — outros usuários locais
são criados via SQL direto ou pela API de Settings depois do primeiro admin logado.

## Estrutura

- `src/auth/AuthContext.tsx` — sessão (token JWT + refresh token), persistida em `localStorage`.
  `mustChangePassword` é lido direto do JWT (claim `must_change_password`), não do backend a cada
  render — ver `decodeMustChangePassword`. Um 401 tenta uma troca automática do refresh token antes
  de deslogar (ver `src/api/client.ts`'s `refreshOnce`).
- `src/pages/ChangePassword.tsx` + o guard em `App.tsx` — toda sessão com `mustChangePassword` é
  redirecionada pra cá antes de qualquer outra tela; o backend aplica o mesmo bloqueio de verdade
  (`middleware.RequirePasswordChanged`), então isso não é só uma UX, dá pra confiar nela.
- `src/api/client.ts` — wrapper fino de `fetch`, sempre exige o token explicitamente.
- `src/api/hooks.ts` — `useList` faz logout automático numa falha de refresh (token expirado/inválido).
- `src/api/eventStream.ts` — hook sobre `GET /api/v1/events/stream` (SSE); Dashboard/Alertas/
  Incidentes usam para recarregar dados ao vivo em vez de polling.
- `src/pages/dashboard/` — três abas (Alertas/Incidentes/Follow-up), cada uma com KPIs, gráficos
  (`src/components/charts/`) e filtros (severidade/status/tag/analista-ou-commander/período —
  `src/components/TimeRangeFilter.tsx` cobre tanto presets quanto um intervalo customizado com
  data+hora via `<input type="datetime-local">`).
- `src/pages/alerts/`, `src/pages/incidents/` — listagem com filtros/paginação e página de detalhe
  (timeline, comentários, papéis da equipe NIST no caso de incidentes, análise por IA).
- `src/pages/playbooks/` — biblioteca de procedimentos por categoria/fase.
- `src/pages/settings/` — um arquivo por painel (Webhooks, Integração de IA, Servidores MCP,
  Armazenamento, SMTP, Usuários e Papéis, Provedores de Identidade, Tags, Escalas de Plantão, SLAs
  de Incidente, Escalonamento, Exportação de Auditoria, Banco de Dados Externo), todos seguindo o
  mesmo padrão: `useList` para carregar, formulário local para criar/editar, ações inline por linha
  para mutações.

## Testes

```bash
npm test              # Vitest, watch mode
npm run test:coverage # com relatório de cobertura
```

~40 arquivos de teste (componentes + páginas), usando Testing Library e `fetch` mockado por teste
(sem servidor real) — ver qualquer `*.test.tsx` existente como referência de padrão ao adicionar um
novo. `npx tsc --noEmit` e `npm run build` continuam sendo a verificação de tipos/build.

## O que falta

- **Descoberta de tools via `tools/list`** — feito para servidores com `transport: "http"`: o botão
  "Discover tools" na linha de cada servidor (`MCPServersPanel.tsx`) chama
  `POST /settings/mcp-servers/{id}/discover-tools`, que fala MCP de verdade com o servidor
  (`backend/internal/mcpclient`) e mostra as tools reais com checkbox em vez de texto livre. O
  formulário de criação ainda pede as tools como texto (comma-separated) porque o servidor precisa
  existir antes de dar pra descobrir tools nele — descubra depois de salvar.
- **Sem testes end-to-end** — a cobertura atual é unitária/componente (fetch mockado); não há
  suíte e2e contra um backend real rodando (isso é o que `task test:smoke`, na raiz do repo, cobre
  no nível de API/HTTP, não de UI).
