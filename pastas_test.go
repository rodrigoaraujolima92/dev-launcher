package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestServicosDoCompose(t *testing.T) {
	conteudo := `version: '3.1'

services:
 db:
   image: postgres:15
   container_name: db
   ports:
     - 5432:5432
   environment:
     POSTGRES_PASSWORD: segredo

 adminer:
   image: adminer
   ports:
     - 7002:8080

networks:
  PRODUCTION:
    external: true
`
	got := servicosDoCompose(conteudo)
	if !reflect.DeepEqual(got, []string{"db", "adminer"}) {
		t.Fatalf("servicos = %v, queria [db adminer]", got)
	}
}

func TestServicosDoComposeIgnoraComentarioEIndentacaoDiferente(t *testing.T) {
	conteudo := `# comentario no topo
services:
    api:
        build: .
        # comentario dentro do servico
        ports:
            - "8080:8080"
    worker:
        build: .
volumes:
    dados:
`
	got := servicosDoCompose(conteudo)
	if !reflect.DeepEqual(got, []string{"api", "worker"}) {
		t.Fatalf("servicos = %v, queria [api worker]", got)
	}
}

func TestServicosDoComposeSemBloco(t *testing.T) {
	if got := servicosDoCompose("volumes:\n  dados:\n"); len(got) != 0 {
		t.Fatalf("sem services: deveria vir vazio, veio %v", got)
	}
	if got := servicosDoCompose(""); len(got) != 0 {
		t.Fatalf("arquivo vazio = %v", got)
	}
}

func TestSugestoesDoPackageJSON(t *testing.T) {
	pacote := []byte(`{
	  "scripts": {
	    "serve": "vue-cli-service serve",
	    "dev": "vite --port 5174",
	    "build": "vue-cli-service build",
	    "test": "jest"
	  }
	}`)

	got := sugestoesDoPackageJSON(pacote)
	if len(got) != 2 {
		t.Fatalf("esperava 2 sugestoes (dev e serve), veio %d: %+v", len(got), got)
	}
	// A ordem segue a lista de interesse: dev antes de serve.
	if got[0].Cmd != "npm run dev" || got[0].Porta != 5174 {
		t.Fatalf("sugestao dev = %+v (porta deveria sair do --port do script)", got[0])
	}
	if got[1].Cmd != "npm run serve" || got[1].Porta != 8080 {
		t.Fatalf("sugestao serve = %+v (vue-cli sem --port assume 8080)", got[1])
	}

	// Script que nao sobe nada nao vira sugestao.
	semServe := []byte(`{"scripts":{"build":"tsc","lint":"eslint ."}}`)
	if got := sugestoesDoPackageJSON(semServe); len(got) != 0 {
		t.Fatalf("nao deveria sugerir nada: %+v", got)
	}
	if got := sugestoesDoPackageJSON([]byte("nao e json")); got != nil {
		t.Fatalf("json quebrado deveria devolver nil, veio %+v", got)
	}
}

func TestListarPastasEsconderRuidoEOrdenar(t *testing.T) {
	base := t.TempDir()
	for _, nome := range []string{"zebra", "alpha", "node_modules", ".git", "Beta"} {
		if err := os.Mkdir(filepath.Join(base, nome), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "arquivo.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	listagem, err := listarPastas(base)
	if err != nil {
		t.Fatal(err)
	}
	nomes := []string{}
	for _, i := range listagem.Itens {
		nomes = append(nomes, i.Nome)
	}
	// node_modules e .git ficam de fora; arquivo nao e pasta; ordem alfabetica sem ligar
	// para maiuscula.
	if !reflect.DeepEqual(nomes, []string{"alpha", "Beta", "zebra"}) {
		t.Fatalf("itens = %v", nomes)
	}
	if listagem.Pai == "" {
		t.Fatal("deveria saber qual e a pasta de cima")
	}
}

func TestListarPastasErroQuandoNaoExiste(t *testing.T) {
	if _, err := listarPastas(filepath.Join(t.TempDir(), "nao-existe")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestMarcasDaPasta(t *testing.T) {
	dir := t.TempDir()
	arquivos := []string{"package.json", "go.mod", "pom.xml", "docker-compose.yml", "run-local.ps1"}
	for _, nome := range arquivos {
		if err := os.WriteFile(filepath.Join(dir, nome), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	marcas := marcasDaPasta(dir)
	for _, querida := range []string{"node", "go", "maven", "compose", "script", "git"} {
		if !contem(marcas, querida) {
			t.Fatalf("faltou a marca %q em %v", querida, marcas)
		}
	}
}

func TestInspecionarPropoeComoRodar(t *testing.T) {
	dir := t.TempDir()
	escrever := func(nome, conteudo string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	escrever("package.json", `{"scripts":{"serve":"vue-cli-service serve"}}`)
	escrever("run-local.ps1", "# sobe local")
	escrever("docker-compose.yml", "services:\n  db:\n    image: postgres\n")

	insp, err := inspecionar(dir)
	if err != nil {
		t.Fatal(err)
	}

	cmds := []string{}
	for _, s := range insp.Sugestoes {
		cmds = append(cmds, s.Cmd)
	}
	if !contem(cmds, "npm run serve") {
		t.Fatalf("faltou o script do package.json: %v", cmds)
	}
	if !contem(cmds, "& './run-local.ps1'") {
		t.Fatalf("faltou o .ps1 da raiz do projeto: %v", cmds)
	}
	if len(insp.Composes) != 1 || !reflect.DeepEqual(insp.Composes[0].Servicos, []string{"db"}) {
		t.Fatalf("compose = %+v", insp.Composes)
	}
}

func TestInspecionarProjetoGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".air.toml"), []byte("[build]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	insp, err := inspecionar(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmds := []string{}
	for _, s := range insp.Sugestoes {
		cmds = append(cmds, s.Cmd)
	}
	// Com cmd/api no lugar, a sugestao aponta para la em vez da raiz.
	if !contem(cmds, "go run ./cmd/api") {
		t.Fatalf("sugestoes = %v", cmds)
	}
	if !contem(cmds, "air") {
		t.Fatalf("com .air.toml deveria sugerir air: %v", cmds)
	}
}
