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

// Casos reais que confundiam a varredura: compose de deploy, Firebase Functions dentro do
// projeto, casca Android, worktree, compose que repete o app e container repetido.
func TestDescobrirNaoConfundePastasDeApoioComProjetos(t *testing.T) {
	compose := "services:\n  db:\n    image: postgres\n    container_name: db\n    ports:\n      - \"5432:5432\"\n" +
		"  api:\n    build: .\n    ports:\n      - \"8083:8083\"\n  migrate:\n    image: migrate/migrate\n"
	raiz := montarPasta(t, map[string]string{
		// backend Nest com compose local, compose de deploy e Firebase Functions
		"loja/backend/package.json":                      `{"name":"loja-backend","scripts":{"start:dev":"nest start --watch"}}`,
		"loja/backend/docker-compose.yml":                "services:\n  redis:\n    image: redis\n    ports:\n      - \"6379:6379\"\n",
		"loja/backend/deploy/homolog/docker-compose.yml": "services:\n  backend:\n    image: x\n  seed:\n    image: x\n",
		"loja/backend/functions/package.json":            `{"scripts":{"serve":"firebase emulators:start"},"dependencies":{"firebase-functions":"^4"}}`,
		// subpasta qualquer com package.json dentro do projeto
		"loja/backend/tools/gerador/package.json": `{"scripts":{"dev":"node gerar.js"}}`,
		// app mobile com casca Android
		"loja/app/package.json":             `{"scripts":{"start":"expo start"}}`,
		"loja/app/android/app/build.gradle": "apply plugin: 'application'",
		// API Go cujo compose sobe o banco, a propria API (build) e uma tarefa
		"loja/contrato/go.mod":             "module c\n",
		"loja/contrato/main.go":            "package main\n",
		"loja/contrato/docker-compose.yml": compose,
		// worktree da mesma API, com o mesmo compose
		"loja-wt/contrato-x/.git":               "gitdir: ../../loja/contrato/.git/worktrees/x\n",
		"loja-wt/contrato-x/go.mod":             "module c\n",
		"loja-wt/contrato-x/main.go":            "package main\n",
		"loja-wt/contrato-x/docker-compose.yml": compose,
		// monorepo: apps/ dentro de um projeto sao sistemas de verdade
		"mono/package.json":           `{"scripts":{"dev":"turbo dev"}}`,
		"mono/apps/site/package.json": `{"scripts":{"dev":"next dev"}}`,
	})
	res, err := descobrirProjetos(raiz)
	if err != nil {
		t.Fatal(err)
	}
	marcado := map[string]bool{}
	motivo := map[string]string{}
	for _, d := range res.Projetos {
		chave := d.Relativo + ":" + d.Nome
		marcado[chave] = d.Sugerido
		motivo[chave] = d.Motivo
		switch {
		case strings.Contains(d.Relativo, "deploy"):
			t.Fatalf("compose de deploy nao entra: %+v", d)
		case strings.Contains(d.Relativo, "functions"):
			t.Fatalf("Firebase Functions nao entra: %+v", d)
		case strings.Contains(d.Relativo, "android"):
			t.Fatalf("casca Android nao entra: %+v", d)
		}
	}
	queria := map[string]bool{
		"loja/backend:loja-backend":          true,
		"loja/backend:redis":                 true, // compose na pasta do proprio app: e o ambiente dele
		"loja/backend/tools/gerador:gerador": false,
		"loja/app:app":                       true,
		"loja/contrato:contrato":             true,
		"loja/contrato:db":                   true,
		"loja/contrato:api":                  false, // build: compila o proprio projeto
		"loja/contrato:migrate":              false, // sem porta: tarefa
		"loja-wt/contrato-x:contrato-x":      false, // worktree
		"mono:mono":                          true,
		"mono/apps/site:site":                true,
	}
	for chave, sugerido := range queria {
		got, ok := marcado[chave]
		if !ok {
			t.Fatalf("faltou %s na lista: %v", chave, marcado)
		}
		if got != sugerido {
			t.Fatalf("%s sugerido=%v, queria %v (motivo %q)", chave, got, sugerido, motivo[chave])
		}
		if !sugerido && motivo[chave] == "" {
			t.Fatalf("%s vem desmarcado sem motivo", chave)
		}
	}
	// O "db" do worktree e o mesmo container do original: aparece uma vez so.
	if _, repetido := marcado["loja-wt/contrato-x:db"]; repetido {
		t.Fatalf("container repetido entrou duas vezes: %v", marcado)
	}
}

func TestComposeQueRepeteOApp(t *testing.T) {
	raiz := montarPasta(t, map[string]string{
		"permissions-api/go.mod":             "module p\n",
		"permissions-api/main.go":            "package main\n",
		"permissions-api/docker-compose.yml": "services:\n  permissions-api:\n    image: empresa/permissions\n    ports:\n      - \"3000:3000\"\n",
	})
	res, err := descobrirProjetos(raiz)
	if err != nil {
		t.Fatal(err)
	}
	d := acharDescoberta(t, res, "permissions-api", "permissions-api")
	for _, p := range res.Projetos {
		if p.Tipo == TipoDocker {
			d = p
		}
	}
	if d.Sugerido || !strings.Contains(d.Motivo, "proprio") {
		t.Fatalf("o container do proprio app deveria vir desmarcado: %+v", d)
	}
}

// ------------------------------------------------------------------
// subprojetos
// ------------------------------------------------------------------

func TestDescobrirMarcaODonoDosSubprojetos(t *testing.T) {
	raiz := montarPasta(t, map[string]string{
		"backend/package.json":               `{"name":"backend","scripts":{"start:dev":"nest start"}}`,
		"backend/docker-compose.yml":         "services:\n  redis:\n    image: redis\n    ports:\n      - \"6379:6379\"\n",
		"backend/tools/gerador/package.json": `{"scripts":{"dev":"node x.js"}}`,
		"infra/docker-compose.yml":           "services:\n  pg:\n    image: postgres\n    ports:\n      - \"5432:5432\"\n",
	})
	res, err := descobrirProjetos(raiz)
	if err != nil {
		t.Fatal(err)
	}
	donos := map[string]string{}
	esperar := map[string]bool{}
	for _, d := range res.Projetos {
		donos[d.Nome] = d.Dono
		esperar[d.Nome] = d.Esperar
	}
	if donos["backend"] != "" || donos["pg"] != "" {
		t.Fatalf("projeto e compose solto nao tem dono: %v", donos)
	}
	if donos["redis"] != "backend" || donos["gerador"] != "backend" {
		t.Fatalf("compose e subpasta do backend sao subprojetos dele: %v", donos)
	}
	if !esperar["redis"] || esperar["gerador"] {
		t.Fatalf("so o redis (infra do backend) deveria sugerir que o backend espere: %v", esperar)
	}
}

func TestSubprojetoAtivarAcompanharEApagarJunto(t *testing.T) {
	cfg := configTeste()
	raiz := t.TempDir()
	compose := filepath.Join(raiz, "backend", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(compose), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compose, []byte("services:\n  redis:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := salvarPerfil(cfg, "", "foco", []string{"api"}); err != nil {
		t.Fatal(err)
	}

	pedido := PedidoSubprojeto{Esperar: true, Entrada: EntradaServico{
		Nome: "redis", Tipo: TipoDocker, Compose: &Compose{Projeto: "backend", Arquivo: compose, Servico: "redis"},
		Container: "backend-redis-1", Pronto: Checagem{Tipo: "porta", Porta: 6379}, Porta: 6379, Timeout: 60,
	}}
	id, err := ativarSubprojeto(cfg, "api", pedido, raiz, "")
	if err != nil {
		t.Fatal(err)
	}
	sub, api := cfg.porID(id), cfg.porID("api")
	if sub.Pai != "api" || sub.Grupo != api.Grupo || !contem(api.Depende, id) {
		t.Fatalf("subprojeto = %+v, depende do pai = %v", sub, api.Depende)
	}
	// Subprojeto de subprojeto nao existe.
	if _, err := ativarSubprojeto(cfg, id, pedido, raiz, ""); err == nil {
		t.Fatal("subprojeto nao pode ter subprojeto")
	}
	// Mover o subprojeto sozinho e recusado; mover o pai leva o subprojeto de grupo.
	if err := moverServico(cfg, id, "web", ""); err == nil {
		t.Fatal("subprojeto nao se move sozinho")
	}
	if err := moverServico(cfg, "api", "web", ""); err != nil {
		t.Fatal(err)
	}
	if cfg.porID(id).Grupo != "web" {
		t.Fatalf("subprojeto deveria acompanhar o pai de grupo: %q", cfg.porID(id).Grupo)
	}
	// Apagar o pai leva o subprojeto, e nada fica apontando para ele.
	if err := removerServico(cfg, "api"); err != nil {
		t.Fatal(err)
	}
	if cfg.porID(id) != nil {
		t.Fatal("subprojeto deveria sair junto com o pai")
	}
	if contem(cfg.perfilPorID("foco").Servicos, id) {
		t.Fatal("perfil ficou citando o subprojeto apagado")
	}
}

func TestRotaSubprojetos(t *testing.T) {
	h, g := servidorTeste(t, nil)
	dir := filepath.Join(g.raiz, "backend") // pasta do projeto "api" no config de teste
	arquivos := map[string]string{
		"go.mod":             "module x\n",
		"main.go":            "package main\n",
		"docker-compose.yml": "services:\n  redis:\n    image: redis\n    ports:\n      - \"6379:6379\"\n",
	}
	for nome, conteudo := range arquivos {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ler := func() []Descoberta {
		t.Helper()
		resp := chamar(t, h, http.MethodGet, "/api/servicos/api/subprojetos", "")
		if resp.Code != http.StatusOK {
			t.Fatalf("subprojetos: %d %s", resp.Code, resp.Body)
		}
		var lista []Descoberta
		if err := json.Unmarshal(resp.Body.Bytes(), &lista); err != nil {
			t.Fatal(err)
		}
		return lista
	}
	lista := ler()
	if len(lista) != 1 || lista[0].Nome != "redis" || lista[0].Ativo != "" || !lista[0].Esperar {
		t.Fatalf("lista = %+v", lista)
	}

	corpo, _ := json.Marshal(PedidoSubprojeto{Esperar: true, Entrada: EntradaServico{
		Nome: "redis", Tipo: TipoDocker, Compose: lista[0].Compose, Container: lista[0].Container,
		Porta: 6379, Pronto: Checagem{Tipo: "porta", Porta: 6379}, Timeout: 60,
	}})
	resp := chamar(t, h, http.MethodPost, "/api/servicos/api/subprojetos", string(corpo))
	if resp.Code != http.StatusOK {
		t.Fatalf("ativar: %d %s", resp.Code, resp.Body)
	}
	if lista = ler(); lista[0].Ativo == "" {
		t.Fatalf("depois de ativar, o item deveria vir como ativo: %+v", lista[0])
	}
	if resp := chamar(t, h, http.MethodGet, "/api/servicos/db/subprojetos", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("servico docker nao tem subprojetos: %d", resp.Code)
	}
}
