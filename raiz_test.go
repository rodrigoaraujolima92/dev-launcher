package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolverRaizRespeitaAPrecedencia(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "dev-launcher", "config.json")

	// Sem nada informado: a pasta acima do config (o comportamento antigo continua valendo
	// para quem nunca configurou raiz).
	raiz, origem := resolverRaiz("", "", "", cfg)
	if raiz != filepath.Clean(base) || origem != RaizPadrao {
		t.Fatalf("padrao = %q (%s), queria %q", raiz, origem, base)
	}

	// Config manda no padrao; flag manda em todos.
	raiz, origem = resolverRaiz("", "", filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "doConfig") || origem != RaizDeConfig {
		t.Fatalf("config = %q (%s)", raiz, origem)
	}
	raiz, origem = resolverRaiz("", filepath.Join(base, "doAmbiente"), filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "doAmbiente") || origem != RaizDeAmbiente {
		t.Fatalf("ambiente = %q (%s)", raiz, origem)
	}
	raiz, origem = resolverRaiz(filepath.Join(base, "daFlag"), filepath.Join(base, "doAmbiente"), filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "daFlag") || origem != RaizDeFlag {
		t.Fatalf("flag = %q (%s)", raiz, origem)
	}
}

func TestResolverRaizRelativaUsaAPastaDoConfig(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "dev-launcher", "config.json")

	// ".." e o caso do launcher morando dentro da pasta de projetos - e o que faz o mesmo
	// config abrir em qualquer maquina, sem caminho absoluto de ninguem.
	raiz, _ := resolverRaiz("", "", "..", cfg)
	if raiz != filepath.Clean(base) {
		t.Fatalf("raiz = %q, queria %q", raiz, base)
	}

	// Relativo NAO pode depender do diretorio de trabalho: o launcher e chamado por atalho.
	anterior, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(anterior) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	outra, _ := resolverRaiz("", "", "..", cfg)
	if outra != raiz {
		t.Fatalf("mudar de diretorio mudou a raiz: %q vs %q", outra, raiz)
	}
}

func TestTornarRelativoMantemOConfigPortavel(t *testing.T) {
	raiz := filepath.FromSlash("C:/Users/fulano/projetos")

	dentro := filepath.Join(raiz, "appdaturma", "appdaturma-backend")
	if got := tornarRelativo(raiz, dentro); got != "appdaturma/appdaturma-backend" {
		t.Fatalf("dentro da raiz = %q, queria caminho relativo com barra normal", got)
	}
	// Fora da raiz continua absoluto: virar "../../.." seria pior de ler e de manter.
	fora := filepath.FromSlash("D:/outro/lugar")
	if got := tornarRelativo(raiz, fora); got != fora {
		t.Fatalf("fora da raiz = %q, queria o absoluto", got)
	}
	// Ja relativo so normaliza a barra.
	if got := tornarRelativo(raiz, filepath.FromSlash("photonow/front")); got != "photonow/front" {
		t.Fatalf("ja relativo = %q", got)
	}
	if got := tornarRelativo("", "qualquer"); got != "qualquer" {
		t.Fatalf("sem raiz = %q", got)
	}
}

func TestCaminhosFaltandoAcusaConfigDeOutraMaquina(t *testing.T) {
	g, _, cfg := gerenteTeste(t, nil)

	// A config de teste aponta para "backend" e "front", que nao existem no tempdir.
	faltando := g.CaminhosFaltando()
	if !contem(faltando, "api") || !contem(faltando, "web") {
		t.Fatalf("deveria acusar os dois projetos app: %v", faltando)
	}

	// Criada a pasta, o aviso some.
	if err := os.MkdirAll(filepath.Join(g.raiz, cfg.porID("api").Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if faltando := g.CaminhosFaltando(); contem(faltando, "api") {
		t.Fatalf("com a pasta criada nao deveria mais acusar: %v", faltando)
	}
}

func TestSalvarServicoGuardaCaminhoRelativo(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	// O seletor de pastas devolve caminho absoluto; o config tem que guardar relativo.
	id, err := salvarServico(cfg, EntradaServico{
		Nome: "cabine", Grupo: "web", Tipo: TipoApp, Dir: dentro, Cmd: "npm run dev", Porta: 5173,
	}, raizNav, raizNav)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.porID(id).Dir; got != "photonow/cabine-foto-front" {
		t.Fatalf("dir gravado = %q, queria relativo a raiz", got)
	}
}

// ------------------------------------------------------------------
// salvar pela tela nao pode estragar as raizes do config
// ------------------------------------------------------------------

// launcherTeste monta a arvore do caso comum e sobe o gerente pelo mesmo caminho do main:
//
//	<tmp>/dev-launcher/config.json      (raiz_projetos "..")
//	<tmp>/photonow/cabine-foto-front
//	<tmp>/appdaturma/backend
func launcherTeste(t *testing.T, raizNavegacao string) (g *Gerente, caminhoCfg, base string) {
	t.Helper()
	base = t.TempDir()
	for _, d := range []string{"dev-launcher", "photonow/cabine-foto-front", "appdaturma/backend"} {
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	caminhoCfg = filepath.Join(base, "dev-launcher", "config.json")
	noDisco := configTeste()
	noDisco.RaizProjetos = ".."
	noDisco.RaizNavegacao = raizNavegacao
	if err := salvarConfig(caminhoCfg, noDisco); err != nil {
		t.Fatal(err)
	}

	cfg, err := carregarConfig(caminhoCfg)
	if err != nil {
		t.Fatal(err)
	}
	raiz, _ := resolverRaiz("", "", cfg.RaizProjetos, caminhoCfg)
	raizNav := resolverRaizNavegacao(cfg.RaizNavegacao, raiz, caminhoCfg)
	g = NovoGerente(raiz, raizNav, caminhoCfg, cfg, novoExecFake(cfg.ids(), nil), &dockerFake{}, NovoCacheGit(novoGitFake()))
	return g, caminhoCfg, base
}

// raizesNoDisco le o arquivo cru: o que interessa e o texto gravado, nao o que o launcher
// entenderia dele. ok=false quer dizer que a chave nem esta no arquivo.
func raizesNoDisco(t *testing.T, caminhoCfg string) (projetos, navegacao string, temNavegacao bool) {
	t.Helper()
	dados, err := os.ReadFile(caminhoCfg)
	if err != nil {
		t.Fatal(err)
	}
	var bruto map[string]json.RawMessage
	if err := json.Unmarshal(dados, &bruto); err != nil {
		t.Fatalf("config gravado invalido: %v", err)
	}
	if v, ok := bruto["raiz_projetos"]; ok {
		if err := json.Unmarshal(v, &projetos); err != nil {
			t.Fatal(err)
		}
	}
	v, temNavegacao := bruto["raiz_navegacao"]
	if temNavegacao {
		if err := json.Unmarshal(v, &navegacao); err != nil {
			t.Fatal(err)
		}
	}
	return projetos, navegacao, temNavegacao
}

func TestResolverRaizNavegacao(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "dev-launcher", "config.json")
	raiz := filepath.Join(base, "projetos")

	if got := resolverRaizNavegacao("", raiz, cfg); got != raiz {
		t.Fatalf("vazia = %q, queria a propria raiz %q", got, raiz)
	}
	// Relativa vale contra a pasta do config, igual a raiz_projetos.
	if got := resolverRaizNavegacao("../photonow", raiz, cfg); got != filepath.Join(base, "photonow") {
		t.Fatalf("relativa = %q", got)
	}
	absoluta := filepath.Join(base, "outra")
	if got := resolverRaizNavegacao(absoluta, raiz, cfg); got != absoluta {
		t.Fatalf("absoluta = %q, queria %q", got, absoluta)
	}
}

func TestResolverPortaUI(t *testing.T) {
	casos := []struct{ daFlag, doConfig, querida int }{
		{0, 0, 7010},    // nada informado: a porta padrao
		{0, 7020, 7020}, // o config manda no padrao
		{8000, 7020, 8000},
		{8000, 0, 8000},
	}
	for _, caso := range casos {
		if got := resolverPortaUI(caso.daFlag, caso.doConfig); got != caso.querida {
			t.Fatalf("resolverPortaUI(%d, %d) = %d, queria %d", caso.daFlag, caso.doConfig, got, caso.querida)
		}
	}
}

func TestSalvarConfigNaoEscapaOComando(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "config.json")
	cfg := configTeste()
	// O comando do appdaturma-backend: o "&" do PowerShell virava & a cada salvamento.
	const cmd = "& './run-local.ps1' > saida.log"
	cfg.porID("api").Cmd = cmd
	if err := salvarConfig(caminho, cfg); err != nil {
		t.Fatal(err)
	}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dados), `"cmd": "`+cmd+`"`) {
		t.Fatalf("o comando deveria ir para o arquivo como foi escrito:\n%s", dados)
	}
	lido, err := carregarConfig(caminho)
	if err != nil {
		t.Fatalf("reler config: %v", err)
	}
	if got := lido.porID("api").Cmd; got != cmd {
		t.Fatalf("comando relido = %q, queria %q", got, cmd)
	}
}

func TestClonarConfigNaoPerdeCampo(t *testing.T) {
	cfg := configTeste()
	cfg.RaizProjetos = ".."
	cfg.RaizNavegacao = "../photonow"
	cfg.Perfis = []*Perfil{{ID: "stack", Nome: "stack", Servicos: []string{"db"}}}
	cfg.PerfilAtivo = "stack"

	// Campo novo no Config tem que ser preenchido aqui: o que o clone esquece some do
	// config.json no primeiro salvamento pela tela.
	v := reflect.ValueOf(*cfg)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Fatalf("o campo %s esta vazio no config de teste - preencha para o clone ser conferido", v.Type().Field(i).Name)
		}
	}
	if !reflect.DeepEqual(clonarConfig(cfg), cfg) {
		t.Fatal("o clone saiu diferente do original - falta copiar algum campo em clonarConfig")
	}
}

func TestSalvarPelaTelaPreservaAsRaizesDoConfig(t *testing.T) {
	g, caminhoCfg, base := launcherTeste(t, "")

	conferir := func(depoisDe string) {
		t.Helper()
		projetos, navegacao, temNavegacao := raizesNoDisco(t, caminhoCfg)
		if projetos != ".." {
			t.Fatalf("depois de %s: raiz_projetos = %q, queria \"..\"", depoisDe, projetos)
		}
		// Vazia no arquivo quer dizer "a raiz"; gravar a pasta resolvida prenderia o config
		// a esta maquina.
		if temNavegacao {
			t.Fatalf("depois de %s: o arquivo ganhou raiz_navegacao = %q", depoisDe, navegacao)
		}
		if cfg := g.Config(); cfg.RaizProjetos != ".." || cfg.RaizNavegacao != "" {
			t.Fatalf("depois de %s: config em memoria = %q / %q", depoisDe, cfg.RaizProjetos, cfg.RaizNavegacao)
		}
	}

	if err := g.Editar([]EdicaoServico{{ID: "web", Selecionado: true, Depende: []string{"api"}}}); err != nil {
		t.Fatalf("editar: %v", err)
	}
	conferir("editar")

	// O seletor de pastas manda caminho absoluto, e a cerca continua valendo mesmo com a
	// raiz de navegacao fora do config.
	id, err := g.SalvarServico(EntradaServico{
		Nome: "cabine", Grupo: "web", Tipo: TipoApp, Cmd: "npm run dev", Porta: 5173,
		Dir: filepath.Join(base, "photonow", "cabine-foto-front"),
	})
	if err != nil {
		t.Fatalf("cadastro dentro da raiz: %v", err)
	}
	conferir("cadastrar")
	if got := g.Config().porID(id).Dir; got != "photonow/cabine-foto-front" {
		t.Fatalf("dir gravado = %q, queria relativo a raiz", got)
	}

	_, err = g.SalvarServico(EntradaServico{
		Nome: "de fora", Grupo: "web", Tipo: TipoApp, Cmd: "npm run dev", Porta: 5174, Dir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "caminho livre") {
		t.Fatalf("pasta fora da raiz deveria ser barrada pela cerca: %v", err)
	}

	if err := g.RemoverServico(id); err != nil {
		t.Fatalf("remover: %v", err)
	}
	conferir("remover")
}

func TestRaizNavegacaoRelativaContinuaRelativaNoDisco(t *testing.T) {
	g, caminhoCfg, base := launcherTeste(t, "../photonow")
	cerca := filepath.Join(base, "photonow")

	// Dentro da raiz de projetos mas fora da cerca: a navegacao relativa foi resolvida.
	_, err := g.SalvarServico(EntradaServico{
		Nome: "backend novo", Grupo: "api", Tipo: TipoApp, Cmd: "go run .", Porta: 9001,
		Dir: filepath.Join(base, "appdaturma", "backend"),
	})
	if err == nil || !strings.Contains(err.Error(), "caminho livre") {
		t.Fatalf("pasta fora da raiz de navegacao deveria ser barrada: %v", err)
	}
	if _, err := g.SalvarServico(EntradaServico{
		Nome: "cabine", Grupo: "web", Tipo: TipoApp, Cmd: "npm run dev", Porta: 5173,
		Dir: filepath.Join(cerca, "cabine-foto-front"),
	}); err != nil {
		t.Fatalf("cadastro dentro da cerca: %v", err)
	}

	projetos, navegacao, _ := raizesNoDisco(t, caminhoCfg)
	if projetos != ".." || navegacao != "../photonow" {
		t.Fatalf("raizes no disco = %q / %q, queria como foram escritas", projetos, navegacao)
	}

	// O seletor de pastas recebe a pasta resolvida, nao o texto do config.
	h := somenteLocal(rotas(g, g.raiz, func() {}))
	resp := chamar(t, h, http.MethodGet, "/api/pastas", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("listar a raiz de navegacao: %d %s", resp.Code, resp.Body)
	}
	var listagem struct {
		Caminho string `json:"caminho"`
		Raiz    string `json:"raiz"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &listagem); err != nil {
		t.Fatal(err)
	}
	if listagem.Raiz != cerca || listagem.Caminho != cerca {
		t.Fatalf("seletor abriu em %q (raiz %q), queria %q", listagem.Caminho, listagem.Raiz, cerca)
	}
}
