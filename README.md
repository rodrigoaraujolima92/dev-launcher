# dev-launcher

Interface web para subir o ambiente local: marca quais projetos sobem, define quem espera
quem, agrupa por empresa/produto, salva perfis de execucao e acompanha o log de cada um.

```powershell
./start-ui.ps1            # compila se precisar, sobe e abre o navegador
./start-ui.ps1 -Porta 7011
./start-ui.ps1 -Encerrar  # encerra o launcher que estiver rodando
./start-ui.ps1 -Raiz D:\projetos
```

O launcher nao vem com projeto nenhum configurado. Na primeira execucao ele cria um
`config.json` vazio ao lado do binario, e os projetos entram pela tela (**+ adicionar projetos**).
Esse arquivo e de cada um e fica fora do Git (`.gitignore`): nome de projeto, pasta, rede
docker e afins so existem se voce cadastrar.

## Compilar

```powershell
go build -o dev-launcher.exe .   # binario unico, ~10 MB
go run .                         # compila e roda, sem gerar o .exe
```

Nao ha npm nem passo de frontend: `web/` (Vue incluso) entra no binario via `go:embed`.
O `start-ui.ps1` chama o `go build` sozinho quando algum `.go`, `.html`, `.css` ou `.js`
esta mais novo que o executavel.

Antes de commitar:

```powershell
gofmt -l . ; go vet ./... ; go test ./...
```

## Onde ficam os projetos (raiz)

Todo caminho relativo do `config.json` e resolvido contra uma **pasta base**, decidida nesta
ordem:

| Origem | Exemplo |
| --- | --- |
| flag `-raiz` | `dev-launcher.exe -raiz D:\projetos` |
| variavel `DEV_LAUNCHER_RAIZ` | `$env:DEV_LAUNCHER_RAIZ = 'D:\projetos'` |
| `raiz_projetos` no config | `"raiz_projetos": ".."` |
| padrao | a pasta acima do `config.json` |

Valor relativo e resolvido contra a **pasta do config**, nunca contra o diretorio de
trabalho - o launcher e chamado por atalho, de qualquer lugar.

E por isso que o config criado na primeira execucao usa `"raiz_projetos": ".."`: com o launcher morando dentro da
pasta que guarda os projetos, o mesmo arquivo funciona em qualquer maquina, sem
`C:\Users\fulano` no meio. Na mesma linha, caminho de projeto **dentro** da raiz e gravado
relativo (`meuapp/meuapp-backend`); so o que esta fora fica absoluto.

Quando um caminho nao existe na maquina - config de outra pessoa, raiz errada, projeto ainda
nao clonado - o launcher avisa no terminal ao subir e marca o cartao com **pasta nao
encontrada**, em vez de falhar so na hora de rodar.

## Encerrar

Tres caminhos, porque um launcher que subiu sem console (janela oculta, atalho) nao tem
Ctrl+C:

1. **Ctrl+C** no terminal onde ele esta rodando;
2. o botao **encerrar** no canto da tela;
3. **`dev-launcher.exe -encerrar`** (ou `./start-ui.ps1 -Encerrar`) em outro terminal.

Encerrar derruba os projetos em **modo gerenciado** - eles sao filhos do launcher, e deixar
orfao significaria porta ocupada por um processo que ninguem mais rastreia. Containers
Docker ficam no ar; eles sao independentes de proposito.

Se a porta ja estiver em uso por outro launcher, ele avisa e mostra como encerrar em vez de
despejar o erro de bind.

## Como funciona

- **Um binario so.** A interface (`web/`) vai embutida via `go:embed`; nao precisa de Node,
  npm nem dependencia externa - so a stdlib do Go.
- **Vue 3 sem passo de build.** `web/vendor/vue.global.prod.js` e servido pelo proprio
  binario (nada de CDN: funciona offline). A tela e reativa, entao uma acao repinta so o que
  mudou em vez de redesenhar a lista inteira - trocar de aba do log ou marcar um projeto nao
  recria os cartoes. Para atualizar o Vue, troque o arquivo em `web/vendor/`.
- **Cor de sintaxe no diff** com `web/vendor/highlight.min.js` (highlight.js 11.11.1, pacote
  `common` + `powershell` e `dockerfile` concatenados no mesmo arquivo). As cores ficam no
  `style.css`, nao no tema do highlight.js.
- **Escuta apenas em `127.0.0.1`** e recusa requisicao cujo `Host` nao seja localhost.
  O servidor executa comandos da maquina, entao nada de expor na rede.
- **Esperar de verdade.** Um servico so entra na fila do proximo quando fica *pronto*
  (porta atendendo, `pg_isready` respondendo), nao quando o processo nasce.
- **Nao reinicia o que ja esta no ar.** Containers sobem com `--no-recreate`; app que ja
  esta atendendo na porta e marcada como "ja estava no ar".

## Git

Cada projeto do tipo `app` mostra no cartao uma linha com o estado do repositorio:

```
⎇ main ↓1                          1 commit atras do origin/main
⎇ feat/polaroid sem upstream 20 mexido(s) +1486 -111
⎇ main em dia
```

- **⎇ branch** - o branch atual (ou `detached`);
- **↓N / ↑N** - commits que faltam aqui / commits locais que o remoto nao tem;
- **sem upstream** - branch local que nunca foi publicado;
- **N mexido(s)** - soma de indice, copia de trabalho e nao rastreados;
- **+N -N** - linhas do `git diff --shortstat HEAD`;
- **em dia** - limpo e sincronizado.

O tooltip abre o detalhe: upstream, contagem por categoria e o ultimo commit (hash, resumo,
autor e quando).

**`git fetch`** na barra de perfis roda `git fetch --prune` em todos os repositorios (ou em
um so, pelo menu `⋯` do cartao). Sem fetch, o `↓` so reflete o que o repositorio local ja
sabia do remoto - por isso o botao existe em vez de um fetch automatico, que mexeria na
rede sem pedir.

**Trocar de branch:** clicar no `⎇ branch` do cartao (ou **trocar de branch** no menu `⋯`)
abre a lista de branches do repositorio, com filtro - Enter troca quando sobra um so. Os
locais vem primeiro; os que so existem no remoto aparecem marcados (`so em origin`), e
trocar para um deles cria o local ja acompanhando (`git switch --track`). **So troca com a
copia de trabalho limpa**: com arquivo mexido, no indice, nao rastreado ou em conflito o
launcher recusa e pede commit ou stash - nada de carregar alteracao para outro branch sem
querer. A checagem e feita lendo o git na hora, nao pelo retrato em cache do cartao, e o
nome so vira argumento do `git switch` se estiver na lista do proprio repositorio.

### Painel git

Clicar no resumo do git no cartao (`↓1 3 mexido(s) +10 -2 git ›`), ou em **painel git** no
menu `⋯`, abre um painel grande com o repositorio do projeto. No topo ficam o branch, o
upstream, `↓/↑` e os botoes **fetch**, **pull** (`--ff-only`: so avanca, nunca cria merge),
**pull c/ rebase** e **push** (sem upstream, publica o branch no `origin` ja acompanhando;
nunca forca). Abas:

- **alteracoes** - arquivos preparados e nao preparados, com a letra do git (`M`, `A`, `D`,
  `R`; `+` para arquivo novo, `!` para conflito). Clicar mostra o diff com numero de linha;
  `+`/`−` preparam ou tiram da preparacao, `↶` descarta. Embaixo, a mensagem e os botoes
  **commit** e **commit e push** (`ctrl+enter` faz o commit). A mensagem fica guardada se o
  painel for fechado sem commitar.
  O diff (aqui e no historico) sai com **cor de sintaxe** pela extensao do arquivo (Go,
  JS/TS, Vue/HTML, CSS, Python, JSON, YAML, SQL, Bash, PowerShell, Dockerfile, C#, Java...).
  Cada lado de um trecho e colorido inteiro, entao comentario e string de varias linhas
  saem certos; extensao desconhecida fica sem cor.
- **historico** - os ultimos 80 commits do branch atual, de outro branch ou de todos, com
  refs, merges e o que ainda **nao foi enviado**; clicar mostra o commit inteiro (`git show`).
- **branches** - locais (mais recentes primeiro) e os que so existem no remoto, com ultimo
  commit, `↑/↓` e upstream apagado. Criar branch (leva as alteracoes junto), trocar, ver o
  historico, **merge no atual** e apagar.
- **stash** - guardar tudo que esta mexido (inclusive arquivos novos), aplicar, aplicar e
  remover, apagar.
- **pull requests** - PRs abertos no GitHub, pelo GitHub CLI (`gh`, precisa estar instalado e
  com `gh auth login`): branch → base, autor, checks, revisao, conflito e `+/-`; o PR do
  branch atual vem marcado, e sem PR aparece o atalho para abrir um. Clicar abre no GitHub.

Se um merge ou rebase parar com conflito, o painel avisa e oferece **continuar** (depois de
resolver e preparar os arquivos) ou **abortar**. O painel se rele sozinho quando o repositorio
muda por fora (commit no editor, arquivo salvo).

**Tudo que pode perder trabalho pede confirmacao num modal:** descartar alteracoes (arquivo
fora do git e apagado), pull com rebase, merge, apagar branch (e uma segunda confirmacao se o
branch tiver commits que nao estao em nenhum outro lugar), apagar stash e abortar
merge/rebase. Nada vindo do navegador vira argumento solto do git: caminho de arquivo tem que
estar na lista que o proprio `git status` devolveu, branch tem que estar na lista do
repositorio, hash tem que ter cara de hash, e todo caminho vai depois de `--`.

Sao tres comandos por repositorio (`status --porcelain=v2`, `diff --shortstat` e `log -1`),
entao o resultado fica em cache por 20s e as pastas sao consultadas em paralelo; projetos
que apontam para a mesma pasta compartilham uma consulta. A varredura automatica anda num
ritmo bem mais lento que a de status dos servicos.

## Docker

O painel **docker** na lateral mostra o estado do engine, a versao e a lista de containers:
primeiro os do config (nome, portas, status, e "ausente" para o que nunca foi criado),
depois, sob o separador *fora do config*, os que estao **rodando** na maquina sem pertencer
a nenhum projeto do launcher. Container parado de fora do config nao aparece - senao a
lista viraria cemiterio de container velho.

Cada container tem, ao passar o mouse: **▶ iniciar**, **↻ reiniciar**, **■ parar** e
**log** (ultimas 300 linhas no painel de log, com botao de atualizar). As acoes aceitas sao
so start/stop/restart, e o nome passa por validacao antes de virar argumento do docker.

O seletor ao lado do nome e a **restart policy** (`docker update --restart=...`): `no`,
`always`, `unless-stopped` e `on-failure`. E o que explica - e resolve - container voltando
sozinho toda vez que o Docker Desktop abre.

Com o engine parado, o painel fica vermelho e oferece **abrir Docker Desktop**: o launcher
acha o executavel (`%ProgramFiles%\Docker\Docker\Docker Desktop.exe` e os outros caminhos
de instalacao), abre e espera o engine responder, mostrando o andamento na tela.

Na subida isso e automatico: **se a selecao tiver algum projeto docker e o engine estiver
parado, o Docker Desktop e aberto e a subida espera ele ficar de pe** (ate 3 minutos, e o
que uma maquina fria com WSL2 costuma levar). Quem sobe so front-end nao paga essa espera.
Os projetos docker da mesma onda compartilham uma unica abertura.

## Acoes

**Por projeto:** `log` e uma acao principal que muda com o estado - `▶ subir` quando esta
parado ou com erro, `■ cancelar` enquanto sobe, `■ parar` (e `↻` reiniciar) quando esta no
ar. O menu `⋯` tem editar, reiniciar, forcar parada (derruba o que estiver na porta mesmo
com o cartao marcando parado), abrir a pasta no Explorer, abrir no VS Code e copiar o
comando.

**Por grupo:** o cabecalho de cada secao tem `+ projeto`, `subir`, `parar`, `reiniciar`,
`renomear`, `↑ ↓` e `apagar` - entao da para derrubar uma empresa inteira sem mexer no
resto. Clicar no nome recolhe o grupo (o navegador lembra), e o contador ao lado diz
quantos estao no ar.

**No topo:** parar / reiniciar / subir o que estiver marcado.

**Subidas ao mesmo tempo:** da para mandar subir um projeto com outro ainda subindo - a
trava e por projeto, nao global. Se o que foi pedido depende de algo que ja esta subindo,
ele espera aquela subida em vez de iniciar o mesmo processo duas vezes. `cancelar` (ou
`parar`) no meio da subida interrompe a espera na hora, em vez de segurar ate o timeout.

## Lendo a tela

- **Selo de estado** ao lado do nome: `parado`, `na fila`/`esperando <quem>`,
  `subindo · 12s`, `no ar`, `erro`, `bloqueado`. Em erro ou bloqueio o motivo aparece na
  linha de baixo, com **ver log**.
- **Pavio:** enquanto o projeto sobe, a barra na base do cartao mostra quanto do timeout ja
  foi gasto; fica ambar no ultimo quarto.
- **Filtro:** a caixa na barra de perfis (atalho `/`, `Esc` limpa) filtra por nome, pasta ou
  porta. Os contadores do topo (`no ar`, `subindo`, `falha`) tambem sao filtros: clicar em
  `falha` deixa na tela so o que quebrou.
- **Log:** ocupa a altura que sobrar da lateral; `▶ subir` num cartao ja abre o log dele.
  Linhas com erro saem em vermelho, avisos em ambar. Docker e plano de subida recolhem
  pelo titulo.
- **Avisos:** erro fica 12s na tela, o resto 5s, e todos fecham no `×`.

## Dois projetos na mesma porta

O front de uma empresa e o de outra, os dois na 8080: a checagem de "pronto" so olha a
porta, entao com um no ar os dois acendiam. Agora, quando a porta e dividida, o launcher
descobre **de quem e o processo que esta escutando** e so esse fica `no ar`. O outro
continua `parado`, com a etiqueta **porta 8080 em uso por <projeto>**.

As pistas, da mais segura para a menos:

1. o processo que escuta descende de um que o launcher segura (modo gerenciado);
2. a linha de comando de quem escuta, ou de um ancestral, cita a pasta do projeto - e o que
   resolve node/vue-cli/vite mesmo quando o projeto subiu por fora do launcher;
3. quem escuta e o Docker, e so um dos projetos da porta e container;
4. o processo nasceu logo depois de o launcher mandar subir um deles - o que sobra para
   `go run` em aba do terminal, que nao deixa pasta nenhuma na linha de comando.

Sem pista nenhuma, os dois aparecem `no ar` com a etiqueta **porta dividida com ...**: na
duvida vale a porta, mas a tela diz que e duvida.

Consequencias praticas: `subir` um projeto com a porta ocupada pelo vizinho falha na hora
dizendo quem esta nela (antes virava "ja estava no ar"), e `parar` / `forcar parada` num
projeto nao derruba mais o vizinho que divide a porta com ele. A tabela de processos so e
lida quando muda quem esta escutando; projeto que nao divide porta nao paga nada.

## Grupos

As secoes da tela sao editaveis: crie um grupo por empresa ou produto, renomeie, reordene com as setas e **arraste o cartao** de um grupo para
outro - soltando em cima de outro projeto ele entra na frente dele, soltando no cabecalho
vai para o fim da secao. Grupo so pode ser apagado quando esta vazio - apagar em cascata
levaria projeto junto sem querer.

## Perfis de execucao

Um perfil e uma visao da tela: o conjunto de projetos que voce usa junto num contexto
("loja completa", "so as APIs", "front + backend").

- **TODOS** (fixo, primeiro da barra) mostra todos os projetos cadastrados e nao mexe na
  selecao.
- **Clicar num perfil** mostra **so os projetos dele** - cartoes, grupos e contadores - e ja
  deixa esses projetos marcados para **subir selecionados**. Grupo sem projeto do perfil
  some da tela.
- **+ perfil** abre o editor: nome e a lista de projetos por grupo, com caixa para marcar o
  grupo inteiro e o atalho **usar a selecao atual**. O `⋯` de um perfil abre o mesmo editor
  para renomear, trocar os projetos ou apagar (apagar pede confirmacao e nao mexe nos
  projetos).
- Projeto adicionado com um perfil aberto entra nele tambem (na importacao da para
  desmarcar), senao sumiria da tela logo depois de cadastrado.

## Adicionar projetos a partir de uma pasta

**+ adicionar projetos** (ou **+ projeto** no cabecalho de um grupo) abre o assistente:

1. Navegue ate a pasta de um projeto - ou ate a pasta de cima, para pegar varios de uma vez
   (front, back, docker-compose de um mesmo sistema, ou todos os repositorios de uma
   empresa) - e clique em **procurar projetos aqui** (ou **procurar aqui** na linha de uma
   subpasta). Para uma pasta fora da pasta de projetos, marque **navegar fora da pasta de
   projetos**.
2. O launcher varre a pasta e as subpastas (ate 4 niveis, pulando `node_modules`, `vendor`,
   `target`, `dist`, `bin`...) e, para cada sistema que reconhece, propoe nome, comando e
   porta. **Nada e executado**: e tudo leitura de arquivo.

   | o que tem na pasta | comando sugerido | de onde vem a porta |
   |---|---|---|
   | `package.json` | `npm/yarn/pnpm/bun` + `dev`, `serve`, `start`... (pelo lockfile) | `--port` do script, `vite.config`, `angular.json`, `.env`, padrao do framework |
   | `go.mod` | `go run .` e um `go run ./cmd/x` por programa; `air` com `.air.toml` | `.env` ou `":8080"` no codigo |
   | `pom.xml` / `build.gradle` | `spring-boot:run` / `bootRun` (com `mvnw`/`gradlew` quando houver), `quarkus:dev` | `server.port` do `application*.properties/yml`, ou 8080 |
   | `*.csproj` (web/worker/exe) | `dotnet run`, `dotnet watch` | `Properties/launchSettings.json` (prefere a url http) |
   | `manage.py`, FastAPI, Flask, Streamlit | `runserver`, `uvicorn`, `flask run`... (usa o `.venv` se existir) | `.env` ou o padrao |
   | `artisan`, `bin/rails`, `Cargo.toml` | `php artisan serve`, `rails server`, `cargo run` | `.env` ou o padrao |
   | `run*.ps1`, `start*.bat`... | o proprio script - passa na frente dos outros comandos | herda a do projeto |
   | `docker-compose*.yml` / `compose*.yml` | um item por servico | primeira porta publicada (`ports`, inclusive `${VAR:-5432}` e sintaxe longa) |

   Biblioteca (Go sem `main`, `.csproj` de classes, `pom.xml` pai de multi-modulo) nao entra.
   O que ja esta cadastrado aparece desmarcado com **ja cadastrado**.
3. Confira a lista: nome, comando (o campo oferece as outras sugestoes) e porta sao
   editaveis, e a tela avisa porta ja usada por outro projeto. Escolha o grupo - com varios
   sistemas, o padrao e um **grupo novo** com o nome da pasta - e, se houver apps e containers
   juntos, deixe **os apps esperam os containers subirem** para eles so arrancarem com o
   banco/fila no ar. **adicionar N projeto(s)** cadastra todos de uma vez: se um falhar,
   nenhum entra e o erro diz qual foi.

Sem porta, o app fica pronto enquanto o processo estiver de pe (checagem **processo
rodando**); container sem porta publicada fica pronto quando esta rodando (`docker top`).

### O que a varredura deixa de fora ou desmarcado

- **Fora:** pastas de deploy (`deploy`, `k8s`, `helm`, `terraform`...), `android`/`ios`,
  exemplos e fixtures, e Firebase Functions.
- **Desmarcado, com o motivo na linha:** worktree do git (copia de outro repositorio) e
  servico do compose que compila o codigo do projeto, que nao publica porta (migrate, seed,
  worker) ou que repete o app da pasta.
- O mesmo container em varios compose aparece uma vez so.

### Subprojetos

O que fica **dentro da pasta de um projeto** - o docker-compose dele (banco, cache), uma
subpasta com outro sistema - nao entra solto: na lista de importacao, a linha do projeto
mostra **N subprojeto(s)/servico(s) que ele pode subir** (clique para ver quais). Pastas de
monorepo (`apps/`, `packages/`, `services/`, `backend/`, `frontend/`...) continuam virando
projetos proprios.

Para usar um subprojeto, abra **editar projeto**: a secao **subprojetos** ja vem com a lista
da pasta. Marcar ativa na hora (e desmarcar remove); **o projeto espera por ele** faz o
principal so subir depois do subprojeto - vem sugerido para banco/cache do compose. Ativado,
o subprojeto aparece logo abaixo do principal, recuado, com subir, parar e log proprios;
acompanha o principal de grupo e de perfil, nao se arrasta sozinho e sai junto quando o
principal e apagado.

**cadastro manual** (no rodape do assistente) abre o formulario completo, para o que a
descoberta nao cobre. O mesmo formulario edita um projeto ja cadastrado (`⋯` > editar
projeto); o **procurar** dele usa o mesmo detector para sugerir o comando. Porta repetida
nao bloqueia o cadastro, so avisa: os dois nao sobem juntos, e a tela diz qual deles esta
com a porta (veja "Dois projetos na mesma porta").

## Modos de execucao (servicos `app`)

| Modo | O que faz |
| --- | --- |
| `gerenciado` | O launcher segura o processo. Log ao vivo na tela, botao **parar** funciona direto. Encerrar o launcher derruba estes junto. |
| `terminal` | Abre uma aba do Windows Terminal (`wt -w dev-launcher`), todas na mesma janela. O log fica na aba; parar usa a porta para achar e matar a arvore de processos. |

## config.json

Tudo que a tela edita vai para este arquivo, que e local (nao vai para o Git). Se ele nao
existir, o launcher cria um vazio - sem projetos, grupos, perfis nem rede docker. Campo a
campo (os valores abaixo sao so exemplo):

```jsonc
{
  "porta_ui": 7010,
  "rede_docker": "minha-rede",          // opcional: criada se nao existir (para compose com rede external)
  "raiz_projetos": "..",                // pasta base; relativa ao proprio config.json
  "raiz_navegacao": "..",               // seletor de pastas; vazio = a raiz
  "grupos": [{ "id": "infra", "nome": "infraestrutura" }],
  "perfis": [{ "id": "so-apis", "nome": "so as APIs", "servicos": ["db", "backend"] }],
  "perfil_ativo": "so-apis",
  "servicos": [ /* ... */ ]
}
```

```jsonc
{
  "id": "backend",
  "nome": "meuapp-backend",
  "grupo": "api",                  // id de um grupo
  "tipo": "app",                   // app | docker
  "dir": "meuapp/meuapp-backend",  // relativo a raiz, ou absoluto
  "cmd": "& './run-local.ps1'",    // roda no pwsh, dentro de "dir"
  "porta": 7003,
  "url": "http://localhost:7003",  // vira o link "abrir"
  "pronto": { "tipo": "porta", "porta": 7003 },
  "timeout": 300,                  // segundos ate desistir de esperar
  "modo": "gerenciado",
  "depende": ["db"],
  "selecionado": true
}
```

Servico `docker` troca `dir`/`cmd` por:

```jsonc
"compose": {
  "projeto": "meuapp",                        // mantem o nome de projeto ja usado na maquina
  "arquivo": "meuapp/docker/docker-compose.yml",
  "servico": "db"
},
"container": "db"
```

O `projeto` importa: e ele que faz o compose reaproveitar os containers existentes
(ex.: `meuapp-db-1`) em vez de criar
duplicatas com outro nome.

Checagens de `pronto`:

| Tipo | Campos | Quando usar |
| --- | --- | --- |
| `porta` | `porta` | padrao - TCP em `127.0.0.1` |
| `comando` | `cmd` (lista) | quando a porta abre antes do servico servir (ex.: `docker exec db pg_isready -U postgres -q`) |
| `http` | `url` | health check HTTP; qualquer resposta < 500 conta |
| `processo` | - | projeto `app` que nao abre porta (bot, worker); so no modo `gerenciado`, porque na aba do terminal o launcher nao enxerga o processo |

Dependencia circular e recusada na hora de salvar, com o caminho do ciclo na mensagem.
Config da primeira versao (sem `grupos`/`perfis`) e migrado sozinho ao abrir.

## API

| Metodo | Rota | Para que |
| --- | --- | --- |
| GET | `/api/estado` | config + estado de cada servico |
| GET | `/api/eventos` | SSE: estado, log, config, fim de subida |
| POST | `/api/subir` · `/api/parar` · `/api/plano` | `{"ids": [...]}` |
| POST | `/api/config` | selecao/modo/dependencias (o que muda a cada clique) |
| POST | `/api/reiniciar` | `{"ids": [...]}` - para e sobe de novo |
| POST/DELETE | `/api/servicos` · `/api/servicos/{id}` | cadastro completo |
| POST | `/api/servicos/{id}/mover` | `{"grupo": "...", "antes": "id ou vazio"}` (arrastar e soltar) |
| POST | `/api/abrir` | `{"id": "...", "alvo": "pasta\|editor"}` |
| GET | `/api/git` | estado do repositorio por projeto (`?forcar=1` ignora o cache) |
| POST | `/api/git/buscar` | `{"ids": [...]}` (vazio = todos) - `git fetch --prune` |
| GET | `/api/servicos/{id}/branches` | branch atual, se esta limpo e a lista de branches |
| POST | `/api/servicos/{id}/branch` | `{"branch": "..."}` - `git switch`, so com a copia de trabalho limpa |
| GET | `/api/servicos/{id}/git` | painel: estado, arquivos alterados, stashes, merge/rebase em andamento |
| GET | `/api/servicos/{id}/git/diff?caminho=...&preparado=1` | diff de um arquivo da lista |
| GET | `/api/servicos/{id}/git/historico?ref=...` | ultimos commits (`ref` vazio = branch atual, `*todos` = todos) |
| GET | `/api/servicos/{id}/git/commits/{hash}` | `git show` do commit |
| GET | `/api/servicos/{id}/git/branches` | branches com ultimo commit e `↑/↓` |
| GET | `/api/servicos/{id}/git/prs` | PRs abertos (GitHub CLI) |
| POST | `/api/servicos/{id}/git/{acao}` | `preparar`, `despreparar`, `descartar`, `commit`, `fetch`, `pull`, `push`, `stash`, `stash-aplicar`, `stash-pop`, `stash-apagar`, `criar-branch`, `apagar-branch`, `merge`, `abortar`, `continuar` |
| GET | `/api/docker` | estado do engine + containers (do config e os de fora rodando) |
| POST | `/api/docker/abrir` | abre o Docker Desktop e espera o engine (responde na hora) |
| POST | `/api/docker/containers/{nome}/{start\|stop\|restart}` | acao no container |
| POST | `/api/docker/containers/{nome}/politica` | `{"politica": "no\|always\|unless-stopped\|on-failure"}` |
| GET | `/api/docker/containers/{nome}/logs?linhas=300` | ultimas linhas do container |
| POST | `/api/encerrar` | encerra o proprio launcher |
| POST/DELETE | `/api/grupos` · `/api/grupos/ordem` · `/api/grupos/{id}` | grupos |
| POST/DELETE | `/api/perfis` · `/api/perfis/{id}/aplicar` · `/api/perfis/{id}` | perfis (`todos/aplicar` volta para o TODOS) |
| GET | `/api/descobrir?caminho=...` | projetos encontrados na pasta e subpastas, com comando e porta sugeridos |
| GET/POST | `/api/servicos/{id}/subprojetos` | lista o que da para subir de dentro da pasta do projeto / ativa um (`{"entrada", "esperar"}`) |
| POST | `/api/importar` | `{"grupo", "grupo_novo", "perfil", "esperar_containers", "projetos": [...]}` - cadastra todos ou nenhum |
| GET | `/api/pastas` · `/api/inspecionar` | seletor de pastas e sugestoes (`?livre=1` sai da cerca) |

## Testes

```powershell
go test ./...
go test -race ./...
```

Os testes usam um executor de mentira (`execFake`) no lugar do docker/pwsh, entao a ordem
de subida, a espera por dependencia e os casos de falha sao verificados sem levantar nada.
As regras de cadastro (cerca de caminho, id gerado, remocao limpando dependencias e
perfis), o parser de `docker-compose` e as sugestoes de comando tem teste proprio.
