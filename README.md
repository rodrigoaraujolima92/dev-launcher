# dev-launcher

Interface web para subir o ambiente local: marca quais projetos sobem, define quem espera
quem, agrupa por empresa/produto, salva perfis de execucao e acompanha o log de cada um.

```powershell
./start-ui.ps1            # compila se precisar, sobe e abre o navegador
./start-ui.ps1 -Porta 7011
./start-ui.ps1 -Encerrar  # encerra o launcher que estiver rodando
./start-ui.ps1 -Raiz D:\projetos
```

Sem interface, so as abas do Windows Terminal: `./start-dev.ps1` (caminho antigo, especifico
do appdaturma; quem manda hoje e o launcher).

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

E por isso que o config daqui usa `"raiz_projetos": ".."`: com o launcher morando dentro da
pasta que guarda os projetos, o mesmo arquivo funciona em qualquer maquina, sem
`C:\Users\fulano` no meio. Na mesma linha, caminho de projeto **dentro** da raiz e gravado
relativo (`appdaturma/appdaturma-backend`); so o que esta fora fica absoluto.

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

## Grupos

As secoes da tela sao editaveis: crie um grupo por empresa ou produto (appdaturma,
photonow, isugar), renomeie, reordene com as setas e **arraste o cartao** de um grupo para
outro - soltando em cima de outro projeto ele entra na frente dele, soltando no cabecalho
vai para o fim da secao. Grupo so pode ser apagado quando esta vazio - apagar em cascata
levaria projeto junto sem querer.

## Perfis de execucao

Um perfil e uma stack salva: marque os projetos que quer naquele contexto e clique em
**salvar selecao como perfil** ("stack completa", "so as APIs", "front + backend").
Clicar no perfil marca exatamente aqueles projetos e desmarca o resto; o `⋯` do chip
renomeia, apaga ou **grava a selecao atual** por cima do perfil.

## Cadastrar um projeto pela tela

**+ novo projeto** (ou **+ projeto** no cabecalho de um grupo) abre o formulario:

1. **procurar** navega a partir de `C:\Users\Pichau\projetos` e mostra etiquetas
   (`GIT`, `NODE`, `GO`, `MAVEN`, `COMPOSE`, `SCRIPT`) para voce reconhecer o projeto.
   Para uma pasta fora dessa raiz, marque **usar caminho fora da pasta de projetos** - a
   cerca e o padrao porque este campo vira diretorio de execucao de um comando.
2. Escolhida a pasta, o launcher sugere como rodar: scripts do `package.json` (com a porta
   lida do `--port` do proprio script), `go run ./cmd/api`, `air`, `mvn spring-boot:run` e
   qualquer `.ps1` na raiz do projeto - e para `docker-compose*.yml` lista os servicos de
   dentro do arquivo.
3. Porta, checagem de "pronto", timeout e dependencias completam o cadastro.

O launcher so adivinha a porta quando ela esta no script (`--port`, vue-cli, vite). Para o
resto - `go run`, Nest, Maven - informe a porta na mao. Projeto que **nao abre porta** (bot,
worker, script) usa a checagem **processo rodando**: fica pronto enquanto o processo que o
launcher iniciou estiver de pe. Se faltar algo, o motivo aparece no proprio formulario,
acima dos botoes.

Porta repetida nao bloqueia o cadastro, so avisa: como a checagem de "pronto" olha a porta,
dois projetos na mesma porta se confundem.

## Modos de execucao (servicos `app`)

| Modo | O que faz |
| --- | --- |
| `gerenciado` | O launcher segura o processo. Log ao vivo na tela, botao **parar** funciona direto. Encerrar o launcher derruba estes junto. |
| `terminal` | Abre uma aba do Windows Terminal (`wt -w appdaturma`), igual ao `start-dev.ps1`. O log fica na aba; parar usa a porta para achar e matar a arvore de processos. |

## config.json

Tudo que a tela edita vai para este arquivo. Campo a campo:

```jsonc
{
  "porta_ui": 7010,
  "rede_docker": "PRODUCTION",          // criada se nao existir (os compose usam como external)
  "raiz_projetos": "..",                // pasta base; relativa ao proprio config.json
  "raiz_navegacao": "..",               // seletor de pastas; vazio = a raiz
  "grupos": [{ "id": "infra", "nome": "infraestrutura" }],
  "perfis": [{ "id": "so-apis", "nome": "so as APIs", "servicos": ["db", "permissions"] }],
  "perfil_ativo": "so-apis",
  "servicos": [ /* ... */ ]
}
```

```jsonc
{
  "id": "backend",
  "nome": "appdaturma-backend",
  "grupo": "api",                  // id de um grupo
  "tipo": "app",                   // app | docker
  "dir": "appdaturma/appdaturma-backend",  // relativo a raiz, ou absoluto
  "cmd": "& './run-local.ps1'",    // roda no pwsh, dentro de "dir"
  "porta": 7003,
  "url": "http://localhost:7003",  // vira o link "abrir"
  "pronto": { "tipo": "porta", "porta": 7003 },
  "timeout": 300,                  // segundos ate desistir de esperar
  "modo": "gerenciado",
  "depende": ["db", "rabbitmq", "mailhog", "permissions"],
  "selecionado": true
}
```

Servico `docker` troca `dir`/`cmd` por:

```jsonc
"compose": {
  "projeto": "dockerappdaturma",              // mantem o nome de projeto ja usado na maquina
  "arquivo": "appdaturma/docker appdaturma/docker-compose.yml",
  "servico": "db"
},
"container": "db"
```

O `projeto` importa: e ele que faz o compose reaproveitar os containers existentes
(`db`, `rabbitmq`, `dockerappdaturma-adminer-1`, `projetos-mailhog-1`) em vez de criar
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
| GET | `/api/docker` | estado do engine + containers (do config e os de fora rodando) |
| POST | `/api/docker/abrir` | abre o Docker Desktop e espera o engine (responde na hora) |
| POST | `/api/docker/containers/{nome}/{start\|stop\|restart}` | acao no container |
| POST | `/api/docker/containers/{nome}/politica` | `{"politica": "no\|always\|unless-stopped\|on-failure"}` |
| GET | `/api/docker/containers/{nome}/logs?linhas=300` | ultimas linhas do container |
| POST | `/api/encerrar` | encerra o proprio launcher |
| POST/DELETE | `/api/grupos` · `/api/grupos/ordem` · `/api/grupos/{id}` | grupos |
| POST/DELETE | `/api/perfis` · `/api/perfis/{id}/aplicar` · `/api/perfis/{id}` | perfis |
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
