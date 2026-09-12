package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// executor de mentira
// ------------------------------------------------------------------

type comportamento struct {
	jaPronto bool          // ja estava no ar antes da subida
	erro     error         // falha na hora de iniciar
	demora   time.Duration // quanto tempo leva para ficar pronto depois de iniciado
	nunca    bool          // sobe mas nunca fica pronto (testa timeout)
}

type execFake struct {
	mu        sync.Mutex
	ids       []string
	comp      map[string]comportamento
	iniciado  map[string]time.Time
	ordem     []string
	prontosAo map[string][]string // quem ja estava pronto quando cada um foi iniciado
	parados   []string
}

func novoExecFake(ids []string, comp map[string]comportamento) *execFake {
	if comp == nil {
		comp = map[string]comportamento{}
	}
	return &execFake{
		ids:       ids,
		comp:      comp,
		iniciado:  map[string]time.Time{},
		prontosAo: map[string][]string{},
	}
}

func (e *execFake) prontoSemTrava(id string) bool {
	c := e.comp[id]
	if c.jaPronto {
		return true
	}
	if c.nunca {
		return false
	}
	inicio, ok := e.iniciado[id]
	if !ok {
		return false
	}
	return time.Since(inicio) >= c.demora
}

func (e *execFake) Pronto(_ context.Context, s *Servico) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prontoSemTrava(s.ID)
}

func (e *execFake) Iniciar(_ context.Context, s *Servico) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.comp[s.ID].erro; err != nil {
		return err
	}
	prontos := []string{}
	for _, id := range e.ids {
		if e.prontoSemTrava(id) {
			prontos = append(prontos, id)
		}
	}
	sort.Strings(prontos)
	e.prontosAo[s.ID] = prontos
	e.ordem = append(e.ordem, s.ID)
	e.iniciado[s.ID] = time.Now()
	return nil
}

func (e *execFake) Parar(_ context.Context, s *Servico) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.parados = append(e.parados, s.ID)
	delete(e.iniciado, s.ID)
	c := e.comp[s.ID]
	c.jaPronto = false
	e.comp[s.ID] = c
	return nil
}

func (e *execFake) instantaneo() (ordem []string, prontosAo map[string][]string, parados []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	prontosAo = map[string][]string{}
	for k, v := range e.prontosAo {
		prontosAo[k] = append([]string{}, v...)
	}
	return append([]string{}, e.ordem...), prontosAo, append([]string{}, e.parados...)
}

// ------------------------------------------------------------------
// apoio
// ------------------------------------------------------------------

func gerenteTeste(t *testing.T, comp map[string]comportamento) (*Gerente, *execFake, *Config) {
	t.Helper()
	cfg := configTeste()
	fake := novoExecFake(cfg.ids(), comp)
	g := NovoGerente(t.TempDir(), "", cfg, fake)
	g.intervalo = 5 * time.Millisecond // nos testes a checagem de "pronto" e bem mais rapida
	return g, fake, cfg
}

// subirEEsperar dispara a subida e so volta quando a orquestracao termina (evento "fim").
func subirEEsperar(t *testing.T, g *Gerente, ids []string, prazo time.Duration) Plano {
	t.Helper()
	inscricao, ch := g.Inscrever()
	defer g.Desinscrever(inscricao)

	plano, err := g.Subir(context.Background(), ids)
	if err != nil {
		t.Fatalf("subir: %v", err)
	}

	limite := time.After(prazo)
	for {
		select {
		case dados := <-ch:
			var evento struct {
				Tipo string `json:"tipo"`
			}
			if json.Unmarshal(dados, &evento) == nil && evento.Tipo == "fim" {
				return plano
			}
		case <-limite:
			t.Fatalf("a subida nao terminou em %s; estados: %s", prazo, resumo(g))
		}
	}
}

func resumo(g *Gerente) string {
	out := ""
	for _, e := range g.Estados() {
		out += e.ID + "=" + e.Status + "(" + e.Mensagem + ") "
	}
	return out
}

func statusDe(g *Gerente, id string) *Estado {
	for _, e := range g.Estados() {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func posicao(lista []string, alvo string) int {
	for i, v := range lista {
		if v == alvo {
			return i
		}
	}
	return -1
}

// ------------------------------------------------------------------
// testes
// ------------------------------------------------------------------

func TestSubirEsperaAsDependenciasFicaremProntas(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{
		"db":     {demora: 40 * time.Millisecond},
		"rabbit": {demora: 20 * time.Millisecond},
		"api":    {demora: 10 * time.Millisecond},
		"web":    {demora: 10 * time.Millisecond},
	})

	plano := subirEEsperar(t, g, []string{"web"}, 5*time.Second)

	if len(plano.Ondas) != 3 {
		t.Fatalf("plano deveria ter 3 ondas, veio %v", plano.Ondas)
	}

	ordem, prontosAo, _ := fake.instantaneo()
	if posicao(ordem, "api") < posicao(ordem, "db") || posicao(ordem, "api") < posicao(ordem, "rabbit") {
		t.Fatalf("api subiu antes das dependencias: %v", ordem)
	}
	if posicao(ordem, "web") < posicao(ordem, "api") {
		t.Fatalf("web subiu antes da api: %v", ordem)
	}

	// O ponto central: quando a api foi iniciada, db e rabbit ja tinham que estar PRONTOS -
	// nao basta terem sido iniciados.
	for _, dep := range []string{"db", "rabbit"} {
		if !contem(prontosAo["api"], dep) {
			t.Fatalf("api iniciou com %s ainda nao pronto (prontos: %v)", dep, prontosAo["api"])
		}
	}
	if !contem(prontosAo["web"], "api") {
		t.Fatalf("web iniciou com a api ainda nao pronta (prontos: %v)", prontosAo["web"])
	}

	for _, id := range []string{"db", "rabbit", "api", "web"} {
		if e := statusDe(g, id); e.Status != StatusPronto {
			t.Fatalf("%s terminou como %s (%s)", id, e.Status, e.Mensagem)
		}
	}
}

func TestSubirSobeEmParaleloQuemNaoDependeDeNinguem(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{
		"db":     {demora: 150 * time.Millisecond},
		"rabbit": {demora: 150 * time.Millisecond},
	})

	inicio := time.Now()
	subirEEsperar(t, g, []string{"db", "rabbit"}, 5*time.Second)
	gasto := time.Since(inicio)

	// Em serie daria 300ms+. Com folga generosa para maquina lenta, 280ms ja denuncia serie.
	if gasto > 280*time.Millisecond {
		t.Fatalf("db e rabbit parecem ter subido em serie (%s)", gasto)
	}
	if ordem, _, _ := fake.instantaneo(); len(ordem) != 2 {
		t.Fatalf("esperava 2 inicios, veio %v", ordem)
	}
}

func TestSubirNaoReiniciaQuemJaEstaNoAr(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{
		"db":     {jaPronto: true},
		"rabbit": {jaPronto: true},
		"api":    {demora: 10 * time.Millisecond},
	})

	subirEEsperar(t, g, []string{"api"}, 5*time.Second)

	ordem, _, _ := fake.instantaneo()
	if contem(ordem, "db") || contem(ordem, "rabbit") {
		t.Fatalf("nao deveria reiniciar quem ja estava no ar: %v", ordem)
	}
	if e := statusDe(g, "db"); e.Mensagem != "ja estava no ar" {
		t.Fatalf("db deveria avisar que ja estava no ar, veio %q", e.Mensagem)
	}
}

func TestFalhaNaDependenciaBloqueiaQuemEspera(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{
		"db":     {erro: errors.New("docker fora do ar")},
		"rabbit": {demora: 5 * time.Millisecond},
	})

	subirEEsperar(t, g, []string{"web"}, 5*time.Second)

	if e := statusDe(g, "db"); e.Status != StatusErro {
		t.Fatalf("db deveria estar em erro, veio %s", e.Status)
	}
	for _, id := range []string{"api", "web"} {
		e := statusDe(g, id)
		if e.Status != StatusBloqueado {
			t.Fatalf("%s deveria estar bloqueado, veio %s (%s)", id, e.Status, e.Mensagem)
		}
	}
	if ordem, _, _ := fake.instantaneo(); contem(ordem, "api") || contem(ordem, "web") {
		t.Fatalf("nada que depende do db deveria ter sido iniciado: %v", ordem)
	}
	// O rabbit nao depende do db: tem que ter subido mesmo com o db quebrado.
	if e := statusDe(g, "rabbit"); e.Status != StatusPronto {
		t.Fatalf("rabbit deveria continuar subindo normalmente, veio %s", e.Status)
	}
}

func TestServicoQueNuncaFicaProntoViraErroPorTimeout(t *testing.T) {
	g, _, cfg := gerenteTeste(t, map[string]comportamento{
		"db": {nunca: true},
	})
	cfg.porID("db").Timeout = 1 // segundo

	subirEEsperar(t, g, []string{"db"}, 6*time.Second)

	e := statusDe(g, "db")
	if e.Status != StatusErro {
		t.Fatalf("db deveria dar erro por timeout, veio %s", e.Status)
	}
	if e.Mensagem == "" {
		t.Fatal("o erro de timeout deveria explicar o motivo")
	}
}

func TestDuasSubidasAoMesmoTempoSaoRecusadas(t *testing.T) {
	g, _, _ := gerenteTeste(t, map[string]comportamento{
		"db": {demora: 300 * time.Millisecond},
	})

	if _, err := g.Subir(context.Background(), []string{"db"}); err != nil {
		t.Fatalf("primeira subida: %v", err)
	}
	if _, err := g.Subir(context.Background(), []string{"db"}); !errors.Is(err, ErrSubidaEmAndamento) {
		t.Fatalf("segunda subida deveria ser recusada, veio %v", err)
	}
}

func TestSubirSemNadaSelecionadoDaErro(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	if _, err := g.Subir(context.Background(), []string{"fantasma"}); err == nil {
		t.Fatal("id inexistente deveria dar erro em vez de subir nada calado")
	}
}

func TestPararUsaOExecutorEMarcaParado(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{"db": {jaPronto: true}})
	g.marcar("db", StatusPronto, "")

	if erros := g.Parar(context.Background(), []string{"db"}); len(erros) != 0 {
		t.Fatalf("parar deu erro: %v", erros)
	}
	if _, _, parados := fake.instantaneo(); !contem(parados, "db") {
		t.Fatalf("executor nao foi chamado para parar: %v", parados)
	}
	if e := statusDe(g, "db"); e.Status != StatusParado {
		t.Fatalf("db deveria ficar parado, veio %s", e.Status)
	}
}

func TestVarrerDescobreQuemSubiuOuCaiuPorFora(t *testing.T) {
	g, fake, _ := gerenteTeste(t, map[string]comportamento{"db": {jaPronto: true}})

	g.varrer(context.Background())
	if e := statusDe(g, "db"); e.Status != StatusPronto {
		t.Fatalf("varredura deveria achar o db no ar, veio %s", e.Status)
	}

	fake.mu.Lock()
	fake.comp["db"] = comportamento{}
	fake.mu.Unlock()

	g.varrer(context.Background())
	e := statusDe(g, "db")
	if e.Status != StatusParado || e.Mensagem != "caiu" {
		t.Fatalf("varredura deveria acusar a queda, veio %s (%s)", e.Status, e.Mensagem)
	}
}

func TestEditarGravaNoDiscoEAvisaATela(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "config.json")
	cfg := configTeste()
	if err := salvarConfig(caminho, cfg); err != nil {
		t.Fatal(err)
	}
	g := NovoGerente(t.TempDir(), caminho, cfg, novoExecFake(cfg.ids(), nil))

	inscricao, ch := g.Inscrever()
	defer g.Desinscrever(inscricao)

	if err := g.Editar([]EdicaoServico{{ID: "web", Selecionado: true, Depende: []string{"db"}}}); err != nil {
		t.Fatalf("editar: %v", err)
	}

	lido, err := carregarConfig(caminho)
	if err != nil {
		t.Fatalf("reler config: %v", err)
	}
	if !lido.porID("web").Selecionado {
		t.Fatal("a selecao nao foi gravada no disco")
	}
	if len(lido.porID("web").Depende) != 1 || lido.porID("web").Depende[0] != "db" {
		t.Fatalf("dependencia nao foi gravada: %v", lido.porID("web").Depende)
	}

	select {
	case dados := <-ch:
		var evento struct {
			Tipo string `json:"tipo"`
		}
		if json.Unmarshal(dados, &evento); evento.Tipo != "config" {
			t.Fatalf("esperava evento de config, veio %q", evento.Tipo)
		}
	case <-time.After(time.Second):
		t.Fatal("a tela nao foi avisada da mudanca")
	}
}

func TestEditarInvalidoNaoTocaNoArquivo(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "config.json")
	cfg := configTeste()
	if err := salvarConfig(caminho, cfg); err != nil {
		t.Fatal(err)
	}
	antes, _ := os.ReadFile(caminho)

	g := NovoGerente(t.TempDir(), caminho, cfg, novoExecFake(cfg.ids(), nil))
	if err := g.Editar([]EdicaoServico{{ID: "db", Depende: []string{"web"}}}); err == nil {
		t.Fatal("ciclo deveria ser recusado")
	}

	depois, _ := os.ReadFile(caminho)
	if string(antes) != string(depois) {
		t.Fatal("o config.json foi alterado por uma edicao invalida")
	}
}

func TestAnelSeguraSoAsUltimasLinhas(t *testing.T) {
	a := novoAnel(3)
	for _, l := range []string{"um", "dois", "tres", "quatro"} {
		a.add(l)
	}
	got := a.tudo()
	if len(got) != 3 || got[0] != "dois" || got[2] != "quatro" {
		t.Fatalf("anel = %v, queria [dois tres quatro]", got)
	}
}

func TestLogsFicamPorServico(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	g.RegistrarLog("db", "subindo postgres")
	g.RegistrarLog("api", "porta 7003")

	if linhas := g.Logs("db"); len(linhas) != 1 || linhas[0] != "subindo postgres" {
		t.Fatalf("log do db = %v", linhas)
	}
	if linhas := g.Logs("api"); len(linhas) != 1 {
		t.Fatalf("log da api = %v", linhas)
	}
}

func TestInscritoLentoNaoTravaAOrquestracao(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	_, ch := g.Inscrever() // ninguem le este canal

	pronto := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ { // bem mais que a capacidade do canal
			g.RegistrarLog("db", "linha")
		}
		close(pronto)
	}()

	select {
	case <-pronto:
	case <-time.After(3 * time.Second):
		t.Fatal("um navegador lento travou o launcher")
	}
	if len(ch) == 0 {
		t.Fatal("o canal deveria ter recebido ao menos os primeiros eventos")
	}
}
