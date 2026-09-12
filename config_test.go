package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// configTeste monta uma cadeia parecida com a real:
//
//	db, rabbit (nao esperam ninguem) -> api (espera os dois) -> web (espera a api)
func configTeste() *Config {
	return &Config{
		PortaUI:    7010,
		RedeDocker: "PRODUCTION",
		Servicos: []*Servico{
			{
				ID: "db", Nome: "Postgres", Tipo: TipoDocker,
				Compose: &Compose{Projeto: "p", Arquivo: "a.yml", Servico: "db"},
				Pronto:  Checagem{Tipo: "porta", Porta: 5432},
				Timeout: 10,
				Depende: []string{},
				Porta:   5432,
			},
			{
				ID: "rabbit", Nome: "RabbitMQ", Tipo: TipoDocker,
				Compose: &Compose{Projeto: "p", Arquivo: "r.yml", Servico: "rabbitmq"},
				Pronto:  Checagem{Tipo: "porta", Porta: 5672},
				Timeout: 10,
				Depende: []string{},
			},
			{
				ID: "api", Nome: "backend", Tipo: TipoApp,
				Dir: "backend", Cmd: "go run .",
				Pronto:  Checagem{Tipo: "porta", Porta: 7003},
				Timeout: 10,
				Modo:    ModoGerenciado,
				Depende: []string{"db", "rabbit"},
			},
			{
				ID: "web", Nome: "frontend", Tipo: TipoApp,
				Dir: "front", Cmd: "npm run serve",
				Pronto:  Checagem{Tipo: "porta", Porta: 8080},
				Timeout: 10,
				Modo:    ModoGerenciado,
				Depende: []string{"api"},
			},
		},
	}
}

func TestValidarAceitaConfigBoa(t *testing.T) {
	if err := configTeste().validar(); err != nil {
		t.Fatalf("config de teste deveria ser valida: %v", err)
	}
}

func TestValidarRecusaProblemas(t *testing.T) {
	casos := []struct {
		nome   string
		ajuste func(*Config)
		trecho string
	}{
		{"dependencia inexistente", func(c *Config) { c.porID("api").Depende = []string{"fantasma"} }, "nao existe"},
		{"id repetido", func(c *Config) { c.Servicos[1].ID = "db" }, "id repetido"},
		{"tipo invalido", func(c *Config) { c.porID("api").Tipo = "magia" }, "tipo invalido"},
		{"app sem cmd", func(c *Config) { c.porID("api").Cmd = "" }, "precisa de dir e cmd"},
		{"docker sem compose", func(c *Config) { c.porID("db").Compose = nil }, "compose.projeto"},
		{"checagem sem porta", func(c *Config) { c.porID("db").Pronto = Checagem{Tipo: "porta"} }, "sem porta"},
		{"checagem desconhecida", func(c *Config) { c.porID("db").Pronto = Checagem{Tipo: "vibes"} }, "pronto.tipo invalido"},
		{"modo invalido", func(c *Config) { c.porID("api").Modo = "turbo" }, "modo invalido"},
		{"depende de si mesmo", func(c *Config) { c.porID("api").Depende = []string{"api"} }, "depende de si mesmo"},
		{"ciclo", func(c *Config) { c.porID("db").Depende = []string{"web"} }, "dependencia circular"},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			cfg := configTeste()
			caso.ajuste(cfg)
			err := cfg.validar()
			if err == nil {
				t.Fatalf("esperava erro contendo %q, veio nil", caso.trecho)
			}
			if !strings.Contains(err.Error(), caso.trecho) {
				t.Fatalf("erro %q nao contem %q", err, caso.trecho)
			}
		})
	}
}

func TestAcharCicloMostraOCaminho(t *testing.T) {
	cfg := configTeste()
	cfg.porID("db").Depende = []string{"web"} // db -> web -> api -> db

	ciclo := acharCiclo(cfg)
	if ciclo == nil {
		t.Fatal("deveria achar o ciclo")
	}
	if ciclo[0] != ciclo[len(ciclo)-1] {
		t.Fatalf("o caminho do ciclo deveria fechar no mesmo id: %v", ciclo)
	}
	for _, id := range []string{"db", "web", "api"} {
		if !contem(ciclo, id) {
			t.Fatalf("ciclo %v deveria citar %s", ciclo, id)
		}
	}
}

func TestAcharCicloNaoAcusaFalsoPositivo(t *testing.T) {
	// Diamante: dois caminhos ate o mesmo no nao sao ciclo.
	cfg := configTeste()
	cfg.porID("web").Depende = []string{"api", "db"}
	if ciclo := acharCiclo(cfg); ciclo != nil {
		t.Fatalf("nao ha ciclo, mas achou %v", ciclo)
	}
}

func TestExpandirDependenciasFechaOConjunto(t *testing.T) {
	cfg := configTeste()

	got := expandirDependencias(cfg, []string{"web"})
	querido := []string{"db", "rabbit", "api", "web"} // ordem do config
	if !reflect.DeepEqual(got, querido) {
		t.Fatalf("expandir(web) = %v, queria %v", got, querido)
	}

	if got := expandirDependencias(cfg, []string{"db"}); !reflect.DeepEqual(got, []string{"db"}) {
		t.Fatalf("expandir(db) = %v, queria [db]", got)
	}
	if got := expandirDependencias(cfg, []string{"nao-existe"}); len(got) != 0 {
		t.Fatalf("id desconhecido deveria sumir, veio %v", got)
	}
	// Repetido nao duplica.
	if got := expandirDependencias(cfg, []string{"api", "api", "db"}); !reflect.DeepEqual(got, []string{"db", "rabbit", "api"}) {
		t.Fatalf("expandir repetido = %v", got)
	}
}

func TestOndasAgrupaPorNivel(t *testing.T) {
	cfg := configTeste()
	ids := expandirDependencias(cfg, []string{"web"})

	got := ondas(cfg, ids)
	querido := [][]string{{"db", "rabbit"}, {"api"}, {"web"}}
	if !reflect.DeepEqual(got, querido) {
		t.Fatalf("ondas = %v, queria %v", got, querido)
	}
}

func TestOndasIgnoraDependenciaForaDoConjunto(t *testing.T) {
	cfg := configTeste()
	// So a api: o db ficou de fora (ja esta no ar por fora), entao ela e a primeira onda.
	got := ondas(cfg, []string{"api"})
	if !reflect.DeepEqual(got, [][]string{{"api"}}) {
		t.Fatalf("ondas = %v, queria [[api]]", got)
	}
}

func TestAplicarEdicaoMudaSoOQueATelaPodeMudar(t *testing.T) {
	cfg := configTeste()
	cmdOriginal := cfg.porID("api").Cmd

	err := aplicarEdicao(cfg, []EdicaoServico{
		{ID: "api", Selecionado: true, Modo: ModoTerminal, Depende: []string{"db", "db", ""}},
		{ID: "web", Selecionado: false},
	})
	if err != nil {
		t.Fatalf("edicao valida falhou: %v", err)
	}

	api := cfg.porID("api")
	if !api.Selecionado || api.Modo != ModoTerminal {
		t.Fatalf("selecao/modo nao aplicados: %+v", api)
	}
	if !reflect.DeepEqual(api.Depende, []string{"db"}) {
		t.Fatalf("depende = %v, queria [db] (sem repetido nem vazio)", api.Depende)
	}
	if api.Cmd != cmdOriginal {
		t.Fatalf("a tela nao pode trocar o comando executado (%q -> %q)", cmdOriginal, api.Cmd)
	}
	if cfg.porID("web").Selecionado {
		t.Fatal("web deveria ter sido desmarcado")
	}
}

func TestAplicarEdicaoRecusaCicloSemSujarOConfig(t *testing.T) {
	cfg := configTeste()
	antes := clonarConfig(cfg)

	err := aplicarEdicao(cfg, []EdicaoServico{{ID: "db", Selecionado: true, Depende: []string{"web"}}})
	if err == nil {
		t.Fatal("ciclo deveria ser recusado")
	}
	if !reflect.DeepEqual(cfg.porID("db").Depende, antes.porID("db").Depende) {
		t.Fatalf("config foi alterado mesmo com erro: %v", cfg.porID("db").Depende)
	}
	if cfg.porID("db").Selecionado {
		t.Fatal("nenhuma parte da edicao invalida deveria valer")
	}
}

func TestAplicarEdicaoRecusaServicoDesconhecido(t *testing.T) {
	if err := aplicarEdicao(configTeste(), []EdicaoServico{{ID: "fantasma"}}); err == nil {
		t.Fatal("esperava erro para id desconhecido")
	}
}

func TestClonarConfigNaoCompartilhaMemoria(t *testing.T) {
	cfg := configTeste()
	copia := clonarConfig(cfg)

	copia.porID("api").Depende[0] = "outro"
	copia.porID("db").Compose.Servico = "trocado"

	if cfg.porID("api").Depende[0] != "db" {
		t.Fatal("mexer na copia alterou a lista de dependencias do original")
	}
	if cfg.porID("db").Compose.Servico != "db" {
		t.Fatal("mexer na copia alterou o compose do original")
	}
}

func TestSalvarECarregarMantemOConteudo(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "config.json")
	cfg := configTeste()
	cfg.porID("web").Selecionado = true

	if err := salvarConfig(caminho, cfg); err != nil {
		t.Fatalf("salvar: %v", err)
	}
	lido, err := carregarConfig(caminho)
	if err != nil {
		t.Fatalf("carregar: %v", err)
	}
	if !lido.porID("web").Selecionado {
		t.Fatal("selecao nao sobreviveu ao salvar/carregar")
	}
	if !reflect.DeepEqual(lido.porID("api").Depende, []string{"db", "rabbit"}) {
		t.Fatalf("dependencias nao sobreviveram: %v", lido.porID("api").Depende)
	}
}

func TestSalvarRecusaConfigInvalido(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "config.json")
	cfg := configTeste()
	cfg.porID("api").Depende = []string{"fantasma"}

	if err := salvarConfig(caminho, cfg); err == nil {
		t.Fatal("config invalido nao deveria ser gravado")
	}
}

func TestCarregarConfigDeVerdade(t *testing.T) {
	// O config.json que vai para producao tem que passar pela mesma validacao.
	cfg, err := carregarConfig("config.json")
	if err != nil {
		t.Fatalf("config.json do projeto esta invalido: %v", err)
	}
	if cfg.porID("backend") == nil || cfg.porID("db") == nil {
		t.Fatal("config.json deveria ter os servicos backend e db")
	}
	if ondas(cfg, expandirDependencias(cfg, []string{"frontend"}))[0][0] != "db" {
		t.Fatal("o db deveria estar na primeira onda quando se pede o frontend")
	}
}

func TestCaminhoAbsoluto(t *testing.T) {
	raiz := filepath.FromSlash("C:/projetos/appdaturma")

	got := caminhoAbsoluto(raiz, "docker appdaturma/docker-compose.yml")
	querido := filepath.Join(raiz, "docker appdaturma", "docker-compose.yml")
	if got != querido {
		t.Fatalf("caminhoAbsoluto = %q, queria %q", got, querido)
	}

	// ".." sobe para a pasta acima da raiz (e onde mora o mailhog.yml).
	got = caminhoAbsoluto(raiz, "../mailhog.yml")
	querido = filepath.Join(filepath.Dir(raiz), "mailhog.yml")
	if got != querido {
		t.Fatalf("caminhoAbsoluto(..) = %q, queria %q", got, querido)
	}

	abs := filepath.FromSlash("D:/outro/lugar.yml")
	if got := caminhoAbsoluto(raiz, abs); got != abs {
		t.Fatalf("caminho absoluto deveria passar direto: %q", got)
	}
}

func contem(lista []string, alvo string) bool {
	for _, v := range lista {
		if v == alvo {
			return true
		}
	}
	return false
}
