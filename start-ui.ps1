#Requires -Version 7
<#
.SYNOPSIS
  Abre a interface web do dev-launcher (selecao de projetos + ordem de subida).

.DESCRIPTION
  Compila o binario se ele nao existir ou estiver mais velho que o codigo, sobe o servidor
  em 127.0.0.1 e abre o navegador. Ctrl+C encerra - e derruba junto o que estiver rodando
  em modo gerenciado.

  Para a versao sem interface, use ./start-dev.ps1 (abas do Windows Terminal).

.EXAMPLE
  ./start-ui.ps1
  ./start-ui.ps1 -Porta 7011
  ./start-ui.ps1 -Recompilar
#>
[CmdletBinding()]
param(
    # Porta da interface (padrao: a do dev-launcher/config.json).
    [int]$Porta,

    # Recompila mesmo que o binario esteja atualizado.
    [switch]$Recompilar,

    # Nao abre o navegador sozinho.
    [switch]$SemNavegador,

    # Encerra o launcher que ja esta rodando e sai (serve quando ele subiu sem console).
    [switch]$Encerrar,

    # Pasta base dos projetos. Sem isto vale raiz_projetos do config (ou DEV_LAUNCHER_RAIZ).
    [string]$Raiz
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false

# O script mora dentro da propria pasta do launcher.
$pasta   = $PSScriptRoot
$binario = Join-Path $pasta 'dev-launcher.exe'

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Go nao encontrado no PATH - e ele que compila o launcher." -ForegroundColor Red
    exit 1
}

# Recompila quando qualquer fonte (.go ou arquivo da interface, que vai embutido no binario
# via go:embed) for mais nova que o executavel.
$precisaCompilar = $Recompilar -or -not (Test-Path $binario)
if (-not $precisaCompilar) {
    $maisNovo = Get-ChildItem $pasta -Recurse -File -Include *.go, *.html, *.css, *.js |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    $precisaCompilar = $maisNovo -and $maisNovo.LastWriteTime -gt (Get-Item $binario).LastWriteTime
}

if ($precisaCompilar) {
    Write-Host "  compilando o launcher..." -ForegroundColor DarkGray
    Push-Location $pasta
    try {
        go build -o dev-launcher.exe .
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  falha ao compilar." -ForegroundColor Red
            exit 1
        }
    } finally {
        Pop-Location
    }
}

# Nome proprio, nao $args: $args e variavel automatica do PowerShell.
$argumentos = @()
if ($Porta -gt 0) { $argumentos += @('-porta', "$Porta") }
if ($SemNavegador) { $argumentos += '-sem-navegador' }
if ($Encerrar) { $argumentos += '-encerrar' }
if ($Raiz) { $argumentos += @('-raiz', "$Raiz") }

& $binario @argumentos
