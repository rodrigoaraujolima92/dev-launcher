# dev-launcher

Interface web para subir o ambiente local do AppDaTurma: marca quais projetos sobem,
define quem espera quem e acompanha o log de cada um.

```powershell
../start-ui.ps1          # compila se precisar, sobe e abre o navegador
../start-ui.ps1 -Porta 7011
```

Sem interface, so as abas do Windows Terminal: `../start-dev.ps1`.

## Como funciona

- **Um binario so.** A interface (`web/`) vai embutida via `go:embed`; nao precisa de Node,
  npm nem dependencia externa - so a stdlib do Go.
- **Escuta apenas em `127.0.0.1`** e recusa requisicao cujo `Host` nao seja localhost.
  O servidor executa comandos da maquina, entao nada de expor na rede.
- **Esperar de verdade.** Um servico so entra na fila do proximo quando fica *pronto*
  (porta atendendo, `pg_isready` respondendo), nao quando o processo nasce.
- **Nao reinicia o que ja esta no ar.** Containers sobem com `--no-recreate`; app que ja
  esta atendendo na porta e marcada como "ja estava no ar".

## Modos de execucao (servicos `app`)

| Modo | O que faz |
| --- | --- |
| `gerenciado` | O launcher segura o processo. Log ao vivo na tela, botao **parar** funciona direto. Ctrl+C no launcher derruba tudo junto. |
| `terminal` | Abre uma aba do Windows Terminal (`wt -w appdaturma`), igual ao `start-dev.ps1`. O log fica na aba; parar usa a porta para achar e matar a arvore de processos. |

## config.json

A tela edita **selecao**, **modo** e **dependencias** - e grava no arquivo. Caminho, comando
e checagem de "pronto" so mudam editando o JSON na mao (de proposito: o navegador nao deve
poder trocar o comando que sera executado).

```jsonc
{
  "id": "backend",
  "nome": "appdaturma-backend",
  "grupo": "api",                  // so agrupa na tela
  "tipo": "app",                   // app | docker
  "dir": "appdaturma-backend",     // relativo a esta pasta/..
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

## Testes

```powershell
go test ./...
go test -race ./...
```

Os testes usam um executor de mentira (`execFake`) no lugar do docker/pwsh, entao a ordem
de subida, a espera por dependencia e os casos de falha sao verificados sem levantar nada.
