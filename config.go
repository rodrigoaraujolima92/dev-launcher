package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Compose descreve como o servico e levantado pelo docker compose.
// Projeto e obrigatorio e precisa bater com o nome de projeto ja usado na maquina
// (dockerappdaturma, projetos): e ele que faz o compose reaproveitar o container
// existente em vez de criar um segundo com outro nome.
type Compose struct {
	Projeto string `json:"projeto"`
	Arquivo string `json:"arquivo"` // relativo a raiz, ou absoluto
	Servico string `json:"servico"`
}

// Checagem e como o launcher decide que um servico esta "pronto" - nao basta o
// processo existir, a dependencia so libera quando ele realmente atende.
type Checagem struct {
	Tipo  string   `json:"tipo"` // porta | comando | http
	Porta int      `json:"porta,omitempty"`
	Cmd   []string `json:"cmd,omitempty"`
	URL   string   `json:"url,omitempty"`
}

type Servico struct {
	ID          string   `json:"id"`
	Nome        string   `json:"nome"`
	Grupo       string   `json:"grupo"` // id de um Grupo
	Tipo        string   `json:"tipo"`  // docker | app
	Compose     *Compose `json:"compose,omitempty"`
	Container   string   `json:"container,omitempty"`
	Dir         string   `json:"dir,omitempty"` // relativo a raiz, ou absoluto
	Cmd         string   `json:"cmd,omitempty"`
	Porta       int      `json:"porta,omitempty"`
	URL         string   `json:"url,omitempty"`
	Pronto      Checagem `json:"pronto"`
	Timeout     int      `json:"timeout"` // segundos esperando ficar pronto
	Modo        string   `json:"modo,omitempty"`
	Depende     []string `json:"depende"`
	Selecionado bool     `json:"selecionado"`
}

// Grupo e uma secao da tela: serve para separar os projetos por empresa/produto
// (appdaturma, photonow, isugar) ou por camada (infra, apis). A ordem do array e a
// ordem em que as secoes aparecem.
type Grupo struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

// Perfil e uma stack salva: o conjunto de servicos que voce quer subir junto naquele
// contexto ("so as APIs", "front + backend"). Aplicar um perfil marca exatamente esses
// servicos e desmarca o resto.
type Perfil struct {
	ID       string   `json:"id"`
	Nome     string   `json:"nome"`
	Servicos []string `json:"servicos"`
}

type Config struct {
	PortaUI       int        `json:"porta_ui"`
	RedeDocker    string     `json:"rede_docker"`
	RaizNavegacao string     `json:"raiz_navegacao,omitempty"` // onde o seletor de pastas comeca
	Grupos        []*Grupo   `json:"grupos"`
	Perfis        []*Perfil  `json:"perfis"`
	PerfilAtivo   string     `json:"perfil_ativo,omitempty"`
	Servicos      []*Servico `json:"servicos"`
}

const (
	TipoDocker = "docker"
	TipoApp    = "app"

	ModoGerenciado = "gerenciado" // o launcher segura o processo e mostra o log aqui
	ModoTerminal   = "terminal"   // abre uma aba do Windows Terminal, como o start-dev.ps1
)

// nomesConhecidos traduz os grupos da primeira versao (quando grupo era so um texto solto
// no servico) para um nome apresentavel na hora de migrar o config.
var nomesConhecidos = map[string]string{
	"infra": "infraestrutura",
	"api":   "apis",
	"web":   "front-ends",
}

func carregarConfig(caminho string) (*Config, error) {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(dados, &cfg); err != nil {
		return nil, fmt.Errorf("config invalido: %w", err)
	}
	cfg.normalizar()
	if err := cfg.validar(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// salvarConfig grava via arquivo temporario + rename: se o processo morrer no meio da
// escrita, o config.json antigo continua inteiro em vez de virar um JSON truncado.
func salvarConfig(caminho string, cfg *Config) error {
	if err := cfg.validar(); err != nil {
		return err
	}
	dados, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dados = append(dados, '\n')
	tmp := caminho + ".tmp"
	if err := os.WriteFile(tmp, dados, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

// normalizar completa o que o arquivo pode nao ter: config da versao anterior nao tinha
// grupos nem perfis, e servico cadastrado na mao pode vir sem grupo.
func (c *Config) normalizar() {
	existe := map[string]bool{}
	for _, g := range c.Grupos {
		existe[g.ID] = true
	}
	for _, s := range c.Servicos {
		if s.Grupo == "" {
			s.Grupo = "geral"
		}
		if !existe[s.Grupo] {
			nome := nomesConhecidos[s.Grupo]
			if nome == "" {
				nome = s.Grupo
			}
			c.Grupos = append(c.Grupos, &Grupo{ID: s.Grupo, Nome: nome})
			existe[s.Grupo] = true
		}
		if s.Depende == nil {
			s.Depende = []string{}
		}
	}
	if c.Grupos == nil {
		c.Grupos = []*Grupo{}
	}
	if c.Perfis == nil {
		c.Perfis = []*Perfil{}
	}
	// Perfil que cita servico apagado fora da tela nao pode derrubar o launcher.
	ids := map[string]bool{}
	for _, s := range c.Servicos {
		ids[s.ID] = true
	}
	for _, p := range c.Perfis {
		limpo := []string{}
		for _, id := range p.Servicos {
			if ids[id] {
				limpo = append(limpo, id)
			}
		}
		p.Servicos = limpo
	}
}

func (c *Config) porID(id string) *Servico {
	for _, s := range c.Servicos {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (c *Config) grupoPorID(id string) *Grupo {
	for _, g := range c.Grupos {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func (c *Config) perfilPorID(id string) *Perfil {
	for _, p := range c.Perfis {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (c *Config) ids() []string {
	out := make([]string, 0, len(c.Servicos))
	for _, s := range c.Servicos {
		out = append(out, s.ID)
	}
	return out
}

func (c *Config) validar() error {
	gruposVistos := map[string]bool{}
	for _, g := range c.Grupos {
		if g.ID == "" {
			return fmt.Errorf("grupo sem id")
		}
		if gruposVistos[g.ID] {
			return fmt.Errorf("grupo repetido: %s", g.ID)
		}
		if strings.TrimSpace(g.Nome) == "" {
			return fmt.Errorf("grupo %s sem nome", g.ID)
		}
		gruposVistos[g.ID] = true
	}

	if len(c.Servicos) == 0 {
		return fmt.Errorf("nenhum servico definido")
	}
	vistos := map[string]bool{}
	for _, s := range c.Servicos {
		if err := validarServico(s); err != nil {
			return err
		}
		if vistos[s.ID] {
			return fmt.Errorf("id repetido: %s", s.ID)
		}
		vistos[s.ID] = true
		if !gruposVistos[s.Grupo] {
			return fmt.Errorf("%s esta no grupo %q, que nao existe", s.ID, s.Grupo)
		}
	}

	for _, s := range c.Servicos {
		for _, d := range s.Depende {
			if !vistos[d] {
				return fmt.Errorf("%s depende de %q, que nao existe", s.ID, d)
			}
		}
	}

	perfisVistos := map[string]bool{}
	for _, p := range c.Perfis {
		if p.ID == "" {
			return fmt.Errorf("perfil sem id")
		}
		if perfisVistos[p.ID] {
			return fmt.Errorf("perfil repetido: %s", p.ID)
		}
		if strings.TrimSpace(p.Nome) == "" {
			return fmt.Errorf("perfil %s sem nome", p.ID)
		}
		perfisVistos[p.ID] = true
		for _, id := range p.Servicos {
			if !vistos[id] {
				return fmt.Errorf("perfil %s cita o servico %q, que nao existe", p.Nome, id)
			}
		}
	}
	if c.PerfilAtivo != "" && !perfisVistos[c.PerfilAtivo] {
		c.PerfilAtivo = "" // perfil apagado: so limpa, nao e motivo para recusar o config
	}

	if ciclo := acharCiclo(c); ciclo != nil {
		return fmt.Errorf("dependencia circular: %s", strings.Join(ciclo, " -> "))
	}
	return nil
}

func validarServico(s *Servico) error {
	if s.ID == "" {
		return fmt.Errorf("servico sem id")
	}
	if strings.TrimSpace(s.Nome) == "" {
		return fmt.Errorf("%s: servico sem nome", s.ID)
	}

	switch s.Tipo {
	case TipoDocker:
		if s.Compose == nil || s.Compose.Arquivo == "" || s.Compose.Servico == "" || s.Compose.Projeto == "" {
			return fmt.Errorf("%s: servico docker precisa de compose.projeto, compose.arquivo e compose.servico", s.ID)
		}
	case TipoApp:
		if s.Dir == "" || s.Cmd == "" {
			return fmt.Errorf("%s: servico app precisa de pasta e comando", s.ID)
		}
		if s.Modo != "" && s.Modo != ModoGerenciado && s.Modo != ModoTerminal {
			return fmt.Errorf("%s: modo invalido %q (use %s ou %s)", s.ID, s.Modo, ModoGerenciado, ModoTerminal)
		}
	default:
		return fmt.Errorf("%s: tipo invalido %q (use %s ou %s)", s.ID, s.Tipo, TipoDocker, TipoApp)
	}

	if s.Porta < 0 || s.Porta > 65535 {
		return fmt.Errorf("%s: porta fora da faixa (%d)", s.ID, s.Porta)
	}
	if s.Timeout < 0 {
		return fmt.Errorf("%s: timeout negativo", s.ID)
	}

	switch s.Pronto.Tipo {
	case "porta":
		if s.Pronto.Porta <= 0 || s.Pronto.Porta > 65535 {
			return fmt.Errorf("%s: checagem por porta sem porta valida", s.ID)
		}
	case "comando":
		if len(s.Pronto.Cmd) == 0 {
			return fmt.Errorf("%s: checagem por comando sem cmd", s.ID)
		}
	case "http":
		if s.Pronto.URL == "" {
			return fmt.Errorf("%s: checagem http sem url", s.ID)
		}
	default:
		return fmt.Errorf("%s: pronto.tipo invalido %q (use porta, comando ou http)", s.ID, s.Pronto.Tipo)
	}

	for _, d := range s.Depende {
		if d == s.ID {
			return fmt.Errorf("%s depende de si mesmo", s.ID)
		}
	}
	return nil
}

// acharCiclo devolve o caminho do primeiro ciclo encontrado, ou nil.
// Existe porque as dependencias sao editaveis pela tela: sem esta trava, um "A espera B,
// B espera A" deixaria os dois travados esperando para sempre, sem erro visivel.
func acharCiclo(c *Config) []string {
	const (
		novo      = 0
		visitando = 1
		fechado   = 2
	)
	cor := map[string]int{}
	var caminho []string
	var ciclo []string

	var visitar func(id string) bool
	visitar = func(id string) bool {
		s := c.porID(id)
		if s == nil {
			return false
		}
		cor[id] = visitando
		caminho = append(caminho, id)
		for _, d := range s.Depende {
			switch cor[d] {
			case novo:
				if visitar(d) {
					return true
				}
			case visitando:
				inicio := 0
				for i, v := range caminho {
					if v == d {
						inicio = i
						break
					}
				}
				ciclo = append(append([]string{}, caminho[inicio:]...), d)
				return true
			}
		}
		caminho = caminho[:len(caminho)-1]
		cor[id] = fechado
		return false
	}

	for _, s := range c.Servicos {
		if cor[s.ID] == novo {
			if visitar(s.ID) {
				return ciclo
			}
		}
	}
	return nil
}

// expandirDependencias devolve o conjunto fechado: os ids pedidos mais tudo que eles
// esperam, recursivamente. Marcar so o frontend e mandar subir tem que levantar backend,
// db e rabbit junto - senao o frontend sobe apontando para uma API que nao existe.
func expandirDependencias(c *Config, ids []string) []string {
	conjunto := map[string]bool{}
	var incluir func(id string)
	incluir = func(id string) {
		if conjunto[id] {
			return
		}
		s := c.porID(id)
		if s == nil {
			return
		}
		conjunto[id] = true
		for _, d := range s.Depende {
			incluir(d)
		}
	}
	for _, id := range ids {
		incluir(id)
	}

	// Ordem final segue a do config para a tela nao ficar dancando a cada clique.
	out := []string{}
	for _, s := range c.Servicos {
		if conjunto[s.ID] {
			out = append(out, s.ID)
		}
	}
	return out
}

// ondas agrupa os ids em niveis: a onda 0 nao espera ninguem do conjunto, a onda 1 espera
// alguem da 0, e assim por diante. Serve so para a tela mostrar o plano de subida - quem
// manda na execucao sao as dependencias, servico por servico.
func ondas(c *Config, ids []string) [][]string {
	conjunto := map[string]bool{}
	for _, id := range ids {
		conjunto[id] = true
	}
	nivel := map[string]int{}

	var calcular func(id string, pilha map[string]bool) int
	calcular = func(id string, pilha map[string]bool) int {
		if n, ok := nivel[id]; ok {
			return n
		}
		if pilha[id] { // ciclo: validar() ja barra, mas nao travamos aqui de novo
			return 0
		}
		pilha[id] = true
		maior := -1
		if s := c.porID(id); s != nil {
			for _, d := range s.Depende {
				if !conjunto[d] {
					continue
				}
				if n := calcular(d, pilha); n > maior {
					maior = n
				}
			}
		}
		delete(pilha, id)
		nivel[id] = maior + 1
		return nivel[id]
	}

	maiorNivel := 0
	for _, id := range ids {
		if n := calcular(id, map[string]bool{}); n > maiorNivel {
			maiorNivel = n
		}
	}

	out := make([][]string, maiorNivel+1)
	for _, s := range c.Servicos { // ordem do config dentro de cada onda
		if !conjunto[s.ID] {
			continue
		}
		n := nivel[s.ID]
		out[n] = append(out[n], s.ID)
	}
	return out
}

// ------------------------------------------------------------------
// edicao rapida (selecao / modo / dependencias)
// ------------------------------------------------------------------

type EdicaoServico struct {
	ID          string   `json:"id"`
	Selecionado bool     `json:"selecionado"`
	Modo        string   `json:"modo,omitempty"`
	Depende     []string `json:"depende,omitempty"`
}

// aplicarEdicao cuida do que muda a todo clique na tela: marcar/desmarcar, trocar o modo e
// ligar dependencias. Cadastro completo (pasta, comando, checagem) passa por salvarServico.
func aplicarEdicao(cfg *Config, edicoes []EdicaoServico) error {
	novo := clonarConfig(cfg)
	for _, e := range edicoes {
		s := novo.porID(e.ID)
		if s == nil {
			return fmt.Errorf("servico desconhecido: %s", e.ID)
		}
		s.Selecionado = e.Selecionado
		if e.Modo != "" {
			s.Modo = e.Modo
		}
		if e.Depende != nil {
			s.Depende = limparLista(e.Depende)
			sort.Strings(s.Depende)
		}
	}
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// ------------------------------------------------------------------
// cadastro de servicos
// ------------------------------------------------------------------

// EntradaServico e o cadastro completo vindo da tela. CaminhoLivre acompanha o checkbox
// "usar caminho fora da pasta de projetos".
type EntradaServico struct {
	ID           string   `json:"id,omitempty"`
	Nome         string   `json:"nome"`
	Grupo        string   `json:"grupo"`
	Tipo         string   `json:"tipo"`
	Dir          string   `json:"dir,omitempty"`
	Cmd          string   `json:"cmd,omitempty"`
	Compose      *Compose `json:"compose,omitempty"`
	Container    string   `json:"container,omitempty"`
	Porta        int      `json:"porta,omitempty"`
	URL          string   `json:"url,omitempty"`
	Pronto       Checagem `json:"pronto"`
	Timeout      int      `json:"timeout"`
	Modo         string   `json:"modo,omitempty"`
	Depende      []string `json:"depende"`
	Selecionado  bool     `json:"selecionado"`
	CaminhoLivre bool     `json:"caminho_livre,omitempty"`
}

// salvarServico cria (ID vazio) ou substitui um servico. Devolve o id gravado.
// raizProjeto resolve caminho relativo; raizNavegacao e a cerca padrao de pastas.
func salvarServico(cfg *Config, entrada EntradaServico, raizProjeto, raizNavegacao string) (string, error) {
	novo := clonarConfig(cfg)

	s := &Servico{
		Nome:        strings.TrimSpace(entrada.Nome),
		Grupo:       strings.TrimSpace(entrada.Grupo),
		Tipo:        entrada.Tipo,
		Compose:     entrada.Compose,
		Container:   strings.TrimSpace(entrada.Container),
		Dir:         strings.TrimSpace(entrada.Dir),
		Cmd:         strings.TrimSpace(entrada.Cmd),
		Porta:       entrada.Porta,
		URL:         strings.TrimSpace(entrada.URL),
		Pronto:      entrada.Pronto,
		Timeout:     entrada.Timeout,
		Modo:        entrada.Modo,
		Depende:     limparLista(entrada.Depende),
		Selecionado: entrada.Selecionado,
	}
	if s.Grupo == "" && len(novo.Grupos) > 0 {
		s.Grupo = novo.Grupos[0].ID
	}
	if s.Timeout == 0 {
		s.Timeout = 120
	}
	// Checagem em branco: porta e o caso comum, entao deduz da porta informada.
	if s.Pronto.Tipo == "" && s.Porta > 0 {
		s.Pronto = Checagem{Tipo: "porta", Porta: s.Porta}
	}
	if s.Tipo == TipoApp && s.Modo == "" {
		s.Modo = ModoGerenciado
	}

	if err := conferirCaminhos(s, raizProjeto, raizNavegacao, entrada.CaminhoLivre); err != nil {
		return "", err
	}

	if entrada.ID == "" {
		usados := map[string]bool{}
		for _, existente := range novo.Servicos {
			usados[existente.ID] = true
		}
		s.ID = gerarID(s.Nome, usados)
		novo.Servicos = append(novo.Servicos, s)
	} else {
		anterior := novo.porID(entrada.ID)
		if anterior == nil {
			return "", fmt.Errorf("servico desconhecido: %s", entrada.ID)
		}
		s.ID = anterior.ID
		*anterior = *s
	}

	if err := novo.validar(); err != nil {
		return "", err
	}
	*cfg = *novo
	return s.ID, nil
}

// conferirCaminhos garante que a pasta/compose existe e, salvo pedido explicito, que fica
// dentro da raiz de navegacao. A cerca e o padrao justamente porque este campo vira
// diretorio de execucao de um comando.
func conferirCaminhos(s *Servico, raizProjeto, raizNavegacao string, livre bool) error {
	verificar := func(rel, rotulo string, precisaPasta bool) error {
		if rel == "" {
			return nil
		}
		abs := caminhoAbsoluto(raizProjeto, rel)
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("%s nao encontrado: %s", rotulo, abs)
		}
		if precisaPasta && !info.IsDir() {
			return fmt.Errorf("%s nao e uma pasta: %s", rotulo, abs)
		}
		if !precisaPasta && info.IsDir() {
			return fmt.Errorf("%s deveria ser um arquivo: %s", rotulo, abs)
		}
		if !livre && raizNavegacao != "" && !dentroDe(raizNavegacao, abs) {
			return fmt.Errorf("%s fica fora de %s - marque \"caminho livre\" para usar assim mesmo", rotulo, raizNavegacao)
		}
		return nil
	}

	if s.Tipo == TipoApp {
		return verificar(s.Dir, "a pasta do projeto", true)
	}
	if s.Compose != nil {
		return verificar(s.Compose.Arquivo, "o arquivo compose", false)
	}
	return nil
}

// removerServico apaga o servico e limpa as sobras: dependencias de outros servicos e
// citacoes nos perfis. Sem isso o config ficaria invalido no proximo salvamento.
func removerServico(cfg *Config, id string) error {
	novo := clonarConfig(cfg)
	if novo.porID(id) == nil {
		return fmt.Errorf("servico desconhecido: %s", id)
	}
	if len(novo.Servicos) == 1 {
		return fmt.Errorf("nao da para apagar o unico servico do config")
	}

	restantes := []*Servico{}
	for _, s := range novo.Servicos {
		if s.ID == id {
			continue
		}
		s.Depende = remover(s.Depende, id)
		restantes = append(restantes, s)
	}
	novo.Servicos = restantes
	for _, p := range novo.Perfis {
		p.Servicos = remover(p.Servicos, id)
	}

	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// ------------------------------------------------------------------
// grupos
// ------------------------------------------------------------------

// salvarGrupo cria (id vazio) ou renomeia um grupo.
func salvarGrupo(cfg *Config, id, nome string) (string, error) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		return "", fmt.Errorf("o grupo precisa de um nome")
	}
	novo := clonarConfig(cfg)

	if id == "" {
		usados := map[string]bool{}
		for _, g := range novo.Grupos {
			usados[g.ID] = true
		}
		g := &Grupo{ID: gerarID(nome, usados), Nome: nome}
		novo.Grupos = append(novo.Grupos, g)
		if err := novo.validar(); err != nil {
			return "", err
		}
		*cfg = *novo
		return g.ID, nil
	}

	g := novo.grupoPorID(id)
	if g == nil {
		return "", fmt.Errorf("grupo desconhecido: %s", id)
	}
	g.Nome = nome
	if err := novo.validar(); err != nil {
		return "", err
	}
	*cfg = *novo
	return g.ID, nil
}

// removerGrupo so apaga grupo vazio: mover os projetos antes e decisao de quem edita, nao
// do launcher - apagar em cascata levaria projeto junto sem querer.
func removerGrupo(cfg *Config, id string) error {
	novo := clonarConfig(cfg)
	if novo.grupoPorID(id) == nil {
		return fmt.Errorf("grupo desconhecido: %s", id)
	}
	for _, s := range novo.Servicos {
		if s.Grupo == id {
			return fmt.Errorf("o grupo ainda tem projetos (%s) - mova ou apague antes", s.Nome)
		}
	}
	restantes := []*Grupo{}
	for _, g := range novo.Grupos {
		if g.ID != id {
			restantes = append(restantes, g)
		}
	}
	novo.Grupos = restantes
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// ordenarGrupos reordena as secoes da tela. Ids omitidos vao para o fim, na ordem atual.
func ordenarGrupos(cfg *Config, ordem []string) error {
	novo := clonarConfig(cfg)
	vistos := map[string]bool{}
	nova := []*Grupo{}
	for _, id := range ordem {
		g := novo.grupoPorID(id)
		if g == nil {
			return fmt.Errorf("grupo desconhecido: %s", id)
		}
		if vistos[id] {
			continue
		}
		vistos[id] = true
		nova = append(nova, g)
	}
	for _, g := range novo.Grupos {
		if !vistos[g.ID] {
			nova = append(nova, g)
		}
	}
	novo.Grupos = nova
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// ------------------------------------------------------------------
// perfis
// ------------------------------------------------------------------

// salvarPerfil cria (id vazio) ou atualiza uma stack salva.
func salvarPerfil(cfg *Config, id, nome string, servicos []string) (string, error) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		return "", fmt.Errorf("o perfil precisa de um nome")
	}
	novo := clonarConfig(cfg)
	lista := limparLista(servicos)
	if len(lista) == 0 {
		return "", fmt.Errorf("o perfil %q nao tem nenhum projeto marcado", nome)
	}

	var p *Perfil
	if id == "" {
		usados := map[string]bool{}
		for _, existente := range novo.Perfis {
			usados[existente.ID] = true
		}
		p = &Perfil{ID: gerarID(nome, usados)}
		novo.Perfis = append(novo.Perfis, p)
	} else {
		p = novo.perfilPorID(id)
		if p == nil {
			return "", fmt.Errorf("perfil desconhecido: %s", id)
		}
	}
	p.Nome = nome
	p.Servicos = lista
	novo.PerfilAtivo = p.ID

	if err := novo.validar(); err != nil {
		return "", err
	}
	*cfg = *novo
	return p.ID, nil
}

func removerPerfil(cfg *Config, id string) error {
	novo := clonarConfig(cfg)
	if novo.perfilPorID(id) == nil {
		return fmt.Errorf("perfil desconhecido: %s", id)
	}
	restantes := []*Perfil{}
	for _, p := range novo.Perfis {
		if p.ID != id {
			restantes = append(restantes, p)
		}
	}
	novo.Perfis = restantes
	if novo.PerfilAtivo == id {
		novo.PerfilAtivo = ""
	}
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// aplicarPerfil marca exatamente os servicos do perfil e desmarca o resto.
func aplicarPerfil(cfg *Config, id string) error {
	novo := clonarConfig(cfg)
	p := novo.perfilPorID(id)
	if p == nil {
		return fmt.Errorf("perfil desconhecido: %s", id)
	}
	doPerfil := map[string]bool{}
	for _, sid := range p.Servicos {
		doPerfil[sid] = true
	}
	for _, s := range novo.Servicos {
		s.Selecionado = doPerfil[s.ID]
	}
	novo.PerfilAtivo = id
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

// ------------------------------------------------------------------
// utilitarios
// ------------------------------------------------------------------

func clonarConfig(c *Config) *Config {
	novo := &Config{
		PortaUI:       c.PortaUI,
		RedeDocker:    c.RedeDocker,
		RaizNavegacao: c.RaizNavegacao,
		PerfilAtivo:   c.PerfilAtivo,
		Grupos:        []*Grupo{},
		Perfis:        []*Perfil{},
		Servicos:      []*Servico{},
	}
	for _, g := range c.Grupos {
		copia := *g
		novo.Grupos = append(novo.Grupos, &copia)
	}
	for _, p := range c.Perfis {
		copia := *p
		copia.Servicos = append([]string{}, p.Servicos...)
		novo.Perfis = append(novo.Perfis, &copia)
	}
	for _, s := range c.Servicos {
		copia := *s
		copia.Depende = append([]string{}, s.Depende...)
		if s.Compose != nil {
			comp := *s.Compose
			copia.Compose = &comp
		}
		if s.Pronto.Cmd != nil {
			copia.Pronto.Cmd = append([]string{}, s.Pronto.Cmd...)
		}
		novo.Servicos = append(novo.Servicos, &copia)
	}
	return novo
}

// caminhoAbsoluto resolve um caminho do config (relativo a raiz, ou ja absoluto).
func caminhoAbsoluto(raiz, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(raiz, filepath.FromSlash(rel)))
}

// dentroDe diz se caminho esta sob raiz. Compara caso-insensitivo porque no Windows
// "C:\Users" e "c:\users" sao a mesma pasta.
func dentroDe(raiz, caminho string) bool {
	rel, err := filepath.Rel(filepath.Clean(raiz), filepath.Clean(caminho))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// gerarID transforma um nome em identificador estavel (appdaturma-backend), sem acento e
// sem espaco, e desempata acrescentando -2, -3 quando ja existe.
func gerarID(nome string, usados map[string]bool) string {
	var b strings.Builder
	anteriorTraco := false
	for _, r := range strings.ToLower(strings.TrimSpace(nome)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			anteriorTraco = false
		case unicode.IsLetter(r):
			if s := semAcento(r); s != 0 {
				b.WriteRune(s)
				anteriorTraco = false
			}
		default:
			if !anteriorTraco && b.Len() > 0 {
				b.WriteByte('-')
				anteriorTraco = true
			}
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "servico"
	}
	if !usados[base] {
		return base
	}
	for i := 2; ; i++ {
		tentativa := fmt.Sprintf("%s-%d", base, i)
		if !usados[tentativa] {
			return tentativa
		}
	}
}

func semAcento(r rune) rune {
	const acentuadas = "áàâãäéèêëíìîïóòôõöúùûüçñ"
	const simples = "aaaaaeeeeiiiiooooouuuucn"
	if i := strings.IndexRune(acentuadas, r); i >= 0 {
		return rune(simples[len([]rune(acentuadas[:i]))])
	}
	return 0
}

func limparLista(itens []string) []string {
	visto := map[string]bool{}
	out := []string{}
	for _, v := range itens {
		v = strings.TrimSpace(v)
		if v == "" || visto[v] {
			continue
		}
		visto[v] = true
		out = append(out, v)
	}
	return out
}

func remover(lista []string, alvo string) []string {
	out := []string{}
	for _, v := range lista {
		if v != alvo {
			out = append(out, v)
		}
	}
	return out
}
