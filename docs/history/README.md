# Histórico de planos

Este diretório guarda planos de melhoria já executados — mantido por valor histórico (o
diagnóstico e o raciocínio por trás de cada decisão), não como documentação do estado atual do
projeto. Para o estado atual, sempre prefira:

- `README.md` (raiz) e `backend/README.md`/`frontend/README.md`/`db/README.md` para arquitetura e
  como cada parte funciona hoje.
- `CHANGELOG.md` para o que mudou e quando.
- `docs/TROUBLESHOOTING.md` para pegadinhas conhecidas.

Um plano arquivado aqui pode descrever um estado que já mudou — cada um traz uma nota de status no
topo linkando pra onde a informação atualizada realmente vive, mas a data de "arquivamento" é o
limite: nada aqui é atualizado retroativamente para acompanhar mudanças posteriores.

## Planos

- [`PLANO_DE_MELHORIAS.md`](PLANO_DE_MELHORIAS.md) — diagnóstico inicial e plano de 3 fases
  (rate limiting/refresh tokens/cache SAML; loop agêntico MCP/normalizadores/SSE; Vault-KMS/
  on-call/CEF). Todas as 3 fases foram implementadas.
