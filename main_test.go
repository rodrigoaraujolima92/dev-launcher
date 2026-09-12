package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func servidorTeste(t *testing.T, comp map[string]comportamento) (http.Handler, *Gerente) {
	t.Helper()
	h, g, _ := servidorTesteComEncerrar(t, comp)
	return h, g
}

// servidorTesteComEncerrar devolve tambem um ponteiro que diz se a rota de encerramento
// chamou o desligamento.
func servidorTesteComEncerrar(t *testing.T, comp map[string]comportamento) (http.Handler, *Gerente, *atomic.Bool) {
	t.Helper()
	g, _, _ := gerenteTeste(t, comp)
	var chamou atomic.Bool
	h := somenteLocal(rotas(g, t.TempDir(), func() { chamou.Store(true) }))
	return h, g, &chamou
}

func chamar(t *testing.T, h http.Handler, metodo, caminho, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	var leitor *strings.Reader = strings.NewReader(corpo)
	req := httptest.NewRequest(metodo, caminho, leitor)
	req.Host = "localhost:7010"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	return resp
}

func TestSomenteLocalBloqueiaHostDeFora(t *testing.T) {
	h, _ := servidorTeste(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/estado", nil)
	req.Host = "launcher.exemplo.com" // dominio apontando para 127.0.0.1
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("host externo deveria receber 403, veio %d", resp.Code)
	}
}

func TestEstadoDevolveConfigEEstados(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodGet, "/api/estado", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d", resp.Code)
	}

	var corpo struct {
		Config  *Config   `json:"config"`
		Estados []*Estado `json:"estados"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("json invalido: %v", err)
	}
	if corpo.Config == nil || len(corpo.Config.Servicos) != 4 {
		t.Fatalf("config veio errado: %+v", corpo.Config)
	}
	if len(corpo.Estados) != 4 {
		t.Fatalf("esperava 4 estados, veio %d", len(corpo.Estados))
	}
}

func TestPlanoRespondeAsOndas(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodPost, "/api/plano", `{"ids":["web"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}

	var plano Plano
	if err := json.Unmarshal(resp.Body.Bytes(), &plano); err != nil {
		t.Fatal(err)
	}
	if len(plano.Ondas) != 3 || len(plano.IDs) != 4 {
		t.Fatalf("plano = %+v", plano)
	}
}

func TestConfigRecusaCicloCom400(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodPost, "/api/config",
		`{"servicos":[{"id":"db","selecionado":true,"depende":["web"]}]}`)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("ciclo deveria dar 400, veio %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "circular") {
		t.Fatalf("a resposta deveria explicar o ciclo: %s", resp.Body)
	}
}

func TestConfigAplicaSelecao(t *testing.T) {
	h, g := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodPost, "/api/config",
		`{"servicos":[{"id":"web","selecionado":true,"modo":"terminal"}]}`)

	if resp.Code != http.StatusOK {
		t.Fatalf("codigo %d: %s", resp.Code, resp.Body)
	}
	web := g.Config().porID("web")
	if !web.Selecionado || web.Modo != ModoTerminal {
		t.Fatalf("edicao nao aplicada: %+v", web)
	}
}

func TestSubirEmAndamentoResponde409(t *testing.T) {
	h, _ := servidorTeste(t, map[string]comportamento{"db": {demora: 400 * time.Millisecond}})

	if resp := chamar(t, h, http.MethodPost, "/api/subir", `{"ids":["db"]}`); resp.Code != http.StatusOK {
		t.Fatalf("primeira subida: %d %s", resp.Code, resp.Body)
	}
	resp := chamar(t, h, http.MethodPost, "/api/subir", `{"ids":["db"]}`)
	if resp.Code != http.StatusConflict {
		t.Fatalf("segunda subida deveria dar 409, veio %d", resp.Code)
	}
}

func TestRotasSemIDsDa400(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	for _, caminho := range []string{"/api/subir", "/api/parar", "/api/plano"} {
		if resp := chamar(t, h, http.MethodPost, caminho, `{"ids":[]}`); resp.Code != http.StatusBadRequest {
			t.Fatalf("%s sem ids deveria dar 400, veio %d", caminho, resp.Code)
		}
	}
}

func TestLogsDevolveAsLinhasDoServico(t *testing.T) {
	h, g := servidorTeste(t, nil)
	g.RegistrarLog("db", "primeira linha")

	resp := chamar(t, h, http.MethodGet, "/api/logs?id=db", "")
	var corpo struct {
		Linhas []string `json:"linhas"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &corpo); err != nil {
		t.Fatal(err)
	}
	if len(corpo.Linhas) != 1 || corpo.Linhas[0] != "primeira linha" {
		t.Fatalf("linhas = %v", corpo.Linhas)
	}
}

func TestCadastrarEApagarProjetoPelaAPI(t *testing.T) {
	h, g := servidorTeste(t, nil)
	pasta := strings.ReplaceAll(t.TempDir(), `\`, `\\`) // o caminho vai dentro de um JSON

	corpo := `{"nome":"Cabine Foto","grupo":"web","tipo":"app","dir":"` + pasta +
		`","cmd":"npm run dev","porta":5173,"pronto":{"tipo":"porta","porta":5173}}`
	resp := chamar(t, h, http.MethodPost, "/api/servicos", corpo)
	if resp.Code != http.StatusOK {
		t.Fatalf("cadastro falhou: %d %s", resp.Code, resp.Body)
	}
	var criado struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &criado); err != nil {
		t.Fatal(err)
	}
	if criado.ID != "cabine-foto" {
		t.Fatalf("id = %q", criado.ID)
	}
	if g.Config().porID(criado.ID) == nil {
		t.Fatal("o projeto nao entrou no config")
	}
	// Servico novo precisa nascer com estado, senao a tela nao consegue desenhar o cartao.
	if statusDe(g, criado.ID) == nil {
		t.Fatal("o projeto novo ficou sem estado")
	}

	resp = chamar(t, h, http.MethodDelete, "/api/servicos/"+criado.ID, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("remocao falhou: %d %s", resp.Code, resp.Body)
	}
	if g.Config().porID(criado.ID) != nil {
		t.Fatal("o projeto continua no config")
	}
	if statusDe(g, criado.ID) != nil {
		t.Fatal("sobrou estado de um projeto apagado")
	}
}

func TestCadastroInvalidoResponde400(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodPost, "/api/servicos",
		`{"nome":"sem pasta","grupo":"api","tipo":"app","cmd":"go run .","porta":1234}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("esperava 400, veio %d: %s", resp.Code, resp.Body)
	}
}

func TestGruposPelaAPI(t *testing.T) {
	h, g := servidorTeste(t, nil)

	resp := chamar(t, h, http.MethodPost, "/api/grupos", `{"nome":"Photonow"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("criar grupo: %d %s", resp.Code, resp.Body)
	}
	if g.Config().grupoPorID("photonow") == nil {
		t.Fatal("grupo nao foi criado")
	}

	resp = chamar(t, h, http.MethodPost, "/api/grupos/ordem", `{"ids":["photonow","web"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("ordenar: %d %s", resp.Code, resp.Body)
	}
	if g.Config().Grupos[0].ID != "photonow" {
		t.Fatalf("ordem = %s", g.Config().Grupos[0].ID)
	}

	// Grupo com projeto dentro nao sai.
	if resp := chamar(t, h, http.MethodDelete, "/api/grupos/infra", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("apagar grupo cheio deveria dar 400, veio %d", resp.Code)
	}
	if resp := chamar(t, h, http.MethodDelete, "/api/grupos/photonow", ""); resp.Code != http.StatusOK {
		t.Fatalf("apagar grupo vazio: %d %s", resp.Code, resp.Body)
	}
}

func TestPerfisPelaAPI(t *testing.T) {
	h, g := servidorTeste(t, nil)

	resp := chamar(t, h, http.MethodPost, "/api/perfis", `{"nome":"so APIs","servicos":["db","rabbit","api"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("criar perfil: %d %s", resp.Code, resp.Body)
	}
	var criado struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &criado); err != nil {
		t.Fatal(err)
	}

	if resp := chamar(t, h, http.MethodPost, "/api/perfis/"+criado.ID+"/aplicar", ""); resp.Code != http.StatusOK {
		t.Fatalf("aplicar perfil: %d %s", resp.Code, resp.Body)
	}
	cfg := g.Config()
	if !cfg.porID("db").Selecionado || cfg.porID("web").Selecionado {
		t.Fatal("aplicar o perfil deveria marcar db e desmarcar web")
	}

	if resp := chamar(t, h, http.MethodDelete, "/api/perfis/"+criado.ID, ""); resp.Code != http.StatusOK {
		t.Fatalf("apagar perfil: %d", resp.Code)
	}
	if len(g.Config().Perfis) != 0 {
		t.Fatal("o perfil continua la")
	}
}

func TestNavegarPastasRespeitaACerca(t *testing.T) {
	h, g := servidorTeste(t, nil)
	base := t.TempDir()
	raiz := filepath.Join(base, "projetos")
	fora := filepath.Join(base, "fora")
	for _, d := range []string{filepath.Join(raiz, "um"), fora} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.mutar(func(cfg *Config) error { cfg.RaizNavegacao = raiz; return nil }); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, h, http.MethodGet, "/api/pastas", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("listar a raiz: %d %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), `"um"`) {
		t.Fatalf("deveria listar a subpasta: %s", resp.Body)
	}

	resp = chamar(t, h, http.MethodGet, "/api/pastas?caminho="+url.QueryEscape(fora), "")
	if resp.Code != http.StatusForbidden {
		t.Fatalf("pasta fora da raiz deveria dar 403, veio %d", resp.Code)
	}

	resp = chamar(t, h, http.MethodGet, "/api/pastas?livre=1&caminho="+url.QueryEscape(fora), "")
	if resp.Code != http.StatusOK {
		t.Fatalf("com livre=1 deveria deixar navegar, veio %d: %s", resp.Code, resp.Body)
	}
}

func TestInspecionarPelaAPI(t *testing.T) {
	h, g := servidorTeste(t, nil)
	raiz := t.TempDir()
	projeto := filepath.Join(raiz, "front")
	if err := os.MkdirAll(projeto, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projeto, "package.json"),
		[]byte(`{"scripts":{"dev":"vite --port 5173"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.mutar(func(cfg *Config) error { cfg.RaizNavegacao = raiz; return nil }); err != nil {
		t.Fatal(err)
	}

	resp := chamar(t, h, http.MethodGet, "/api/inspecionar?caminho="+url.QueryEscape(projeto), "")
	if resp.Code != http.StatusOK {
		t.Fatalf("inspecionar: %d %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), "npm run dev") {
		t.Fatalf("deveria sugerir o script: %s", resp.Body)
	}
}

func TestMoverProjetoPelaAPI(t *testing.T) {
	h, g := servidorTeste(t, nil)

	resp := chamar(t, h, http.MethodPost, "/api/servicos/web/mover", `{"grupo":"infra","antes":"db"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("mover: %d %s", resp.Code, resp.Body)
	}
	cfg := g.Config()
	if cfg.porID("web").Grupo != "infra" {
		t.Fatalf("grupo = %q", cfg.porID("web").Grupo)
	}
	if cfg.Servicos[0].ID != "web" {
		t.Fatalf("o projeto deveria ter ido para a frente do db: %s", cfg.Servicos[0].ID)
	}

	if resp := chamar(t, h, http.MethodPost, "/api/servicos/web/mover", `{"grupo":"fantasma"}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("grupo inexistente deveria dar 400, veio %d", resp.Code)
	}
}

func TestReiniciarParaEDepoisSobe(t *testing.T) {
	h, g := servidorTeste(t, map[string]comportamento{"db": {jaPronto: true}})
	g.marcar("db", StatusPronto, "")

	resp := chamar(t, h, http.MethodPost, "/api/reiniciar", `{"ids":["db"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("reiniciar: %d %s", resp.Code, resp.Body)
	}

	fake := g.exec.(*execFake)
	// A subida roda em segundo plano: espera ela registrar o inicio antes de conferir.
	// Parar limpa o "jaPronto" do fake, entao a subida seguinte precisa iniciar de verdade.
	limite := time.Now().Add(3 * time.Second)
	for time.Now().Before(limite) {
		if ordem, _, _ := fake.instantaneo(); contem(ordem, "db") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ordem, _, parados := fake.instantaneo()
	if !contem(parados, "db") {
		t.Fatal("o projeto nao foi parado antes de subir")
	}
	if !contem(ordem, "db") {
		t.Fatalf("o projeto nao voltou a subir: %v", ordem)
	}
}

func TestAbrirPastaRecusaProjetoDesconhecido(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	resp := chamar(t, h, http.MethodPost, "/api/abrir", `{"id":"fantasma","alvo":"pasta"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("esperava 400, veio %d", resp.Code)
	}
}

func TestCaminhoDoProjeto(t *testing.T) {
	g, _, _ := gerenteTeste(t, nil)

	caminhoApp, err := g.Caminho("api")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(caminhoApp) != "backend" {
		t.Fatalf("caminho da app = %q", caminhoApp)
	}

	// Para servico docker vale a pasta do arquivo compose, nao o arquivo.
	caminhoDocker, err := g.Caminho("db")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(caminhoDocker) == ".yml" {
		t.Fatalf("deveria devolver a pasta, veio o arquivo: %q", caminhoDocker)
	}
	if _, err := g.Caminho("fantasma"); err == nil {
		t.Fatal("projeto inexistente deveria dar erro")
	}
}

func TestEncerrarPelaAPI(t *testing.T) {
	h, _, chamou := servidorTesteComEncerrar(t, nil)

	resp := chamar(t, h, http.MethodPost, "/api/encerrar", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("encerrar: %d %s", resp.Code, resp.Body)
	}
	// A rota responde antes de desligar, para a tela conseguir avisar; o desligamento vem
	// logo depois, em outra goroutine.
	limite := time.Now().Add(3 * time.Second)
	for time.Now().Before(limite) && !chamou.Load() {
		time.Sleep(20 * time.Millisecond)
	}
	if !chamou.Load() {
		t.Fatal("a rota respondeu mas nao desligou o launcher")
	}
}

func TestEncerrarSoAceitaLocalhost(t *testing.T) {
	h, _, chamou := servidorTesteComEncerrar(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/encerrar", nil)
	req.Host = "launcher.exemplo.com"
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("esperava 403, veio %d", resp.Code)
	}
	time.Sleep(300 * time.Millisecond)
	if chamou.Load() {
		t.Fatal("uma pagina de fora conseguiu derrubar o launcher")
	}
}

func TestInterfaceEstaEmbutidaNoBinario(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	for _, caminho := range []string{"/", "/style.css", "/app.js", "/vendor/vue.global.prod.js"} {
		resp := chamar(t, h, http.MethodGet, caminho, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("%s deveria ser servido pelo binario, veio %d", caminho, resp.Code)
		}
		if resp.Body.Len() == 0 {
			t.Fatalf("%s veio vazio", caminho)
		}
	}
}
