# dev-launcher

Interface web para subir o ambiente local: marca quais projetos sobem, define quem espera
quem, agrupa por empresa/produto, salva perfis de execucao e acompanha o log de cada um.

```powershell
../start-ui.ps1          # compila se precisar, sobe e abre o navegador
../start-ui.ps1 -Porta 7011
```

Sem interface, so as abas do Windows Terminal: `../start-dev.ps1`.

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

## Acoes

**Por projeto:** `log`, `subir`, `↻` (reiniciar: para e sobe de novo), `parar` e o menu `⋯`
com editar, abrir a pasta no Explorer, abrir no VS Code e copiar o comando.

**Por grupo:** o cabecalho de cada secao tem `+ projeto`, `subir`, `parar`, `reiniciar`,
`renomear`, `↑ ↓` e `apagar` - entao da para derrubar uma empresa inteira sem mexer no
resto.

**No topo:** parar / reiniciar / subir o que estiver marcado.

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

Porta repetida nao bloqueia o cadastro, so avisa: como a checagem de "pronto" olha a porta,
dois projetos na mesma porta se confundem.

## Modos de execucao (servicos `app`)

| Modo | O que faz |
| --- | --- |
| `gerenciado` | O launcher segura o processo. Log ao vivo na tela, botao **parar** funciona direto. Ctrl+C no launcher derruba tudo junto. |
| `terminal` | Abre uma aba do Windows Terminal (`wt -w appdaturma`), igual ao `start-dev.ps1`. O log fica na aba; parar usa a porta para achar e matar a arvore de processos. |

## config.json

Tudo que a tela edita vai para este arquivo. Campo a campo:

```jsonc
{
  "porta_ui": 7010,
  "rede_docker": "PRODUCTION",          // criada se nao existir (os compose usam como external)
  "raiz_navegacao": "C:\\Users\\Pichau\\projetos",  // onde o seletor de pastas comeca
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
  "dir": "appdaturma-backend",     // relativo a esta pasta/.., ou absoluto
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
  "arquivo": "docker appdaturma/docker-compose.yml",
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
