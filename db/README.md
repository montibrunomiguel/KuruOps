# ArgusOps — banco de dados

Migrations em `migrations/`, formato `golang-migrate` (`{version}_{name}.up.sql` / `.down.sql`).

## Setup local

Via Task (recomendado — ver `README.md` na raiz): `task db:up && task db:migrate && task db:roles`
faz tudo isso, incluindo o role da aplicação abaixo, contra o Postgres do `docker-compose.yml`.

Manual, contra um Postgres já rodando:

```bash
createdb argusops
migrate -database "postgres://localhost:5432/argusops?sslmode=disable" -path migrations up
```

Sem `golang-migrate` instalado: `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`

## Role da aplicação (obrigatório antes de rodar `cmd/api` / `cmd/ingest` / `cmd/worker`)

As migrations em `0008_row_level_security.up.sql` e `0010_webhook_token_lookup_policy.up.sql` só
protegem os dados se a aplicação conectar com um role que **não** seja dono das tabelas e **não**
tenha `BYPASSRLS`. Rodar as migrations como superusuário/dono e depois conectar a aplicação com
esse mesmo usuário torna a RLS inofensiva — o dono da tabela ignora as políticas por padrão.

`task db:roles` roda exatamente isso (`db/init/argusops_app_role.sql`, idempotente — pode rodar de
novo a cada deploy). Equivalente manual:

```sql
create role argusops_app with login password '...' nosuperuser nocreatedb nocreaterole nobypassrls;
grant usage on schema public to argusops_app;
grant select, insert, update, delete on all tables in schema public to argusops_app;
grant usage, select on all sequences in schema public to argusops_app;
```

`DATABASE_URL` do backend deve apontar para `argusops_app`, não para o usuário dono das tabelas
usado para rodar as migrations.

## Role do worker (`cmd/worker`, refresh das materialized views)

`cmd/worker` **não** conecta como `argusops_app` -- conecta como `argusops_worker`
(`db/init/argusops_worker_role.sql`, também criado por `task db:roles`), que tem `BYPASSRLS`.

Motivo: `REFRESH MATERIALIZED VIEW` só pode ser rodado pelo dono da view, e uma materialized view
roda sua query com o privilégio do **dono**, não de quem chama o REFRESH (mesma regra de views
comuns). `mv_alert_daily_stats`/`mv_incident_kpis` são agregados cross-tenant por definição
(agrupados por `tenant_id`, sem um tenant único) e o refresh roda fora de qualquer contexto de
tenant -- então, se o dono da view for um role sem `BYPASSRLS`, a policy `tenant_id =
current_tenant_id()` de `alerts`/`incidents` nunca casa (não há tenant setado), e o REFRESH
"funciona" mas sempre recalcula para zero linhas, sem erro nenhum. `argusops_worker` existe só para
isso: só `SELECT`, nunca `INSERT`/`UPDATE`/`DELETE`, e não deve ser reusado para mais nada -- ver o
comentário em `db/init/argusops_worker_role.sql` para o histórico completo desse bug.

## Por que RLS e não só filtro na aplicação

O modelo de acesso por tags do protótipo (`allowedTags` / `resourceAccess`) já aponta o caminho
certo, mas se ficar só na camada de aplicação, uma query nova sem `WHERE tenant_id = ...` vaza
dados de outro cliente. As políticas em `0008_row_level_security.up.sql` fecham essa classe de bug
no banco: toda tabela sensível só devolve linhas do tenant setado via
`select set_config('app.tenant_id', ...)` na transação (ver `internal/db.Pool.WithTenant` no
backend Go).

## Limitação conhecida: materialized views e RLS

Postgres não suporta RLS em materialized views. `mv_alert_daily_stats` e `mv_incident_kpis`
(`0009_materialized_views.up.sql`) são agregações cross-tenant por definição — toda query contra
elas na camada de API **precisa** incluir `where tenant_id = $1` manualmente. Isso é uma exceção
documentada ao princípio "isolamento no banco, não na query", não um descuido.
