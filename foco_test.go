package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// focoTeste monta um Foco com relogio fixo, sem rede e com a notificacao do sistema
// capturada numa lista.
func focoTeste(t *testing.T, agora time.Time) (*Foco, *[]AvisoSistema, *sync.WaitGroup) {
	t.Helper()
	f, err := NovoFoco(filepath.Join(t.TempDir(), "foco.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	avisos := []AvisoSistema{}
	f.agora = func() time.Time { return agora }
	f.buscarNome = func(context.Context, string) string { return "" }
	f.avisar = func(a AvisoSistema) error {
		mu.Lock()
		avisos = append(avisos, a)
		mu.Unlock()
		wg.Done()
		return nil
	}
	return f, &avisos, &wg
}

func minutos(n int) *int { return &n }

func TestLerLinkYouTube(t *testing.T) {
	casos := []struct {
		link, video, lista string
		erro               bool
	}{
		{link: "https://www.youtube.com/watch?v=jfKfPfyJRdk", video: "jfKfPfyJRdk"},
		{link: "youtube.com/watch?v=jfKfPfyJRdk&t=30s", video: "jfKfPfyJRdk"},
		{link: "https://youtu.be/jfKfPfyJRdk?si=abc", video: "jfKfPfyJRdk"},
		{link: "https://www.youtube.com/live/jfKfPfyJRdk?feature=share", video: "jfKfPfyJRdk"},
		{link: "https://m.youtube.com/embed/jfKfPfyJRdk", video: "jfKfPfyJRdk"},
		{link: "https://music.youtube.com/watch?v=jfKfPfyJRdk", video: "jfKfPfyJRdk"},
		{link: "jfKfPfyJRdk", video: "jfKfPfyJRdk"},
		{link: "https://www.youtube.com/playlist?list=PLOzDu-MXXLliO9fBNZOQTBDddoA3FzZUo", lista: "PLOzDu-MXXLliO9fBNZOQTBDddoA3FzZUo"},
		{link: "https://www.youtube.com/watch?v=jfKfPfyJRdk&list=PLOzDu-MXXLliO9fBNZOQTBDddoA3FzZUo", video: "jfKfPfyJRdk", lista: "PLOzDu-MXXLliO9fBNZOQTBDddoA3FzZUo"},
		{link: "https://vimeo.com/123", erro: true},
		{link: "https://www.youtube.com/watch?v=curto", erro: true},
		{link: "https://www.youtube.com/@LofiGirl", erro: true},
		{link: "", erro: true},
	}
	for _, c := range casos {
		v, l, err := lerLinkYouTube(c.link)
		if c.erro {
			if err == nil {
				t.Errorf("%q deveria dar erro, veio %q %q", c.link, v, l)
			}
			continue
		}
		if err != nil || v != c.video || l != c.lista {
			t.Errorf("%q: veio (%q, %q, %v), esperava (%q, %q)", c.link, v, l, err, c.video, c.lista)
		}
	}
}

func TestFocoNovoVemComEstacoesPadrao(t *testing.T) {
	f, _, _ := focoTeste(t, time.Now())
	if len(f.Dados().Estacoes) == 0 {
		t.Fatal("primeira abertura deveria trazer estacoes para tocar")
	}
}

func TestSalvarTarefaValidaEGrava(t *testing.T) {
	f, _, _ := focoTeste(t, time.Date(2026, 10, 28, 10, 0, 0, 0, time.Local))

	if _, err := f.SalvarTarefa(EntradaTarefa{Titulo: "  "}); err == nil {
		t.Fatal("titulo vazio deveria ser recusado")
	}
	if _, err := f.SalvarTarefa(EntradaTarefa{Titulo: "x", Hora: "10:00"}); err == nil {
		t.Fatal("hora sem data deveria ser recusada")
	}
	if _, err := f.SalvarTarefa(EntradaTarefa{Titulo: "x", Data: "28/10/2026"}); err == nil {
		t.Fatal("data fora do formato deveria ser recusada")
	}

	id, err := f.SalvarTarefa(EntradaTarefa{Titulo: " revisar PR ", Data: "2026-10-28", Hora: "14:30", Lembrete: minutos(10)})
	if err != nil {
		t.Fatal(err)
	}
	// Sem data o lembrete cai sozinho, em vez de dar erro.
	id2, err := f.SalvarTarefa(EntradaTarefa{Titulo: "sem dia", Lembrete: minutos(5)})
	if err != nil {
		t.Fatal(err)
	}

	// Relendo do disco: o que voltou e o que foi gravado.
	relido, err := NovoFoco(f.caminho)
	if err != nil {
		t.Fatal(err)
	}
	d := relido.Dados()
	if len(d.Tarefas) != 2 || d.Tarefas[0].ID != id2 || d.Tarefas[1].ID != id {
		t.Fatalf("a nova entra no topo; veio %+v", d.Tarefas)
	}
	if d.Tarefas[1].Titulo != "revisar PR" || d.Tarefas[1].Status != StatusAFazer {
		t.Fatalf("titulo/status: %+v", d.Tarefas[1])
	}
	if d.Tarefas[0].Lembrete != nil {
		t.Fatal("lembrete sem data deveria ter caido")
	}
}

func TestMudarStatusMarcaConclusao(t *testing.T) {
	f, _, _ := focoTeste(t, time.Now())
	id, _ := f.SalvarTarefa(EntradaTarefa{Titulo: "x"})

	if err := f.MudarStatus(id, StatusFeito); err != nil {
		t.Fatal(err)
	}
	if f.Dados().Tarefas[0].Concluida == nil {
		t.Fatal("feito deveria registrar quando terminou")
	}
	if err := f.MudarStatus(id, StatusAFazer); err != nil {
		t.Fatal(err)
	}
	if f.Dados().Tarefas[0].Concluida != nil {
		t.Fatal("reabrir deveria apagar a conclusao")
	}
	if err := f.MudarStatus(id, "talvez"); err == nil {
		t.Fatal("status desconhecido deveria ser recusado")
	}
}

func TestLembreteDisparaUmaVezSo(t *testing.T) {
	agora := time.Date(2026, 10, 28, 14, 21, 0, 0, time.Local)
	f, avisos, wg := focoTeste(t, agora)

	// Vence as 14:20 (14:30 - 10 min).
	id, _ := f.SalvarTarefa(EntradaTarefa{Titulo: "daily", Data: "2026-10-28", Hora: "14:30", Lembrete: minutos(10)})
	// Ainda nao venceu.
	f.SalvarTarefa(EntradaTarefa{Titulo: "depois", Data: "2026-10-28", Hora: "16:00", Lembrete: minutos(0)})
	// Sem lembrete.
	f.SalvarTarefa(EntradaTarefa{Titulo: "sem aviso", Data: "2026-10-28", Hora: "09:00"})
	// Ja feita.
	idFeita, _ := f.SalvarTarefa(EntradaTarefa{Titulo: "feita", Data: "2026-10-28", Hora: "14:00", Lembrete: minutos(0)})
	f.MudarStatus(idFeita, StatusFeito)

	wg.Add(1)
	saiu := f.VerificarLembretes()
	wg.Wait()
	if len(saiu) != 1 || saiu[0].ID != id {
		t.Fatalf("so a daily deveria avisar, veio %+v", saiu)
	}
	if len(*avisos) != 1 || (*avisos)[0].Titulo != "daily" || !(*avisos)[0].Fixo {
		t.Fatalf("notificacao: %+v", *avisos)
	}
	if !strings.Contains((*avisos)[0].Texto, "em 9 min") {
		t.Fatalf("texto do aviso: %q", (*avisos)[0].Texto)
	}

	// Segunda volta do vigia: nada de novo.
	if saiu := f.VerificarLembretes(); len(saiu) != 0 {
		t.Fatalf("lembrete repetiu: %+v", saiu)
	}

	// Mudar a hora rearma o lembrete.
	f.SalvarTarefa(EntradaTarefa{ID: id, Titulo: "daily", Data: "2026-10-28", Hora: "14:15", Lembrete: minutos(0)})
	wg.Add(1)
	if saiu := f.VerificarLembretes(); len(saiu) != 1 {
		t.Fatalf("hora nova deveria rearmar o lembrete, veio %+v", saiu)
	}
	wg.Wait()
}

func TestLembreteVelhoNaoViraNotificacao(t *testing.T) {
	agora := time.Date(2026, 10, 28, 20, 0, 0, 0, time.Local)
	f, avisos, _ := focoTeste(t, agora)
	f.SalvarTarefa(EntradaTarefa{Titulo: "de manha", Data: "2026-10-28", Hora: "08:00", Lembrete: minutos(0)})

	if saiu := f.VerificarLembretes(); len(saiu) != 0 || len(*avisos) != 0 {
		t.Fatalf("lembrete de 12h atras nao deveria notificar: %+v", saiu)
	}
	// Mas fica marcado: nao volta a ser considerado.
	if f.Dados().Tarefas[0].Avisado == "" {
		t.Fatal("lembrete velho deveria ficar marcado como dado")
	}
}

func TestLembreteSemHoraContaDasNove(t *testing.T) {
	tarefa := &Tarefa{Data: "2026-10-28", Lembrete: minutos(0)}
	quando, ok := momentoDoLembrete(tarefa)
	if !ok || quando.Hour() != 9 || quando.Minute() != 0 {
		t.Fatalf("sem hora deveria avisar as 9h, veio %v %v", quando, ok)
	}
}

func TestVigiaSemNadaNaoRegravaArquivo(t *testing.T) {
	f, _, _ := focoTeste(t, time.Now())
	f.VerificarLembretes()
	if _, err := os.Stat(f.caminho); !os.IsNotExist(err) {
		t.Fatal("volta do vigia sem lembrete nao deveria gravar o arquivo")
	}
}

func TestAdicionarEstacaoRecusaRepetida(t *testing.T) {
	f, _, _ := focoTeste(t, time.Now())
	if _, err := f.AdicionarEstacao(context.Background(), "", "https://youtu.be/aaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.AdicionarEstacao(context.Background(), "outra", "https://www.youtube.com/watch?v=aaaaaaaaaaa"); err == nil {
		t.Fatal("o mesmo video duas vezes deveria ser recusado")
	}
	d := f.Dados()
	ultima := d.Estacoes[len(d.Estacoes)-1]
	if ultima.Video != "aaaaaaaaaaa" || ultima.Nome == "" || ultima.URL != "https://www.youtube.com/watch?v=aaaaaaaaaaa" {
		t.Fatalf("estacao: %+v", ultima)
	}
}

func TestToastEscapaTexto(t *testing.T) {
	x := montarToast(AvisoSistema{Titulo: `deploy <prod> & "rollback"`, Texto: "agora", Abrir: "http://127.0.0.1:7010/#foco", Fixo: true})
	if strings.Contains(x, "<prod>") || !strings.Contains(x, "&lt;prod&gt; &amp;") {
		t.Fatalf("titulo sem escape: %s", x)
	}
	if !strings.Contains(x, `scenario="reminder"`) || !strings.Contains(x, `activationType="system"`) {
		t.Fatalf("lembrete fixo precisa de cenario e botao: %s", x)
	}
}

func TestRotasFoco(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)
	f, avisos, wg := focoTeste(t, time.Now())
	g.foco = f
	h := somenteLocal(rotas(g, t.TempDir(), func() {}))

	resp := chamar(t, h, http.MethodPost, "/api/foco/tarefas", `{"titulo":"escrever testes","data":"2026-11-02","hora":"10:00","lembrete":15}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("criar: %d %s", resp.Code, resp.Body)
	}
	var criada struct {
		ID   string     `json:"id"`
		Foco *DadosFoco `json:"foco"`
	}
	json.NewDecoder(resp.Body).Decode(&criada)
	if criada.ID == "" || len(criada.Foco.Tarefas) != 1 {
		t.Fatalf("resposta: %+v", criada)
	}

	if resp := chamar(t, h, http.MethodPost, "/api/foco/tarefas/"+criada.ID+"/status", `{"status":"feito"}`); resp.Code != http.StatusOK {
		t.Fatalf("status: %d %s", resp.Code, resp.Body)
	}
	if resp := chamar(t, h, http.MethodPost, "/api/foco/tarefas/limpar-feitas", ``); resp.Code != http.StatusOK || len(f.Dados().Tarefas) != 0 {
		t.Fatalf("limpar feitas: %d %s", resp.Code, resp.Body)
	}

	if resp := chamar(t, h, http.MethodPost, "/api/foco/estacoes", `{"url":"https://vimeo.com/1"}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("link de fora do YouTube deveria dar 400, veio %d", resp.Code)
	}

	wg.Add(1)
	if resp := chamar(t, h, http.MethodPost, "/api/foco/notificar", `{"titulo":"pomodoro","texto":"pausa"}`); resp.Code != http.StatusOK {
		t.Fatalf("notificar: %d %s", resp.Code, resp.Body)
	}
	if len(*avisos) != 1 || (*avisos)[0].Titulo != "pomodoro" {
		t.Fatalf("notificacao avulsa: %+v", *avisos)
	}
}
