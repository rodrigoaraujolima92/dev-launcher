package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// ExecutorSO e a implementacao real: docker compose para a infra e pwsh para as apps.
type ExecutorSO struct {
	raiz string
	rede string

	aoLogar   func(id, linha string)
	aoTerPID  func(id string, pid int)
	aoEncerra func(id string, mensagem string)

	mu    sync.Mutex
	procs map[string]*exec.Cmd
}

func NovoExecutorSO(raiz, rede string) *ExecutorSO {
	return &ExecutorSO{
		raiz:      raiz,
		rede:      rede,
		procs:     map[string]*exec.Cmd{},
		aoLogar:   func(string, string) {},
		aoTerPID:  func(string, int) {},
		aoEncerra: func(string, string) {},
	}
}

func (e *ExecutorSO) logar(id, formato string, args ...any) {
	e.aoLogar(id, fmt.Sprintf(formato, args...))
}

func (e *ExecutorSO) Pronto(ctx context.Context, s *Servico) bool {
	switch s.Pronto.Tipo {
	case "porta":
		return portaEscutando(s.Pronto.Porta)
	case "http":
		return httpRespondendo(ctx, s.Pronto.URL)
	case "comando":
		if len(s.Pronto.Cmd) == 0 {
			return false
		}
		_, err := rodarComando(ctx, 15*time.Second, s.Pronto.Cmd[0], s.Pronto.Cmd[1:]...)
		return err == nil
	default:
		return false
	}
}

func (e *ExecutorSO) Iniciar(ctx context.Context, s *Servico) error {
	switch s.Tipo {
	case TipoDocker:
		return e.iniciarDocker(ctx, s)
	case TipoApp:
		return e.iniciarApp(ctx, s)
	default:
		return fmt.Errorf("tipo desconhecido: %s", s.Tipo)
	}
}

func (e *ExecutorSO) iniciarDocker(ctx context.Context, s *Servico) error {
	if err := dockerDisponivel(ctx); err != nil {
		return err
	}
	if err := garantirRede(ctx, e.rede); err != nil {
		return fmt.Errorf("nao consegui garantir a rede %s: %w", e.rede, err)
	}
	arquivo := caminhoAbsoluto(e.raiz, s.Compose.Arquivo)
	if _, err := os.Stat(arquivo); err != nil {
		return fmt.Errorf("compose nao encontrado: %s", arquivo)
	}
	// --no-recreate: o objetivo e garantir que esteja no ar, nao aplicar mudanca de config.
	// Sem ele o compose derruba e recria um container que ja estava rodando ha horas so
	// porque o hash da config mudou.
	args := []string{
		"compose", "-p", s.Compose.Projeto, "-f", arquivo,
		"up", "-d", "--no-recreate", s.Compose.Servico,
	}
	e.logar(s.ID, "> docker %s", strings.Join(args[1:], " "))
	saida, err := rodarComando(ctx, 5*time.Minute, "docker", args...)
	for _, linha := range strings.Split(saida, "\n") {
		if l := strings.TrimSpace(linha); l != "" {
			e.logar(s.ID, "%s", l)
		}
	}
	if err != nil {
		return fmt.Errorf("docker compose up falhou")
	}
	return nil
}

func (e *ExecutorSO) iniciarApp(ctx context.Context, s *Servico) error {
	dir := caminhoAbsoluto(e.raiz, s.Dir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("diretorio nao encontrado: %s", dir)
	}
	shell := shellDisponivel()
	if shell == "" {
		return fmt.Errorf("nem pwsh nem powershell encontrados no PATH")
	}

	if s.Modo == ModoTerminal {
		if comandoExiste("wt") {
			// -w appdaturma agrupa todas as abas na mesma janela do Windows Terminal.
			args := []string{"-w", "appdaturma", "new-tab", "--title", s.Nome, "-d", dir,
				shell, "-NoExit", "-Command", s.Cmd}
			e.logar(s.ID, "> wt new-tab %s", s.Nome)
			if err := exec.CommandContext(ctx, "wt", args...).Start(); err != nil {
				return fmt.Errorf("nao consegui abrir a aba do Windows Terminal: %w", err)
			}
			return nil
		}
		e.logar(s.ID, "Windows Terminal (wt) nao encontrado - subindo em modo gerenciado.")
	}

	cmd := exec.Command(shell, "-NoLogo", "-NoProfile", "-Command", s.Cmd)
	cmd.Dir = dir
	saida, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	erroPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	e.logar(s.ID, "> %s  (em %s)", s.Cmd, dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("nao consegui iniciar: %w", err)
	}

	e.mu.Lock()
	e.procs[s.ID] = cmd
	e.mu.Unlock()
	e.aoTerPID(s.ID, cmd.Process.Pid)

	id := s.ID
	go e.bombearLog(id, saida)
	go e.bombearLog(id, erroPipe)
	go func() {
		err := cmd.Wait()
		e.mu.Lock()
		if e.procs[id] == cmd {
			delete(e.procs, id)
		}
		e.mu.Unlock()
		if err != nil {
			e.aoEncerra(id, fmt.Sprintf("processo encerrou: %v", err))
		} else {
			e.aoEncerra(id, "processo encerrou")
		}
	}()
	return nil
}

func (e *ExecutorSO) bombearLog(id string, r io.Reader) {
	leitor := bufio.NewScanner(r)
	leitor.Buffer(make([]byte, 0, 64*1024), 1024*1024) // linha de stack trace passa facil de 64KB
	for leitor.Scan() {
		linha := limparANSI(leitor.Text())
		if strings.TrimSpace(linha) == "" {
			continue
		}
		e.aoLogar(id, linha)
	}
}

func (e *ExecutorSO) Parar(ctx context.Context, s *Servico) error {
	if s.Tipo == TipoDocker {
		arquivo := caminhoAbsoluto(e.raiz, s.Compose.Arquivo)
		e.logar(s.ID, "> docker compose stop %s", s.Compose.Servico)
		saida, err := rodarComando(ctx, 2*time.Minute, "docker",
			"compose", "-p", s.Compose.Projeto, "-f", arquivo, "stop", s.Compose.Servico)
		if saida != "" {
			e.logar(s.ID, "%s", saida)
		}
		if err != nil {
			return fmt.Errorf("docker compose stop falhou")
		}
		return nil
	}

	e.mu.Lock()
	cmd := e.procs[s.ID]
	e.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		e.logar(s.ID, "> taskkill /T /F no PID %d", cmd.Process.Pid)
		if err := matarArvore(ctx, cmd.Process.Pid); err != nil {
			return fmt.Errorf("nao consegui matar o processo: %w", err)
		}
		return nil
	}

	// Sem processo nosso (subiu em aba do terminal, ou ja estava no ar antes do launcher):
	// acha quem esta segurando a porta e derruba a arvore.
	if s.Porta == 0 {
		return fmt.Errorf("nao sei como parar: sem processo gerenciado e sem porta configurada")
	}
	pids := pidsNaPorta(ctx, s.Porta)
	if len(pids) == 0 {
		return nil // ja esta parado
	}
	for _, pid := range pids {
		e.logar(s.ID, "> taskkill /T /F no PID %d (porta %d)", pid, s.Porta)
		if err := matarArvore(ctx, pid); err != nil {
			return fmt.Errorf("nao consegui matar o PID %d: %w", pid, err)
		}
	}
	return nil
}

// PararGerenciados derruba tudo que o launcher esta segurando. Chamado no encerramento:
// sem isso os dev servers ficariam orfaos, segurando porta, e o proximo launcher nao teria
// mais o PID deles - so daria para matar pela porta, no escuro.
func (e *ExecutorSO) PararGerenciados(ctx context.Context) []string {
	e.mu.Lock()
	pids := map[string]int{}
	for id, cmd := range e.procs {
		if cmd.Process != nil {
			pids[id] = cmd.Process.Pid
		}
	}
	e.mu.Unlock()

	derrubados := []string{}
	for id, pid := range pids {
		if err := matarArvore(ctx, pid); err == nil {
			derrubados = append(derrubados, id)
		}
	}
	sort.Strings(derrubados)
	return derrubados
}

func shellDisponivel() string {
	for _, nome := range []string{"pwsh", "powershell"} {
		if comandoExiste(nome) {
			return nome
		}
	}
	return ""
}
