<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Plan history

This directory holds improvement plans that have already been executed — kept for historical
value (the diagnosis and reasoning behind each decision), not as documentation of the project's
current state. For the current state, always prefer:

- `README.md` (root) and `backend/README.md`/`frontend/README.md`/`db/README.md` for architecture
  and how each part works today.
- `CHANGELOG.md` for what changed and when.
- `docs/TROUBLESHOOTING.md` for known gotchas.

A plan archived here may describe a state that has since changed — each one carries a status note
at the top linking to where the up-to-date information actually lives, but the "archival" date is
the boundary: nothing here is updated retroactively to track later changes.

## Plans

- [`PLANO_DE_MELHORIAS.md`](PLANO_DE_MELHORIAS.md) — initial diagnosis and 3-phase plan (rate
  limiting/refresh tokens/SAML cache; MCP agentic loop/normalizers/SSE; Vault-KMS/on-call/CEF).
  All 3 phases have been implemented.
