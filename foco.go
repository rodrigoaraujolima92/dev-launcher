package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A aba "foco": tarefas com data e lembrete, e as estacoes de lofi (links do YouTube).
//
// Fica num arquivo proprio, o foco.json ao lado do config.json, e nao no config: tarefa e
// coisa pessoal que muda a cada clique, e o config e o retrato dos projetos. Misturar os
// dois faria cada tarefa marcada aparecer como mudanca no config de quem versiona o seu.
//
// O lembrete sai do launcher, e nao do navegador: assim ele chega como notificacao do
// Windows mesmo com a aba fechada, enquanto o launcher estiver no ar.

const (
	StatusAFazer  = "afazer"
	StatusFazendo = "fazendo"
	StatusFeito   = "feito"

	// Tarefa com data e sem hora conta a partir deste horario para o lembrete.
	horaPadraoLembrete = "09:00"
	// Lembrete que passou ha mais que isso (launcher desligado, por exemplo) nao vira
	// notificacao: e marcado como dado e fica so o "atrasada" na tela.
	toleranciaLembrete = 6 * time.Hour
	// Uma semana antes e o maximo que o seletor oferece; acima disso e quase certo engano.
	maxLembreteMin = 7 * 24 * 60
)

type Tarefa struct {
	ID         string `json:"id"`
	Titulo     string `json:"titulo"`
	Notas      string `json:"notas,omitempty"`
	Status     string `json:"status"`               // afazer | fazendo | feito
	Prioridade string `json:"prioridade,omitempty"` // "" (normal) | alta | baixa
	Data       string `json:"data,omitempty"`       // AAAA-MM-DD
	Hora       string `json:"hora,omitempty"`       // HH:MM
	Lembrete   *int   `json:"lembrete,omitempty"`   // minutos antes; nil = sem aviso
	// Avisado guarda o instante do lembrete que ja saiu. Mudar data, hora ou antecedencia
	// muda o instante, e o lembrete volta a valer sozinho - sem flag para lembrar de zerar.
	Avisado   string     `json:"avisado,omitempty"`
	Criada    time.Time  `json:"criada"`
	Concluida *time.Time `json:"concluida,omitempty"`
}

// Estacao e um link do YouTube que o player toca: um video (live ou nao), uma playlist ou
// os dois (video dentro da playlist).
type Estacao struct {
	ID    string `json:"id"`
	Nome  string `json:"nome"`
	URL   string `json:"url"`
	Video string `json:"video,omitempty"`
	Lista string `json:"lista,omitempty"`
}

type DadosFoco struct {
	Tarefas  []*Tarefa  `json:"tarefas"`
	Estacoes []*Estacao `json:"estacoes"`
}

// AvisoSistema e uma notificacao do Windows. Fixo deixa o aviso na tela ate a pessoa
// dispensar (cenario "reminder"), que e o que um lembrete de tarefa pede.
type AvisoSistema struct {
	Titulo string
	Texto  string
	Abrir  string // URL aberta ao clicar no aviso
	Fixo   bool
}

type Foco struct {
	mu      sync.Mutex
	caminho string
	dados   *DadosFoco

	emitir  func(map[string]any)     // evento para a tela (SSE)
	avisar  func(AvisoSistema) error // notificacao do sistema; os testes trocam
	agora   func() time.Time
	urlTela string // endereco da aba foco, aberto ao clicar na notificacao
	// buscarNome descobre o titulo de um link novo (oEmbed do YouTube). Os testes trocam
	// para nao depender de rede.
	buscarNome func(ctx context.Context, link string) string
}

// estacoesPadrao entram so quando o foco.json ainda nao existe: a pessoa ja abre a aba com
// algo para tocar, e apaga o que nao quiser. Live do YouTube muda de ID quando o canal
// reinicia a transmissao: quando uma parar de tocar, troque o link pela tela.
func estacoesPadrao() []*Estacao {
	return []*Estacao{
		{ID: "lofi-girl", Nome: "lofi girl · beats to relax/study", URL: "https://www.youtube.com/watch?v=rFZHOHl-L8A", Video: "rFZHOHl-L8A"},
		{ID: "lofi-girl-sleep", Nome: "lofi girl · beats to sleep/chill", URL: "https://www.youtube.com/watch?v=JD-kMIpDfnY", Video: "JD-kMIpDfnY"},
		{ID: "lofi-girl-jazz", Nome: "lofi girl · jazz lofi", URL: "https://www.youtube.com/watch?v=E2vONfzoyRI", Video: "E2vONfzoyRI"},
		{ID: "synthwave", Nome: "lofi girl · synthwave", URL: "https://www.youtube.com/watch?v=4xDzrJKXOOY", Video: "4xDzrJKXOOY"},
		{ID: "chillhop", Nome: "chillhop radio · jazzy & lofi", URL: "https://www.youtube.com/watch?v=5yx6BWlEVcY", Video: "5yx6BWlEVcY"},
	}
}

// NovoFoco le o foco.json (ou comeca um novo, com as estacoes padrao). Arquivo ilegivel
// e erro: sobrescrever calado jogaria as tarefas fora.
func NovoFoco(caminho string) (*Foco, error) {
	f := &Foco{
		caminho:    caminho,
		emitir:     func(map[string]any) {},
		avisar:     avisoDoSistema,
		agora:      time.Now,
		buscarNome: nomeNoYouTube,
	}
	bruto, err := os.ReadFile(caminho)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f.dados = &DadosFoco{Tarefas: []*Tarefa{}, Estacoes: estacoesPadrao()}
		return f, nil
	case err != nil:
		return nil, err
	}
	var d DadosFoco
	if err := json.Unmarshal(bruto, &d); err != nil {
		return nil, fmt.Errorf("%s ilegivel: %w", caminho, err)
	}
	if d.Tarefas == nil {
		d.Tarefas = []*Tarefa{}
	}
	if d.Estacoes == nil {
		d.Estacoes = []*Estacao{}
	}
	f.dados = &d
	return f, nil
}

// Dados devolve uma copia: quem recebe pode serializar sem segurar a trava.
func (f *Foco) Dados() *DadosFoco {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonarFoco(f.dados)
}

func clonarFoco(d *DadosFoco) *DadosFoco {
	out := &DadosFoco{Tarefas: make([]*Tarefa, 0, len(d.Tarefas)), Estacoes: make([]*Estacao, 0, len(d.Estacoes))}
	for _, t := range d.Tarefas {
		c := *t
		if t.Lembrete != nil {
			v := *t.Lembrete
			c.Lembrete = &v
		}
		if t.Concluida != nil {
			v := *t.Concluida
			c.Concluida = &v
		}
		out.Tarefas = append(out.Tarefas, &c)
	}
	for _, e := range d.Estacoes {
		c := *e
		out.Estacoes = append(out.Estacoes, &c)
	}
	return out
}

// mutar aplica a mudanca, grava e avisa a tela. Se o disco recusar, a memoria volta ao
// que era - a tela e o arquivo nunca ficam divergentes.
func (f *Foco) mutar(fn func(d *DadosFoco) error) error {
	f.mu.Lock()
	antes := clonarFoco(f.dados)
	if err := fn(f.dados); err != nil {
		f.dados = antes
		f.mu.Unlock()
		return err
	}
	if err := salvarFoco(f.caminho, f.dados); err != nil {
		f.dados = antes
		f.mu.Unlock()
		return fmt.Errorf("nao consegui gravar %s: %w", f.caminho, err)
	}
	copia := clonarFoco(f.dados)
	f.mu.Unlock()
	f.emitir(map[string]any{"tipo": "foco", "foco": copia})
	return nil
}

func salvarFoco(caminho string, d *DadosFoco) error {
	if caminho == "" {
		return nil
	}
	var dados bytes.Buffer
	enc := json.NewEncoder(&dados)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return err
	}
	tmp := caminho + ".tmp"
	if err := os.WriteFile(tmp, dados.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

func novoIDFoco() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ------------------------------------------------------------------
// tarefas
// ------------------------------------------------------------------

type EntradaTarefa struct {
	ID         string `json:"id"`
	Titulo     string `json:"titulo"`
	Notas      string `json:"notas"`
	Status     string `json:"status"`
	Prioridade string `json:"prioridade"`
	Data       string `json:"data"`
	Hora       string `json:"hora"`
	Lembrete   *int   `json:"lembrete"`
}

func (e *EntradaTarefa) validar() error {
	e.Titulo = strings.TrimSpace(e.Titulo)
	e.Notas = strings.TrimSpace(e.Notas)
	e.Data = strings.TrimSpace(e.Data)
	e.Hora = strings.TrimSpace(e.Hora)
	if e.Titulo == "" {
		return errors.New("a tarefa precisa de um titulo")
	}
	if utf8.RuneCountInString(e.Titulo) > 200 {
		return errors.New("titulo grande demais (maximo 200 caracteres)")
	}
	if utf8.RuneCountInString(e.Notas) > 4000 {
		return errors.New("notas grandes demais (maximo 4000 caracteres)")
	}
	switch e.Status {
	case "":
		e.Status = StatusAFazer
	case StatusAFazer, StatusFazendo, StatusFeito:
	default:
		return fmt.Errorf("status desconhecido: %s", e.Status)
	}
	switch e.Prioridade {
	case "", "alta", "baixa":
	case "normal":
		e.Prioridade = ""
	default:
		return fmt.Errorf("prioridade desconhecida: %s", e.Prioridade)
	}
	if e.Data != "" {
		if _, err := time.Parse("2006-01-02", e.Data); err != nil {
			return fmt.Errorf("data invalida: %s (use AAAA-MM-DD)", e.Data)
		}
	}
	if e.Hora != "" {
		if e.Data == "" {
			return errors.New("hora sem data: escolha o dia da tarefa")
		}
		if _, err := time.Parse("15:04", e.Hora); err != nil {
			return fmt.Errorf("hora invalida: %s (use HH:MM)", e.Hora)
		}
	}
	if e.Lembrete != nil {
		if e.Data == "" {
			// Sem dia nao existe quando avisar: o lembrete cai em vez de dar erro, que e o
			// que acontece quando se apaga a data de uma tarefa que tinha aviso.
			e.Lembrete = nil
		} else if *e.Lembrete < 0 || *e.Lembrete > maxLembreteMin {
			return errors.New("lembrete fora do intervalo (de 0 a 7 dias antes)")
		}
	}
	return nil
}

// SalvarTarefa cria (ID vazio) ou atualiza uma tarefa e devolve o ID.
func (f *Foco) SalvarTarefa(e EntradaTarefa) (string, error) {
	if err := e.validar(); err != nil {
		return "", err
	}
	id := strings.TrimSpace(e.ID)
	err := f.mutar(func(d *DadosFoco) error {
		agora := f.agora()
		if id == "" {
			id = novoIDFoco()
			t := &Tarefa{ID: id, Criada: agora}
			preencherTarefa(t, e, agora)
			// Nova entra no topo: e a que a pessoa acabou de pensar.
			d.Tarefas = append([]*Tarefa{t}, d.Tarefas...)
			return nil
		}
		t := tarefaPorID(d, id)
		if t == nil {
			return fmt.Errorf("tarefa %s nao encontrada", id)
		}
		preencherTarefa(t, e, agora)
		return nil
	})
	return id, err
}

func preencherTarefa(t *Tarefa, e EntradaTarefa, agora time.Time) {
	t.Titulo, t.Notas, t.Prioridade = e.Titulo, e.Notas, e.Prioridade
	t.Data, t.Hora, t.Lembrete = e.Data, e.Hora, e.Lembrete
	mudarStatus(t, e.Status, agora)
}

func mudarStatus(t *Tarefa, status string, agora time.Time) {
	if status == StatusFeito && t.Status != StatusFeito {
		c := agora
		t.Concluida = &c
	}
	if status != StatusFeito {
		t.Concluida = nil
	}
	t.Status = status
}

func (f *Foco) MudarStatus(id, status string) error {
	switch status {
	case StatusAFazer, StatusFazendo, StatusFeito:
	default:
		return fmt.Errorf("status desconhecido: %s", status)
	}
	return f.mutar(func(d *DadosFoco) error {
		t := tarefaPorID(d, id)
		if t == nil {
			return fmt.Errorf("tarefa %s nao encontrada", id)
		}
		mudarStatus(t, status, f.agora())
		return nil
	})
}

func (f *Foco) RemoverTarefa(id string) error {
	return f.mutar(func(d *DadosFoco) error {
		for i, t := range d.Tarefas {
			if t.ID == id {
				d.Tarefas = append(d.Tarefas[:i], d.Tarefas[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("tarefa %s nao encontrada", id)
	})
}

// LimparFeitas tira da lista tudo que ja foi concluido e devolve quantas sairam.
func (f *Foco) LimparFeitas() (int, error) {
	n := 0
	err := f.mutar(func(d *DadosFoco) error {
		fica := d.Tarefas[:0]
		for _, t := range d.Tarefas {
			if t.Status == StatusFeito {
				n++
				continue
			}
			fica = append(fica, t)
		}
		d.Tarefas = fica
		return nil
	})
	return n, err
}

func tarefaPorID(d *DadosFoco, id string) *Tarefa {
	for _, t := range d.Tarefas {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// ------------------------------------------------------------------
// lembretes
// ------------------------------------------------------------------

// momentoDoLembrete diz quando a tarefa avisa. ok=false quando ela nao tem lembrete.
func momentoDoLembrete(t *Tarefa) (time.Time, bool) {
	if t.Lembrete == nil || t.Data == "" {
		return time.Time{}, false
	}
	hora := t.Hora
	if hora == "" {
		hora = horaPadraoLembrete
	}
	base, err := time.ParseInLocation("2006-01-02 15:04", t.Data+" "+hora, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return base.Add(-time.Duration(*t.Lembrete) * time.Minute), true
}

// Vigiar confere os lembretes de tempos em tempos ate o contexto acabar.
func (f *Foco) Vigiar(ctx context.Context, intervalo time.Duration) {
	f.VerificarLembretes()
	pulso := time.NewTicker(intervalo)
	defer pulso.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pulso.C:
			f.VerificarLembretes()
		}
	}
}

// VerificarLembretes dispara o que venceu e devolve as tarefas avisadas. A marca de
// "avisado" e gravada antes de notificar: se o Windows recusar a notificacao, ela nao
// fica repetindo a cada volta.
func (f *Foco) VerificarLembretes() []*Tarefa {
	agora := f.agora()
	var vencidas []*Tarefa
	var avisar []bool
	err := f.mutar(func(d *DadosFoco) error {
		for _, t := range d.Tarefas {
			if t.Status == StatusFeito {
				continue
			}
			quando, ok := momentoDoLembrete(t)
			if !ok || agora.Before(quando) {
				continue
			}
			marca := quando.Format(time.RFC3339)
			if t.Avisado == marca {
				continue
			}
			t.Avisado = marca
			c := *t
			vencidas = append(vencidas, &c)
			avisar = append(avisar, agora.Sub(quando) <= toleranciaLembrete)
		}
		if len(vencidas) == 0 {
			return errSemMudanca
		}
		return nil
	})
	if err != nil {
		return nil
	}
	saiu := []*Tarefa{}
	for i, t := range vencidas {
		if !avisar[i] {
			continue
		}
		saiu = append(saiu, t)
		texto := textoDoLembrete(t, agora)
		f.emitir(map[string]any{"tipo": "lembrete", "tarefa": t, "texto": texto})
		aviso := AvisoSistema{Titulo: t.Titulo, Texto: texto, Abrir: f.urlTela, Fixo: true}
		go func() {
			if err := f.avisar(aviso); err != nil {
				f.emitir(map[string]any{"tipo": "aviso", "texto": "notificacao do sistema falhou: " + err.Error(), "erro": true})
			}
		}()
	}
	return saiu
}

// errSemMudanca desfaz o mutar sem gravar nem emitir: a volta do vigia que nao achou nada
// nao pode reescrever o arquivo a cada 15 segundos.
var errSemMudanca = errors.New("sem mudanca")

func textoDoLembrete(t *Tarefa, agora time.Time) string {
	if t.Hora == "" {
		dia, _ := time.ParseInLocation("2006-01-02", t.Data, time.Local)
		return "tarefa para " + diaRelativo(dia, agora)
	}
	inicio, err := time.ParseInLocation("2006-01-02 15:04", t.Data+" "+t.Hora, time.Local)
	if err != nil {
		return "lembrete"
	}
	falta := inicio.Sub(agora).Round(time.Minute)
	quando := diaRelativo(inicio, agora) + " as " + t.Hora
	switch {
	case falta <= 0:
		return "agora · " + quando
	case falta < time.Hour:
		return fmt.Sprintf("em %d min · %s", int(falta.Minutes()), quando)
	default:
		return "comeca " + quando
	}
}

func diaRelativo(dia, agora time.Time) string {
	a, m, d := agora.Date()
	hoje := time.Date(a, m, d, 0, 0, 0, 0, time.Local)
	a, m, d = dia.Date()
	alvo := time.Date(a, m, d, 0, 0, 0, 0, time.Local)
	switch int(alvo.Sub(hoje).Hours() / 24) {
	case 0:
		return "hoje"
	case 1:
		return "amanha"
	case -1:
		return "ontem"
	}
	return alvo.Format("02/01")
}

// ------------------------------------------------------------------
// estacoes (links do YouTube)
// ------------------------------------------------------------------

var (
	reVideoYT = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	reListaYT = regexp.MustCompile(`^[A-Za-z0-9_-]{10,64}$`)
)

// lerLinkYouTube aceita as formas que se copiam do navegador - watch?v=, youtu.be/, /live/,
// /embed/, /shorts/, playlist?list= - e o ID solto. So o ID e guardado para tocar: o resto
// da URL nunca chega no player.
func lerLinkYouTube(bruto string) (video, lista string, err error) {
	bruto = strings.TrimSpace(bruto)
	if reVideoYT.MatchString(bruto) {
		return bruto, "", nil
	}
	if !strings.Contains(bruto, "://") {
		bruto = "https://" + bruto
	}
	u, err := url.Parse(bruto)
	if err != nil || u.Host == "" {
		return "", "", errors.New("link invalido: cole o endereco de um video, live ou playlist do YouTube")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(strings.TrimPrefix(host, "m."), "music.")
	partes := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch host {
	case "youtu.be":
		video = partes[0]
	case "youtube.com", "youtube-nocookie.com":
		switch {
		case len(partes) >= 2 && (partes[0] == "live" || partes[0] == "embed" || partes[0] == "shorts" || partes[0] == "v"):
			video = partes[1]
		default:
			video = u.Query().Get("v")
		}
	default:
		return "", "", errors.New("so links do YouTube (youtube.com ou youtu.be)")
	}
	lista = u.Query().Get("list")
	if video != "" && !reVideoYT.MatchString(video) {
		return "", "", fmt.Errorf("id de video invalido: %s", video)
	}
	if lista != "" && !reListaYT.MatchString(lista) {
		return "", "", fmt.Errorf("id de playlist invalido: %s", lista)
	}
	if video == "" && lista == "" {
		return "", "", errors.New("o link nao tem video nem playlist")
	}
	return video, lista, nil
}

func (f *Foco) AdicionarEstacao(ctx context.Context, nome, link string) (string, error) {
	video, lista, err := lerLinkYouTube(link)
	if err != nil {
		return "", err
	}
	nome = strings.TrimSpace(nome)
	if utf8.RuneCountInString(nome) > 80 {
		return "", errors.New("nome grande demais (maximo 80 caracteres)")
	}
	canonica := "https://www.youtube.com/watch?v=" + video
	if video == "" {
		canonica = "https://www.youtube.com/playlist?list=" + lista
	} else if lista != "" {
		canonica += "&list=" + lista
	}
	if nome == "" && f.buscarNome != nil {
		nome = f.buscarNome(ctx, canonica)
	}
	if nome == "" {
		nome = "lofi " + video + lista
	}
	id := novoIDFoco()
	err = f.mutar(func(d *DadosFoco) error {
		for _, e := range d.Estacoes {
			if e.Video == video && e.Lista == lista {
				return fmt.Errorf("esse link ja esta na lista (%s)", e.Nome)
			}
		}
		d.Estacoes = append(d.Estacoes, &Estacao{ID: id, Nome: nome, URL: canonica, Video: video, Lista: lista})
		return nil
	})
	return id, err
}

func (f *Foco) RemoverEstacao(id string) error {
	return f.mutar(func(d *DadosFoco) error {
		for i, e := range d.Estacoes {
			if e.ID == id {
				d.Estacoes = append(d.Estacoes[:i], d.Estacoes[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("estacao %s nao encontrada", id)
	})
}

// nomeNoYouTube pega o titulo pelo oEmbed publico do YouTube. Sem rede ou sem resposta,
// volta vazio e a estacao fica com um nome generico - nao vale travar o cadastro por isso.
func nomeNoYouTube(ctx context.Context, link string) string {
	ctx, cancelar := context.WithTimeout(ctx, 5*time.Second)
	defer cancelar()
	endereco := "https://www.youtube.com/oembed?format=json&url=" + url.QueryEscape(link)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var corpo struct {
		Titulo string `json:"title"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&corpo); err != nil {
		return ""
	}
	nome := strings.TrimSpace(corpo.Titulo)
	if utf8.RuneCountInString(nome) > 80 {
		nome = string([]rune(nome)[:79]) + "…"
	}
	return nome
}

// ------------------------------------------------------------------
// rotas
// ------------------------------------------------------------------

func rotasFoco(mux *http.ServeMux, f *Foco) {
	mux.HandleFunc("GET /api/foco", func(w http.ResponseWriter, r *http.Request) {
		responderJSON(w, http.StatusOK, f.Dados())
	})

	mux.HandleFunc("POST /api/foco/tarefas", func(w http.ResponseWriter, r *http.Request) {
		var e EntradaTarefa
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&e); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		id, err := f.SalvarTarefa(e)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"id": id, "foco": f.Dados()})
	})

	mux.HandleFunc("POST /api/foco/tarefas/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		var corpo struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&corpo); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		if err := f.MudarStatus(r.PathValue("id"), corpo.Status); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"foco": f.Dados()})
	})

	mux.HandleFunc("DELETE /api/foco/tarefas/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := f.RemoverTarefa(r.PathValue("id")); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"foco": f.Dados()})
	})

	mux.HandleFunc("POST /api/foco/tarefas/limpar-feitas", func(w http.ResponseWriter, r *http.Request) {
		n, err := f.LimparFeitas()
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"removidas": n, "foco": f.Dados()})
	})

	mux.HandleFunc("POST /api/foco/estacoes", func(w http.ResponseWriter, r *http.Request) {
		var corpo struct {
			Nome string `json:"nome"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&corpo); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		id, err := f.AdicionarEstacao(r.Context(), corpo.Nome, corpo.URL)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"id": id, "foco": f.Dados()})
	})

	mux.HandleFunc("DELETE /api/foco/estacoes/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := f.RemoverEstacao(r.PathValue("id")); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"foco": f.Dados()})
	})

	// Notificacao avulsa: fim do pomodoro e o botao "testar notificacao".
	mux.HandleFunc("POST /api/foco/notificar", func(w http.ResponseWriter, r *http.Request) {
		var corpo struct {
			Titulo string `json:"titulo"`
			Texto  string `json:"texto"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&corpo); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		titulo := strings.TrimSpace(corpo.Titulo)
		if titulo == "" || utf8.RuneCountInString(titulo) > 120 || utf8.RuneCountInString(corpo.Texto) > 300 {
			responderErro(w, http.StatusBadRequest, errors.New("titulo vazio ou texto grande demais"))
			return
		}
		if err := f.avisar(AvisoSistema{Titulo: titulo, Texto: strings.TrimSpace(corpo.Texto), Abrir: f.urlTela}); err != nil {
			responderErro(w, http.StatusBadGateway, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"enviado": true})
	})
}
