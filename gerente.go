package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	StatusParado    = "parado"
	StatusEsperando = "esperando"
	StatusSubindo   = "subindo"
	StatusPronto    = "pronto"
	StatusErro      = "erro"
	StatusBloqueado = "bloqueado" // dependencia falhou, nem tentou subir
)

// Executor isola tudo que toca o sistema operacional (docker, pwsh, portas).
// A orquestracao conversa so com esta interface - e por isso que a ordem de subida e a
// espera por dependencia podem ser testadas sem subir container nenhum.
type Executor interface {
	Iniciar(ctx context.Context, s *Servico) error
	Pronto(ctx context.Context, s *Servico) bool
	Parar(ctx context.Context, s *Servico) error
}

type Estado struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"`
	Mensagem string    `json:"mensagem"`
	PID      int       `json:"pid"`
	Desde    time.Time `json:"desde"`
}

type anel struct {
	itens []string
	max   int
}

func novoAnel(max int) *anel { return &anel{max: max} }

func (a *anel) add(linha string) {
	a.itens = append(a.itens, linha)
	if len(a.itens) > a.max {
		a.itens = a.itens[len(a.itens)-a.max:]
	}
}

func (a *anel) tudo() []string {
	return append([]string{}, a.itens...)
}

type tarefa struct {
	once sync.Once
	err  error
	done chan struct{}
}

type Gerente struct {
	mu         sync.Mutex
	raiz       string
	caminhoCfg string
	cfg        *Config
	exec       Executor
	estados    map[string]*Estado
	logs       map[string]*anel
	inscritos  map[int]chan []byte
	proxID     int
	intervalo  time.Duration // de quanto em quanto tempo repete a checagem de "pronto"
	subindo    bool
}

func NovoGerente(raiz, caminhoCfg string, cfg *Config, exe Executor) *Gerente {
	g := &Gerente{
		raiz:       raiz,
		caminhoCfg: caminhoCfg,
		cfg:        cfg,
		exec:       exe,
		estados:    map[string]*Estado{},
		logs:       map[string]*anel{},
		inscritos:  map[int]chan []byte{},
		intervalo:  time.Second,
	}
	for _, s := range cfg.Servicos {
		g.estados[s.ID] = &Estado{ID: s.ID, Status: StatusParado, Desde: time.Now()}
		g.logs[s.ID] = novoAnel(500)
	}
	return g
}

// ------------------------------------------------------------------
// Estado e eventos
// ------------------------------------------------------------------

func (g *Gerente) Config() *Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	return clonarConfig(g.cfg)
}

func (g *Gerente) Estados() []*Estado {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []*Estado{}
	for _, s := range g.cfg.Servicos {
		if e, ok := g.estados[s.ID]; ok {
			copia := *e
			out = append(out, &copia)
		}
	}
	return out
}

func (g *Gerente) Logs(id string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if a, ok := g.logs[id]; ok {
		return a.tudo()
	}
	return nil
}

func (g *Gerente) marcar(id, status, mensagem string) {
	g.mu.Lock()
	e, ok := g.estados[id]
	if !ok {
		e = &Estado{ID: id}
		g.estados[id] = e
	}
	mudou := e.Status != status || e.Mensagem != mensagem
	if e.Status != status {
		e.Desde = time.Now()
	}
	e.Status = status
	e.Mensagem = mensagem
	g.mu.Unlock()
	if mudou {
		g.emitirEstados()
	}
}

func (g *Gerente) definirPID(id string, pid int) {
	g.mu.Lock()
	if e, ok := g.estados[id]; ok {
		e.PID = pid
	}
	g.mu.Unlock()
}

func (g *Gerente) RegistrarLog(id, linha string) {
	g.mu.Lock()
	a, ok := g.logs[id]
	if !ok {
		a = novoAnel(500)
		g.logs[id] = a
	}
	a.add(linha)
	g.mu.Unlock()
	g.emitir(map[string]any{"tipo": "log", "id": id, "linha": linha})
}

func (g *Gerente) Inscrever() (int, chan []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.proxID++
	ch := make(chan []byte, 128)
	g.inscritos[g.proxID] = ch
	return g.proxID, ch
}

func (g *Gerente) Desinscrever(id int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ch, ok := g.inscritos[id]; ok {
		delete(g.inscritos, id)
		close(ch)
	}
}

func (g *Gerente) emitir(evento map[string]any) {
	dados, err := json.Marshal(evento)
	if err != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, ch := range g.inscritos {
		select {
		case ch <- dados:
		default:
			// Navegador lento nao pode segurar a orquestracao: descarta o evento.
			// O proximo snapshot de estado corrige a tela.
		}
	}
}

func (g *Gerente) emitirEstados() {
	g.emitir(map[string]any{"tipo": "estado", "estados": g.Estados()})
}

// ------------------------------------------------------------------
// Edicao do config pela tela
// ------------------------------------------------------------------

func (g *Gerente) Editar(edicoes []EdicaoServico) error {
	g.mu.Lock()
	if err := aplicarEdicao(g.cfg, edicoes); err != nil {
		g.mu.Unlock()
		return err
	}
	cfg := clonarConfig(g.cfg)
	caminho := g.caminhoCfg
	g.mu.Unlock()

	if caminho != "" {
		if err := salvarConfig(caminho, cfg); err != nil {
			return fmt.Errorf("nao consegui salvar o config: %w", err)
		}
	}
	g.emitir(map[string]any{"tipo": "config", "config": cfg})
	return nil
}

// ------------------------------------------------------------------
// Subida
// ------------------------------------------------------------------

type Plano struct {
	IDs   []string   `json:"ids"`
	Ondas [][]string `json:"ondas"`
}

// Planejar mostra o que aconteceria: o conjunto fechado de servicos e em que ondas eles
// sobem. A tela usa isso para o usuario conferir antes de apertar o botao.
func (g *Gerente) Planejar(ids []string) Plano {
	g.mu.Lock()
	defer g.mu.Unlock()
	completos := expandirDependencias(g.cfg, ids)
	return Plano{IDs: completos, Ondas: ondas(g.cfg, completos)}
}

var ErrSubidaEmAndamento = errors.New("ja existe uma subida em andamento")

// Subir dispara a orquestracao em segundo plano e devolve o plano na hora - quem acompanha
// o andamento e o SSE. Uma subida por vez: duas em paralelo disputariam as mesmas portas.
func (g *Gerente) Subir(ctx context.Context, ids []string) (Plano, error) {
	g.mu.Lock()
	if g.subindo {
		g.mu.Unlock()
		return Plano{}, ErrSubidaEmAndamento
	}
	cfg := g.cfg
	completos := expandirDependencias(cfg, ids)
	if len(completos) == 0 {
		g.mu.Unlock()
		return Plano{}, errors.New("nenhum servico selecionado")
	}
	plano := Plano{IDs: completos, Ondas: ondas(cfg, completos)}
	g.subindo = true
	g.mu.Unlock()

	conjunto := map[string]bool{}
	for _, id := range completos {
		conjunto[id] = true
	}
	tarefas := map[string]*tarefa{}
	for _, id := range completos {
		tarefas[id] = &tarefa{done: make(chan struct{})}
		g.marcar(id, StatusEsperando, "na fila")
	}

	go func() {
		var wg sync.WaitGroup
		for _, id := range completos {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				g.executar(ctx, id, conjunto, tarefas)
			}(id)
		}
		wg.Wait()
		g.mu.Lock()
		g.subindo = false
		g.mu.Unlock()
		g.emitir(map[string]any{"tipo": "fim"})
	}()

	return plano, nil
}

func (g *Gerente) servico(id string) *Servico {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg.porID(id)
}

func (g *Gerente) executar(ctx context.Context, id string, conjunto map[string]bool, tarefas map[string]*tarefa) {
	t := tarefas[id]
	t.once.Do(func() {
		defer close(t.done)

		s := g.servico(id)
		if s == nil {
			t.err = fmt.Errorf("servico %s nao existe", id)
			return
		}

		for _, d := range s.Depende {
			if !conjunto[d] {
				// Dependencia fora do conjunto: o usuario tirou da selecao de proposito
				// (ex.: o banco ja esta no ar por fora). Segue sem esperar.
				continue
			}
			g.marcar(id, StatusEsperando, "esperando "+d)
			select {
			case <-tarefas[d].done:
			case <-ctx.Done():
				t.err = ctx.Err()
				g.marcar(id, StatusErro, "cancelado")
				return
			}
			if tarefas[d].err != nil {
				t.err = fmt.Errorf("dependencia %s falhou", d)
				g.marcar(id, StatusBloqueado, "dependencia "+d+" falhou")
				return
			}
		}

		if g.exec.Pronto(ctx, s) {
			g.marcar(id, StatusPronto, "ja estava no ar")
			return
		}

		g.marcar(id, StatusSubindo, "")
		if err := g.exec.Iniciar(ctx, s); err != nil {
			t.err = err
			g.marcar(id, StatusErro, err.Error())
			return
		}
		if err := g.esperarPronto(ctx, s); err != nil {
			t.err = err
			g.marcar(id, StatusErro, err.Error())
			return
		}
		g.marcar(id, StatusPronto, "")
	})
}

func (g *Gerente) esperarPronto(ctx context.Context, s *Servico) error {
	limite := time.Duration(s.Timeout) * time.Second
	if limite <= 0 {
		limite = 120 * time.Second
	}
	prazo := time.Now().Add(limite)
	for {
		if g.exec.Pronto(ctx, s) {
			return nil
		}
		if time.Now().After(prazo) {
			return fmt.Errorf("nao ficou pronto em %s", limite)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(g.intervalo):
		}
	}
}

// ------------------------------------------------------------------
// Parada
// ------------------------------------------------------------------

func (g *Gerente) Parar(ctx context.Context, ids []string) []error {
	var erros []error
	for _, id := range ids {
		s := g.servico(id)
		if s == nil {
			continue
		}
		if err := g.exec.Parar(ctx, s); err != nil {
			erros = append(erros, fmt.Errorf("%s: %w", id, err))
			g.marcar(id, StatusErro, err.Error())
			continue
		}
		g.definirPID(id, 0)
		g.marcar(id, StatusParado, "")
	}
	return erros
}

// ------------------------------------------------------------------
// Varredura periodica
// ------------------------------------------------------------------

// Vigiar corrige a tela quando algo muda por fora: um container derrubado no Docker
// Desktop, um dev server que morreu sozinho, ou um servico que ja estava no ar quando o
// launcher abriu. Nao mexe em quem esta no meio da subida.
func (g *Gerente) Vigiar(ctx context.Context, intervalo time.Duration) {
	tick := time.NewTicker(intervalo)
	defer tick.Stop()
	for {
		g.varrer(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (g *Gerente) varrer(ctx context.Context) {
	g.mu.Lock()
	servicos := append([]*Servico{}, g.cfg.Servicos...)
	ocupado := g.subindo
	g.mu.Unlock()

	for _, s := range servicos {
		g.mu.Lock()
		status := StatusParado
		if e, ok := g.estados[s.ID]; ok {
			status = e.Status
		}
		g.mu.Unlock()

		// Enquanto uma subida esta em andamento, quem manda no status e a orquestracao.
		if ocupado && (status == StatusSubindo || status == StatusEsperando) {
			continue
		}
		if g.exec.Pronto(ctx, s) {
			if status != StatusPronto {
				g.marcar(s.ID, StatusPronto, "")
			}
			continue
		}
		if status == StatusPronto {
			g.marcar(s.ID, StatusParado, "caiu")
		}
	}
}
