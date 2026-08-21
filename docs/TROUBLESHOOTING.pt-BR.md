<p align="right"><a href="TROUBLESHOOTING.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Troubleshooting

Pegadinhas reais já encontradas neste repo — a maioria só existia como conhecimento tácito de quem
mexeu no deploy/testes antes, não documentado em lugar nenhum. Adicione aqui qualquer coisa nova
que te custou mais de alguns minutos pra descobrir.

## Deploy / Docker Compose

### `task db:up` ou `task deploy:up` travando/falhando em vez de simplesmente esperar o Postgres subir

As tasks que esperam um serviço ficar saudável usam `docker compose up --wait`, não um loop
manual com `sleep`/`seq`. Isso é proposital: `sleep` e `seq` não são garantidos no PATH puro do
Windows (fora do Git Bash/WSL), então um loop hand-rolled de polling quebra silenciosamente ali. Se
você for adicionar uma nova task que precisa esperar um container, prefira `--wait
--wait-timeout N` (usa o `healthcheck:` já declarado no `docker-compose.yml`) em vez de escrever
seu próprio loop.

### Healthcheck do container `frontend` falha com `wget: bad address` ou connection refused, mas o nginx "parece" estar rodando

O script de entrypoint da imagem oficial do nginx que habilita IPv6 (`20-envsubst-on-templates.sh`
e afins) só faz o patch no `default.conf` *original, não modificado* da imagem — como
`frontend/nginx.conf` sobrescreve esse arquivo, o patch nunca roda, e o nginx fica IPv4-only. Só
que o musl libc da imagem Alpine resolve `localhost` para `::1` (IPv6) primeiro no
`/etc/hosts` — então `wget http://localhost/` de dentro do próprio container (é assim que o
healthcheck do `docker-compose.yml` testa) tenta IPv6 primeiro, recebe connection refused (nginx
não está escutando ali), e falha.

Fix: `frontend/nginx.conf` declara `listen [::]:80;` explicitamente, ao lado do `listen 80;`
normal — dual-stack manual, já que o auto-patch da imagem não se aplica. `api`/`ingest` não têm
esse problema porque `net.Listen(":PORT")` do Go já faz bind dual-stack por padrão.

### Volume Docker nomeado montado vazio fica com dono `root`, e o processo do container (rodando como usuário não-root) não consegue escrever nele

Acontece com qualquer volume novo montado num diretório que a imagem não tinha antes — Docker cria
o ponto de montagem como `root:root` na primeira vez, mesmo que o container rode como `USER
argusops` (ver `backend/Dockerfile`). Sintoma: erro `permission denied` tentando escrever ali logo
na subida do container (foi assim que o volume `dev-jwt-keys:/app/.dev-keys` quebrou na primeira
tentativa).

Fix: criar o diretório de destino *dentro do Dockerfile*, como o usuário não-root, antes do
`ENTRYPOINT` (`RUN mkdir -p /app/.dev-keys` depois do `USER argusops` herdado) — Docker copia a
ownership desse diretório pro volume na primeira montagem, já que ele existe e tem dono certo no
layer da imagem. Se o volume já foi criado uma vez com dono errado (por já ter subido antes do
fix), rebuildar a imagem sozinho não resolve — é preciso `docker volume rm` no volume específico
pra forçar a reinicialização de ownership.

## Testes

### Um slice Go `nil` retornado como JSON vira `null`, não `[]`, e quebra código de frontend que assume array

`json.Marshal` de um `[]T` que é `nil` (não `[]T{}`) produz o literal `null`. Isso é
frequentemente invisível em Go (`for range nil` não panica, `len(nil)` é 0) mas quebra qualquer
consumidor JS que chama `.map`/`.length` direto na resposta assumindo array. Acontece
principalmente quando um valor vem de um `map[K][]V` e a chave não existe — `assignees[id]` num
mapa sem essa chave retorna o zero value do slice, que é `nil`, não `[]V{}`.

Fix: normalizar explicitamente antes de serializar. Ver `orEmptyUserSummarySlice` em
`backend/internal/repository/incident_repository.go` como padrão a copiar — e cobrir com um teste
que falha se a normalização for removida (`incident_repository_test.go` comenta isso
explicitamente: "Must be [], not nil").

### Suíte de testes do frontend (Vitest) falha com `[vitest-pool-runner]: Timeout waiting for worker to respond`, mas rodando de novo passa

Sintoma observado especificamente no Windows rodando vários processos pesados em paralelo (ex.:
`go test`, `golangci-lint`, e `vitest` ao mesmo tempo) — contenção de CPU real, não um teste
quebrado. `Test Files`/`Tests` ainda aparecem como "passed" no resumo mesmo com esse erro no meio
do log. Antes de investigar um teste específico por causa disso, rode a suíte isolada (sem outros
processos pesados concorrentes) uma vez — se passar limpo, foi contenção, não bug.

### `go test` falhando com `fatal error: out of memory allocating heap arena map`

Falha transitória de alocação do runtime Go sob pressão de memória do sistema (várias JVMs/Docker
containers/processos Node rodando ao mesmo tempo), não um bug no código. Rodar de novo
normalmente resolve; se persistir, feche processos concorrentes pesados primeiro.

### `secrets.EnvStore.Resolve` de uma ref que nunca existiu não retorna erro

Ao contrário do que a assinatura `(string, error)` sugere, `EnvStore.Resolve` sempre retorna `nil`
de erro — uma ref desconhecida simplesmente resolve pra string vazia `""`. Um teste que espera
"resolver uma ref inválida deveria falhar" vai quebrar de um jeito não óbvio (não é o erro que
falha, é o step seguinte que recebe uma credencial vazia). Isso é comportamento do `EnvStore`
especificamente — `VaultStore`/`AWSKMSStore` reais retornam erro de verdade nesse caso.

## Ferramentas de linha de comando neste ambiente (Windows/Git Bash)

### Matar um processo em background (`kill $PID` do Bash) não derruba de fato o processo, ele continua segurando a porta

No Git Bash/MSYS, `$!` (PID do último comando em background) é um PID no espaço do MSYS, que não
necessariamente corresponde ao PID nativo do Windows que o `netstat`/Gerenciador de Tarefas
enxergam — comum quando o comando em background é um wrapper que por sua vez spawna outro processo
(ex.: `npx` spawnando um `node.exe` separado). `kill`/`pkill -P` nesse PID não alcança o processo
real.

Fix confiável: descobrir o PID nativo de verdade pela porta (`netstat -ano | grep ":PORTA" | grep
LISTENING`, última coluna) e matar esse com `taskkill //F //PID <pid>` — não confiar em `$!` para
esse cenário. Ver `backend/scripts/run-mcp-reference-test.sh`'s `stop_server` para um exemplo
funcional desse padrão.

### Um processo em background segurando a mesma saída (`stdout`) do script pai trava qualquer `| tail` ou outro consumidor de pipe pra sempre

Se você inicia um processo em background (`comando &`) sem redirecionar `stdout`/`stderr` pra um
arquivo, ele herda o descritor de arquivo do script pai. Se esse script inteiro estiver sendo
executado como parte de um pipe (`task minha-task | tail -N`), o pipe só fecha (EOF) quando *todo*
processo que tem aquele descritor aberto termina — incluindo o processo em background, mesmo que o
script "principal" já tenha logicamente terminado e feito seu `trap ... EXIT`. Sintoma: o comando
parece travado indefinidamente sem nenhuma saída, mesmo que o trabalho de verdade (ex.: os testes)
já tenha rodado e passado há muito tempo.

Fix: sempre redirecionar a saída de um processo em background pra um arquivo/`/dev/null`
explicitamente (`comando >"$LOG_FILE" 2>&1 &`), nunca deixar herdar o stdout do script pai.
