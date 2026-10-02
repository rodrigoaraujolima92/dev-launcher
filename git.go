package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CommitInfo struct {
	Hash   string `json:"hash"`
	Resumo string `json:"resumo"`
	Autor  string `json:"autor"`
	Quando string `json:"quando"` // relativo: "30 hours ago"
}

// EstadoGit e o retrato de um repositorio: onde esta, quanto falta sincronizar e o que ha
// de mexido na copia local.
type EstadoGit struct {
	Repo          bool       `json:"repo"` // a pasta esta dentro de um repositorio git
	Branch        string     `json:"branch"`
	Upstream      string     `json:"upstream"`
	Frente        int        `json:"frente"` // commits locais que o remoto nao tem
	Atras         int        `json:"atras"`  // commits do remoto que faltam aqui
	Preparados    int        `json:"preparados"`
	Modificados   int        `json:"modificados"`
	NaoRastreados int        `json:"nao_rastreados"`
	Conflitos     int        `json:"conflitos"`
	Adicoes       int        `json:"adicoes"`
	Remocoes      int        `json:"remocoes"`
	Commit        CommitInfo `json:"commit"`
	Erro          string     `json:"erro,omitempty"`
}

func (e EstadoGit) Limpo() bool {
	return e.Preparados == 0 && e.Modificados == 0 && e.NaoRastreados == 0 && e.Conflitos == 0
}

// SondaGit isola as chamadas ao git - a interface existe para os testes rodarem sem
// depender de repositorio de verdade no disco.
type SondaGit interface {
	Estado(ctx context.Context, dir string) EstadoGit
	Buscar(ctx context.Context, dir string) error
	Branches(ctx context.Context, dir string) ([]Branch, error)
	Trocar(ctx context.Context, dir string, b Branch) error
}

// Branch e um destino possivel de troca. Remoto vem preenchido quando o branch so existe no
// remoto (origin/feat-x sem feat-x local): trocar para ele cria o local ja acompanhando.
type Branch struct {
	Nome   string `json:"nome"`
	Remoto string `json:"remoto,omitempty"`
}

type SondaGitSO struct{}

func (SondaGitSO) Estado(ctx context.Context, dir string) EstadoGit {
	if !comandoExiste("git") {
		return EstadoGit{Erro: "git nao encontrado no PATH"}
	}
	// porcelain=v2 --branch traz branch, upstream, frente/atras e o estado de cada arquivo
	// numa chamada so.
	saida, err := rodarComando(ctx, 20*time.Second, "git", "-C", dir,
		"status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if err != nil {
		if strings.Contains(strings.ToLower(saida), "not a git repository") {
			return EstadoGit{} // pasta fora de repositorio: nao e erro, so nao tem git
		}
		return EstadoGit{Erro: primeiraLinha(saida)}
	}

	estado := lerPorcelainV2(saida)
	estado.Repo = true

	// HEAD como referencia pega mexido no indice e na copia de trabalho de uma vez.
	if diff, err := rodarComando(ctx, 20*time.Second, "git", "-C", dir, "diff", "--shortstat", "HEAD"); err == nil {
		estado.Adicoes, estado.Remocoes = lerShortstat(diff)
	}
	if log, err := rodarComando(ctx, 10*time.Second, "git", "-C", dir,
		"log", "-1", "--format=%h%x09%s%x09%an%x09%ar"); err == nil {
		estado.Commit = lerCommit(log)
	}
	return estado
}

func (SondaGitSO) Buscar(ctx context.Context, dir string) error {
	// --prune limpa branch remoto que sumiu; sem isso o "atras" continuaria contando contra
	// um upstream que nao existe mais.
	saida, err := rodarComando(ctx, 2*time.Minute, "git", "-C", dir, "fetch", "--prune", "--quiet")
	if err != nil {
		if saida == "" {
			saida = "git fetch falhou"
		}
		return errString(primeiraLinha(saida))
	}
	return nil
}

func (SondaGitSO) Branches(ctx context.Context, dir string) ([]Branch, error) {
	saida, err := rodarComando(ctx, 20*time.Second, "git", "-C", dir,
		"for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		if saida == "" {
			saida = "nao consegui listar os branches"
		}
		return nil, errString(primeiraLinha(saida))
	}
	return lerBranches(saida), nil
}

func (SondaGitSO) Trocar(ctx context.Context, dir string, b Branch) error {
	args := []string{"-C", dir, "switch", b.Nome}
	if b.Remoto != "" {
		// --track explicito em vez de confiar no palpite do git: com dois remotos tendo o
		// mesmo branch, o "git switch nome" puro se recusa por ambiguidade.
		args = []string{"-C", dir, "switch", "--track", b.Remoto + "/" + b.Nome}
	}
	saida, err := rodarComando(ctx, time.Minute, "git", args...)
	if err != nil {
		if saida == "" {
			saida = "git switch falhou"
		}
		return errString(primeiraLinha(saida))
	}
	return nil
}

// lerBranches interpreta a saida de "git for-each-ref --format=%(refname) refs/heads
// refs/remotes": primeiro os locais, depois os que so existem em algum remoto.
//
//	refs/heads/main
//	refs/remotes/origin/HEAD          (ponteiro do remoto, nao e branch)
//	refs/remotes/origin/feat/polaroid
func lerBranches(saida string) []Branch {
	locais := []Branch{}
	remotos := []Branch{}
	visto := map[string]bool{}
	for _, linha := range strings.Split(saida, "\n") {
		linha = strings.TrimSpace(linha)
		if nome, ok := strings.CutPrefix(linha, "refs/heads/"); ok && nome != "" {
			locais = append(locais, Branch{Nome: nome})
			visto[nome] = true
		}
	}
	for _, linha := range strings.Split(saida, "\n") {
		resto, ok := strings.CutPrefix(strings.TrimSpace(linha), "refs/remotes/")
		if !ok {
			continue
		}
		remoto, nome, ok := strings.Cut(resto, "/")
		if !ok || nome == "" || nome == "HEAD" || visto[nome] {
			continue
		}
		remotos = append(remotos, Branch{Nome: nome, Remoto: remoto})
		visto[nome] = true
	}
	return append(locais, remotos...)
}

type errString string

func (e errString) Error() string { return string(e) }

func primeiraLinha(texto string) string {
	texto = strings.TrimSpace(texto)
	if i := strings.IndexAny(texto, "\r\n"); i >= 0 {
		return texto[:i]
	}
	return texto
}

// lerPorcelainV2 interpreta a saida de "git status --porcelain=v2 --branch".
//
//	# branch.head main
//	# branch.upstream origin/main
//	# branch.ab +2 -5
//	1 .M N... 100644 ... arquivo.go      (X = indice, Y = copia de trabalho)
//	? nao-rastreado.txt
func lerPorcelainV2(saida string) EstadoGit {
	var estado EstadoGit
	for _, linha := range strings.Split(saida, "\n") {
		linha = strings.TrimRight(linha, "\r")
		if linha == "" {
			continue
		}
		switch {
		case strings.HasPrefix(linha, "# branch.head "):
			estado.Branch = strings.TrimSpace(strings.TrimPrefix(linha, "# branch.head "))
		case strings.HasPrefix(linha, "# branch.upstream "):
			estado.Upstream = strings.TrimSpace(strings.TrimPrefix(linha, "# branch.upstream "))
		case strings.HasPrefix(linha, "# branch.ab "):
			estado.Frente, estado.Atras = lerFrenteAtras(strings.TrimPrefix(linha, "# branch.ab "))
		case strings.HasPrefix(linha, "1 "), strings.HasPrefix(linha, "2 "):
			campos := strings.Fields(linha)
			if len(campos) < 2 || len(campos[1]) < 2 {
				continue
			}
			// XY: X e o indice (staged), Y a copia de trabalho. "." quer dizer sem mudanca.
			if campos[1][0] != '.' {
				estado.Preparados++
			}
			if campos[1][1] != '.' {
				estado.Modificados++
			}
		case strings.HasPrefix(linha, "u "):
			estado.Conflitos++
		case strings.HasPrefix(linha, "? "):
			estado.NaoRastreados++
		}
	}
	return estado
}

func lerFrenteAtras(texto string) (frente, atras int) {
	for _, campo := range strings.Fields(texto) {
		if len(campo) < 2 {
			continue
		}
		valor, err := strconv.Atoi(campo[1:])
		if err != nil {
			continue
		}
		switch campo[0] {
		case '+':
			frente = valor
		case '-':
			atras = valor
		}
	}
	return frente, atras
}

var (
	reInsercoes = regexp.MustCompile(`(\d+) insertions?\(\+\)`)
	reRemocoes  = regexp.MustCompile(`(\d+) deletions?\(-\)`)
)

// lerShortstat pega os numeros de "1 file changed, 87 insertions(+), 1 deletion(-)".
func lerShortstat(saida string) (adicoes, remocoes int) {
	if achado := reInsercoes.FindStringSubmatch(saida); len(achado) == 2 {
		adicoes, _ = strconv.Atoi(achado[1])
	}
	if achado := reRemocoes.FindStringSubmatch(saida); len(achado) == 2 {
		remocoes, _ = strconv.Atoi(achado[1])
	}
	return adicoes, remocoes
}

func lerCommit(linha string) CommitInfo {
	campos := strings.Split(strings.TrimSpace(primeiraLinha(linha)), "\t")
	info := CommitInfo{}
	if len(campos) > 0 {
		info.Hash = campos[0]
	}
	if len(campos) > 1 {
		info.Resumo = campos[1]
	}
	if len(campos) > 2 {
		info.Autor = campos[2]
	}
	if len(campos) > 3 {
		info.Quando = campos[3]
	}
	return info
}

// ------------------------------------------------------------------
// cache
// ------------------------------------------------------------------

// CacheGit guarda o estado por pasta: varios projetos podem apontar para o mesmo
// repositorio, e "git status" a cada varredura sairia caro sem necessidade.
type CacheGit struct {
	sonda SondaGit
	mu    sync.Mutex
	itens map[string]itemGit
	// Validade de uma leitura antes de consultar o git de novo.
	Validade time.Duration
	// Quantas pastas sao consultadas ao mesmo tempo.
	Paralelo int
}

type itemGit struct {
	estado EstadoGit
	quando time.Time
}

func NovoCacheGit(sonda SondaGit) *CacheGit {
	return &CacheGit{sonda: sonda, itens: map[string]itemGit{}, Validade: 20 * time.Second, Paralelo: 6}
}

// Estados consulta as pastas em paralelo, respeitando o cache. forcar ignora a validade.
func (c *CacheGit) Estados(ctx context.Context, dirs []string, forcar bool) map[string]EstadoGit {
	out := map[string]EstadoGit{}
	pendentes := []string{}

	c.mu.Lock()
	for _, dir := range dirs {
		item, ok := c.itens[dir]
		if ok && !forcar && time.Since(item.quando) < c.Validade {
			out[dir] = item.estado
			continue
		}
		pendentes = append(pendentes, dir)
	}
	c.mu.Unlock()

	if len(pendentes) == 0 {
		return out
	}

	paralelo := c.Paralelo
	if paralelo <= 0 {
		paralelo = 4
	}
	vagas := make(chan struct{}, paralelo)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, dir := range pendentes {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			vagas <- struct{}{}
			defer func() { <-vagas }()

			estado := c.sonda.Estado(ctx, dir)
			mu.Lock()
			out[dir] = estado
			mu.Unlock()

			c.mu.Lock()
			c.itens[dir] = itemGit{estado: estado, quando: time.Now()}
			c.mu.Unlock()
		}(dir)
	}
	wg.Wait()
	return out
}

// Buscar roda o fetch e ja invalida o cache da pasta.
func (c *CacheGit) Buscar(ctx context.Context, dir string) error {
	err := c.sonda.Buscar(ctx, dir)
	c.mu.Lock()
	delete(c.itens, dir)
	c.mu.Unlock()
	return err
}

func (c *CacheGit) Branches(ctx context.Context, dir string) ([]Branch, error) {
	return c.sonda.Branches(ctx, dir)
}

// Trocar muda de branch e invalida o cache da pasta - mesmo quando falha, porque um switch
// recusado no meio pode ter deixado o repositorio diferente do que a tela mostra.
func (c *CacheGit) Trocar(ctx context.Context, dir string, b Branch) error {
	err := c.sonda.Trocar(ctx, dir, b)
	c.mu.Lock()
	delete(c.itens, dir)
	c.mu.Unlock()
	return err
}
