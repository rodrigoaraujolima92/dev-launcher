package main

import (
	"testing"
	"time"
)

// saidaProcessos imita o scriptProcessos numa maquina com o painel-front (vue-cli) no ar
// pelo launcher, e uma api em Go que alguem subiu numa aba de terminal.
const saidaProcessos = `4|0|0|
27752|33536|1790948000000|C:\Users\fulano\projetos\dev-launcher\dev-launcher.exe "C:\Users\fulano\projetos\dev-launcher\dev-launcher.exe"
38004|27752|1790948500000|C:\Program Files\PowerShell\7\pwsh.exe pwsh -NoLogo -NoProfile -Command "npm run serve"
30960|38004|1790948501000|C:\nvm4w\nodejs\node.exe "C:\nvm4w\nodejs\node.exe" C:\nvm4w\nodejs/node_modules/npm/bin/npm-cli.js run serve
43332|30960|1790948502000|C:\WINDOWS\system32\cmd.exe C:\WINDOWS\system32\cmd.exe /d /s /c vue-cli-service serve --port 8080 | tee saida.log
34396|43332|1790948503000|C:\nvm4w\nodejs\node.exe "node" "C:\Users\fulano\projetos\outroapp\painel-front\node_modules\.bin\\..\@vue\cli-service\bin\vue-cli-service.js" serve --port 8080
9100|1500|1790948700000|C:\Program Files\PowerShell\7\pwsh.exe pwsh -NoExit -Command "go run ./cmd/api"
9200|9100|1790948701000|C:\Program Files\Go\bin\go.exe go run ./cmd/api
9300|9200|1790948709000|C:\Users\fulano\AppData\Local\Temp\go-build42\b001\exe\api.exe C:\Users\fulano\AppData\Local\Temp\go-build42\b001\exe\api.exe
7700|900|1790940000000|C:\Program Files\Docker\Docker\resources\com.docker.backend.exe "com.docker.backend.exe" services
WARNING: isto nao e uma linha de processo
`

const launcher = 27752

func TestLerTabelaProcessos(t *testing.T) {
	procs := lerTabelaProcessos(saidaProcessos)

	if len(procs) != 10 {
		t.Fatalf("esperava 10 processos (a linha de aviso fica de fora), veio %d", len(procs))
	}
	node := procs[34396]
	if node.Pai != 43332 || node.Criado.UnixMilli() != 1790948503000 {
		t.Fatalf("node = %+v", node)
	}
	// A linha de comando pode ter "|": so os tres primeiros campos sao separadores.
	if cmd := procs[43332]; cmd.Linha[len(cmd.Linha)-len("| tee saida.log"):] != "| tee saida.log" {
		t.Fatalf("a linha de comando foi cortada no pipe: %q", cmd.Linha)
	}
	// Processo do sistema vem sem data: nao pode virar 1970.
	if !procs[4].Criado.IsZero() {
		t.Fatalf("processo sem data = %v", procs[4].Criado)
	}
}

func TestDonoDaPortaPeloProcessoQueOLauncherSegura(t *testing.T) {
	procs := lerTabelaProcessos(saidaProcessos)
	candidatos := []candidatoPorta{
		// As pastas estao de proposito trocadas: quando o launcher segura o processo, e
		// essa a pista que manda, nao a linha de comando.
		{ID: "frontend", Dir: `C:\Users\fulano\projetos\outroapp\painel-front`},
		{ID: "painel", Dir: `C:\Users\fulano\projetos\meuapp\meuapp-frontend`, PIDProprio: 38004},
	}
	if got := donoDaPorta([]int{34396}, candidatos, procs, launcher); got != "painel" {
		t.Fatalf("dono = %q, queria quem tem o processo gerenciado na arvore", got)
	}
}

func TestDonoDaPortaPelaPastaNaLinhaDeComando(t *testing.T) {
	procs := lerTabelaProcessos(saidaProcessos)
	candidatos := []candidatoPorta{
		{ID: "frontend", Dir: `C:\Users\fulano\projetos\meuapp\meuapp-frontend`},
		// Barra normal e caixa diferente: e a mesma pasta no Windows.
		{ID: "painel", Dir: `c:/users/fulano/projetos/outroapp/painel-front`},
	}
	if got := donoDaPorta([]int{34396}, candidatos, procs, launcher); got != "painel" {
		t.Fatalf("dono = %q, queria o projeto cuja pasta aparece no comando do node", got)
	}

	// Prefixo nao conta: "painel" nao e a pasta "painel-front".
	soPrefixo := []candidatoPorta{{ID: "errado", Dir: `C:\Users\fulano\projetos\outroapp\painel`}}
	if got := donoDaPorta([]int{34396}, soPrefixo, procs, launcher); got != "" {
		t.Fatalf("prefixo de pasta virou dono: %q", got)
	}

	// A pasta do proprio launcher nao e pista: a subida da arvore para nele.
	doLauncher := []candidatoPorta{{ID: "launcher", Dir: `C:\Users\fulano\projetos\dev-launcher`}}
	if got := donoDaPorta([]int{34396}, doLauncher, procs, launcher); got != "" {
		t.Fatalf("a linha de comando do launcher contou como pista: %q", got)
	}
}

func TestDonoDaPortaPeloHorarioDaSubida(t *testing.T) {
	procs := lerTabelaProcessos(saidaProcessos)
	nascido := procs[9300].Criado // o api.exe do "go run": nenhuma pasta na linha de comando

	candidatos := []candidatoPorta{
		{ID: "antigo", Dir: `C:\projetos\a`, Iniciado: nascido.Add(-time.Hour), Prazo: 2 * time.Minute},
		{ID: "recente", Dir: `C:\projetos\b`, Iniciado: nascido.Add(-9 * time.Second), Prazo: 2 * time.Minute},
	}
	if got := donoDaPorta([]int{9300}, candidatos, procs, launcher); got != "recente" {
		t.Fatalf("dono = %q, queria quem o launcher mandou subir logo antes de o processo nascer", got)
	}

	// Processo que nasceu fora da janela de qualquer subida: nao da para afirmar nada.
	candidatos[1].Iniciado = nascido.Add(-time.Hour)
	if got := donoDaPorta([]int{9300}, candidatos, procs, launcher); got != "" {
		t.Fatalf("sem pista nenhuma deveria ficar sem dono, veio %q", got)
	}
}

func TestDonoDaPortaDockerSoComUmContainerCandidato(t *testing.T) {
	procs := lerTabelaProcessos(saidaProcessos)

	misto := []candidatoPorta{{ID: "db", Docker: true}, {ID: "api", Dir: `C:\projetos\api`}}
	if got := donoDaPorta([]int{7700}, misto, procs, launcher); got != "db" {
		t.Fatalf("quem escuta e o docker e so um candidato e container: veio %q", got)
	}
	doisContainers := []candidatoPorta{{ID: "db", Docker: true}, {ID: "db2", Docker: true}}
	if got := donoDaPorta([]int{7700}, doisContainers, procs, launcher); got != "" {
		t.Fatalf("com dois containers candidatos nao da para escolher, veio %q", got)
	}
}

func TestCadeiaDeProcessosIgnoraPaiQueReaproveitouOPID(t *testing.T) {
	agora := time.Now()
	procs := map[int]Processo{
		10: {PID: 10, Pai: 20, Criado: agora},
		// O "pai" nasceu depois do filho: e outro processo que herdou o numero 20.
		20: {PID: 20, Pai: 30, Criado: agora.Add(time.Minute), Linha: `C:\projetos\a\server.exe`},
	}
	if cadeia := cadeiaDeProcessos(10, procs, 0); len(cadeia) != 1 {
		t.Fatalf("a cadeia deveria parar no filho, veio %d processos", len(cadeia))
	}
	candidatos := []candidatoPorta{{ID: "a", Dir: `C:\projetos\a`}}
	if got := donoDaPorta([]int{10}, candidatos, procs, 0); got != "" {
		t.Fatalf("pai falso virou pista: %q", got)
	}
}

func TestMesmosInteiros(t *testing.T) {
	if !mesmosInteiros([]int{3, 1, 2}, []int{2, 3, 1}) {
		t.Fatal("a ordem nao deveria importar")
	}
	if mesmosInteiros([]int{1, 2}, []int{1, 3}) || mesmosInteiros([]int{1}, []int{1, 1}) {
		t.Fatal("conjuntos diferentes deram como iguais")
	}
}
