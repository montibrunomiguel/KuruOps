# Contributing to ArgusOps

First off, thank you for considering contributing to ArgusOps! It's contributions like yours that make ArgusOps a great open-source cybersecurity incident management tool.

## Code of Conduct

By participating in this project, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting Started

### Prerequisites

- [Docker](https://www.docker.com/) & Docker Compose
- [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`)
- [Go 1.25+](https://go.dev/) (if running backend outside Docker)
- [Node.js 20+](https://nodejs.org/) (if running frontend outside Docker)

### Local Environment Setup

1. Fork and clone the repository:
   ```bash
   git clone https://github.com/your-username/ArgusOps.git
   cd ArgusOps
   ```

2. Start the full development stack with a single command:
   ```bash
   task deploy:up
   ```

3. Access the web interface at `http://localhost:3000` with default credentials: `admin@argusops.local` / `ChangeMe123!`.

## Development Workflow

- **Backend Development**: Go code resides in `backend/`. Run `task backend:vet` and `task backend:test` before submitting PRs.
- **Frontend Development**: React + Vite + TS code resides in `frontend/`. For UI hot reload, run `task frontend:dev`.
- **Database Migrations**: Database changes are managed via `golang-migrate` under `db/migrations/`.

## Quality Verification

Before submitting a Pull Request, make sure all tests pass:

```bash
task test         # Runs backend vet, Go tests, frontend typecheck, vitest, and build
task test:smoke   # Runs full end-to-end HTTP/RLS smoke test against local deploy
```

## Pull Request Guidelines

1. Create a descriptive feature branch: `git checkout -b feature/my-cool-feature` or `fix/issue-description`.
2. Commit your changes with clear commit messages (e.g. `feat(ingest): add Wazuh webhook normalizer`).
3. Ensure code formatting is clean (`gofmt` for Go, Prettier/ESLint for TypeScript).
4. Make sure all automated tests pass.
5. Push to your fork and submit a Pull Request to the `main` branch.

Thank you for contributing!
