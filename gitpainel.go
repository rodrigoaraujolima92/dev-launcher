package main

// Painel git: o que a tela precisa para trabalhar num repositorio sem sair do launcher - ver
// os arquivos mexidos e o diff de cada um, preparar, descartar, commitar, puxar, enviar,
// ler o historico e guardar alteracoes no stash.
//
// Regra de seguranca de todo este arquivo: nada que vem do navegador vira argumento solto do
// git. Caminho de arquivo so passa se estiver na lista de alteracoes que o proprio git acabou
// de devolver, hash de commit tem que ter cara de hash, stash e indice da lista, nome de
// branch passa pelo check-ref-format. E todo caminho vai depois de "--".

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ArquivoGit e um arquivo com alteracao. Indice e Trabalho sao as letras do git (M, A, D,
// R, C...) de cada lado; vazio quer dizer "sem mudanca desse lado". Nao rastreado tem
// Trabalho "?"; em conflito, os dois lados vem "U".
type ArquivoGit struct {
	Caminho  string `json:"caminho"`
	Origem   string `json:"origem,omitempty"` // nome antigo, quando o git detectou renomeacao
	Indice   string `json:"indice"`
	Trabalho string `json:"trabalho"`
	Conflito bool   `json:"conflito,omitempty"`
}

type StashGit struct {
	Indice int    `json:"indice"`
	Resumo string `json:"resumo"`
	Quando string `json:"quando"`
}

type CommitLog struct {
	Hash       string   `json:"hash"`
	Curto      string   `json:"curto"`
	Resumo     string   `json:"resumo"`
	Autor      string   `json:"autor"`
	Quando     string   `json:"quando"`
	Refs       []string `json:"refs,omitempty"`
	Merge      bool     `json:"merge,omitempty"`
	NaoEnviado bool     `json:"nao_enviado,omitempty"` // existe aqui e ainda nao no upstream
}

// BranchInfo e uma linha da aba de branches. Local tem Remoto vazio; branch que so existe
// no remoto vem com Remoto preenchido.
type BranchInfo struct {
	Nome     string `json:"nome"`
	Ref      string `json:"ref"` // refs/heads/x ou refs/remotes/origin/x
	Remoto   string `json:"remoto,omitempty"`
	Atual    bool   `json:"atual,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Frente   int    `json:"frente,omitempty"`
	Atras    int    `json:"atras,omitempty"`
	Sumiu    bool   `json:"sumiu,omitempty"` // o upstream foi apagado no remoto
	// Principal: main, master ou o branch padrao de algum remoto. O painel nunca apaga.
	Principal bool   `json:"principal,omitempty"`
	Hash      string `json:"hash"`
	Resumo    string `json:"resumo"`
	Quando    string `json:"quando"`
}

// PRGit e um pull request aberto, como o GitHub CLI devolve.
type PRGit struct {
	Numero    int    `json:"numero"`
	Titulo    string `json:"titulo"`
	Autor     string `json:"autor"`
	Branch    string `json:"branch"`
	Base      string `json:"base"`
	URL       string `json:"url"`
	Rascunho  bool   `json:"rascunho,omitempty"`
	Criado    string `json:"criado"`
	Revisao   string `json:"revisao,omitempty"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	Checks    string `json:"checks,omitempty"`  // ok, falhou, pendente
	Adicoes   int    `json:"adicoes"`
	Remocoes  int    `json:"remocoes"`
	Conflitos bool   `json:"conflitos,omitempty"`
}

type ListaPRs struct {
	Repo   string  `json:"repo"` // dono/nome
	URL    string  `json:"url"`
	Padrao string  `json:"padrao"` // branch padrao: dele nao se abre PR
	PRs    []PRGit `json:"prs"`
}

const refTodos = "*todos"

// PainelGit e o retrato que o modal desenha: estado do branch, arquivos e stashes.
type PainelGit struct {
	Estado   EstadoGit    `json:"estado"`
	Arquivos []ArquivoGit `json:"arquivos"`
	Cortado  bool         `json:"cortado,omitempty"` // lista grande demais, so os primeiros
	Stashes  []StashGit   `json:"stashes"`
	// "merge" ou "rebase" quando uma dessas parou no meio (conflito) esperando decisao.
	EmAndamento string `json:"em_andamento,omitempty"`
}

// PedidoGit e o corpo das acoes. Cada acao usa so os campos que fazem sentido para ela.
type PedidoGit struct {
	Caminhos []string `json:"caminhos"`
	Todos    bool     `json:"todos"`
	Mensagem string   `json:"mensagem"`
	Push     bool     `json:"push"`
	Rebase   bool     `json:"rebase"`
	Indice   int      `json:"indice"`
	Nome     string   `json:"nome"`
	Forcar   bool     `json:"forcar"`
}

const (
	maxArquivosGit = 3000
	maxSaidaGit    = 512 * 1024 // diff de arquivo gigante nao pode travar o navegador
	maxCommitsLog  = 80
)

// ------------------------------------------------------------------
// execucao
// ------------------------------------------------------------------

// rodarGit roda o git com a pasta de trabalho em dir e devolve stdout e stderr separados:
// o diff vem no stdout, e o progresso do push vem no stderr mesmo quando da certo.
//
// GIT_TERMINAL_PROMPT=0 faz o git falhar em vez de ficar esperando usuario e senha num
// terminal que ninguem esta vendo. O gerenciador de credenciais do Windows continua
// funcionando, porque abre a propria janela.
func rodarGit(ctx context.Context, prazo time.Duration, dir string, args ...string) (saida, erro string, codigo int, err error) {
	return rodarPrograma(ctx, prazo, dir, "git", args...)
}

// rodarPrograma e o rodarGit para qualquer ferramenta da familia (git, gh). Editor e
// pergunta interativa ficam desligados: ninguem esta olhando o terminal do launcher.
func rodarPrograma(ctx context.Context, prazo time.Duration, dir, programa string, args ...string) (saida, erro string, codigo int, err error) {
	ctx, cancelar := context.WithTimeout(ctx, prazo)
	defer cancelar()
	cmd := exec.CommandContext(ctx, programa, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	codigo = 0
	if cmd.ProcessState != nil {
		codigo = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("%s %s passou de %s sem terminar", programa, args[0], prazo)
	}
	return out.String(), errOut.String(), codigo, err
}

// git roda um comando que tem que dar certo. No erro, a mensagem e o que o git escreveu -
// as primeiras linhas, que e onde ele explica o motivo (push recusado, pull divergente...).
func git(ctx context.Context, prazo time.Duration, dir string, args ...string) (string, error) {
	saida, erro, codigo, err := rodarGit(ctx, prazo, dir, args...)
	if err != nil {
		return saida, erroDoGit(erro, saida, codigo, args[0], err)
	}
	return saida, nil
}

func erroDoGit(erro, saida string, codigo int, comando string, err error) error {
	if strings.Contains(err.Error(), "passou de") {
		return err
	}
	texto := strings.TrimSpace(erro)
	if texto == "" {
		texto = strings.TrimSpace(saida)
	}
	if texto == "" {
		return fmt.Errorf("git %s falhou (codigo %d)", comando, codigo)
	}
	linhas := []string{}
	for _, l := range strings.Split(texto, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			linhas = append(linhas, l)
		}
		if len(linhas) == 8 {
			break
		}
	}
	return errors.New(strings.Join(linhas, "\n"))
}

func cortarSaida(texto string) (string, bool) {
	if len(texto) <= maxSaidaGit {
		return texto, false
	}
	corte := strings.LastIndexByte(texto[:maxSaidaGit], '\n')
	if corte < 0 {
		corte = maxSaidaGit
	}
	return texto[:corte], true
}

// raizDoRepo devolve a pasta de cima do repositorio. Tudo do painel roda de la: os caminhos
// do "git status --porcelain" sao sempre relativos a raiz, mesmo com o projeto numa
// subpasta de um monorepo.
func raizDoRepo(ctx context.Context, dir string) (string, error) {
	saida, err := git(ctx, 10*time.Second, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(saida), nil
}

// travasGit serializa as acoes num mesmo repositorio: clique duplo em "commit" nao pode
// virar dois git disputando o index.lock.
var travasGit sync.Map

func travarRepo(raiz string) func() {
	v, _ := travasGit.LoadOrStore(strings.ToLower(raiz), &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ------------------------------------------------------------------
// leitura
// ------------------------------------------------------------------

func arquivosGit(ctx context.Context, raiz string) ([]ArquivoGit, bool, error) {
	// -z: sem aspas nem escape em caminho com acento ou espaco, e cada entrada separada por
	// NUL - nenhum nome de arquivo consegue quebrar a leitura.
	saida, err := git(ctx, 30*time.Second, raiz, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, false, err
	}
	lista := lerArquivosZ(saida)
	if len(lista) > maxArquivosGit {
		return lista[:maxArquivosGit], true, nil
	}
	return lista, false, nil
}

// lerArquivosZ interpreta "git status --porcelain=v2 -z":
//
//	1 XY sub mH mI mW hH hI caminho
//	2 XY sub mH mI mW hH hI Rnn caminho NUL origem
//	u XY sub m1 m2 m3 mW h1 h2 h3 caminho
//	? caminho
func lerArquivosZ(saida string) []ArquivoGit {
	itens := strings.Split(saida, "\x00")
	out := []ArquivoGit{}
	for i := 0; i < len(itens); i++ {
		item := itens[i]
		if len(item) < 2 {
			continue
		}
		switch item[0] {
		case '1':
			if p := strings.SplitN(item, " ", 9); len(p) == 9 && len(p[1]) == 2 {
				out = append(out, ArquivoGit{Caminho: p[8], Indice: letraGit(p[1][0]), Trabalho: letraGit(p[1][1])})
			}
		case '2':
			if p := strings.SplitN(item, " ", 10); len(p) == 10 && len(p[1]) == 2 {
				a := ArquivoGit{Caminho: p[9], Indice: letraGit(p[1][0]), Trabalho: letraGit(p[1][1])}
				if i+1 < len(itens) {
					a.Origem = itens[i+1]
					i++
				}
				out = append(out, a)
			}
		case 'u':
			if p := strings.SplitN(item, " ", 11); len(p) == 11 {
				out = append(out, ArquivoGit{Caminho: p[10], Indice: "U", Trabalho: "U", Conflito: true})
			}
		case '?':
			out = append(out, ArquivoGit{Caminho: item[2:], Trabalho: "?"})
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Caminho) < strings.ToLower(out[b].Caminho) })
	return out
}

func letraGit(c byte) string {
	if c == '.' {
		return ""
	}
	return string(c)
}

func stashesGit(ctx context.Context, raiz string) ([]StashGit, error) {
	saida, err := git(ctx, 15*time.Second, raiz, "stash", "list", "--format=%gd%x1f%gs%x1f%cr")
	if err != nil {
		return nil, err
	}
	return lerStashes(saida), nil
}

// lerStashes le "stash@{0}<US>On main: mensagem<US>2 hours ago".
func lerStashes(saida string) []StashGit {
	out := []StashGit{}
	for _, linha := range strings.Split(saida, "\n") {
		campos := strings.Split(strings.TrimRight(linha, "\r"), "\x1f")
		if len(campos) < 3 {
			continue
		}
		ref := strings.TrimSuffix(strings.TrimPrefix(campos[0], "stash@{"), "}")
		n, err := strconv.Atoi(ref)
		if err != nil {
			continue
		}
		out = append(out, StashGit{Indice: n, Resumo: campos[1], Quando: campos[2]})
	}
	return out
}

// historicoGit le o log. ref vazio e o branch atual; refTodos junta todos os branches;
// qualquer outro valor tem que ser um ref completo da lista de branches do repositorio.
func historicoGit(ctx context.Context, raiz, upstream, ref string) ([]CommitLog, error) {
	args := []string{"log", "-n", strconv.Itoa(maxCommitsLog), "--format=%H%x1f%h%x1f%s%x1f%an%x1f%ar%x1f%D%x1f%P%x1e"}
	switch ref {
	case "":
	case refTodos:
		args = append(args, "--all")
	default:
		branches, err := branchesInfoGit(ctx, raiz)
		if err != nil {
			return nil, err
		}
		conhecido := false
		for _, b := range branches {
			conhecido = conhecido || b.Ref == ref
		}
		if !conhecido {
			return nil, fmt.Errorf("branch desconhecido: %s", ref)
		}
		// Ref completo (refs/heads/...) nunca comeca com "-", e o "--" fecha a lista.
		args = append(args, ref, "--")
	}
	saida, erro, _, err := rodarGit(ctx, 30*time.Second, raiz, args...)
	if err != nil {
		// Repositorio recem-criado ainda nao tem commit: historico vazio, nao erro.
		if strings.Contains(erro, "does not have any commits") || strings.Contains(erro, "bad default revision") {
			return []CommitLog{}, nil
		}
		return nil, erroDoGit(erro, saida, 1, "log", err)
	}
	commits := lerLog(saida)
	if upstream != "" {
		if fora, err := git(ctx, 15*time.Second, raiz, "rev-list", "--max-count=500", "@{upstream}..HEAD"); err == nil {
			naoEnviados := map[string]bool{}
			for _, h := range strings.Fields(fora) {
				naoEnviados[h] = true
			}
			for i := range commits {
				commits[i].NaoEnviado = naoEnviados[commits[i].Hash]
			}
		}
	}
	return commits, nil
}

func lerLog(saida string) []CommitLog {
	out := []CommitLog{}
	for _, registro := range strings.Split(saida, "\x1e") {
		campos := strings.Split(strings.TrimSpace(registro), "\x1f")
		if len(campos) < 6 {
			continue
		}
		c := CommitLog{Hash: campos[0], Curto: campos[1], Resumo: campos[2], Autor: campos[3], Quando: campos[4]}
		for _, ref := range strings.Split(campos[5], ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				c.Refs = append(c.Refs, ref)
			}
		}
		if len(campos) > 6 {
			c.Merge = len(strings.Fields(campos[6])) > 1
		}
		out = append(out, c)
	}
	return out
}

var reHash = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func mostrarCommitGit(ctx context.Context, raiz, hash string) (string, bool, error) {
	if !reHash.MatchString(hash) {
		return "", false, fmt.Errorf("hash invalido: %q", hash)
	}
	saida, err := git(ctx, 30*time.Second, raiz, "-c", "core.quotepath=off",
		"show", "--no-color", "--no-ext-diff", "-M", "--stat", "--patch", "--format=fuller", hash, "--")
	if err != nil {
		return "", false, err
	}
	texto, cortado := cortarSaida(saida)
	return texto, cortado, nil
}

// diffArquivoGit devolve o diff de um lado do arquivo: o preparado (indice contra HEAD) ou o
// da copia de trabalho (contra o indice). Nao rastreado e comparado com o vazio.
func diffArquivoGit(ctx context.Context, raiz string, a ArquivoGit, preparado bool) (string, bool, error) {
	base := []string{"-c", "core.quotepath=off", "diff", "--no-color", "--no-ext-diff"}
	var args []string
	switch {
	case a.Trabalho == "?" && !preparado:
		// --no-index devolve codigo 1 quando ha diferenca - e sempre ha, contra o vazio.
		args = append(base, "--no-index", "--", "/dev/null", a.Caminho)
		saida, erro, codigo, err := rodarGit(ctx, 30*time.Second, raiz, args...)
		if err != nil && codigo != 1 {
			return "", false, erroDoGit(erro, saida, codigo, "diff", err)
		}
		texto, cortado := cortarSaida(saida)
		return texto, cortado, nil
	case preparado:
		args = append(base, "--cached", "-M", "--", a.Caminho)
		if a.Origem != "" {
			args = append(args, a.Origem)
		}
	default:
		args = append(base, "--", a.Caminho)
	}
	saida, err := git(ctx, 30*time.Second, raiz, args...)
	if err != nil {
		return "", false, err
	}
	texto, cortado := cortarSaida(saida)
	return texto, cortado, nil
}

// branchesInfoGit lista os branches locais (mais recentes primeiro) e depois os que so
// existem em algum remoto.
func branchesInfoGit(ctx context.Context, raiz string) ([]BranchInfo, error) {
	saida, err := git(ctx, 20*time.Second, raiz, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%1f%(upstream:short)%1f%(upstream:track)%1f%(committerdate:relative)%1f%(subject)%1f%(objectname:short)%1f%(HEAD)%1f%(symref)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	return lerBranchesInfo(saida), nil
}

func lerBranchesInfo(saida string) []BranchInfo {
	locais, remotos := []BranchInfo{}, []BranchInfo{}
	principais := map[string]bool{"main": true, "master": true}
	for _, linha := range strings.Split(saida, "\n") {
		campos := strings.Split(strings.TrimRight(linha, "\r"), "\x1f")
		if len(campos) < 7 {
			continue
		}
		b := BranchInfo{Ref: campos[0], Upstream: campos[1], Quando: campos[3], Resumo: campos[4], Hash: campos[5], Atual: campos[6] == "*"}
		track := strings.Trim(campos[2], "[]")
		b.Sumiu = track == "gone"
		for _, parte := range strings.Split(track, ",") {
			parte = strings.TrimSpace(parte)
			if v, ok := strings.CutPrefix(parte, "ahead "); ok {
				b.Frente, _ = strconv.Atoi(v)
			} else if v, ok := strings.CutPrefix(parte, "behind "); ok {
				b.Atras, _ = strconv.Atoi(v)
			}
		}
		if nome, ok := strings.CutPrefix(b.Ref, "refs/heads/"); ok {
			b.Nome = nome
			locais = append(locais, b)
		} else if resto, ok := strings.CutPrefix(b.Ref, "refs/remotes/"); ok {
			remoto, nome, ok := strings.Cut(resto, "/")
			if !ok || nome == "" {
				continue
			}
			if nome == "HEAD" {
				// refs/remotes/origin/HEAD aponta para o branch padrao do remoto.
				if len(campos) > 7 {
					if padrao, ok := strings.CutPrefix(campos[7], "refs/remotes/"+remoto+"/"); ok && padrao != "" {
						principais[padrao] = true
					}
				}
				continue
			}
			b.Nome, b.Remoto = nome, remoto
			remotos = append(remotos, b)
		}
	}
	tem := map[string]bool{}
	for _, b := range locais {
		tem[b.Nome] = true
	}
	for _, b := range remotos {
		if !tem[b.Nome] {
			locais = append(locais, b)
			tem[b.Nome] = true
		}
	}
	for i := range locais {
		locais[i].Principal = principais[locais[i].Nome]
	}
	return locais
}

// emAndamentoGit diz se um merge ou rebase parou no meio esperando decisao.
func emAndamentoGit(ctx context.Context, raiz string) string {
	if _, err := git(ctx, 10*time.Second, raiz, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		return "merge"
	}
	for _, pasta := range []string{"rebase-merge", "rebase-apply"} {
		caminho, err := git(ctx, 10*time.Second, raiz, "rev-parse", "--git-path", pasta)
		if err != nil {
			continue
		}
		caminho = strings.TrimSpace(caminho)
		if !filepath.IsAbs(caminho) {
			caminho = filepath.Join(raiz, caminho)
		}
		if info, err := os.Stat(caminho); err == nil && info.IsDir() {
			return "rebase"
		}
	}
	return ""
}

// prsGit lista os PRs abertos pelo GitHub CLI. Sem o gh, ou com repositorio fora do GitHub,
// o erro explica o que falta - o resto do painel funciona igual.
func prsGit(ctx context.Context, raiz string) (ListaPRs, error) {
	if !comandoExiste("gh") {
		return ListaPRs{}, errors.New("GitHub CLI (gh) nao encontrado no PATH - instale em https://cli.github.com e rode \"gh auth login\" para ver os PRs aqui")
	}
	repo, erro, codigo, err := rodarPrograma(ctx, 30*time.Second, raiz, "gh", "repo", "view", "--json", "nameWithOwner,url,defaultBranchRef")
	if err != nil {
		switch {
		case strings.Contains(erro, "none of the git remotes"):
			return ListaPRs{}, errors.New("este repositorio nao tem remoto no GitHub - a lista de PRs so funciona com repositorios do GitHub")
		case strings.Contains(erro, "gh auth login"):
			return ListaPRs{}, errors.New("o GitHub CLI nao esta autenticado - rode \"gh auth login\" num terminal")
		}
		return ListaPRs{}, erroDoGit(erro, repo, codigo, "repo view", err)
	}
	var info struct {
		NameWithOwner string `json:"nameWithOwner"`
		URL           string `json:"url"`
		Padrao        struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(repo), &info); err != nil {
		return ListaPRs{}, fmt.Errorf("resposta inesperada do gh: %v", err)
	}
	saida, erro, codigo, err := rodarPrograma(ctx, 30*time.Second, raiz, "gh", "pr", "list", "--state", "open", "--limit", "50",
		"--json", "number,title,author,headRefName,baseRefName,url,isDraft,createdAt,reviewDecision,statusCheckRollup,additions,deletions,mergeable")
	if err != nil {
		return ListaPRs{}, erroDoGit(erro, saida, codigo, "pr list", err)
	}
	prs, err := lerPRs(saida)
	if err != nil {
		return ListaPRs{}, err
	}
	return ListaPRs{Repo: info.NameWithOwner, URL: info.URL, Padrao: info.Padrao.Name, PRs: prs}, nil
}

func lerPRs(saida string) ([]PRGit, error) {
	var brutos []struct {
		Number      int                    `json:"number"`
		Title       string                 `json:"title"`
		Author      struct{ Login string } `json:"author"`
		HeadRefName string                 `json:"headRefName"`
		BaseRefName string                 `json:"baseRefName"`
		URL         string                 `json:"url"`
		IsDraft     bool                   `json:"isDraft"`
		CreatedAt   string                 `json:"createdAt"`
		Review      string                 `json:"reviewDecision"`
		Additions   int                    `json:"additions"`
		Deletions   int                    `json:"deletions"`
		Mergeable   string                 `json:"mergeable"`
		Checks      []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(saida), &brutos); err != nil {
		return nil, fmt.Errorf("resposta inesperada do gh: %v", err)
	}
	out := []PRGit{}
	for _, b := range brutos {
		pr := PRGit{
			Numero: b.Number, Titulo: b.Title, Autor: b.Author.Login, Branch: b.HeadRefName,
			Base: b.BaseRefName, URL: b.URL, Rascunho: b.IsDraft, Criado: b.CreatedAt,
			Revisao: b.Review, Adicoes: b.Additions, Remocoes: b.Deletions,
			Conflitos: b.Mergeable == "CONFLICTING",
		}
		// Um check vermelho ja pinta o PR inteiro; senao, qualquer um rodando deixa pendente.
		for _, c := range b.Checks {
			resultado := strings.ToUpper(c.Conclusion + c.State)
			switch {
			case strings.Contains(resultado, "FAIL") || strings.Contains(resultado, "ERROR") ||
				strings.Contains(resultado, "CANCEL") || strings.Contains(resultado, "TIMED_OUT") ||
				strings.Contains(resultado, "ACTION_REQUIRED"):
				pr.Checks = "falhou"
			case pr.Checks != "falhou" && (c.Status != "" && c.Status != "COMPLETED" || c.State == "PENDING" || c.State == "EXPECTED"):
				pr.Checks = "pendente"
			case pr.Checks == "":
				pr.Checks = "ok"
			}
		}
		out = append(out, pr)
	}
	return out, nil
}

func temCommit(ctx context.Context, raiz string) bool {
	_, err := git(ctx, 10*time.Second, raiz, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// ------------------------------------------------------------------
// gerente
// ------------------------------------------------------------------

// abrirRepoGit resolve o projeto para a raiz do repositorio, lendo o git na hora.
func (g *Gerente) abrirRepoGit(ctx context.Context, id string) (raiz string, estado EstadoGit, err error) {
	dir, estado, err := g.repoDoProjeto(ctx, id)
	if err != nil {
		return "", estado, err
	}
	raiz, err = raizDoRepo(ctx, dir)
	return raiz, estado, err
}

// PainelGit monta o retrato completo de um projeto para o modal.
func (g *Gerente) PainelGit(ctx context.Context, id string) (PainelGit, error) {
	raiz, estado, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return PainelGit{}, err
	}
	return g.montarPainel(ctx, raiz, estado)
}

func (g *Gerente) montarPainel(ctx context.Context, raiz string, estado EstadoGit) (PainelGit, error) {
	arquivos, cortado, err := arquivosGit(ctx, raiz)
	if err != nil {
		return PainelGit{}, err
	}
	stashes, err := stashesGit(ctx, raiz)
	if err != nil {
		stashes = []StashGit{} // stash e acessorio: nao derruba o painel inteiro
	}
	return PainelGit{
		Estado: estado, Arquivos: arquivos, Cortado: cortado, Stashes: stashes,
		EmAndamento: emAndamentoGit(ctx, raiz),
	}, nil
}

func (g *Gerente) BranchesInfoGit(ctx context.Context, id string) ([]BranchInfo, error) {
	raiz, _, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return nil, err
	}
	return branchesInfoGit(ctx, raiz)
}

func (g *Gerente) PRsGit(ctx context.Context, id string) (ListaPRs, error) {
	raiz, _, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return ListaPRs{}, err
	}
	return prsGit(ctx, raiz)
}

// DiffGit devolve o diff de um arquivo que esteja na lista de alteracoes.
func (g *Gerente) DiffGit(ctx context.Context, id, caminho string, preparado bool) (string, bool, error) {
	raiz, _, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return "", false, err
	}
	arquivos, _, err := arquivosGit(ctx, raiz)
	if err != nil {
		return "", false, err
	}
	for _, a := range arquivos {
		if a.Caminho == caminho {
			return diffArquivoGit(ctx, raiz, a, preparado)
		}
	}
	return "", false, fmt.Errorf("%s nao tem mais alteracao", caminho)
}

func (g *Gerente) HistoricoGit(ctx context.Context, id, ref string) ([]CommitLog, error) {
	raiz, estado, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return nil, err
	}
	return historicoGit(ctx, raiz, estado.Upstream, ref)
}

func (g *Gerente) CommitGit(ctx context.Context, id, hash string) (string, bool, error) {
	raiz, _, err := g.abrirRepoGit(ctx, id)
	if err != nil {
		return "", false, err
	}
	return mostrarCommitGit(ctx, raiz, hash)
}

// AcaoGit executa uma acao que muda o repositorio e devolve a saida do git (para a tela
// mostrar o que aconteceu) e o painel ja relido.
func (g *Gerente) AcaoGit(ctx context.Context, id, acao string, p PedidoGit) (string, PainelGit, error) {
	if !acaoGitConhecida[acao] {
		return "", PainelGit{}, fmt.Errorf("acao git desconhecida: %s", acao)
	}
	dir, _, err := g.repoDoProjeto(ctx, id)
	if err != nil {
		return "", PainelGit{}, err
	}
	raiz, err := raizDoRepo(ctx, dir)
	if err != nil {
		return "", PainelGit{}, err
	}

	destravar := travarRepo(raiz)
	saida, err := executarAcaoGit(ctx, raiz, acao, p)
	destravar()

	// Releitura mesmo com erro: um pull que parou no meio deixa o repositorio diferente do
	// que a tela mostrava. Os cartoes de todos os projetos tambem atualizam - dois projetos
	// podem morar no mesmo repositorio.
	g.git.Esquecer()
	g.PublicarGit(ctx, false)
	estado := g.git.Estados(ctx, []string{dir}, false)[dir]
	painel, errPainel := g.montarPainel(ctx, raiz, estado)
	if err != nil {
		return "", painel, err
	}
	return strings.TrimSpace(saida), painel, errPainel
}

var acaoGitConhecida = map[string]bool{
	"preparar": true, "despreparar": true, "descartar": true, "commit": true,
	"pull": true, "push": true, "stash": true, "stash-aplicar": true, "stash-pop": true,
	"stash-apagar": true, "criar-branch": true, "apagar-branch": true, "merge": true,
	"abortar": true, "continuar": true, "fetch": true,
}

// branchLocal procura um branch pelo nome na lista do proprio repositorio.
func branchPorNome(ctx context.Context, raiz, nome string) (BranchInfo, error) {
	lista, err := branchesInfoGit(ctx, raiz)
	if err != nil {
		return BranchInfo{}, err
	}
	for _, b := range lista {
		if b.Nome == nome {
			return b, nil
		}
	}
	return BranchInfo{}, fmt.Errorf("branch desconhecido: %s", nome)
}

func executarAcaoGit(ctx context.Context, raiz, acao string, p PedidoGit) (string, error) {
	switch acao {
	case "preparar":
		if p.Todos {
			return git(ctx, time.Minute, raiz, "add", "-A")
		}
		alvos, err := escolherArquivos(ctx, raiz, p.Caminhos)
		if err != nil {
			return "", err
		}
		// -A tambem registra o arquivo apagado; sem ele "git add" de arquivo que sumiu falha.
		return git(ctx, time.Minute, raiz, append([]string{"add", "-A", "--"}, caminhosDe(alvos, false)...)...)

	case "despreparar":
		if !temCommit(ctx, raiz) {
			// Antes do primeiro commit nao ha HEAD para onde voltar: so tira do indice.
			if p.Todos {
				return git(ctx, time.Minute, raiz, "rm", "-r", "-q", "--cached", "--", ".")
			}
			alvos, err := escolherArquivos(ctx, raiz, p.Caminhos)
			if err != nil {
				return "", err
			}
			return git(ctx, time.Minute, raiz, append([]string{"rm", "-r", "-q", "--cached", "--"}, caminhosDe(alvos, false)...)...)
		}
		if p.Todos {
			return git(ctx, time.Minute, raiz, "reset", "-q")
		}
		alvos, err := escolherArquivos(ctx, raiz, p.Caminhos)
		if err != nil {
			return "", err
		}
		// Na renomeacao, os dois nomes: senao o antigo continua marcado como apagado.
		return git(ctx, time.Minute, raiz, append([]string{"restore", "--staged", "--"}, caminhosDe(alvos, true)...)...)

	case "descartar":
		return descartarGit(ctx, raiz, p.Caminhos)

	case "commit":
		return commitGit(ctx, raiz, p)

	case "fetch":
		saida, erro, codigo, err := rodarGit(ctx, 2*time.Minute, raiz, "fetch", "--all", "--prune")
		if err != nil {
			return "", erroDoGit(erro, saida, codigo, "fetch", err)
		}
		if t := strings.TrimSpace(saida + erro); t != "" {
			return t, nil
		}
		return "fetch concluido - nada novo no remoto", nil

	case "pull":
		modo := "--ff-only"
		if p.Rebase {
			modo = "--rebase"
		}
		saida, erro, codigo, err := rodarGit(ctx, 3*time.Minute, raiz, "pull", modo)
		if err != nil {
			if strings.Contains(erro, "Not possible to fast-forward") || strings.Contains(erro, "diverg") {
				return "", errors.New("o branch local e o remoto divergiram (os dois tem commits que o outro nao tem) - use \"pull com rebase\" ou resolva no terminal")
			}
			return "", erroDoGit(erro, saida, codigo, "pull", err)
		}
		return saida + erro, nil

	case "push":
		return pushGit(ctx, raiz)

	case "stash":
		arquivos, _, err := arquivosGit(ctx, raiz)
		if err != nil {
			return "", err
		}
		if len(arquivos) == 0 {
			return "", errors.New("nada para guardar: a copia de trabalho esta limpa")
		}
		args := []string{"stash", "push", "--include-untracked"}
		if m := strings.TrimSpace(p.Mensagem); m != "" {
			args = append(args, "-m", m)
		}
		return git(ctx, time.Minute, raiz, args...)

	case "stash-aplicar", "stash-pop", "stash-apagar":
		lista, err := stashesGit(ctx, raiz)
		if err != nil {
			return "", err
		}
		achou := false
		for _, s := range lista {
			achou = achou || s.Indice == p.Indice
		}
		if !achou {
			return "", fmt.Errorf("stash %d nao existe mais", p.Indice)
		}
		verbo := map[string]string{"stash-aplicar": "apply", "stash-pop": "pop", "stash-apagar": "drop"}[acao]
		return git(ctx, time.Minute, raiz, "stash", verbo, fmt.Sprintf("stash@{%d}", p.Indice))

	case "criar-branch":
		nome := strings.TrimSpace(p.Nome)
		if nome == "" || strings.HasPrefix(nome, "-") {
			return "", fmt.Errorf("nome de branch invalido: %q", nome)
		}
		if _, err := git(ctx, 10*time.Second, raiz, "check-ref-format", "--branch", nome); err != nil {
			return "", fmt.Errorf("nome de branch invalido: %q", nome)
		}
		// Alteracoes locais vao junto para o branch novo - e o jeito comum de comecar uma
		// feature depois de ja ter mexido.
		saida, erro, codigo, err := rodarGit(ctx, time.Minute, raiz, "switch", "-c", nome)
		if err != nil {
			return "", erroDoGit(erro, saida, codigo, "switch", err)
		}
		return saida + erro, nil

	case "apagar-branch":
		b, err := branchPorNome(ctx, raiz, strings.TrimSpace(p.Nome))
		if err != nil {
			return "", err
		}
		// Regra fixa, sem "forcar": o painel nunca apaga branch principal nem branch remoto.
		if b.Principal {
			return "", fmt.Errorf("%s e um branch principal - o painel nunca apaga branch principal", b.Nome)
		}
		if b.Remoto != "" {
			return "", fmt.Errorf("%s so existe em %s - o painel nunca apaga branch remoto", b.Nome, b.Remoto)
		}
		if b.Atual {
			return "", fmt.Errorf("%s e o branch atual - troque de branch antes de apagar", b.Nome)
		}
		modo := "-d"
		if p.Forcar {
			modo = "-D"
		}
		saida, erro, codigo, err := rodarGit(ctx, 30*time.Second, raiz, "branch", modo, b.Nome)
		if err != nil {
			if strings.Contains(erro, "not fully merged") {
				return "", fmt.Errorf("%s tem commits que nao foram integrados em lugar nenhum - apagar assim perde esses commits", b.Nome)
			}
			return "", erroDoGit(erro, saida, codigo, "branch", err)
		}
		return saida + erro, nil

	case "merge":
		b, err := branchPorNome(ctx, raiz, strings.TrimSpace(p.Nome))
		if err != nil {
			return "", err
		}
		if b.Atual {
			return "", errors.New("nao da para integrar um branch nele mesmo")
		}
		// Arquivo novo fora do git nao bloqueia: se o merge fosse sobrescrever algum, o
		// proprio git recusa. Ja alteracao em arquivo rastreado misturaria com o merge.
		arquivos, _, err := arquivosGit(ctx, raiz)
		if err != nil {
			return "", err
		}
		for _, a := range arquivos {
			if a.Trabalho != "?" {
				return "", errors.New("ha alteracoes locais em arquivos do git - faca commit ou stash antes do merge")
			}
		}
		// Nome curto (e nao o ref completo) para a mensagem sair "Merge branch 'x'". O nome veio
		// da lista do repositorio, entao nao comeca com "-".
		alvo := b.Nome
		if b.Remoto != "" {
			alvo = b.Remoto + "/" + b.Nome
		}
		saida, erro, codigo, err := rodarGit(ctx, 3*time.Minute, raiz, "merge", "--no-edit", alvo)
		if err != nil {
			if strings.Contains(saida+erro, "CONFLICT") {
				return "", errors.New("o merge parou com conflito - resolva os arquivos marcados, prepare e clique em \"continuar\", ou aborte o merge")
			}
			return "", erroDoGit(erro, saida, codigo, "merge", err)
		}
		return saida + erro, nil

	case "abortar", "continuar":
		andamento := emAndamentoGit(ctx, raiz)
		if andamento == "" {
			return "", errors.New("nao ha merge nem rebase em andamento")
		}
		var args []string
		switch {
		case acao == "abortar":
			args = []string{andamento, "--abort"}
		case andamento == "rebase":
			args = []string{"rebase", "--continue"}
		default:
			// Merge com os conflitos resolvidos termina num commit com a mensagem que o git
			// ja preparou (MERGE_MSG).
			args = []string{"commit", "--no-edit"}
		}
		saida, erro, codigo, err := rodarGit(ctx, 2*time.Minute, raiz, args...)
		if err != nil {
			if strings.Contains(saida+erro, "CONFLICT") || strings.Contains(saida+erro, "unmerged") || strings.Contains(saida+erro, "Unmerged") {
				return "", errors.New("ainda ha arquivos em conflito - resolva e prepare cada um antes de continuar")
			}
			return "", erroDoGit(erro, saida, codigo, args[0], err)
		}
		return saida + erro, nil
	}
	return "", fmt.Errorf("acao git desconhecida: %s", acao)
}

// escolherArquivos devolve os arquivos pedidos, conferidos contra a lista do git.
func escolherArquivos(ctx context.Context, raiz string, caminhos []string) ([]ArquivoGit, error) {
	if len(caminhos) == 0 {
		return nil, errors.New("nenhum arquivo escolhido")
	}
	arquivos, _, err := arquivosGit(ctx, raiz)
	if err != nil {
		return nil, err
	}
	porCaminho := map[string]ArquivoGit{}
	for _, a := range arquivos {
		porCaminho[a.Caminho] = a
	}
	out := []ArquivoGit{}
	for _, c := range caminhos {
		a, ok := porCaminho[c]
		if !ok {
			return nil, fmt.Errorf("%s nao esta na lista de alteracoes (a tela pode estar desatualizada)", c)
		}
		out = append(out, a)
	}
	return out, nil
}

func caminhosDe(arquivos []ArquivoGit, comOrigem bool) []string {
	out := []string{}
	for _, a := range arquivos {
		out = append(out, a.Caminho)
		if comOrigem && a.Origem != "" {
			out = append(out, a.Origem)
		}
	}
	return out
}

// descartarGit joga fora a alteracao da copia de trabalho. Arquivo rastreado volta ao que
// esta no indice (o que ja foi preparado fica); arquivo nao rastreado e apagado.
func descartarGit(ctx context.Context, raiz string, caminhos []string) (string, error) {
	alvos, err := escolherArquivos(ctx, raiz, caminhos)
	if err != nil {
		return "", err
	}
	novos, rastreados := []string{}, []string{}
	for _, a := range alvos {
		switch {
		case a.Conflito:
			return "", fmt.Errorf("%s esta em conflito - resolva o conflito antes de descartar", a.Caminho)
		case a.Trabalho == "?":
			novos = append(novos, a.Caminho)
		case a.Trabalho != "":
			rastreados = append(rastreados, a.Caminho)
		}
	}
	if len(novos) == 0 && len(rastreados) == 0 {
		return "", errors.New("nada a descartar na copia de trabalho (o que esta preparado continua)")
	}
	if len(rastreados) > 0 {
		if _, err := git(ctx, time.Minute, raiz, append([]string{"restore", "--"}, rastreados...)...); err != nil {
			return "", err
		}
	}
	if len(novos) > 0 {
		if _, err := git(ctx, time.Minute, raiz, append([]string{"clean", "-f", "-q", "--"}, novos...)...); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d arquivo(s) descartado(s)", len(novos)+len(rastreados)), nil
}

func commitGit(ctx context.Context, raiz string, p PedidoGit) (string, error) {
	mensagem := strings.TrimSpace(p.Mensagem)
	if mensagem == "" {
		return "", errors.New("escreva a mensagem do commit")
	}
	arquivos, _, err := arquivosGit(ctx, raiz)
	if err != nil {
		return "", err
	}
	preparados := 0
	for _, a := range arquivos {
		if a.Conflito {
			return "", fmt.Errorf("%s ainda esta em conflito - resolva e prepare o arquivo antes do commit", a.Caminho)
		}
		if a.Indice != "" {
			preparados++
		}
	}
	if preparados == 0 {
		return "", errors.New("nada preparado para o commit - prepare os arquivos primeiro")
	}
	// A mensagem vai como valor de -m: o git a le inteira, mesmo comecando com "-".
	saida, err := git(ctx, 2*time.Minute, raiz, "commit", "-m", mensagem)
	if err != nil {
		return "", err
	}
	if !p.Push {
		return primeiraLinha(saida), nil
	}
	enviado, err := pushGit(ctx, raiz)
	if err != nil {
		return "", fmt.Errorf("commit feito, mas o push falhou: %w", err)
	}
	return primeiraLinha(saida) + "\n" + enviado, nil
}

// pushGit envia o branch atual. Sem upstream, publica no origin (ou no unico remoto que
// houver) ja configurando o acompanhamento. Nunca forca.
func pushGit(ctx context.Context, raiz string) (string, error) {
	estado, err := git(ctx, 20*time.Second, raiz, "status", "--porcelain=v2", "--branch", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	e := lerPorcelainV2(estado)
	if e.Branch == "" || e.Branch == "(detached)" {
		return "", errors.New("HEAD solto (detached): troque para um branch antes do push")
	}
	args := []string{"push"}
	if e.Upstream == "" {
		remotos, err := git(ctx, 10*time.Second, raiz, "remote")
		if err != nil {
			return "", err
		}
		lista := strings.Fields(remotos)
		if len(lista) == 0 {
			return "", errors.New("o repositorio nao tem remoto configurado")
		}
		remoto := lista[0]
		if contemString(lista, "origin") {
			remoto = "origin"
		}
		args = []string{"push", "-u", remoto, "HEAD"}
	}
	saida, erro, codigo, err := rodarGit(ctx, 3*time.Minute, raiz, args...)
	if err != nil {
		if strings.Contains(erro, "rejected") && (strings.Contains(erro, "fetch first") || strings.Contains(erro, "non-fast-forward")) {
			return "", errors.New("o remoto tem commits que voce nao tem - faca pull antes do push")
		}
		return "", erroDoGit(erro, saida, codigo, "push", err)
	}
	return strings.TrimSpace(saida + erro), nil
}

// ------------------------------------------------------------------
// rotas
// ------------------------------------------------------------------

func rotasPainelGit(mux *http.ServeMux, g *Gerente) {
	mux.HandleFunc("GET /api/servicos/{id}/git", func(w http.ResponseWriter, r *http.Request) {
		painel, err := g.PainelGit(r.Context(), r.PathValue("id"))
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, painel)
	})

	mux.HandleFunc("GET /api/servicos/{id}/git/diff", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		texto, cortado, err := g.DiffGit(r.Context(), r.PathValue("id"), q.Get("caminho"), q.Get("preparado") == "1")
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"diff": texto, "cortado": cortado})
	})

	mux.HandleFunc("GET /api/servicos/{id}/git/historico", func(w http.ResponseWriter, r *http.Request) {
		commits, err := g.HistoricoGit(r.Context(), r.PathValue("id"), r.URL.Query().Get("ref"))
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, commits)
	})

	mux.HandleFunc("GET /api/servicos/{id}/git/branches", func(w http.ResponseWriter, r *http.Request) {
		lista, err := g.BranchesInfoGit(r.Context(), r.PathValue("id"))
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, lista)
	})

	mux.HandleFunc("GET /api/servicos/{id}/git/prs", func(w http.ResponseWriter, r *http.Request) {
		lista, err := g.PRsGit(r.Context(), r.PathValue("id"))
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, lista)
	})

	mux.HandleFunc("GET /api/servicos/{id}/git/commits/{hash}", func(w http.ResponseWriter, r *http.Request) {
		texto, cortado, err := g.CommitGit(r.Context(), r.PathValue("id"), r.PathValue("hash"))
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"diff": texto, "cortado": cortado})
	})

	mux.HandleFunc("POST /api/servicos/{id}/git/{acao}", func(w http.ResponseWriter, r *http.Request) {
		var p PedidoGit
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		saida, painel, err := g.AcaoGit(r.Context(), r.PathValue("id"), r.PathValue("acao"), p)
		if err != nil {
			// O painel relido vai junto: a tela mostra o erro e onde o repositorio ficou.
			responderJSON(w, http.StatusBadRequest, map[string]any{"erro": err.Error(), "painel": painel})
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"saida": saida, "painel": painel, "git": g.EstadosGit(r.Context(), false)})
	})
}
