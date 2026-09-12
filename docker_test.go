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

func TestMontarListaPoeOConfigNaFrenteEDepoisOsDeFora(t *testing.T) {
	// Alem dos tres do saidaDockerPS, um container de fora que esta rodando.
	todos := lerContainers(saidaDockerPS + "grafana\trunning\tUp 5 minutes\t0.0.0.0:3000->3000/tcp\tgrafana/grafana\n")

	got := montarLista(todos, []string{"rabbitmq", "db", "mailhog"})
	if len(got) != 4 {
		t.Fatalf("esperava 3 do config + 1 de fora, veio %d: %+v", len(got), got)
	}
	// Ordem do config primeiro, mesmo diferindo da ordem do docker ps.
	if got[0].Nome != "rabbitmq" || got[1].Nome != "db" {
		t.Fatalf("a ordem deveria seguir o config: %+v", got)
	}
	if got[2].Nome != "mailhog" || got[2].Estado != "ausente" {
		t.Fatalf("container nao criado deveria aparecer como ausente: %+v", got[2])
	}
	for i := 0; i < 3; i++ {
		if !got[i].DoConfig {
			t.Fatalf("%s deveria estar marcado como do config", got[i].Nome)
		}
	}
	if got[3].Nome != "grafana" || got[3].DoConfig {
		t.Fatalf("o container de fora deveria vir por ultimo e sem a marca: %+v", got[3])
	}
	// contrato-api esta parado e nao e do config: fica de fora para a lista nao virar
	// cemiterio de container velho.
	for _, c := range got {
		if c.Nome == "contrato-api" {
			t.Fatal("container parado de fora do config nao deveria aparecer")
		}
	}
}

func TestMontarListaSemConfigMostraSoOsRodando(t *testing.T) {
	got := montarLista(lerContainers(saidaDockerPS), nil)
	if len(got) != 2 {
		t.Fatalf("esperava so os 2 rodando, veio %+v", got)
	}
	for _, c := range got {
		if c.Estado != "running" || c.DoConfig {
			t.Fatalf("container inesperado: %+v", c)
		}
	}
}

func TestValidarContainer(t *testing.T) {
	if err := validarContainer("db", "stop"); err != nil {
		t.Fatalf("nome e acao validos: %v", err)
	}
	// Nome que comeca com "-" viraria flag do docker.
	if err := validarContainer("--volumes", "stop"); err == nil {
		t.Fatal("nome comecando com - deveria ser recusado")
	}
	if err := validarContainer("db meu", "stop"); err == nil {
		t.Fatal("nome com espaco deveria ser recusado")
	}
	if err := validarContainer("", "stop"); err == nil {
		t.Fatal("nome vazio deveria ser recusado")
	}
	// Acao fora da lista fechada.
	if err := validarContainer("db", "rm"); err == nil {
		t.Fatal("acao fora da lista deveria ser recusada")
	}
	if err := validarContainer("db", ""); err != nil {
		t.Fatalf("acao vazia (caso do log) deveria passar: %v", err)
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

func TestRotaDeAcaoNoContainer(t *testing.T) {
	h, g := servidorTeste(t, nil)
	sonda := g.docker.(*dockerFake)

	resp := chamar(t, h, http.MethodPost, "/api/docker/containers/db/stop", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("parar: %d %s", resp.Code, resp.Body)
	}
	if got := sonda.acoesFeitas(); len(got) != 1 || got[0] != [2]string{"db", "stop"} {
		t.Fatalf("acoes = %v", got)
	}

	// Acao fora da lista e nome invalido nao chegam ao docker.
	if resp := chamar(t, h, http.MethodPost, "/api/docker/containers/db/rm", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("acao invalida deveria dar 400, veio %d", resp.Code)
	}
	if resp := chamar(t, h, http.MethodPost, "/api/docker/containers/--volumes/stop", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("nome invalido deveria dar 400, veio %d", resp.Code)
	}
	if len(sonda.acoesFeitas()) != 1 {
		t.Fatalf("nada invalido deveria ter chegado ao docker: %v", sonda.acoesFeitas())
	}
}

func TestAcaoNoContainerRepublicaOEstado(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	inscricao, ch := g.Inscrever()
	defer g.Desinscrever(inscricao)

	if _, err := g.AcaoContainer(context.Background(), "db", "restart"); err != nil {
		t.Fatalf("acao: %v", err)
	}
	// Sem republicar, a tela continuaria mostrando o status antigo depois de parar/reiniciar.
	select {
	case dados := <-ch:
		var evento struct {
			Tipo string `json:"tipo"`
		}
		if json.Unmarshal(dados, &evento); evento.Tipo != "docker" {
			t.Fatalf("esperava evento de docker, veio %q", evento.Tipo)
		}
	case <-time.After(time.Second):
		t.Fatal("a tela nao foi avisada depois da acao")
	}
}

func TestLerPoliticas(t *testing.T) {
	saida := "/db\talways\n/rabbitmq\tunless-stopped\n/solto\t\n"

	got := lerPoliticas(saida)
	if got["db"] != "always" || got["rabbitmq"] != "unless-stopped" {
		t.Fatalf("politicas = %v", got)
	}
	// Container criado sem policy nenhuma vira "no" - e o que o docker considera.
	if got["solto"] != "no" {
		t.Fatalf("policy vazia deveria virar 'no': %v", got)
	}
	if len(lerPoliticas("lixo sem tab")) != 0 {
		t.Fatal("linha sem tabulacao deveria ser ignorada")
	}
}

func TestValidarPolitica(t *testing.T) {
	for _, boa := range []string{"no", "always", "unless-stopped", "on-failure"} {
		if err := validarPolitica(boa); err != nil {
			t.Fatalf("%q deveria ser aceita: %v", boa, err)
		}
	}
	for _, ruim := range []string{"", "sim", "always;rm -rf", "on-failure:5"} {
		if err := validarPolitica(ruim); err == nil {
			t.Fatalf("%q deveria ser recusada", ruim)
		}
	}
}

func TestRotaDePoliticaDeRestart(t *testing.T) {
	h, g := servidorTeste(t, nil)
	sonda := g.docker.(*dockerFake)

	resp := chamar(t, h, http.MethodPost, "/api/docker/containers/db/politica", `{"politica":"no"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("trocar politica: %d %s", resp.Code, resp.Body)
	}
	if got := sonda.acoesFeitas(); len(got) != 1 || got[0] != [2]string{"db", "politica:no"} {
		t.Fatalf("acoes = %v", got)
	}

	if resp := chamar(t, h, http.MethodPost, "/api/docker/containers/db/politica", `{"politica":"talvez"}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("politica invalida deveria dar 400, veio %d", resp.Code)
	}
	// A rota literal /politica nao pode ser engolida pela rota generica /{acao}.
	if len(sonda.acoesFeitas()) != 1 {
		t.Fatalf("politica invalida nao deveria chegar ao docker: %v", sonda.acoesFeitas())
	}
}

func TestRotaDeLogDoContainer(t *testing.T) {
	h, g := servidorTeste(t, nil)
	g.docker.(*dockerFake).log = "linha 1\nlinha 2"

	resp := chamar(t, h, http.MethodGet, "/api/docker/containers/db/logs?linhas=50", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("logs: %d %s", resp.Code, resp.Body)
	}
	var corpo struct {
		Nome  string `json:"nome"`
		Texto string `json:"texto"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.Nome != "db" || !strings.Contains(corpo.Texto, "linha 2") {
		t.Fatalf("corpo = %+v", corpo)
	}

	if resp := chamar(t, h, http.MethodGet, "/api/docker/containers/-f/logs", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("nome invalido deveria dar 400, veio %d", resp.Code)
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
