package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ------------------------------------------------------------------
// leitura das saidas do git (sem repositorio)
// ------------------------------------------------------------------

func TestLerArquivosZ(t *testing.T) {
	saida := strings.Join([]string{
		"1 .M N... 100644 100644 100644 aaa aaa src/main.go",
		"1 A. N... 000000 100644 100644 000 bbb pasta com espaco/novo.txt",
		"2 R. N... 100644 100644 100644 ccc ccc R100 nome-novo.go", "nome-velho.go",
		"u UU N... 100644 100644 100644 100644 d1 d2 d3 conflito.go",
		"? ação.md",
		"",
	}, "\x00")
	got := lerArquivosZ(saida)
	queria := []ArquivoGit{
		{Caminho: "ação.md", Trabalho: "?"},
		{Caminho: "conflito.go", Indice: "U", Trabalho: "U", Conflito: true},
		{Caminho: "nome-novo.go", Origem: "nome-velho.go", Indice: "R"},
		{Caminho: "pasta com espaco/novo.txt", Indice: "A"},
		{Caminho: "src/main.go", Trabalho: "M"},
	}
	if len(got) != len(queria) {
		t.Fatalf("arquivos = %+v", got)
	}
	for i := range queria {
		if got[i] != queria[i] {
			t.Fatalf("arquivo[%d] = %+v, queria %+v", i, got[i], queria[i])
		}
	}
}

func TestLerStashesELog(t *testing.T) {
	st := lerStashes("stash@{0}\x1fOn main: wip\x1f2 hours ago\nstash@{1}\x1fWIP on dev: abc x\x1f3 days ago\n")
	if len(st) != 2 || st[1].Indice != 1 || st[0].Resumo != "On main: wip" {
		t.Fatalf("stashes = %+v", st)
	}
	log := lerLog("h1\x1fh1c\x1fprimeiro\x1fAna\x1f1 hour ago\x1fHEAD -> main, origin/main\x1fp1\x1e\n" +
		"h2\x1fh2c\x1fMerge\x1fBia\x1f2 hours ago\x1f\x1fp1 p2\x1e")
	if len(log) != 2 || len(log[0].Refs) != 2 || log[0].Merge || !log[1].Merge {
		t.Fatalf("log = %+v", log)
	}
}

func TestLerBranchesInfo(t *testing.T) {
	saida := strings.Join([]string{
		"refs/heads/main\x1forigin/main\x1f[ahead 2, behind 1]\x1f1 hour ago\x1fcommit a\x1faaa\x1f*",
		"refs/heads/velho\x1forigin/velho\x1f[gone]\x1f3 weeks ago\x1fcommit b\x1fbbb\x1f ",
		"refs/remotes/origin/HEAD\x1f\x1f\x1f1 hour ago\x1fcommit a\x1faaa\x1f ",
		"refs/remotes/origin/main\x1f\x1f\x1f1 hour ago\x1fcommit a\x1faaa\x1f ",
		"refs/remotes/origin/feat/x\x1f\x1f\x1f2 days ago\x1fcommit c\x1fccc\x1f ",
	}, "\n")
	got := lerBranchesInfo(saida)
	if len(got) != 3 {
		t.Fatalf("branches = %+v", got)
	}
	if !got[0].Atual || got[0].Frente != 2 || got[0].Atras != 1 {
		t.Fatalf("main = %+v", got[0])
	}
	if !got[1].Sumiu {
		t.Fatalf("velho deveria estar com upstream sumido: %+v", got[1])
	}
	if got[2].Nome != "feat/x" || got[2].Remoto != "origin" || got[2].Ref != "refs/remotes/origin/feat/x" {
		t.Fatalf("branch so no remoto = %+v", got[2])
	}
}

func TestLerBranchesInfoPrincipal(t *testing.T) {
	// O padrao do remoto (origin/HEAD -> develop) e main/master contam como principal.
	saida := strings.Join([]string{
		"refs/heads/develop\x1forigin/develop\x1f\x1f1 hour ago\x1fa\x1faaa\x1f*\x1f",
		"refs/heads/main\x1f\x1f\x1f1 day ago\x1fb\x1fbbb\x1f \x1f",
		"refs/heads/feat/y\x1f\x1f\x1f1 day ago\x1fc\x1fccc\x1f \x1f",
		"refs/remotes/origin/HEAD\x1f\x1f\x1f1 hour ago\x1fa\x1faaa\x1f \x1frefs/remotes/origin/develop",
		"refs/remotes/origin/develop\x1f\x1f\x1f1 hour ago\x1fa\x1faaa\x1f \x1f",
		"refs/remotes/origin/master\x1f\x1f\x1f1 week ago\x1fd\x1fddd\x1f \x1f",
	}, "\n")
	principal := map[string]bool{}
	for _, b := range lerBranchesInfo(saida) {
		principal[b.Nome] = b.Principal
	}
	want := map[string]bool{"develop": true, "main": true, "master": true, "feat/y": false}
	for nome, p := range want {
		if got, ok := principal[nome]; !ok || got != p {
			t.Fatalf("%s: principal = %v (presente %v), queria %v", nome, got, ok, p)
		}
	}
}

func TestPainelGitNuncaApagaPrincipalNemRemoto(t *testing.T) {
	g, dir, remoto := gerenteComRepo(t)
	ctx := context.Background()

	// O remoto ganha um branch "develop" que vira o padrao dele, e um branch de feature.
	outro := t.TempDir()
	rodar(t, outro, "clone", "-q", remoto, ".")
	rodar(t, outro, "push", "-q", "origin", "main:develop", "main:feat/remota")
	rodar(t, remoto, "symbolic-ref", "HEAD", "refs/heads/develop")
	rodar(t, dir, "fetch", "-q", "origin")
	rodar(t, dir, "remote", "set-head", "origin", "--auto")
	rodar(t, dir, "branch", "-q", "develop", "origin/develop")
	rodar(t, dir, "switch", "-q", "-c", "feat/local")

	lista, err := g.BranchesInfoGit(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	principal := map[string]bool{}
	for _, b := range lista {
		principal[b.Nome] = b.Principal
	}
	if !principal["main"] || !principal["develop"] || principal["feat/local"] || principal["feat/remota"] {
		t.Fatalf("marca de principal errada: %+v", lista)
	}

	// Principal nao sai nem forcando.
	for _, nome := range []string{"main", "develop"} {
		for _, forcar := range []bool{false, true} {
			_, _, err := g.AcaoGit(ctx, "api", "apagar-branch", PedidoGit{Nome: nome, Forcar: forcar})
			if err == nil || !strings.Contains(err.Error(), "principal") {
				t.Fatalf("apagar %s (forcar=%v) deveria ser recusado: %v", nome, forcar, err)
			}
		}
		rodar(t, dir, "rev-parse", "--verify", "-q", "refs/heads/"+nome)
	}

	// Branch que so existe no remoto tambem nao, nem forcando.
	for _, forcar := range []bool{false, true} {
		_, _, err := g.AcaoGit(ctx, "api", "apagar-branch", PedidoGit{Nome: "feat/remota", Forcar: forcar})
		if err == nil || !strings.Contains(err.Error(), "remoto") {
			t.Fatalf("apagar branch remoto (forcar=%v) deveria ser recusado: %v", forcar, err)
		}
	}
	if got := rodar(t, remoto, "branch", "--list", "feat/remota", "develop", "main"); len(strings.Split(got, "\n")) != 3 {
		t.Fatalf("o remoto perdeu branch: %q", got)
	}
}

func TestLerPRs(t *testing.T) {
	saida := `[
	 {"number":12,"title":"Login novo","author":{"login":"ana"},"headRefName":"feat/login","baseRefName":"main",
	  "url":"https://github.com/o/r/pull/12","isDraft":false,"createdAt":"2026-10-01T10:00:00Z",
	  "reviewDecision":"APPROVED","additions":10,"deletions":2,"mergeable":"MERGEABLE",
	  "statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]},
	 {"number":13,"title":"WIP","author":{"login":"bia"},"headRefName":"wip","baseRefName":"main",
	  "url":"u","isDraft":true,"createdAt":"2026-10-02T10:00:00Z","reviewDecision":"","additions":1,"deletions":0,
	  "mergeable":"CONFLICTING","statusCheckRollup":[{"status":"IN_PROGRESS","conclusion":""}]},
	 {"number":14,"title":"Sem CI","author":{"login":"caio"},"headRefName":"x","baseRefName":"main",
	  "url":"u","isDraft":false,"createdAt":"2026-10-03T10:00:00Z","statusCheckRollup":[]}
	]`
	prs, err := lerPRs(saida)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 3 {
		t.Fatalf("prs = %+v", prs)
	}
	if prs[0].Checks != "falhou" || prs[0].Autor != "ana" || prs[0].Revisao != "APPROVED" {
		t.Fatalf("pr 12 = %+v", prs[0])
	}
	if prs[1].Checks != "pendente" || !prs[1].Rascunho || !prs[1].Conflitos {
		t.Fatalf("pr 13 = %+v", prs[1])
	}
	if prs[2].Checks != "" {
		t.Fatalf("pr sem CI nao tem status de check: %+v", prs[2])
	}
}

// ------------------------------------------------------------------
// repositorio de verdade
// ------------------------------------------------------------------

func precisaGit(t *testing.T) {
	t.Helper()
	if !comandoExiste("git") {
		t.Skip("git nao esta no PATH")
	}
}

func rodar(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	saida, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, saida)
	}
	return strings.TrimSpace(string(saida))
}

func escrever(t *testing.T, dir, nome, conteudo string) {
	t.Helper()
	caminho := filepath.Join(dir, nome)
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// iniciarRepo cria um repositorio com um commit e um remoto "origin" (bare, numa pasta
// temporaria) ja acompanhando o main.
func iniciarRepo(t *testing.T, dir string) (remoto string) {
	t.Helper()
	precisaGit(t)
	remoto = t.TempDir()
	rodar(t, remoto, "init", "-q", "--bare", "-b", "main")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rodar(t, dir, "init", "-q", "-b", "main")
	rodar(t, dir, "config", "user.name", "Teste")
	rodar(t, dir, "config", "user.email", "teste@exemplo.com")
	rodar(t, dir, "config", "commit.gpgsign", "false")
	rodar(t, dir, "config", "core.autocrlf", "false")
	escrever(t, dir, "leia.md", "linha 1\n")
	rodar(t, dir, "add", "-A")
	rodar(t, dir, "commit", "-q", "-m", "primeiro")
	rodar(t, dir, "remote", "add", "origin", remoto)
	rodar(t, dir, "push", "-q", "-u", "origin", "main")
	return remoto
}

func gerenteComRepo(t *testing.T) (*Gerente, string, string) {
	t.Helper()
	precisaGit(t)
	g, _, _ := gerenteTeste(t, nil)
	g.git = NovoCacheGit(SondaGitSO{}) // estado lido do git de verdade
	dir := filepath.Join(g.raiz, "backend")
	remoto := iniciarRepo(t, dir)
	return g, dir, remoto
}

func acao(t *testing.T, g *Gerente, nome string, p PedidoGit) PainelGit {
	t.Helper()
	_, painel, err := g.AcaoGit(context.Background(), "api", nome, p)
	if err != nil {
		t.Fatalf("%s: %v", nome, err)
	}
	return painel
}

func TestPainelGitFluxoCompleto(t *testing.T) {
	g, dir, _ := gerenteComRepo(t)
	ctx := context.Background()

	escrever(t, dir, "leia.md", "linha 1\nlinha 2\n")
	escrever(t, dir, "src/novo arquivo.go", "package x\n")

	painel, err := g.PainelGit(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(painel.Arquivos) != 2 || painel.Estado.Branch != "main" {
		t.Fatalf("painel = %+v", painel)
	}

	// Diff da copia de trabalho e do arquivo nao rastreado (contra o vazio).
	diff, _, err := g.DiffGit(ctx, "api", "leia.md", false)
	if err != nil || !strings.Contains(diff, "+linha 2") {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	diff, _, err = g.DiffGit(ctx, "api", "src/novo arquivo.go", false)
	if err != nil || !strings.Contains(diff, "+package x") {
		t.Fatalf("diff do nao rastreado = %q, %v", diff, err)
	}

	// Commit sem nada preparado e recusado.
	if _, _, err := g.AcaoGit(ctx, "api", "commit", PedidoGit{Mensagem: "x"}); err == nil {
		t.Fatal("commit sem arquivo preparado deveria falhar")
	}

	painel = acao(t, g, "preparar", PedidoGit{Caminhos: []string{"leia.md"}})
	for _, a := range painel.Arquivos {
		if a.Caminho == "leia.md" && a.Indice != "M" {
			t.Fatalf("leia.md deveria estar preparado: %+v", a)
		}
	}
	diff, _, err = g.DiffGit(ctx, "api", "leia.md", true)
	if err != nil || !strings.Contains(diff, "+linha 2") {
		t.Fatalf("diff preparado = %q, %v", diff, err)
	}

	// Despreparar e preparar de novo.
	painel = acao(t, g, "despreparar", PedidoGit{Todos: true})
	for _, a := range painel.Arquivos {
		if a.Indice != "" {
			t.Fatalf("nada deveria continuar preparado: %+v", a)
		}
	}
	acao(t, g, "preparar", PedidoGit{Caminhos: []string{"leia.md"}})

	// Mensagem comecando com "-" vai inteira como valor de -m.
	painel = acao(t, g, "commit", PedidoGit{Mensagem: "--amend nao e opcao aqui"})
	if len(painel.Arquivos) != 1 || painel.Estado.Frente != 1 {
		t.Fatalf("depois do commit = %+v", painel)
	}
	if got := rodar(t, dir, "log", "-1", "--format=%s"); got != "--amend nao e opcao aqui" {
		t.Fatalf("mensagem do commit = %q", got)
	}

	historico, err := g.HistoricoGit(ctx, "api", "")
	if err != nil || len(historico) != 2 || !historico[0].NaoEnviado || historico[1].NaoEnviado {
		t.Fatalf("historico = %+v, %v", historico, err)
	}
	texto, _, err := g.CommitGit(ctx, "api", historico[0].Curto)
	if err != nil || !strings.Contains(texto, "+linha 2") {
		t.Fatalf("show = %q, %v", texto, err)
	}

	// Push manda o commit; o painel volta em dia.
	painel = acao(t, g, "push", PedidoGit{})
	if painel.Estado.Frente != 0 {
		t.Fatalf("depois do push ainda ha %d a frente", painel.Estado.Frente)
	}

	// Descartar o nao rastreado apaga o arquivo.
	painel = acao(t, g, "descartar", PedidoGit{Caminhos: []string{"src/novo arquivo.go"}})
	if len(painel.Arquivos) != 0 {
		t.Fatalf("deveria estar limpo: %+v", painel.Arquivos)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "novo arquivo.go")); !os.IsNotExist(err) {
		t.Fatalf("arquivo descartado continua no disco: %v", err)
	}
}

func TestPainelGitStash(t *testing.T) {
	g, dir, _ := gerenteComRepo(t)
	ctx := context.Background()

	if _, _, err := g.AcaoGit(ctx, "api", "stash", PedidoGit{}); err == nil {
		t.Fatal("stash sem alteracao deveria falhar")
	}
	escrever(t, dir, "leia.md", "mexido\n")
	escrever(t, dir, "solto.txt", "x\n")
	painel := acao(t, g, "stash", PedidoGit{Mensagem: "meu wip"})
	if len(painel.Arquivos) != 0 || len(painel.Stashes) != 1 || !strings.Contains(painel.Stashes[0].Resumo, "meu wip") {
		t.Fatalf("depois do stash = %+v", painel)
	}
	if _, _, err := g.AcaoGit(ctx, "api", "stash-pop", PedidoGit{Indice: 5}); err == nil {
		t.Fatal("stash inexistente deveria ser recusado")
	}
	painel = acao(t, g, "stash-pop", PedidoGit{Indice: 0})
	if len(painel.Arquivos) != 2 || len(painel.Stashes) != 0 {
		t.Fatalf("depois do pop = %+v", painel)
	}
	acao(t, g, "stash", PedidoGit{})
	painel = acao(t, g, "stash-apagar", PedidoGit{Indice: 0})
	if len(painel.Stashes) != 0 {
		t.Fatalf("stash deveria ter sido apagado: %+v", painel.Stashes)
	}
}

func TestPainelGitBranchesEMerge(t *testing.T) {
	g, dir, _ := gerenteComRepo(t)
	ctx := context.Background()

	for _, nome := range []string{"", "-x", "com espaco", "a..b"} {
		if _, _, err := g.AcaoGit(ctx, "api", "criar-branch", PedidoGit{Nome: nome}); err == nil {
			t.Fatalf("nome %q deveria ser recusado", nome)
		}
	}
	painel := acao(t, g, "criar-branch", PedidoGit{Nome: "feat/teste"})
	if painel.Estado.Branch != "feat/teste" {
		t.Fatalf("branch atual = %q", painel.Estado.Branch)
	}
	escrever(t, dir, "feat.txt", "feature\n")
	acao(t, g, "preparar", PedidoGit{Todos: true})
	acao(t, g, "commit", PedidoGit{Mensagem: "feature"})

	// Historico de outro branch e de todos.
	if c, err := g.HistoricoGit(ctx, "api", "refs/heads/main"); err != nil || len(c) != 1 {
		t.Fatalf("historico do main = %+v, %v", c, err)
	}
	if c, err := g.HistoricoGit(ctx, "api", refTodos); err != nil || len(c) != 2 {
		t.Fatalf("historico de todos = %+v, %v", c, err)
	}
	if _, err := g.HistoricoGit(ctx, "api", "--all"); err == nil {
		t.Fatal("ref fora da lista deveria ser recusado")
	}

	// Apagar o branch atual e recusado.
	if _, _, err := g.AcaoGit(ctx, "api", "apagar-branch", PedidoGit{Nome: "feat/teste"}); err == nil {
		t.Fatal("apagar o branch atual deveria falhar")
	}
	rodar(t, dir, "switch", "-q", "main")

	// Sem forcar, branch com commit nao integrado nao sai.
	if _, _, err := g.AcaoGit(ctx, "api", "apagar-branch", PedidoGit{Nome: "feat/teste"}); err == nil ||
		!strings.Contains(err.Error(), "nao foram integrados") {
		t.Fatalf("deveria explicar que ha commit nao integrado: %v", err)
	}

	acao(t, g, "merge", PedidoGit{Nome: "feat/teste"})
	if _, err := os.Stat(filepath.Join(dir, "feat.txt")); err != nil {
		t.Fatalf("merge nao trouxe o arquivo: %v", err)
	}
	// Integrado, o branch sai sem forcar.
	acao(t, g, "apagar-branch", PedidoGit{Nome: "feat/teste"})
	lista, err := g.BranchesInfoGit(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range lista {
		if b.Nome == "feat/teste" {
			t.Fatalf("branch continua na lista: %+v", lista)
		}
	}
}

func TestPainelGitMergeComConflitoEAbortar(t *testing.T) {
	g, dir, _ := gerenteComRepo(t)
	ctx := context.Background()

	rodar(t, dir, "switch", "-q", "-c", "outro")
	escrever(t, dir, "leia.md", "do outro\n")
	rodar(t, dir, "commit", "-q", "-am", "outro")
	rodar(t, dir, "switch", "-q", "main")
	escrever(t, dir, "leia.md", "do main\n")
	rodar(t, dir, "commit", "-q", "-am", "main")

	_, painel, err := g.AcaoGit(ctx, "api", "merge", PedidoGit{Nome: "outro"})
	if err == nil || !strings.Contains(err.Error(), "conflito") {
		t.Fatalf("merge deveria parar com conflito: %v", err)
	}
	if painel.EmAndamento != "merge" || painel.Estado.Conflitos != 1 {
		t.Fatalf("painel = %+v", painel)
	}
	// Conflito nao pode ser descartado nem commitado.
	if _, _, err := g.AcaoGit(ctx, "api", "descartar", PedidoGit{Caminhos: []string{"leia.md"}}); err == nil {
		t.Fatal("descartar arquivo em conflito deveria falhar")
	}
	painel = acao(t, g, "abortar", PedidoGit{})
	if painel.EmAndamento != "" || len(painel.Arquivos) != 0 {
		t.Fatalf("depois de abortar = %+v", painel)
	}
}

func TestPainelGitPullEPushRecusado(t *testing.T) {
	g, dir, remoto := gerenteComRepo(t)
	ctx := context.Background()

	// Outra copia do repositorio manda um commit para o remoto.
	outro := t.TempDir()
	rodar(t, outro, "clone", "-q", remoto, ".")
	rodar(t, outro, "config", "user.name", "Outra")
	rodar(t, outro, "config", "user.email", "outra@exemplo.com")
	escrever(t, outro, "dela.txt", "x\n")
	rodar(t, outro, "add", "-A")
	rodar(t, outro, "commit", "-q", "-m", "dela")
	rodar(t, outro, "push", "-q")

	// Commit local sem ter puxado: o push e recusado com a explicacao.
	escrever(t, dir, "meu.txt", "y\n")
	acao(t, g, "preparar", PedidoGit{Todos: true})
	acao(t, g, "commit", PedidoGit{Mensagem: "meu"})
	if _, _, err := g.AcaoGit(ctx, "api", "push", PedidoGit{}); err == nil || !strings.Contains(err.Error(), "pull") {
		t.Fatalf("push deveria ser recusado pedindo pull: %v", err)
	}
	// Divergiu: pull --ff-only explica, pull com rebase resolve.
	if _, _, err := g.AcaoGit(ctx, "api", "pull", PedidoGit{}); err == nil || !strings.Contains(err.Error(), "divergiram") {
		t.Fatalf("pull ff-only deveria explicar a divergencia: %v", err)
	}
	painel := acao(t, g, "pull", PedidoGit{Rebase: true})
	if painel.Estado.Atras != 0 || painel.Estado.Frente != 1 {
		t.Fatalf("depois do rebase = %+v", painel.Estado)
	}
	acao(t, g, "push", PedidoGit{})
}

func TestPainelGitPushSemUpstream(t *testing.T) {
	g, dir, remoto := gerenteComRepo(t)
	rodar(t, dir, "switch", "-q", "-c", "novo")
	painel := acao(t, g, "push", PedidoGit{})
	if painel.Estado.Upstream != "origin/novo" {
		t.Fatalf("push deveria configurar o upstream: %+v", painel.Estado)
	}
	if got := rodar(t, remoto, "branch", "--list", "novo"); !strings.Contains(got, "novo") {
		t.Fatalf("branch nao chegou no remoto: %q", got)
	}
}

func TestPainelGitRecusaEntradaForaDaLista(t *testing.T) {
	g, dir, _ := gerenteComRepo(t)
	ctx := context.Background()
	escrever(t, dir, "leia.md", "mexido\n")

	for _, caminho := range []string{"--force", "../fora.txt", "nao-existe.txt"} {
		if _, _, err := g.AcaoGit(ctx, "api", "preparar", PedidoGit{Caminhos: []string{caminho}}); err == nil {
			t.Fatalf("caminho %q deveria ser recusado", caminho)
		}
		if _, _, err := g.DiffGit(ctx, "api", caminho, false); err == nil {
			t.Fatalf("diff de %q deveria ser recusado", caminho)
		}
	}
	for _, hash := range []string{"--all", "HEAD", "abc", "zzzzzzz"} {
		if _, _, err := g.CommitGit(ctx, "api", hash); err == nil {
			t.Fatalf("hash %q deveria ser recusado", hash)
		}
	}
	if _, _, err := g.AcaoGit(ctx, "api", "rm-rf", PedidoGit{}); err == nil {
		t.Fatal("acao desconhecida deveria ser recusada")
	}
	if _, _, err := g.AcaoGit(ctx, "db", "push", PedidoGit{}); err == nil {
		t.Fatal("servico docker nao tem repositorio")
	}
}

func TestRotasPainelGit(t *testing.T) {
	precisaGit(t)
	h, g := servidorTeste(t, nil)
	g.git = NovoCacheGit(SondaGitSO{})
	dir := filepath.Join(g.raiz, "backend")
	iniciarRepo(t, dir)
	escrever(t, dir, "leia.md", "mexido\n")

	resp := chamar(t, h, http.MethodGet, "/api/servicos/api/git", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	var painel PainelGit
	if err := json.Unmarshal(resp.Body.Bytes(), &painel); err != nil {
		t.Fatal(err)
	}
	if len(painel.Arquivos) != 1 {
		t.Fatalf("painel = %+v", painel)
	}

	resp = chamar(t, h, http.MethodGet, "/api/servicos/api/git/diff?caminho=leia.md", "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "+mexido") {
		t.Fatalf("diff: %d %s", resp.Code, resp.Body)
	}

	resp = chamar(t, h, http.MethodPost, "/api/servicos/api/git/preparar", `{"todos":true}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("preparar: %d %s", resp.Code, resp.Body)
	}
	resp = chamar(t, h, http.MethodPost, "/api/servicos/api/git/commit", `{"mensagem":""}`)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "painel") {
		t.Fatalf("commit sem mensagem deveria dar 400 com o painel: %d %s", resp.Code, resp.Body)
	}

	for _, rota := range []string{"/api/servicos/api/git/historico", "/api/servicos/api/git/branches"} {
		if resp := chamar(t, h, http.MethodGet, rota, ""); resp.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", rota, resp.Code, resp.Body)
		}
	}
}
