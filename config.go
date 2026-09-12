package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Compose descreve como o servico e levantado pelo docker compose.
// Projeto e obrigatorio e precisa bater com o nome de projeto ja usado na maquina
// (dockerappdaturma, projetos): e ele que faz o compose reaproveitar o container
// existente em vez de criar um segundo com outro nome.
type Compose struct {
	Projeto string `json:"projeto"`
	Arquivo string `json:"arquivo"` // relativo a raiz
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
	Grupo       string   `json:"grupo"`
	Tipo        string   `json:"tipo"` // docker | app
	Compose     *Compose `json:"compose,omitempty"`
	Container   string   `json:"container,omitempty"`
	Dir         string   `json:"dir,omitempty"` // relativo a raiz
	Cmd         string   `json:"cmd,omitempty"`
	Porta       int      `json:"porta,omitempty"`
	URL         string   `json:"url,omitempty"`
	Pronto      Checagem `json:"pronto"`
	Timeout     int      `json:"timeout"` // segundos esperando ficar pronto
	Modo        string   `json:"modo,omitempty"`
	Depende     []string `json:"depende"`
	Selecionado bool     `json:"selecionado"`
}

type Config struct {
	PortaUI    int        `json:"porta_ui"`
	RedeDocker string     `json:"rede_docker"`
	Servicos   []*Servico `json:"servicos"`
}

const (
	TipoDocker = "docker"
	TipoApp    = "app"

	ModoGerenciado = "gerenciado" // o launcher segura o processo e mostra o log aqui
	ModoTerminal   = "terminal"   // abre uma aba do Windows Terminal, como o start-dev.ps1
)

func carregarConfig(caminho string) (*Config, error) {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(dados, &cfg); err != nil {
		return nil, fmt.Errorf("config invalido: %w", err)
	}
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

func (c *Config) porID(id string) *Servico {
	for _, s := range c.Servicos {
		if s.ID == id {
			return s
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
	if len(c.Servicos) == 0 {
		return fmt.Errorf("nenhum servico definido")
	}
	vistos := map[string]bool{}
	for _, s := range c.Servicos {
		if s.ID == "" {
			return fmt.Errorf("servico sem id")
		}
		if vistos[s.ID] {
			return fmt.Errorf("id repetido: %s", s.ID)
		}
		vistos[s.ID] = true

		switch s.Tipo {
		case TipoDocker:
			if s.Compose == nil || s.Compose.Arquivo == "" || s.Compose.Servico == "" || s.Compose.Projeto == "" {
				return fmt.Errorf("%s: servico docker precisa de compose.projeto, compose.arquivo e compose.servico", s.ID)
			}
		case TipoApp:
			if s.Dir == "" || s.Cmd == "" {
				return fmt.Errorf("%s: servico app precisa de dir e cmd", s.ID)
			}
			if s.Modo != "" && s.Modo != ModoGerenciado && s.Modo != ModoTerminal {
				return fmt.Errorf("%s: modo invalido %q (use %s ou %s)", s.ID, s.Modo, ModoGerenciado, ModoTerminal)
			}
		default:
			return fmt.Errorf("%s: tipo invalido %q (use %s ou %s)", s.ID, s.Tipo, TipoDocker, TipoApp)
		}

		switch s.Pronto.Tipo {
		case "porta":
			if s.Pronto.Porta == 0 {
				return fmt.Errorf("%s: checagem por porta sem porta", s.ID)
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
	}

	for _, s := range c.Servicos {
		for _, d := range s.Depende {
			if !vistos[d] {
				return fmt.Errorf("%s depende de %q, que nao existe", s.ID, d)
			}
		}
	}

	if ciclo := acharCiclo(c); ciclo != nil {
		return fmt.Errorf("dependencia circular: %s", strings.Join(ciclo, " -> "))
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

// aplicarEdicao copia da tela so o que a tela pode mudar (selecao, modo e dependencias).
// Caminho, comando e checagem ficam de fora de proposito: quem mexe neles e o config.json,
// nao o navegador - um POST torto nao deve conseguir trocar o comando que sera executado.
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
			limpo := []string{}
			visto := map[string]bool{}
			for _, d := range e.Depende {
				if d == "" || visto[d] {
					continue
				}
				visto[d] = true
				limpo = append(limpo, d)
			}
			sort.Strings(limpo)
			s.Depende = limpo
		}
	}
	if err := novo.validar(); err != nil {
		return err
	}
	*cfg = *novo
	return nil
}

type EdicaoServico struct {
	ID          string   `json:"id"`
	Selecionado bool     `json:"selecionado"`
	Modo        string   `json:"modo,omitempty"`
	Depende     []string `json:"depende,omitempty"`
}

func clonarConfig(c *Config) *Config {
	novo := &Config{PortaUI: c.PortaUI, RedeDocker: c.RedeDocker}
	for _, s := range c.Servicos {
		copia := *s
		copia.Depende = append([]string{}, s.Depende...)
		if s.Compose != nil {
			comp := *s.Compose
			copia.Compose = &comp
		}
		copia.Pronto.Cmd = append([]string{}, s.Pronto.Cmd...)
		novo.Servicos = append(novo.Servicos, &copia)
	}
	return novo
}

// caminhoAbsoluto resolve um caminho do config (sempre relativo a raiz) em caminho real.
func caminhoAbsoluto(raiz, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(raiz, filepath.FromSlash(rel)))
}
