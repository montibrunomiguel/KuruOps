<p align="right"><a href="CONTRIBUTING.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Contributing to KuruOps

First off, thank you for considering contributing to KuruOps! It's contributions like yours that make KuruOps a great open-source cybersecurity incident management tool.

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
   git clone https://github.com/your-username/KuruOps.git
   cd KuruOps
   ```

2. Start the full development stack with a single command:
   ```bash
   task deploy:up
   ```

3. Access the web interface at `http://localhost:3000` with default credentials: `admin@kuruops.local` / `ChangeMe123!`.

## Development Workflow

- **Backend Development**: Go code resides in `backend/`. Run `task backend:vet`, `task backend:lint` (golangci-lint), and `task backend:test` before submitting PRs.
- **Frontend Development**: React + Vite + TS code resides in `frontend/`. For UI hot reload, run `task frontend:dev`.
- **Database Migrations**: Database changes are managed via `golang-migrate` under `db/migrations/`.

## Quality Verification

Before submitting a Pull Request, make sure all tests pass:

```bash
task test         # backend vet/lint/vulncheck/tests + frontend typecheck/lint/audit/vitest/build
task test:smoke   # Runs full end-to-end HTTP/RLS smoke test against local deploy
```

`task test` doesn't need a live Postgres. Two more checks do, and aren't included above:

```bash
task backend:test:coverage-gate   # integration suite w/ coverage, fails if it dropped vs. backend/coverage-baseline.txt
task backend:test:migration       # external-database-migration feature's own integration test (see its Taskfile entry)
```

## Pull Request Guidelines

1. Create a descriptive feature branch: `git checkout -b feature/my-cool-feature` or `fix/issue-description`.
2. Commit your changes with clear commit messages (e.g. `feat(ingest): add Wazuh webhook normalizer`).
3. Ensure code formatting is clean (`gofmt` for Go) and lint passes (`task backend:lint` /
   `npm run lint`). The frontend still has no Prettier configured — `npm run typecheck` covers
   formatting-adjacent type errors, but not style.
4. Make sure all automated tests pass.
5. Push to your fork and submit a Pull Request to the `main` branch.

## Dependency Updates

Dependabot proposes updates weekly (see `.github/dependabot.yml`). Patch and minor bumps are
grouped into one PR per ecosystem and are routinely safe to merge once CI is green.

**A major bump is never merged without a human reading it.** Majors deliberately arrive as
separate PRs so they can be. This is not a formality: fifteen Dependabot PRs were once merged in
one sitting and broke the build in six unrelated ways — a runtime library moved a major version
while its paired package did not, a linter dropped a transitive dependency and orphaned a plugin,
a CI action moved to a line driving a config schema this repository does not use. Each was
obvious in isolation and invisible in a batch.

When reviewing a major, the questions worth asking are:

- Does this package have a paired one that must move together? (`react`/`react-dom`,
  `@types/react`/`@types/react-dom`, a plugin and the tool it plugs into.)
- Does it drop something it used to provide transitively?
- Does it change a config format this repository has a file for?
- Does the changelog mention new lint rules or new errors on code that used to pass?

If a major is blocked on other work, add it to the `ignore` list in `.github/dependabot.yml` with
a comment saying what unblocks it, rather than closing the PR and letting it reopen next week.

Thank you for contributing!
