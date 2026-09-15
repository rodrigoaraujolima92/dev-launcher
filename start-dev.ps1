#Requires -Version 7
<#
.SYNOPSIS
  Sobe o ambiente local do AppDaTurma: containers Docker + um terminal por servico.

.DESCRIPTION
  Docker (reaproveita os containers que ja existem, mesmos nomes de projeto compose):
    db + adminer  ->  docker appdaturma/docker-compose.yml        (projeto dockerappdaturma)
    rabbitmq      ->  docker appdaturma/docker-compose-rabbit.yml (projeto dockerappdaturma)
    mailhog       ->  ../mailhog.yml                              (projeto projetos)

  Terminais (uma aba do Windows Terminal para cada, na janela "appdaturma"):
    backend      7003  mvn spring-boot:run via run-local.ps1
    permissions  7019  go run ./cmd/api
    contrato     8083  air (ou go run ./cmd/api se o air nao estiver instalado)
    frontend     8080  vue-cli-service serve
    admin        8081  vue-cli-service serve

.EXAMPLE
  ./start-dev.ps1
  ./start-dev.ps1 -SoDocker
  ./start-dev.ps1 -Apps backend,frontend
  ./start-dev.ps1 -SoTerminais -Apps admin
#>
[CmdletBinding()]
param(
    # Sobe so os containers, nao abre terminal nenhum.
    [switch]$SoDocker,

    # Pula o Docker e so abre os terminais.
    [switch]$SoTerminais,

    # Recria os containers mesmo que ja estejam no ar (use depois de mexer num compose).
    [switch]$Recriar,

    # Subconjunto de apps. Sem isso, sobe todos.
    [ValidateSet('backend', 'permissions', 'contrato', 'frontend', 'admin')]
    [string[]]$Apps
)

$ErrorActionPreference = 'Stop'
# Sem isso, no PowerShell 7.4+ qualquer exe que retorna codigo != 0 (docker exec / pg_isready
# enquanto o banco ainda esta subindo) vira excecao e mata o script no meio.
$PSNativeCommandUseErrorActionPreference = $false

# O script mora em projetos/dev-launcher: a raiz dos projetos e a pasta de cima.
# Este script e o caminho antigo, especifico do appdaturma - quem manda hoje e o
# dev-launcher (./start-ui.ps1), que le os caminhos do config.json.
$projetos  = Split-Path $PSScriptRoot -Parent
$raiz      = Join-Path $projetos 'appdaturma'
$dockerDir = Join-Path $raiz 'docker appdaturma'

# ------------------------------------------------------------------
# Catalogo
# ------------------------------------------------------------------
$stacks = @(
    @{ Nome = 'db + adminer'; Projeto = 'dockerappdaturma'; Arquivo = Join-Path $dockerDir 'docker-compose.yml' }
    @{ Nome = 'rabbitmq'    ; Projeto = 'dockerappdaturma'; Arquivo = Join-Path $dockerDir 'docker-compose-rabbit.yml' }
    @{ Nome = 'mailhog'     ; Projeto = 'projetos'        ; Arquivo = Join-Path $projetos 'mailhog.yml' }
)

# O comando de cada aba nao pode conter ';': o Windows Terminal usa ';' como separador de abas.
$catalogo = [ordered]@{
    backend     = @{ Titulo = 'backend 7003'    ; Dir = 'appdaturma-backend' ; Porta = 7003; Cmd = "& './run-local.ps1'" }
    permissions = @{ Titulo = 'permissions 7019'; Dir = 'permissions-api'    ; Porta = 7019; Cmd = 'go run ./cmd/api' }
    contrato    = @{ Titulo = 'contrato 8083'   ; Dir = 'contrato-api'       ; Porta = 8083; Cmd = 'if (Get-Command air -ErrorAction SilentlyContinue) { air } else { go run ./cmd/api }' }
    frontend    = @{ Titulo = 'frontend 8080'   ; Dir = 'appdaturma-frontend'; Porta = 8080; Cmd = 'npm run serve -- --port 8080' }
    admin       = @{ Titulo = 'admin 8081'      ; Dir = 'appdaturma-admin'   ; Porta = 8081; Cmd = 'npm run serve -- --port 8081' }
}

# ------------------------------------------------------------------
# Auxiliares
# ------------------------------------------------------------------
function Escrever($texto, $cor = 'Gray') { Write-Host $texto -ForegroundColor $cor }

function Porta-Ocupada([int]$porta) {
    [bool](Get-NetTCPConnection -LocalPort $porta -State Listen -ErrorAction SilentlyContinue)
}

function Esperar-Postgres([int]$timeoutSegundos = 90) {
    $fim = (Get-Date).AddSeconds($timeoutSegundos)
    while ((Get-Date) -lt $fim) {
        docker exec db pg_isready -U postgres -q 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { return $true }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Esperar-Porta([int]$porta, [int]$timeoutSegundos = 90) {
    $fim = (Get-Date).AddSeconds($timeoutSegundos)
    while ((Get-Date) -lt $fim) {
        $cliente = [System.Net.Sockets.TcpClient]::new()
        try {
            if ($cliente.ConnectAsync('127.0.0.1', $porta).Wait(1000)) { return $true }
        } catch { } finally { $cliente.Dispose() }
        Start-Sleep -Seconds 2
    }
    return $false
}

# ------------------------------------------------------------------
# Docker
# ------------------------------------------------------------------
if (-not $SoTerminais) {
    Escrever "`n== Docker ==" Cyan

    docker info 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Escrever "  Docker nao esta respondendo. Abra o Docker Desktop e rode de novo." Red
        exit 1
    }

    # Os compose de db/adminer/rabbit usam a rede PRODUCTION como external:
    # se ela nao existir, o 'up' falha antes de criar qualquer container.
    if (-not (docker network ls --format '{{.Name}}' | Where-Object { $_ -eq 'PRODUCTION' })) {
        Escrever "  criando rede PRODUCTION..." DarkGray
        docker network create PRODUCTION | Out-Null
    }

    foreach ($stack in $stacks) {
        if (-not (Test-Path $stack.Arquivo)) {
            Escrever "  [pulado] $($stack.Nome): $($stack.Arquivo) nao encontrado." Yellow
            continue
        }
        Escrever "  subindo $($stack.Nome)..." DarkGray
        # -p mantem o nome de projeto original -> reaproveita os containers existentes
        # (db, rabbitmq, dockerappdaturma-adminer-1, projetos-mailhog-1) em vez de duplicar.
        #
        # --no-recreate: o objetivo aqui e garantir que esteja no ar, nao aplicar mudanca de
        # config. Sem ele, o compose recria container que ja estava rodando ha horas so porque
        # o hash da config mudou - derruba banco e fila no meio do trabalho a toa. Mexeu num
        # docker-compose e quer valer agora? Rode com -Recriar.
        # @(...) por fora e obrigatorio: sem isso o if devolve uma string, e splatar string
        # ('@opcoes') manda um argumento por caractere - o docker responde "no such service: -".
        $opcoes = @(if ($Recriar) { '--force-recreate' } else { '--no-recreate' })
        docker compose -p $stack.Projeto -f $stack.Arquivo up -d @opcoes
        if ($LASTEXITCODE -ne 0) { Escrever "  FALHOU: $($stack.Nome)" Red }
    }

    Escrever "  aguardando postgres..." DarkGray
    if (Esperar-Postgres) { Escrever "  db pronto (5432)" Green }
    else { Escrever "  db nao respondeu no tempo esperado - veja 'docker logs db'" Yellow }

    Escrever "  aguardando rabbitmq..." DarkGray
    if (Esperar-Porta 5672) { Escrever "  rabbitmq pronto (5672)" Green }
    else { Escrever "  rabbitmq nao respondeu no tempo esperado - veja 'docker logs rabbitmq'" Yellow }
}

if ($SoDocker) {
    Escrever "`nContainers no ar. Terminais nao foram abertos (-SoDocker)." Cyan
    docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
    return
}

# ------------------------------------------------------------------
# Terminais
# ------------------------------------------------------------------
Escrever "`n== Terminais ==" Cyan

$escolhidos = if ($Apps) { $Apps } else { $catalogo.Keys }
$usaWt      = [bool](Get-Command wt -ErrorAction SilentlyContinue)
if (-not $usaWt) { Escrever "  Windows Terminal (wt) nao encontrado - abrindo janelas separadas do pwsh." Yellow }

foreach ($chave in $escolhidos) {
    $app = $catalogo[$chave]
    $dir = Join-Path $raiz $app.Dir

    if (-not (Test-Path $dir)) {
        Escrever "  [pulado] $chave : $dir nao encontrado." Yellow
        continue
    }
    if (Porta-Ocupada $app.Porta) {
        Escrever "  [pulado] $chave : porta $($app.Porta) ja esta em uso (ja esta rodando?)." Yellow
        continue
    }

    Escrever "  abrindo $($app.Titulo)" DarkGray
    if ($usaWt) {
        # -w appdaturma: todas as abas caem na mesma janela, criada na primeira chamada.
        wt -w appdaturma new-tab --title $app.Titulo -d $dir pwsh -NoExit -Command $app.Cmd
    } else {
        Start-Process pwsh -WorkingDirectory $dir -ArgumentList '-NoExit', '-Command', $app.Cmd
    }
    Start-Sleep -Milliseconds 700
}

Escrever "`n== Enderecos ==" Cyan
Escrever "  frontend     http://localhost:8080"
Escrever "  admin        http://localhost:8081"
Escrever "  backend      http://localhost:7003"
Escrever "  permissions  http://localhost:7019"
Escrever "  contrato     http://localhost:8083"
Escrever "  adminer      http://localhost:7002"
Escrever "  mailhog      http://localhost:8025"
Escrever "  rabbitmq     http://localhost:15672"
Escrever ""
