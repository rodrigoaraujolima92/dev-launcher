package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContainerInfo e o que a tela mostra de cada container.
type ContainerInfo struct {
	Nome     string `json:"nome"`
	Estado   string `json:"estado"` // running, exited, created, ausente
	Status   string `json:"status"` // "Up 2 hours", "Exited (0) 3 weeks ago"
	Portas   string `json:"portas"`
	Imagem   string `json:"imagem"`
	Politica string `json:"politica"`  // restart policy: no, always, unless-stopped, on-failure
	DoConfig bool   `json:"do_config"` // citado por algum projeto do launcher
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
	// Acao roda start, stop ou restart num container.
	Acao(ctx context.Context, nome, acao string) (string, error)
	// Logs devolve as ultimas linhas do container (stdout + stderr).
	Logs(ctx context.Context, nome string, linhas int) (string, error)
	// Politica troca a restart policy do container (docker update --restart=...).
	Politica(ctx context.Context, nome, politica string) error
}

// politicasRestart sao as aceitas pelo docker update. "no" e o que desliga o container de
// voltar sozinho quando o Docker Desktop abre.
var politicasRestart = map[string]bool{
	"no": true, "always": true, "unless-stopped": true, "on-failure": true,
}

func validarPolitica(politica string) error {
	if !politicasRestart[politica] {
		return fmt.Errorf("politica invalida: %q (use no, always, unless-stopped ou on-failure)", politica)
	}
	return nil
}

// acoesContainer sao as unicas acoes aceitas. Lista fechada de proposito: o nome vem da
// tela, e "docker <qualquer coisa>" nao pode virar um caminho aberto.
var acoesContainer = map[string]bool{"start": true, "stop": true, "restart": true}

// reNomeContainer cobre o que o docker aceita como nome. Serve para recusar de saida algo
// como "-f" ou "--volumes", que viraria uma flag em vez de um nome.
var reNomeContainer = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func validarContainer(nome, acao string) error {
	if !reNomeContainer.MatchString(nome) {
		return fmt.Errorf("nome de container invalido: %q", nome)
	}
	if acao != "" && !acoesContainer[acao] {
		return fmt.Errorf("acao invalida: %q (use start, stop ou restart)", acao)
	}
	return nil
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
		estado.Containers = montarLista(lerContainers(saida), nomes)
		s.completarPoliticas(ctx, estado.Containers)
	}
	return estado
}

// completarPoliticas busca a restart policy de todos os containers de uma vez - um
// "docker inspect" por container deixaria a varredura lenta a toa.
func (s *SondaDockerSO) completarPoliticas(ctx context.Context, containers []ContainerInfo) {
	nomes := []string{}
	for _, c := range containers {
		if c.Estado != "ausente" {
			nomes = append(nomes, c.Nome)
		}
	}
	if len(nomes) == 0 {
		return
	}
	args := append([]string{"inspect", "--format", "{{.Name}}\t{{.HostConfig.RestartPolicy.Name}}"}, nomes...)
	saida, err := rodarComando(ctx, 20*time.Second, "docker", args...)
	if err != nil {
		return
	}
	politicas := lerPoliticas(saida)
	for i := range containers {
		if p, ok := politicas[containers[i].Nome]; ok {
			containers[i].Politica = p
		}
	}
}

// lerPoliticas interpreta a saida do inspect. O nome vem com barra na frente ("/db").
func lerPoliticas(saida string) map[string]string {
	out := map[string]string{}
	for _, linha := range strings.Split(saida, "\n") {
		// Sem TrimSpace na linha inteira: container sem policy termina em tabulacao, e o
		// trim comeria justamente o campo vazio que precisamos ler como "no".
		linha = strings.TrimRight(linha, "\r")
		if strings.TrimSpace(linha) == "" {
			continue
		}
		campos := strings.SplitN(linha, "\t", 2)
		if len(campos) != 2 {
			continue
		}
		nome := strings.TrimPrefix(strings.TrimSpace(campos[0]), "/")
		politica := strings.TrimSpace(campos[1])
		if nome == "" {
			continue
		}
		if politica == "" {
			politica = "no" // container criado sem policy nenhuma
		}
		out[nome] = politica
	}
	return out
}

func (s *SondaDockerSO) Acao(ctx context.Context, nome, acao string) (string, error) {
	if err := validarContainer(nome, acao); err != nil {
		return "", err
	}
	saida, err := rodarComando(ctx, 2*time.Minute, "docker", acao, nome)
	if err != nil {
		if saida == "" {
			saida = "docker " + acao + " falhou"
		}
		return saida, fmt.Errorf("%s", saida)
	}
	return saida, nil
}

func (s *SondaDockerSO) Politica(ctx context.Context, nome, politica string) error {
	if err := validarContainer(nome, ""); err != nil {
		return err
	}
	if err := validarPolitica(politica); err != nil {
		return err
	}
	saida, err := rodarComando(ctx, 60*time.Second, "docker", "update", "--restart="+politica, nome)
	if err != nil {
		if saida == "" {
			saida = "docker update falhou"
		}
		return fmt.Errorf("%s", saida)
	}
	return nil
}

func (s *SondaDockerSO) Logs(ctx context.Context, nome string, linhas int) (string, error) {
	if err := validarContainer(nome, ""); err != nil {
		return "", err
	}
	if linhas <= 0 || linhas > 2000 {
		linhas = 300
	}
	// O docker logs joga stderr no stderr: rodarComando junta os dois, que e o que se quer
	// aqui (a maioria das imagens loga em stderr).
	saida, err := rodarComando(ctx, 30*time.Second, "docker", "logs", "--tail", strconv.Itoa(linhas), nome)
	if err != nil && saida == "" {
		return "", fmt.Errorf("nao consegui ler o log de %s", nome)
	}
	return saida, nil
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

// montarLista devolve, nesta ordem: os containers do config (na ordem do config, marcados,
// inclusive os que ainda nao existem) e depois os demais que estao RODANDO.
//
// Os parados de fora do config ficam de fora de proposito: a maquina acumula container
// velho de meses, e a lista viraria um cemiterio em vez de um painel.
func montarLista(todos []ContainerInfo, nomes []string) []ContainerInfo {
	porNome := map[string]ContainerInfo{}
	for _, c := range todos {
		porNome[c.Nome] = c
	}
	doConfig := map[string]bool{}
	out := []ContainerInfo{}

	for _, nome := range nomes {
		doConfig[nome] = true
		if c, ok := porNome[nome]; ok {
			c.DoConfig = true
			out = append(out, c)
			continue
		}
		out = append(out, ContainerInfo{Nome: nome, Estado: "ausente", Status: "nao criado ainda", DoConfig: true})
	}

	for _, c := range todos {
		if doConfig[c.Nome] || c.Estado != "running" {
			continue
		}
		out = append(out, c)
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
