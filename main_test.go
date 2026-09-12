package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func servidorTeste(t *testing.T, comp map[string]comportamento) (http.Handler, *Gerente) {
	t.Helper()
	g, _, _ := gerenteTeste(t, comp)
	return somenteLocal(rotas(g, t.TempDir())), g
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

func TestInterfaceEstaEmbutidaNoBinario(t *testing.T) {
	h, _ := servidorTeste(t, nil)
	for _, caminho := range []string{"/", "/style.css", "/app.js"} {
		resp := chamar(t, h, http.MethodGet, caminho, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("%s deveria ser servido pelo binario, veio %d", caminho, resp.Code)
		}
		if resp.Body.Len() == 0 {
			t.Fatalf("%s veio vazio", caminho)
		}
	}
}
