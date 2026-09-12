package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const saidaDockerPS = "db\trunning\tUp 2 hours\t0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp\tpostgres:15\n" +
	"rabbitmq\trunning\tUp 10 hours\t0.0.0.0:5672->5672/tcp\trabbitmq:3.7.8-management\n" +
	"contrato-api\texited\tExited (2) 10 hours ago\t\tconceicao/contrato-api:0bea626\n"

func TestLerContainers(t *testing.T) {
	got := lerContainers(saidaDockerPS)
	if len(got) != 3 {
		t.Fatalf("esperava 3 containers, veio %d", len(got))
	}
	if got[0].Nome != "db" || got[0].Estado != "running" || got[0].Imagem != "postgres:15" {
		t.Fatalf("primeiro container = %+v", got[0])
	}
	// Container parado nao tem portas: os campos vazios nao podem desalinhar a leitura.
	if got[2].Nome != "contrato-api" || got[2].Estado != "exited" || got[2].Portas != "" {
		t.Fatalf("container parado = %+v", got[2])
	}
	if len(lerContainers("")) != 0 {
		t.Fatal("saida vazia deveria dar lista vazia")
	}
}

func TestFiltrarContainersSegueOConfigEMarcaAusente(t *testing.T) {
	todos := lerContainers(saidaDockerPS)

	// Ordem pedida diferente da ordem do docker ps, e um container que nunca foi criado.
	got := filtrarContainers(todos, []string{"rabbitmq", "db", "mailhog"})
	if len(got) != 3 {
		t.Fatalf("esperava 3, veio %d", len(got))
	}
	if got[0].Nome != "rabbitmq" || got[1].Nome != "db" {
		t.Fatalf("a ordem deveria seguir o config: %+v", got)
	}
	if got[2].Nome != "mailhog" || got[2].Estado != "ausente" {
		t.Fatalf("container nao criado deveria aparecer como ausente: %+v", got[2])
	}
	// contrato-api existe na maquina mas nao esta no config: nao entra.
	for _, c := range got {
		if c.Nome == "contrato-api" {
			t.Fatal("container de fora do config nao deveria aparecer")
		}
	}
	if len(filtrarContainers(todos, nil)) != 0 {
		t.Fatal("sem nomes no config, a lista e vazia")
	}
}

func TestPrimeiroExistente(t *testing.T) {
	dir := t.TempDir()
	arquivo := filepath.Join(dir, "Docker Desktop.exe")
	if err := os.WriteFile(arquivo, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := primeiroExistente([]string{"", filepath.Join(dir, "nao-existe.exe"), arquivo})
	if got != arquivo {
		t.Fatalf("primeiroExistente = %q", got)
	}
	if primeiroExistente([]string{filepath.Join(dir, "nada.exe")}) != "" {
		t.Fatal("sem candidato valido deveria devolver vazio")
	}
	// Pasta nao conta como executavel.
	if primeiroExistente([]string{dir}) != "" {
		t.Fatal("uma pasta nao pode ser confundida com o executavel")
	}
}

func TestCaminhosDockerDesktopUsamAsVariaveisDoWindows(t *testing.T) {
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("LOCALAPPDATA", `C:\Users\alguem\AppData\Local`)

	candidatos := caminhosDockerDesktop()
	if len(candidatos) == 0 {
		t.Fatal("deveria montar ao menos um candidato")
	}
	achou := false
	for _, c := range candidatos {
		if strings.HasSuffix(c, filepath.Join("Docker", "Docker", "Docker Desktop.exe")) {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("faltou o caminho padrao do instalador: %v", candidatos)
	}
}

// ------------------------------------------------------------------
// gerente + rotas
// ------------------------------------------------------------------

func TestNomesDeContainerSaemDoConfig(t *testing.T) {
	g, _, cfg := gerenteTeste(t, nil)
	cfg.porID("db").Container = "db"
	// rabbit nao tem container: cai para o nome do servico no compose.
	cfg.porID("rabbit").Container = ""

	got := g.nomesDeContainer()
	if !reflect.DeepEqual(got, []string{"db", "rabbitmq"}) {
		t.Fatalf("nomes = %v, queria [db rabbitmq]", got)
	}
}

func TestPublicarDockerSoEmiteQuandoMuda(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	sonda := g.docker.(*dockerFake)

	inscricao, ch := g.Inscrever()
	defer g.Desinscrever(inscricao)

	g.PublicarDocker(context.Background())
	if len(ch) != 1 {
		t.Fatalf("a primeira publicacao deveria emitir, veio %d evento(s)", len(ch))
	}
	<-ch

	g.PublicarDocker(context.Background()) // estado igual
	if len(ch) != 0 {
		t.Fatal("estado igual nao deveria virar evento")
	}

	sonda.mu.Lock()
	sonda.estado = EstadoDocker{Rodando: false, Mensagem: "engine parado"}
	sonda.mu.Unlock()

	g.PublicarDocker(context.Background())
	if len(ch) != 1 {
		t.Fatal("mudanca de estado deveria emitir")
	}
	var evento struct {
		Tipo   string       `json:"tipo"`
		Docker EstadoDocker `json:"docker"`
	}
	if err := json.Unmarshal(<-ch, &evento); err != nil {
		t.Fatal(err)
	}
	if evento.Tipo != "docker" || evento.Docker.Rodando {
		t.Fatalf("evento = %+v", evento)
	}
}

func TestAbrirDockerChamaASonda(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	sonda := g.docker.(*dockerFake)

	if err := g.AbrirDocker(context.Background()); err != nil {
		t.Fatalf("abrir: %v", err)
	}
	if sonda.vezes() != 1 {
		t.Fatalf("a sonda deveria ter sido chamada uma vez, veio %d", sonda.vezes())
	}

	sonda.mu.Lock()
	sonda.erro = errors.New("Docker Desktop nao encontrado")
	sonda.mu.Unlock()
	if err := g.AbrirDocker(context.Background()); err == nil {
		t.Fatal("o erro da sonda deveria chegar a quem chamou")
	}
}

func TestRotaDockerDevolveOEstado(t *testing.T) {
	h, g := servidorTeste(t, nil)
	sonda := g.docker.(*dockerFake)
	sonda.mu.Lock()
	sonda.estado = EstadoDocker{
		Rodando:    true,
		Versao:     "27.0",
		Containers: []ContainerInfo{{Nome: "db", Estado: "running", Status: "Up 2 hours"}},
	}
	sonda.mu.Unlock()

	resp := chamar(t, h, http.MethodGet, "/api/docker", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d", resp.Code)
	}
	var estado EstadoDocker
	if err := json.Unmarshal(resp.Body.Bytes(), &estado); err != nil {
		t.Fatal(err)
	}
	if !estado.Rodando || estado.Versao != "27.0" || len(estado.Containers) != 1 {
		t.Fatalf("estado = %+v", estado)
	}
}

func TestRotaAbrirDockerDisparaEmSegundoPlano(t *testing.T) {
	h, g := servidorTeste(t, nil)
	sonda := g.docker.(*dockerFake)

	resp := chamar(t, h, http.MethodPost, "/api/docker/abrir", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	// A rota responde na hora; abrir o Docker Desktop pode levar mais de um minuto.
	limite := time.Now().Add(3 * time.Second)
	for time.Now().Before(limite) && sonda.vezes() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if sonda.vezes() == 0 {
		t.Fatal("a rota respondeu mas nao chamou a sonda")
	}
}

// ------------------------------------------------------------------
// executor
// ------------------------------------------------------------------

func TestExecutorGaranteOEngineAntesDeSubirContainer(t *testing.T) {
	sonda := &dockerFake{erro: errors.New("Docker Desktop nao encontrado")}
	exe := NovoExecutorSO(t.TempDir(), "PRODUCTION", sonda)

	var logado []string
	exe.aoLogar = func(_, linha string) { logado = append(logado, linha) }

	servico := &Servico{
		ID: "db", Nome: "Postgres", Tipo: TipoDocker,
		Compose: &Compose{Projeto: "p", Arquivo: "a.yml", Servico: "db"},
	}
	err := exe.Iniciar(context.Background(), servico)
	if err == nil {
		t.Fatal("com o Docker fora do ar, iniciar deveria falhar")
	}
	if !strings.Contains(err.Error(), "Docker Desktop") {
		t.Fatalf("a mensagem deveria vir da sonda: %v", err)
	}
	if sonda.vezes() != 1 {
		t.Fatalf("a sonda deveria ter sido chamada, veio %d", sonda.vezes())
	}
	// O andamento da sonda vai para o log do projeto.
	if len(logado) == 0 || !strings.Contains(strings.Join(logado, " "), "Docker Desktop") {
		t.Fatalf("o log do projeto deveria mostrar o andamento: %v", logado)
	}
}

func TestExecutorNaoChamaASondaParaProjetoApp(t *testing.T) {
	sonda := &dockerFake{}
	exe := NovoExecutorSO(t.TempDir(), "PRODUCTION", sonda)

	// Pasta inexistente: o erro sai antes de qualquer coisa de docker.
	servico := &Servico{ID: "web", Nome: "front", Tipo: TipoApp, Dir: "nao-existe", Cmd: "npm run dev"}
	if err := exe.Iniciar(context.Background(), servico); err == nil {
		t.Fatal("esperava erro de pasta")
	}
	if sonda.vezes() != 0 {
		t.Fatal("quem sobe so app nao deveria acordar o Docker Desktop")
	}
}
