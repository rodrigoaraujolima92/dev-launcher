package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// montarPasta cria arquivos a partir de um mapa caminho -> conteudo.
func montarPasta(t *testing.T, arquivos map[string]string) string {
	t.Helper()
	raiz := t.TempDir()
	for caminho, conteudo := range arquivos {
		completo := filepath.Join(raiz, filepath.FromSlash(caminho))
		if err := os.MkdirAll(filepath.Dir(completo), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(completo, []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return raiz
}

func acharDescoberta(t *testing.T, res *ResultadoDescoberta, relativo, nome string) Descoberta {
	t.Helper()
	for _, d := range res.Projetos {
		if d.Relativo == relativo && (nome == "" || d.Nome == nome) {
			return d
		}
	}
	nomes := []string{}
	for _, d := range res.Projetos {
		nomes = append(nomes, d.Relativo+":"+d.Nome)
	}
	t.Fatalf("nao achou %s:%s em %v", relativo, nome, nomes)
	return Descoberta{}
}

func TestDescobrirVariosSistemasNaMesmaPasta(t *testing.T) {
	raiz := montarPasta(t, map[string]string{
		// front com yarn e porta no vite.config
		"front/package.json":   `{"name":"@empresa/portal","scripts":{"dev":"vite","build":"vite build"}}`,
		"front/yarn.lock":      "",
		"front/vite.config.ts": "export default { server: { port: 3001 } }",
		// api Go com programa em cmd/api e porta no codigo
		"api/go.mod":          "module api\n",
		"api/cmd/api/main.go": "package main\nfunc main() { http.ListenAndServe(\":7003\", nil) }\n",
		"api/cmd/migrar/x.go": "package main\n",
		// biblioteca Go: nao e projeto rodavel
		"lib/go.mod":  "module lib\n",
		"lib/util.go": "package lib\n",
		// Spring com wrapper e porta no properties
		"conta/pom.xml":  "<project><parent>spring-boot-starter-parent</parent></project>",
		"conta/mvnw.cmd": "",
		"conta/src/main/resources/application.properties": "server.port=8081\n",
		// multi-modulo: o pai nao entra, o modulo sim
		"erp/pom.xml":     "<project><modules><module>web</module></modules></project>",
		"erp/web/pom.xml": "<project>spring-boot</project>",
		"erp/web/src/main/resources/application.yml": "spring:\n  app: x\nserver:\n  port: 9090\n",
		// .NET web com launchSettings
		"dotnet/Api/Api.csproj":                     `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`,
		"dotnet/Api/Properties/launchSettings.json": `{"profiles":{"x":{"applicationUrl":"https://localhost:7100;http://localhost:5100"}}}`,
		"dotnet/Lib/Lib.csproj":                     `<Project Sdk="Microsoft.NET.Sdk"></Project>`,
		// FastAPI
		"ia/requirements.txt": "fastapi\nuvicorn\n",
		"ia/main.py":          "app = FastAPI()\n",
		"ia/.env":             "PORT=8010\n",
		// script de subida e script qualquer
		"robo/run-local.ps1": "# sobe\n",
		"util/limpar.ps1":    "# nao sobe nada\n",
		// dependencia instalada nunca entra
		"front/node_modules/vite/package.json": `{"scripts":{"dev":"x"}}`,
		// compose na raiz
		"docker-compose.yml": strings.Join([]string{
			"name: loja",
			"services:",
			"  db:",
			"    image: postgres:16",
			"    container_name: loja-pg",
			"    ports:",
			`      - "${DB_PORT:-5433}:5432"`,
			"  fila:",
			"    image: rabbitmq:3",
			"    ports:",
			"      - target: 5672",
			"        published: 5672",
			"  worker:",
			"    build: .",
			"",
		}, "\n"),
	})

	res, err := descobrirProjetos(raiz)
	if err != nil {
		t.Fatal(err)
	}

	front := acharDescoberta(t, res, "front", "")
	if front.Nome != "portal" || front.Cmd != "yarn dev" || front.Porta != 3001 || front.Tecnologia != "node" {
		t.Fatalf("front = %+v", front)
	}
	api := acharDescoberta(t, res, "api", "")
	if api.Cmd != "go run ./cmd/api" || api.Porta != 7003 || len(api.Comandos) != 2 {
		t.Fatalf("api = %+v", api)
	}
	conta := acharDescoberta(t, res, "conta", "")
	if conta.Cmd != `.\mvnw spring-boot:run` || conta.Porta != 8081 {
		t.Fatalf("conta = %+v", conta)
	}
	web := acharDescoberta(t, res, "erp/web", "")
	if web.Cmd != "mvn spring-boot:run" || web.Porta != 9090 {
		t.Fatalf("modulo maven = %+v", web)
	}
	dotnet := acharDescoberta(t, res, "dotnet/Api", "")
	if dotnet.Nome != "Api" || dotnet.Cmd != "dotnet run" || dotnet.Porta != 5100 {
		t.Fatalf("dotnet = %+v", dotnet)
	}
	ia := acharDescoberta(t, res, "ia", "")
	if !strings.Contains(ia.Cmd, "uvicorn main:app") || ia.Porta != 8010 {
		t.Fatalf("fastapi = %+v", ia)
	}
	robo := acharDescoberta(t, res, "robo", "")
	if robo.Cmd != "& './run-local.ps1'" || robo.Tecnologia != "script" {
		t.Fatalf("script de subida = %+v", robo)
	}

	db := acharDescoberta(t, res, ".", "db")
	if db.Tipo != TipoDocker || db.Container != "loja-pg" || db.Porta != 5433 || db.Compose.Projeto != "loja" {
		t.Fatalf("db = %+v / %+v", db, db.Compose)
	}
	fila := acharDescoberta(t, res, ".", "fila")
	if fila.Porta != 5672 || fila.Container != "loja-fila-1" {
		t.Fatalf("fila = %+v", fila)
	}
	worker := acharDescoberta(t, res, ".", "worker")
	if worker.Porta != 0 {
		t.Fatalf("worker sem ports nao tem porta: %+v", worker)
	}

	for _, d := range res.Projetos {
		switch {
		case d.Relativo == "lib", d.Relativo == "erp", d.Relativo == "dotnet/Lib", d.Relativo == "util":
			t.Fatalf("%s nao e projeto rodavel: %+v", d.Relativo, d)
		case strings.Contains(d.Relativo, "node_modules"):
			t.Fatalf("node_modules nunca entra: %+v", d)
		}
	}
}

func TestPortaPublicada(t *testing.T) {
	casos := map[string]int{
		`"5432:5432"`:            5432,
		"127.0.0.1:8080:80/tcp":  8080,
		"${API_PORT:-7003}:7003": 7003,
		"8000-8010:8000-8010":    8000,
		"5672":                   5672,
		"${SEM_PADRAO}:80":       0,
		"nada":                   0,
	}
	for entrada, queria := range casos {
		if got := portaPublicada(entrada); got != queria {
			t.Fatalf("portaPublicada(%q) = %d, queria %d", entrada, got, queria)
		}
	}
}

func TestLerComposeSemNomeUsaAPasta(t *testing.T) {
	raiz := montarPasta(t, map[string]string{
		"Minha Loja/docker-compose.yml":          "services:\n  redis:\n    image: redis\n    ports: [\"6379:6379\"]\n",
		"Minha Loja/docker-compose.override.yml": "services:\n  redis:\n    ports: [\"6380:6379\"]\n",
	})
	res, err := descobrirProjetos(filepath.Join(raiz, "Minha Loja"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projetos) != 1 {
		t.Fatalf("o override nao deveria duplicar o servico: %+v", res.Projetos)
	}
	d := res.Projetos[0]
	if d.Compose.Projeto != "minhaloja" || d.Container != "minhaloja-redis-1" || d.Porta != 6379 {
		t.Fatalf("redis = %+v / %+v", d, d.Compose)
	}
}

// ------------------------------------------------------------------
// importacao
// ------------------------------------------------------------------

func entradaApp(nome, dir, cmd string, porta int) EntradaServico {
	e := EntradaServico{Nome: nome, Tipo: TipoApp, Dir: dir, Cmd: cmd, Porta: porta, Timeout: 120, Modo: ModoGerenciado}
	if porta > 0 {
		e.Pronto = Checagem{Tipo: "porta", Porta: porta}
	} else {
		e.Pronto = Checagem{Tipo: "processo"}
	}
	return e
}

func TestImportarProjetosEmGrupoNovoComPerfilEDependencias(t *testing.T) {
	cfg := configTeste()
	raiz := t.TempDir()
	for _, p := range []string{"loja/front", "loja/api"} {
		if err := os.MkdirAll(filepath.Join(raiz, filepath.FromSlash(p)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	compose := filepath.Join(raiz, "loja", "docker-compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  db:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := salvarPerfil(cfg, "", "trabalho", []string{"db"}); err != nil {
		t.Fatal(err)
	}

	pedido := PedidoImportar{
		GrupoNovo: "loja", Perfil: "trabalho", EsperarContainers: true,
		Projetos: []EntradaServico{
			entradaApp("front", filepath.Join(raiz, "loja", "front"), "npm run dev", 5173),
			entradaApp("api", filepath.Join(raiz, "loja", "api"), "go run .", 0),
			{Nome: "db", Tipo: TipoDocker, Compose: &Compose{Projeto: "loja", Arquivo: compose, Servico: "db"},
				Container: "loja-db-1", Pronto: Checagem{Tipo: "comando", Cmd: []string{"docker", "top", "loja-db-1"}}, Timeout: 120},
		},
	}
	ids, err := importarProjetos(cfg, pedido, raiz, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("ids = %v", ids)
	}
	grupo := cfg.grupoPorID("loja")
	if grupo == nil {
		t.Fatalf("grupo novo nao foi criado: %+v", cfg.Grupos)
	}
	// "api" e "db" ja existiam no config de teste: os ids novos ganham sufixo.
	front, api, db := cfg.porID(ids[0]), cfg.porID(ids[1]), cfg.porID(ids[2])
	for _, s := range []*Servico{front, api, db} {
		if s.Grupo != "loja" || !s.Selecionado {
			t.Fatalf("%s deveria estar no grupo loja e marcado (perfil aberto): %+v", s.ID, s)
		}
	}
	if !contem(front.Depende, db.ID) || !contem(api.Depende, db.ID) || len(db.Depende) != 0 {
		t.Fatalf("apps deveriam esperar o container: front=%v api=%v", front.Depende, api.Depende)
	}
	if front.Dir != "loja/front" && front.Dir != filepath.FromSlash("loja/front") {
		t.Fatalf("pasta deveria ser gravada relativa a raiz: %q", front.Dir)
	}
	perfil := cfg.perfilPorID("trabalho")
	for _, id := range ids {
		if !contem(perfil.Servicos, id) {
			t.Fatalf("%s deveria ter entrado no perfil: %v", id, perfil.Servicos)
		}
	}
}

func TestImportarEntraTudoOuNada(t *testing.T) {
	cfg := configTeste()
	raiz := t.TempDir()
	antes := len(cfg.Servicos)
	pedido := PedidoImportar{
		Grupo: "api",
		Projetos: []EntradaServico{
			entradaApp("ok", raiz, "go run .", 7100),
			entradaApp("quebrado", filepath.Join(raiz, "nao-existe"), "go run .", 7101),
		},
	}
	_, err := importarProjetos(cfg, pedido, raiz, "")
	if err == nil || !strings.Contains(err.Error(), "quebrado") {
		t.Fatalf("o erro deveria citar o projeto que falhou: %v", err)
	}
	if len(cfg.Servicos) != antes {
		t.Fatalf("nada deveria ter entrado: %d servicos", len(cfg.Servicos))
	}
	if _, err := importarProjetos(cfg, PedidoImportar{Grupo: "api"}, raiz, ""); err == nil {
		t.Fatal("importar lista vazia deveria falhar")
	}
}

// ------------------------------------------------------------------
// perfis
// ------------------------------------------------------------------

func TestPerfilTodosEPerfilComoVisao(t *testing.T) {
	cfg := configTeste()
	if _, err := salvarPerfil(cfg, "", "so a api", []string{"api"}); err != nil {
		t.Fatal(err)
	}
	// Salvar abre o perfil e a selecao vira exatamente a dele.
	if cfg.PerfilAtivo != "so-a-api" {
		t.Fatalf("perfil ativo = %q", cfg.PerfilAtivo)
	}
	for _, s := range cfg.Servicos {
		if s.Selecionado != (s.ID == "api") {
			t.Fatalf("%s selecionado = %v", s.ID, s.Selecionado)
		}
	}
	// TODOS mostra tudo e nao mexe na selecao.
	if err := aplicarPerfil(cfg, PerfilTodos); err != nil {
		t.Fatal(err)
	}
	if cfg.PerfilAtivo != "" || !cfg.porID("api").Selecionado || cfg.porID("db").Selecionado {
		t.Fatalf("todos: ativo=%q", cfg.PerfilAtivo)
	}
	// Ninguem pode criar um perfil com o id reservado.
	id, err := salvarPerfil(cfg, "", "Todos", []string{"db"})
	if err != nil || id == PerfilTodos {
		t.Fatalf("perfil chamado Todos ganhou id %q (%v)", id, err)
	}
}

func TestProjetoNovoEntraNoPerfilAberto(t *testing.T) {
	cfg := configTeste()
	raiz := t.TempDir()
	if _, err := salvarPerfil(cfg, "", "foco", []string{"db"}); err != nil {
		t.Fatal(err)
	}
	e := entradaApp("novo", raiz, "go run .", 7200)
	e.Perfil = "foco"
	id, err := salvarServico(cfg, e, raiz, "")
	if err != nil {
		t.Fatal(err)
	}
	if !contem(cfg.perfilPorID("foco").Servicos, id) {
		t.Fatalf("projeto novo deveria entrar no perfil aberto: %v", cfg.perfilPorID("foco").Servicos)
	}
}

func TestRotasDescobrirEImportar(t *testing.T) {
	h, g := servidorTeste(t, nil)
	pasta := filepath.Join(g.raiz, "novo-sistema")
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pasta, "package.json"), []byte(`{"scripts":{"dev":"vite --port 5199"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A pasta "backend" ja e do projeto "api" no config de teste.
	if err := os.MkdirAll(filepath.Join(g.raiz, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.raiz, "backend", "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.raiz, "backend", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, h, http.MethodGet, "/api/descobrir?livre=1&caminho="+url.QueryEscape(g.raiz), "")
	if resp.Code != http.StatusOK {
		t.Fatalf("descobrir: %d %s", resp.Code, resp.Body)
	}
	var res ResultadoDescoberta
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if d := acharDescoberta(t, &res, "backend", ""); d.Cadastrado != "api" {
		t.Fatalf("backend deveria aparecer como ja cadastrado: %+v", d)
	}
	novo := acharDescoberta(t, &res, "novo-sistema", "")
	if novo.Cadastrado != "" || novo.Porta != 5199 {
		t.Fatalf("novo-sistema = %+v", novo)
	}

	corpo, _ := json.Marshal(PedidoImportar{
		GrupoNovo: "novos", CaminhoLivre: true,
		Projetos: []EntradaServico{entradaApp(novo.Nome, novo.Dir, novo.Cmd, novo.Porta)},
	})
	resp = chamar(t, h, http.MethodPost, "/api/importar", string(corpo))
	if resp.Code != http.StatusOK {
		t.Fatalf("importar: %d %s", resp.Code, resp.Body)
	}
	if s := g.Config().porID("novo-sistema"); s == nil || s.Grupo != "novos" {
		t.Fatalf("projeto importado = %+v", s)
	}
}
