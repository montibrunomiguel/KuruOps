# ArgusOps frontend

React + Vite + TypeScript. Cobre **Dashboard, Alertas, Incidentes, Playbooks e Settings** (gerenciamento completo de alertas e resposta a incidentes de SOC/SIEM).

## Rodando local

```bash
npm install
npm run dev   # :5173, faz proxy de /api e /auth para http://localhost:8080 (cmd/api)
```

Precisa do `cmd/api` do backend rodando (ver `backend/README.md`). Login: `admin@argusops.local` /
`ChangeMe123!` — todo deploy novo já vem com esse admin, semeado pela migration
`0013_seed_default_admin.up.sql`. Não há campo de "empresa" no login (ArgusOps é single-instance,
"empresa" existe só como tag em alertas/incidentes) e não há tela de signup — outros usuários locais
são criados via SQL direto ou pela API de Settings depois do primeiro admin logado.

## Estrutura

- `src/auth/AuthContext.tsx` — sessão (token JWT + usuário), persistida em `localStorage`.
  `mustChangePassword` é lido direto do JWT (claim `must_change_password`), não do backend a cada
  render — ver `decodeMustChangePassword`.
- `src/pages/ChangePassword.tsx` + o guard em `App.tsx` — toda sessão com `mustChangePassword` é
  redirecionada pra cá antes de qualquer outra tela; o backend aplica o mesmo bloqueio de verdade
  (`middleware.RequirePasswordChanged`), então isso não é só uma UX, dá pra confiar nela.
- `src/api/client.ts` — wrapper fino de `fetch`, sempre exige o token explicitamente
- `src/api/hooks.ts` — `useList` faz logout automático num 401 (token expirado/inválido)
- `src/pages/settings/` — um arquivo por painel, todos seguindo o mesmo padrão: `useList` para
  listar, formulário local para criar, ações inline por linha para mutações

## O que falta

- **Dashboard, Alertas, Incidentes, Playbooks** — não implementados nesta passada (escopo foi
  Settings + LDAP/SAML + client MCP, nessa ordem, a pedido).
- **Descoberta de tools via `tools/list`** — feito para servidores com `transport: "http"`: o botão
  "Discover tools" na linha de cada servidor (`MCPServersPanel.tsx`) chama
  `POST /settings/mcp-servers/{id}/discover-tools`, que fala MCP de verdade com o servidor
  (`backend/internal/mcpclient`) e mostra as tools reais com checkbox em vez de texto livre. O
  formulário de criação ainda pede as tools como texto (comma-separated) porque o servidor precisa
  existir antes de dar pra descobrir tools nele — descubra depois de salvar.
- Sem testes automatizados ainda (nem unitário nem e2e) — só verificação manual via
  `npm run build`/`tsc --noEmit` e smoke test das telas de login/settings neste PR (sem backend
  rodando, então sem verificar o fluxo de dados de ponta a ponta).
