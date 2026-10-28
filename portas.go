package main

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Disputa de porta: dois projetos do config com a mesma porta (o front de uma empresa e o
// de outra, os dois na 8080). A checagem de "pronto" so olha a porta, entao com um deles no
// ar os dois pareciam estar. Este arquivo descobre de quem e o processo que esta escutando.

// Processo e uma linha da tabela de processos do Windows - o bastante para dizer de quem e
// um PID: quem e o pai, quando nasceu e com que linha de comando.
type Processo struct {
	PID    int
	Pai    int
	Criado time.Time
	Linha  string // executavel + linha de comando
}

// candidatoPorta e um projeto que usa a porta em disputa, com as pistas que o ligam a um
// processo.
type candidatoPorta struct {
	ID         string
	Dir        string        // pasta absoluta do projeto; vazio para servico docker
	Docker     bool          // sobe por docker compose
	PIDProprio int           // processo que o launcher segura (0 = nenhum)
	Iniciado   time.Time     // ultima vez que o launcher mandou subir
	Prazo      time.Duration // timeout de subida do projeto
}

// ArbitroDePorta diz qual projeto esta de fato atras de uma porta que mais de um usa.
// Devolve o id do dono, ou "" quando nao da para afirmar.
type ArbitroDePorta interface {
	DonoDaPorta(ctx context.Context, porta int, candidatos []*Servico) string
}

// donoDaPorta decide, em ordem de confianca:
//
//  1. o processo que escuta descende de um que o launcher segura - certeza;
//  2. a linha de comando de quem escuta (ou de um ancestral) cita a pasta do projeto -
//     e o caso do node/vue-cli, que roda ".../meu-front/node_modules/...";
//  3. quem escuta e o docker, e so um dos candidatos e container;
//  4. o processo nasceu logo depois de o launcher mandar subir um dos candidatos - o que
//     sobra para "go run" em aba do terminal, que nao deixa pasta nenhuma na linha de comando.
//
// eu e o PID do proprio launcher: a subida da arvore para nele, senao a pasta do launcher
// na linha de comando contaria como pista.
func donoDaPorta(ouvintes []int, candidatos []candidatoPorta, procs map[int]Processo, eu int) string {
	cadeias := make([][]Processo, 0, len(ouvintes))
	for _, pid := range ouvintes {
		cadeias = append(cadeias, cadeiaDeProcessos(pid, procs, eu))
	}

	for _, cadeia := range cadeias {
		for _, p := range cadeia {
			for _, c := range candidatos {
				if c.PIDProprio != 0 && c.PIDProprio == p.PID {
					return c.ID
				}
			}
		}
	}

	for _, cadeia := range cadeias {
		// Do processo que escuta para cima: a pista mais perto dele vale mais que a de um
		// ancestral distante (o editor aberto em outra pasta, por exemplo).
		for _, p := range cadeia {
			melhor, tamanho := "", 0
			for _, c := range candidatos {
				// Pasta mais longa ganha: "projetos/app" nao pode levar o que e de
				// "projetos/app/admin".
				if c.Dir != "" && len(c.Dir) > tamanho && contemPasta(p.Linha, c.Dir) {
					melhor, tamanho = c.ID, len(c.Dir)
				}
			}
			if melhor != "" {
				return melhor
			}
		}
	}

	for _, cadeia := range cadeias {
		if len(cadeia) == 0 || !processoDoDocker(cadeia[0].Linha) {
			continue
		}
		unico := ""
		for _, c := range candidatos {
			if !c.Docker {
				continue
			}
			if unico != "" {
				unico = ""
				break
			}
			unico = c.ID
		}
		if unico != "" {
			return unico
		}
	}

	for _, cadeia := range cadeias {
		if len(cadeia) == 0 || cadeia[0].Criado.IsZero() {
			continue
		}
		nascido := cadeia[0].Criado
		melhor := ""
		var quando time.Time
		for _, c := range candidatos {
			if c.Iniciado.IsZero() || !c.Iniciado.After(quando) {
				continue
			}
			// Dois segundos de folga para tras: o relogio do launcher e o da tabela de
			// processos nao sao lidos no mesmo instante.
			if nascido.Before(c.Iniciado.Add(-2*time.Second)) || nascido.After(c.Iniciado.Add(c.Prazo)) {
				continue
			}
			melhor, quando = c.ID, c.Iniciado
		}
		if melhor != "" {
			return melhor
		}
	}
	return ""
}

// cadeiaDeProcessos devolve o processo e seus ancestrais, do mais novo para o mais velho.
func cadeiaDeProcessos(pid int, procs map[int]Processo, eu int) []Processo {
	cadeia := []Processo{}
	vistos := map[int]bool{}
	for len(cadeia) < 16 {
		p, ok := procs[pid]
		if !ok || pid == 0 || pid == eu || vistos[pid] {
			break
		}
		// O Windows reaproveita PID: "pai" que nasceu depois do filho e outro processo que
		// herdou o numero, nao ancestral de verdade.
		if n := len(cadeia); n > 0 && !p.Criado.IsZero() && !cadeia[n-1].Criado.IsZero() && p.Criado.After(cadeia[n-1].Criado) {
			break
		}
		vistos[pid] = true
		cadeia = append(cadeia, p)
		pid = p.Pai
	}
	return cadeia
}

// contemPasta diz se a linha de comando cita a pasta - a pasta inteira, nao um prefixo:
// "...\app-admin" nao pode casar com "...\app-admin-v2".
func contemPasta(linha, pasta string) bool {
	normalizar := func(s string) string {
		return strings.TrimRight(strings.ReplaceAll(strings.ToLower(s), "/", `\`), `\`)
	}
	l, p := normalizar(linha), normalizar(pasta)
	if p == "" {
		return false
	}
	for inicio := 0; ; {
		i := strings.Index(l[inicio:], p)
		if i < 0 {
			return false
		}
		fim := inicio + i + len(p)
		if fim == len(l) || strings.ContainsRune(`\" '`, rune(l[fim])) {
			return true
		}
		inicio = fim
	}
}

func processoDoDocker(linha string) bool {
	l := strings.ToLower(linha)
	for _, nome := range []string{"com.docker", "docker desktop", "wslrelay", "vpnkit"} {
		if strings.Contains(l, nome) {
			return true
		}
	}
	return false
}

// scriptProcessos lista PID, pai, nascimento (ms desde 1970) e linha de comando, um processo
// por linha. Os tres primeiros campos sao numeros, entao o "|" da linha de comando nao
// atrapalha quem le.
const scriptProcessos = `[Console]::OutputEncoding = [Text.Encoding]::UTF8; ` +
	`Get-CimInstance Win32_Process | ForEach-Object { $c = 0; ` +
	`if ($_.CreationDate) { $c = ([DateTimeOffset]$_.CreationDate).ToUnixTimeMilliseconds() }; ` +
	`"{0}|{1}|{2}|{3}" -f $_.ProcessId, $_.ParentProcessId, $c, ` +
	`((($_.ExecutablePath, $_.CommandLine) -join ' ') -replace '\s+', ' ') }`

func tabelaDeProcessos(ctx context.Context) (map[int]Processo, error) {
	shell := shellDisponivel()
	if shell == "" {
		return nil, errString("nem pwsh nem powershell encontrados no PATH")
	}
	saida, err := rodarComando(ctx, 20*time.Second, shell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", scriptProcessos)
	if err != nil {
		return nil, errString(primeiraLinha(saida))
	}
	return lerTabelaProcessos(saida), nil
}

// lerTabelaProcessos interpreta a saida do scriptProcessos. Separado do exec para poder ser
// testado com saida fixa; linha que nao segue o formato (aviso do PowerShell) e ignorada.
func lerTabelaProcessos(saida string) map[int]Processo {
	procs := map[int]Processo{}
	for _, linha := range strings.Split(saida, "\n") {
		campos := strings.SplitN(strings.TrimRight(linha, "\r"), "|", 4)
		if len(campos) < 4 {
			continue
		}
		pid, errPID := strconv.Atoi(strings.TrimSpace(campos[0]))
		pai, errPai := strconv.Atoi(strings.TrimSpace(campos[1]))
		ms, errMs := strconv.ParseInt(strings.TrimSpace(campos[2]), 10, 64)
		if errPID != nil || errPai != nil || errMs != nil || pid < 0 {
			continue
		}
		p := Processo{PID: pid, Pai: pai, Linha: strings.TrimSpace(campos[3])}
		if ms > 0 {
			p.Criado = time.UnixMilli(ms)
		}
		procs[pid] = p
	}
	return procs
}

// ------------------------------------------------------------------
// arbitro de verdade (ExecutorSO)
// ------------------------------------------------------------------

// donoEmCache guarda a ultima decisao por porta. Ler a tabela de processos custa perto de um
// segundo, e a resposta so muda quando muda quem esta escutando.
type donoEmCache struct {
	dono      string
	pids      []int
	chave     string    // ids dos candidatos: config editado invalida a decisao
	visto     time.Time // ultima vez que os PIDs foram conferidos no netstat
	resolvido time.Time // ultima vez que a tabela de processos foi lida
}

func (e *ExecutorSO) DonoDaPorta(ctx context.Context, porta int, candidatos []*Servico) string {
	ids := make([]string, 0, len(candidatos))
	for _, s := range candidatos {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	chave := strings.Join(ids, ",")

	e.mu.Lock()
	anterior, tem := e.donos[porta]
	e.mu.Unlock()
	tem = tem && anterior.chave == chave
	// A varredura pergunta pela mesma porta uma vez por candidato: dois segundos de folga
	// evitam um netstat para cada um.
	if tem && time.Since(anterior.visto) < 2*time.Second {
		return anterior.dono
	}

	pids := pidsNaPorta(ctx, porta)
	if len(pids) == 0 {
		return ""
	}
	// Mesmos PIDs, mesma resposta. O "nao sei" vale por menos tempo: pode ser que a tabela
	// tenha sido lida cedo demais, antes de o filho que escuta existir.
	if tem && mesmosInteiros(anterior.pids, pids) &&
		(anterior.dono != "" || time.Since(anterior.resolvido) < 15*time.Second) {
		anterior.visto = time.Now()
		e.mu.Lock()
		e.donos[porta] = anterior
		e.mu.Unlock()
		return anterior.dono
	}

	dono := ""
	if procs, err := tabelaDeProcessos(ctx); err == nil {
		dono = donoDaPorta(pids, e.candidatosDaPorta(candidatos), procs, os.Getpid())
	}
	agora := time.Now()
	e.mu.Lock()
	e.donos[porta] = donoEmCache{dono: dono, pids: pids, chave: chave, visto: agora, resolvido: agora}
	e.mu.Unlock()
	return dono
}

func (e *ExecutorSO) candidatosDaPorta(servicos []*Servico) []candidatoPorta {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]candidatoPorta, 0, len(servicos))
	for _, s := range servicos {
		c := candidatoPorta{ID: s.ID, Docker: s.Tipo == TipoDocker, Iniciado: e.iniciados[s.ID]}
		if s.Tipo == TipoApp && s.Dir != "" {
			c.Dir = caminhoAbsoluto(e.raiz, s.Dir)
		}
		if cmd := e.procs[s.ID]; cmd != nil && cmd.Process != nil {
			c.PIDProprio = cmd.Process.Pid
		}
		c.Prazo = time.Duration(s.Timeout) * time.Second
		if c.Prazo <= 0 {
			c.Prazo = 120 * time.Second
		}
		out = append(out, c)
	}
	return out
}

// portaDeOutro diz se os PIDs que escutam na porta ja foram atribuidos a outro projeto.
// E o que impede o "parar" de um projeto de derrubar o vizinho que divide a porta com ele.
func (e *ExecutorSO) portaDeOutro(porta int, pids []int, id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.donos[porta]
	if !ok || c.dono == "" || c.dono == id || !mesmosInteiros(c.pids, pids) {
		return ""
	}
	return c.dono
}

func mesmosInteiros(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]int{}, a...)
	y := append([]int{}, b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
