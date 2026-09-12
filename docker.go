package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ContainerInfo e o que a tela mostra de cada container do config.
type ContainerInfo struct {
	Nome   string `json:"nome"`
	Estado string `json:"estado"` // running, exited, created...
	Status string `json:"status"` // "Up 2 hours", "Exited (0) 3 weeks ago"
	Portas string `json:"portas"`
	Imagem string `json:"imagem"`
}

type EstadoDocker struct {
	Instalado  bool            `json:"instalado"` // cliente docker no PATH
	Rodando    bool            `json:"rodando"`   // engine respondendo
	Iniciando  bool            `json:"iniciando"` // Docker Desktop abrindo agora
	Versao     string          `json:"versao"`
	Desktop    string          `json:"desktop"` // caminho do Docker Desktop.exe, se achado
	Mensagem   string          `json:"mensagem"`
	Containers []ContainerInfo `json:"containers"`
}

// SondaDocker isola as chamadas ao docker. A interface existe para os testes: sem ela,
// testar a tela e as rotas exigiria um Docker de verdade na maquina.
type SondaDocker interface {
	Estado(ctx context.Context, nomes []string) EstadoDocker
	// Garantir devolve nil quando o engine esta respondendo - abrindo o Docker Desktop e
	// esperando, se precisar. logar recebe o andamento (vai para o log do projeto).
	Garantir(ctx context.Context, logar func(string)) error
}

// caminhosDockerDesktop sao os lugares onde o instalador costuma deixar o executavel.
func caminhosDockerDesktop() []string {
	candidatos := []string{}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")} {
		if base != "" {
			candidatos = append(candidatos, filepath.Join(base, "Docker", "Docker", "Docker Desktop.exe"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidatos = append(candidatos, filepath.Join(local, "Docker", "Docker Desktop.exe"))
	}
	return candidatos
}

// primeiroExistente devolve o primeiro caminho que existe no disco.
func primeiroExistente(candidatos []string) string {
	for _, c := range candidatos {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

type SondaDockerSO struct {
	mu        sync.Mutex
	iniciando bool

	// Tempo maximo esperando o engine responder depois de abrir o Docker Desktop.
	// Em maquina fria o WSL2 leva bem mais que os 30s de um start comum.
	Espera time.Duration
}

func NovaSondaDockerSO() *SondaDockerSO {
	return &SondaDockerSO{Espera: 3 * time.Minute}
}

func (s *SondaDockerSO) Estado(ctx context.Context, nomes []string) EstadoDocker {
	s.mu.Lock()
	iniciando := s.iniciando
	s.mu.Unlock()

	estado := EstadoDocker{
		Instalado:  comandoExiste("docker"),
		Iniciando:  iniciando,
		Desktop:    primeiroExistente(caminhosDockerDesktop()),
		Containers: []ContainerInfo{},
	}
	if !estado.Instalado {
		estado.Mensagem = "docker nao encontrado no PATH"
		return estado
	}

	versao, err := rodarComando(ctx, 10*time.Second, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil || versao == "" {
		if iniciando {
			estado.Mensagem = "Docker Desktop abrindo..."
		} else {
			estado.Mensagem = "engine parado - o Docker Desktop nao esta rodando"
		}
		return estado
	}
	estado.Rodando = true
	estado.Versao = versao

	saida, err := rodarComando(ctx, 15*time.Second, "docker", "ps", "-a", "--format",
		"{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Ports}}\t{{.Image}}")
	if err == nil {
		estado.Containers = filtrarContainers(lerContainers(saida), nomes)
	}
	return estado
}

// lerContainers interpreta a saida tabulada do docker ps.
func lerContainers(saida string) []ContainerInfo {
	out := []ContainerInfo{}
	for _, linha := range strings.Split(saida, "\n") {
		linha = strings.TrimSpace(linha)
		if linha == "" {
			continue
		}
		campos := strings.Split(linha, "\t")
		if len(campos) < 2 {
			continue
		}
		info := ContainerInfo{Nome: campos[0], Estado: campos[1]}
		if len(campos) > 2 {
			info.Status = campos[2]
		}
		if len(campos) > 3 {
			info.Portas = campos[3]
		}
		if len(campos) > 4 {
			info.Imagem = campos[4]
		}
		out = append(out, info)
	}
	return out
}

// filtrarContainers deixa so os containers citados no config, na ordem em que aparecem la.
// Sem isso a tela mostraria todo container da maquina, inclusive de outros projetos.
func filtrarContainers(todos []ContainerInfo, nomes []string) []ContainerInfo {
	if len(nomes) == 0 {
		return []ContainerInfo{}
	}
	porNome := map[string]ContainerInfo{}
	for _, c := range todos {
		porNome[c.Nome] = c
	}
	out := []ContainerInfo{}
	for _, nome := range nomes {
		if c, ok := porNome[nome]; ok {
			out = append(out, c)
			continue
		}
		out = append(out, ContainerInfo{Nome: nome, Estado: "ausente", Status: "nao criado ainda"})
	}
	return out
}

func (s *SondaDockerSO) Garantir(ctx context.Context, logar func(string)) error {
	if logar == nil {
		logar = func(string) {}
	}
	if !comandoExiste("docker") {
		return fmt.Errorf("docker nao encontrado no PATH")
	}
	if err := dockerDisponivel(ctx); err == nil {
		return nil
	}

	// Um projeto por vez abre o Docker Desktop: db, rabbit e mailhog sobem na mesma onda e
	// chegam aqui juntos. Quem nao ganhou o lock espera o mesmo engine ficar de pe.
	s.mu.Lock()
	if s.iniciando {
		s.mu.Unlock()
		logar("aguardando o Docker Desktop que ja esta abrindo...")
		return s.esperarEngine(ctx, logar)
	}
	s.iniciando = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.iniciando = false
		s.mu.Unlock()
	}()

	desktop := primeiroExistente(caminhosDockerDesktop())
	if desktop == "" {
		return fmt.Errorf("Docker Desktop nao encontrado - abra manualmente e tente de novo")
	}
	logar("Docker Desktop nao esta rodando: abrindo " + desktop)
	if err := abrirPrograma(desktop); err != nil {
		return fmt.Errorf("nao consegui abrir o Docker Desktop: %w", err)
	}
	return s.esperarEngine(ctx, logar)
}

func (s *SondaDockerSO) esperarEngine(ctx context.Context, logar func(string)) error {
	espera := s.Espera
	if espera <= 0 {
		espera = 3 * time.Minute
	}
	prazo := time.Now().Add(espera)
	avisado := false
	for {
		if err := dockerDisponivel(ctx); err == nil {
			logar("engine do Docker respondendo.")
			return nil
		}
		if time.Now().After(prazo) {
			return fmt.Errorf("o Docker nao subiu em %s", espera)
		}
		if !avisado {
			logar("esperando o engine do Docker subir (pode levar um minuto)...")
			avisado = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
