package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const saidaPorcelain = `# branch.oid 840a5a4ae543ee551accf72cbcc32aeeb51e9788
# branch.head fix/login
# branch.upstream origin/fix/login
# branch.ab +2 -5
1 .M N... 100644 100644 100644 aaa bbb src/App.vue
1 M. N... 100644 100644 100644 ccc ddd src/main.js
1 MM N... 100644 100644 100644 eee fff src/api.js
2 R. N... 100644 100644 100644 ggg hhh R100 novo.js	velho.js
u UU N... 100644 100644 100644 100644 iii jjj kkk conflito.txt
? nao-rastreado.txt
? outro.txt
`

func TestLerPorcelainV2(t *testing.T) {
	got := lerPorcelainV2(saidaPorcelain)

	if got.Branch != "fix/login" || got.Upstream != "origin/fix/login" {
		t.Fatalf("branch/upstream = %q / %q", got.Branch, got.Upstream)
	}
	if got.Frente != 2 || got.Atras != 5 {
		t.Fatalf("frente/atras = %d / %d, queria 2 / 5", got.Frente, got.Atras)
	}
	// X = indice: "M." e "MM" e o rename "R." contam como preparados.
	if got.Preparados != 3 {
		t.Fatalf("preparados = %d, queria 3", got.Preparados)
	}
	// Y = copia de trabalho: ".M" e "MM".
	if got.Modificados != 2 {
		t.Fatalf("modificados = %d, queria 2", got.Modificados)
	}
	if got.NaoRastreados != 2 {
		t.Fatalf("nao rastreados = %d, queria 2", got.NaoRastreados)
	}
	if got.Conflitos != 1 {
		t.Fatalf("conflitos = %d, queria 1", got.Conflitos)
	}
	if got.Limpo() {
		t.Fatal("com arquivo mexido, Limpo() nao pode ser verdadeiro")
	}
}

func TestLerPorcelainV2RepoLimpoESemUpstream(t *testing.T) {
	got := lerPorcelainV2("# branch.oid abc\n# branch.head main\n")

	if got.Branch != "main" {
		t.Fatalf("branch = %q", got.Branch)
	}
	// Branch local sem upstream nao tem "# branch.ab": nao pode virar atras/frente falso.
	if got.Upstream != "" || got.Frente != 0 || got.Atras != 0 {
		t.Fatalf("sem upstream deveria zerar tudo: %+v", got)
	}
	if !got.Limpo() {
		t.Fatal("sem arquivo listado, o repo esta limpo")
	}
}

func TestLerFrenteAtras(t *testing.T) {
	casos := []struct {
		entrada       string
		frente, atras int
	}{
		{"+2 -5", 2, 5},
		{"+0 -0", 0, 0},
		{"+13 -0", 13, 0},
		{"lixo", 0, 0},
	}
	for _, caso := range casos {
		f, a := lerFrenteAtras(caso.entrada)
		if f != caso.frente || a != caso.atras {
			t.Fatalf("lerFrenteAtras(%q) = %d/%d, queria %d/%d", caso.entrada, f, a, caso.frente, caso.atras)
		}
	}
}

func TestLerShortstat(t *testing.T) {
	casos := []struct {
		entrada           string
		adicoes, remocoes int
	}{
		{" 3 files changed, 87 insertions(+), 1 deletion(-)", 87, 1},
		{" 1 file changed, 12 insertions(+)", 12, 0},
		{" 1 file changed, 4 deletions(-)", 0, 4},
		{"", 0, 0}, // repo limpo: o git nao imprime nada
	}
	for _, caso := range casos {
		a, r := lerShortstat(caso.entrada)
		if a != caso.adicoes || r != caso.remocoes {
			t.Fatalf("lerShortstat(%q) = +%d -%d, queria +%d -%d", caso.entrada, a, r, caso.adicoes, caso.remocoes)
		}
	}
}

func TestLerCommit(t *testing.T) {
	got := lerCommit("840a5a4a\tMerge pull request #92 from app-da-turma/fix\tRodrigo Lima\t30 hours ago\n")
	if got.Hash != "840a5a4a" || got.Autor != "Rodrigo Lima" || got.Quando != "30 hours ago" {
		t.Fatalf("commit = %+v", got)
	}
	if !strings.HasPrefix(got.Resumo, "Merge pull request") {
		t.Fatalf("resumo = %q", got.Resumo)
	}
	if got := lerCommit(""); got.Hash != "" {
		t.Fatalf("linha vazia = %+v", got)
	}
}

func TestLerBranches(t *testing.T) {
	got := lerBranches(`refs/heads/feat/polaroid
refs/heads/main
refs/remotes/origin/HEAD
refs/remotes/origin/feat/polaroid
refs/remotes/origin/fix/login
refs/remotes/origin/main
refs/remotes/upstream/fix/login
`)
	queria := []Branch{
		{Nome: "feat/polaroid"},
		{Nome: "main"},
		// So no remoto: entra uma vez, com o primeiro remoto que o tiver. origin/HEAD e
		// ponteiro, e quem ja tem branch local nao repete.
		{Nome: "fix/login", Remoto: "origin"},
	}
	if len(got) != len(queria) {
		t.Fatalf("branches = %+v, queria %+v", got, queria)
	}
	for i := range queria {
		if got[i] != queria[i] {
			t.Fatalf("branches[%d] = %+v, queria %+v", i, got[i], queria[i])
		}
	}
	if got := lerBranches(""); len(got) != 0 {
		t.Fatalf("repositorio sem branch = %+v", got)
	}
}

// ------------------------------------------------------------------
// cache
// ------------------------------------------------------------------

type gitFake struct {
	mu        sync.Mutex
	estados   map[string]EstadoGit
	consultas map[string]int
	buscas    []string
	erro      error
	demora    time.Duration
	branches  []Branch
	trocas    []Branch
	erroTroca error
}

func novoGitFake() *gitFake {
	return &gitFake{estados: map[string]EstadoGit{}, consultas: map[string]int{}}
}

func (g *gitFake) Estado(_ context.Context, dir string) EstadoGit {
	if g.demora > 0 {
		time.Sleep(g.demora)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.consultas[dir]++
	if e, ok := g.estados[dir]; ok {
		return e
	}
	return EstadoGit{Repo: true, Branch: "main", Upstream: "origin/main"}
}

func (g *gitFake) Buscar(_ context.Context, dir string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.buscas = append(g.buscas, dir)
	return g.erro
}

func (g *gitFake) Branches(_ context.Context, _ string) ([]Branch, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Branch{}, g.branches...), nil
}

// Trocar faz o que o git faria: depois de um switch que deu certo, o estado da pasta passa a
// apontar para o branch novo.
func (g *gitFake) Trocar(_ context.Context, dir string, b Branch) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trocas = append(g.trocas, b)
	if g.erroTroca != nil {
		return g.erroTroca
	}
	g.estados[dir] = EstadoGit{Repo: true, Branch: b.Nome}
	return nil
}

func (g *gitFake) trocasFeitas() []Branch {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Branch{}, g.trocas...)
}

func (g *gitFake) vezes(dir string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.consultas[dir]
}

func (g *gitFake) buscasFeitas() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string{}, g.buscas...)
}

func TestCacheGitEvitaConsultaRepetida(t *testing.T) {
	fake := novoGitFake()
	cache := NovoCacheGit(fake)
	cache.Validade = time.Minute

	cache.Estados(context.Background(), []string{"/a", "/b"}, false)
	cache.Estados(context.Background(), []string{"/a", "/b"}, false)

	if fake.vezes("/a") != 1 || fake.vezes("/b") != 1 {
		t.Fatalf("cache nao segurou: /a=%d /b=%d", fake.vezes("/a"), fake.vezes("/b"))
	}

	// forcar ignora a validade.
	cache.Estados(context.Background(), []string{"/a"}, true)
	if fake.vezes("/a") != 2 {
		t.Fatalf("forcar deveria consultar de novo: %d", fake.vezes("/a"))
	}
}

func TestCacheGitRespeitaValidade(t *testing.T) {
	fake := novoGitFake()
	cache := NovoCacheGit(fake)
	cache.Validade = 20 * time.Millisecond

	cache.Estados(context.Background(), []string{"/a"}, false)
	time.Sleep(40 * time.Millisecond)
	cache.Estados(context.Background(), []string{"/a"}, false)

	if fake.vezes("/a") != 2 {
		t.Fatalf("passada a validade deveria consultar de novo: %d", fake.vezes("/a"))
	}
}

func TestCacheGitConsultaEmParalelo(t *testing.T) {
	fake := novoGitFake()
	fake.demora = 80 * time.Millisecond
	cache := NovoCacheGit(fake)
	cache.Paralelo = 4

	inicio := time.Now()
	got := cache.Estados(context.Background(), []string{"/a", "/b", "/c", "/d"}, false)
	gasto := time.Since(inicio)

	if len(got) != 4 {
		t.Fatalf("esperava 4 respostas, veio %d", len(got))
	}
	// Em serie seriam 320ms; com folga, 240ms ja denuncia serie.
	if gasto > 240*time.Millisecond {
		t.Fatalf("as consultas parecem ter sido em serie (%s)", gasto)
	}
}

func TestCacheGitBuscarInvalida(t *testing.T) {
	fake := novoGitFake()
	cache := NovoCacheGit(fake)
	cache.Validade = time.Minute

	cache.Estados(context.Background(), []string{"/a"}, false)
	if err := cache.Buscar(context.Background(), "/a"); err != nil {
		t.Fatal(err)
	}
	cache.Estados(context.Background(), []string{"/a"}, false)

	if fake.vezes("/a") != 2 {
		t.Fatal("depois do fetch o cache da pasta deveria ter sido descartado")
	}
	if got := fake.buscasFeitas(); len(got) != 1 || got[0] != "/a" {
		t.Fatalf("buscas = %v", got)
	}
}

// ------------------------------------------------------------------
// gerente e rotas
// ------------------------------------------------------------------

func TestEstadosGitSaoPorProjeto(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	fake.estados[filepath.Join(g.raiz, "backend")] = EstadoGit{
		Repo: true, Branch: "fix/login", Atras: 5, Modificados: 2,
	}

	got := g.EstadosGit(context.Background(), false)

	// So os projetos do tipo app entram: db e rabbit sao docker.
	if len(got) != 2 {
		t.Fatalf("esperava api e web, veio %v", got)
	}
	if got["api"].Branch != "fix/login" || got["api"].Atras != 5 {
		t.Fatalf("estado da api = %+v", got["api"])
	}
	if _, tem := got["db"]; tem {
		t.Fatal("servico docker nao deveria consultar git")
	}
}

func TestProjetosNaMesmaPastaCompartilhamUmaConsulta(t *testing.T) {
	g, _, cfg := gerenteTeste(t, nil)
	cfg.porID("web").Dir = cfg.porID("api").Dir // dois projetos, um repositorio so
	fake := g.git.sonda.(*gitFake)

	got := g.EstadosGit(context.Background(), false)

	if fake.vezes(filepath.Join(g.raiz, "backend")) != 1 {
		t.Fatalf("a pasta deveria ser consultada uma vez, foram %d", fake.vezes(filepath.Join(g.raiz, "backend")))
	}
	if got["api"].Branch != got["web"].Branch {
		t.Fatal("os dois projetos deveriam ver o mesmo estado")
	}
}

func TestPublicarGitSoEmiteQuandoMuda(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	g.git.Validade = 0 // sempre consulta

	inscricao, ch := g.Inscrever()
	defer g.Desinscrever(inscricao)

	g.PublicarGit(context.Background(), true)
	if len(ch) != 1 {
		t.Fatalf("a primeira publicacao deveria emitir, veio %d", len(ch))
	}
	<-ch

	g.PublicarGit(context.Background(), true)
	if len(ch) != 0 {
		t.Fatal("estado igual nao deveria virar evento")
	}

	fake.mu.Lock()
	fake.estados[filepath.Join(g.raiz, "backend")] = EstadoGit{Repo: true, Branch: "outra"}
	fake.mu.Unlock()

	g.PublicarGit(context.Background(), true)
	if len(ch) != 1 {
		t.Fatal("mudanca de branch deveria emitir")
	}
}

func TestBuscarGitEmTodosOuNumProjeto(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)

	if erros := g.BuscarGit(context.Background(), nil); len(erros) != 0 {
		t.Fatalf("erros = %v", erros)
	}
	if got := fake.buscasFeitas(); len(got) != 2 {
		t.Fatalf("sem ids deveria buscar nos 2 projetos app, veio %v", got)
	}

	fake.mu.Lock()
	fake.buscas = nil
	fake.erro = errors.New("sem rede")
	fake.mu.Unlock()

	erros := g.BuscarGit(context.Background(), []string{"api"})
	if got := fake.buscasFeitas(); len(got) != 1 {
		t.Fatalf("com id deveria buscar so num projeto, veio %v", got)
	}
	if len(erros) != 1 || !strings.Contains(erros[0].Error(), "api") {
		t.Fatalf("o erro deveria dizer de qual projeto veio: %v", erros)
	}
}

func TestTrocarBranchSoComRepositorioLimpo(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	dir := filepath.Join(g.raiz, "backend")
	fake.branches = []Branch{{Nome: "main"}, {Nome: "fix/login", Remoto: "origin"}}

	// Arquivo nao rastreado tambem conta: a tela mostra como "mexido".
	for _, sujo := range []EstadoGit{
		{Repo: true, Branch: "main", Modificados: 1},
		{Repo: true, Branch: "main", Preparados: 1},
		{Repo: true, Branch: "main", NaoRastreados: 1},
		{Repo: true, Branch: "main", Conflitos: 1},
	} {
		fake.mu.Lock()
		fake.estados[dir] = sujo
		fake.mu.Unlock()
		err := g.TrocarBranch(context.Background(), "api", "fix/login")
		if err == nil || !strings.Contains(err.Error(), "alteracao") {
			t.Fatalf("com %+v deveria recusar citando as alteracoes, veio %v", sujo, err)
		}
	}
	if got := fake.trocasFeitas(); len(got) != 0 {
		t.Fatalf("repositorio sujo nao pode chegar no git switch: %v", got)
	}

	fake.mu.Lock()
	fake.estados[dir] = EstadoGit{Repo: true, Branch: "main"}
	fake.mu.Unlock()
	if err := g.TrocarBranch(context.Background(), "api", "fix/login"); err != nil {
		t.Fatal(err)
	}
	// O branch vai inteiro para a sonda: e o Remoto que diz que o local precisa ser criado.
	if got := fake.trocasFeitas(); len(got) != 1 || got[0] != (Branch{Nome: "fix/login", Remoto: "origin"}) {
		t.Fatalf("trocas = %+v", got)
	}
	if got := g.EstadosGit(context.Background(), false)["api"].Branch; got != "fix/login" {
		t.Fatalf("depois da troca a tela deveria ver fix/login, viu %q", got)
	}
}

func TestTrocarBranchRecusaOQueNaoEstaNaLista(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	fake.branches = []Branch{{Nome: "main"}, {Nome: "dev"}}

	// O nome vem do navegador: so vira argumento do git se o repositorio conhecer.
	for _, nome := range []string{"--force", "nao-existe", ""} {
		if err := g.TrocarBranch(context.Background(), "api", nome); err == nil {
			t.Fatalf("%q deveria ser recusado", nome)
		}
	}
	// Servico docker nao tem repositorio; id desconhecido tambem nao.
	for _, id := range []string{"db", "fantasma"} {
		if err := g.TrocarBranch(context.Background(), id, "dev"); err == nil {
			t.Fatalf("%q nao deveria aceitar troca de branch", id)
		}
	}
	// Pedir o branch em que ja esta nao e erro, e nao roda git a toa.
	if err := g.TrocarBranch(context.Background(), "api", "main"); err != nil {
		t.Fatal(err)
	}
	if got := fake.trocasFeitas(); len(got) != 0 {
		t.Fatalf("nenhuma troca deveria ter chegado ao git: %v", got)
	}
}

func TestTrocarBranchDevolveOErroDoGit(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	fake.branches = []Branch{{Nome: "main"}, {Nome: "dev"}}
	fake.erroTroca = errors.New("fatal: nao deu")

	err := g.TrocarBranch(context.Background(), "api", "dev")
	if err == nil || !strings.Contains(err.Error(), "nao deu") {
		t.Fatalf("o erro do git deveria chegar na tela: %v", err)
	}
}

func TestRotasDeBranch(t *testing.T) {
	h, g := servidorTeste(t, nil)
	fake := g.git.sonda.(*gitFake)
	fake.branches = []Branch{{Nome: "main"}, {Nome: "dev"}}

	resp := chamar(t, h, http.MethodGet, "/api/servicos/api/branches", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	var lista ListaBranches
	if err := json.Unmarshal(resp.Body.Bytes(), &lista); err != nil {
		t.Fatal(err)
	}
	if lista.Atual != "main" || !lista.Limpo || len(lista.Branches) != 2 {
		t.Fatalf("lista = %+v", lista)
	}

	resp = chamar(t, h, http.MethodPost, "/api/servicos/api/branch", `{"branch":"dev"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	var corpo struct {
		Git map[string]EstadoGit `json:"git"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &corpo); err != nil {
		t.Fatal(err)
	}
	if corpo.Git["api"].Branch != "dev" {
		t.Fatalf("a resposta deveria trazer o branch novo: %+v", corpo.Git["api"])
	}

	fake.mu.Lock()
	fake.estados[filepath.Join(g.raiz, "backend")] = EstadoGit{Repo: true, Branch: "dev", Modificados: 3}
	fake.mu.Unlock()
	resp = chamar(t, h, http.MethodPost, "/api/servicos/api/branch", `{"branch":"main"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("repositorio sujo deveria dar 400, deu %d", resp.Code)
	}
}

func TestRotaGitDevolveOsEstados(t *testing.T) {
	h, g := servidorTeste(t, nil)
	g.git.sonda.(*gitFake).estados[filepath.Join(g.raiz, "backend")] = EstadoGit{
		Repo: true, Branch: "main", Adicoes: 87, Remocoes: 1,
	}

	resp := chamar(t, h, http.MethodGet, "/api/git", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d", resp.Code)
	}
	var estados map[string]EstadoGit
	if err := json.Unmarshal(resp.Body.Bytes(), &estados); err != nil {
		t.Fatal(err)
	}
	if estados["api"].Adicoes != 87 || estados["api"].Remocoes != 1 {
		t.Fatalf("estado da api = %+v", estados["api"])
	}
}

func TestRotaBuscarGitRespondeNaHora(t *testing.T) {
	h, g := servidorTeste(t, nil)
	fake := g.git.sonda.(*gitFake)

	resp := chamar(t, h, http.MethodPost, "/api/git/buscar", `{"ids":["api"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	// O fetch roda em segundo plano: espera ele registrar.
	limite := time.Now().Add(3 * time.Second)
	for time.Now().Before(limite) && len(fake.buscasFeitas()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(fake.buscasFeitas()) != 1 {
		t.Fatalf("buscas = %v", fake.buscasFeitas())
	}
}
