<p align="right"><a href="CONTRIBUTING.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Contribuindo com o KuruOps

Antes de mais nada, obrigado por considerar contribuir com o KuruOps! São contribuições como a sua
que fazem do KuruOps uma ótima ferramenta open-source de gestão de incidentes de cibersegurança.

## Código de Conduta

Ao participar deste projeto, você concorda em seguir nosso
[Código de Conduta](CODE_OF_CONDUCT.pt-BR.md).

## Começando

### Pré-requisitos

- [Docker](https://www.docker.com/) & Docker Compose
- [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`)
- [Go 1.25+](https://go.dev/) (se for rodar o backend fora do Docker)
- [Node.js 20+](https://nodejs.org/) (se for rodar o frontend fora do Docker)

### Configurando o ambiente local

1. Faça um fork e clone o repositório:
   ```bash
   git clone https://github.com/your-username/KuruOps.git
   cd KuruOps
   ```

2. Suba toda a stack de desenvolvimento com um único comando:
   ```bash
   task deploy:up
   ```

3. Acesse a interface web em `http://localhost:3000` com as credenciais padrão:
   `admin@kuruops.local` / `ChangeMe123!`.

## Fluxo de Desenvolvimento

- **Desenvolvimento do Backend**: o código Go fica em `backend/`. Rode `task backend:vet`,
  `task backend:lint` (golangci-lint) e `task backend:test` antes de enviar PRs.
- **Desenvolvimento do Frontend**: o código React + Vite + TS fica em `frontend/`. Para hot reload
  de UI, rode `task frontend:dev`.
- **Migrations de Banco de Dados**: mudanças no banco são gerenciadas via `golang-migrate` em
  `db/migrations/`.

## Verificação de Qualidade

Antes de enviar um Pull Request, garanta que todos os testes passem:

```bash
task test         # vet/lint/vulncheck/testes do backend + typecheck/lint/audit/vitest/build do frontend
task test:smoke   # roda o smoke test HTTP/RLS ponta a ponta completo contra o deploy local
```

`task test` não precisa de um Postgres real rodando. Dois checks adicionais precisam, e não estão
incluídos acima:

```bash
task backend:test:coverage-gate   # suíte de integração com cobertura, falha se cair vs. backend/coverage-baseline.txt
task backend:test:migration       # teste de integração próprio da feature de migração de banco externo (ver sua entrada no Taskfile)
```

## Diretrizes para Pull Requests

1. Crie uma branch de feature descritiva: `git checkout -b feature/minha-feature-legal` ou
   `fix/descricao-do-problema`.
2. Faça commit das suas mudanças com mensagens claras (ex.: `feat(ingest): add Wazuh webhook normalizer`).
3. Garanta que a formatação do código esteja limpa (`gofmt` para Go) e que o lint passe
   (`task backend:lint` / `npm run lint`). O frontend ainda não tem Prettier configurado —
   `npm run typecheck` cobre erros de tipo próximos de formatação, mas não estilo.
4. Garanta que todos os testes automatizados passem.
5. Envie para o seu fork e abra um Pull Request para a branch `main`.

Obrigado por contribuir!
