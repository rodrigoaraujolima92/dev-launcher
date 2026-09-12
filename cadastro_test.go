package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// pastasTeste monta uma arvore parecida com C:\Users\Pichau\projetos:
//
//	<tmp>/projetos/photonow/cabine-foto-front   (dentro da cerca)
//	<tmp>/fora/coisa                            (fora da cerca)
func pastasTeste(t *testing.T) (raizNavegacao, dentro, fora string) {
	t.Helper()
	base := t.TempDir()
	raizNavegacao = filepath.Join(base, "projetos")
	dentro = filepath.Join(raizNavegacao, "photonow", "cabine-foto-front")
	fora = filepath.Join(base, "fora", "coisa")
	for _, d := range []string{dentro, fora} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return raizNavegacao, dentro, fora
}

func TestSalvarServicoCriaComIDGeradoEDefaults(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	id, err := salvarServico(cfg, EntradaServico{
		Nome:  "Cabine Foto Front",
		Grupo: "web",
		Tipo:  TipoApp,
		Dir:   dentro,
		Cmd:   "npm run dev",
		Porta: 5173,
	}, t.TempDir(), raizNav)
	if err != nil {
		t.Fatalf("cadastro valido falhou: %v", err)
	}
	if id != "cabine-foto-front" {
		t.Fatalf("id gerado = %q, queria cabine-foto-front", id)
	}

	novo := cfg.porID(id)
	if novo.Timeout != 120 {
		t.Fatalf("timeout padrao = %d, queria 120", novo.Timeout)
	}
	if novo.Modo != ModoGerenciado {
		t.Fatalf("modo padrao = %q", novo.Modo)
	}
	// Checagem em branco vira "porta", deduzida da porta informada.
	if novo.Pronto.Tipo != "porta" || novo.Pronto.Porta != 5173 {
		t.Fatalf("checagem deduzida = %+v", novo.Pronto)
	}
}

func TestSalvarServicoDesempataIDRepetido(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	entrada := EntradaServico{Nome: "backend", Grupo: "api", Tipo: TipoApp, Dir: dentro, Cmd: "go run .", Porta: 9001}
	primeiro, err := salvarServico(cfg, entrada, t.TempDir(), raizNav)
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := salvarServico(cfg, entrada, t.TempDir(), raizNav)
	if err != nil {
		t.Fatal(err)
	}
	if primeiro == segundo {
		t.Fatalf("o segundo cadastro deveria ganhar id proprio, os dois vieram %q", primeiro)
	}
	if segundo != "backend-2" {
		t.Fatalf("id do segundo = %q, queria backend-2", segundo)
	}
}

func TestSalvarServicoBarraPastaForaDaCerca(t *testing.T) {
	raizNav, _, fora := pastasTeste(t)
	cfg := configTeste()

	entrada := EntradaServico{Nome: "coisa", Grupo: "api", Tipo: TipoApp, Dir: fora, Cmd: "go run .", Porta: 9100}
	_, err := salvarServico(cfg, entrada, t.TempDir(), raizNav)
	if err == nil {
		t.Fatal("pasta fora da raiz deveria ser recusada sem o caminho livre")
	}
	if !strings.Contains(err.Error(), "caminho livre") {
		t.Fatalf("a mensagem deveria explicar a saida: %v", err)
	}

	// Com o checkbox marcado, passa.
	entrada.CaminhoLivre = true
	if _, err := salvarServico(cfg, entrada, t.TempDir(), raizNav); err != nil {
		t.Fatalf("com caminho livre deveria aceitar: %v", err)
	}
}

func TestSalvarServicoRecusaPastaInexistente(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	_, err := salvarServico(cfg, EntradaServico{
		Nome: "fantasma", Grupo: "api", Tipo: TipoApp,
		Dir: filepath.Join(dentro, "nao-existe"), Cmd: "go run .", Porta: 9200,
	}, t.TempDir(), raizNav)
	if err == nil || !strings.Contains(err.Error(), "nao encontrado") {
		t.Fatalf("esperava erro de pasta inexistente, veio %v", err)
	}
}

func TestSalvarServicoEdicaoMantemOID(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	id, err := salvarServico(cfg, EntradaServico{
		ID: "web", Nome: "frontend", Grupo: "web", Tipo: TipoApp,
		Dir: dentro, Cmd: "npm run serve -- --port 8090", Porta: 8090,
		Pronto: Checagem{Tipo: "porta", Porta: 8090}, Depende: []string{"api"},
	}, t.TempDir(), raizNav)
	if err != nil {
		t.Fatal(err)
	}
	if id != "web" {
		t.Fatalf("editar nao pode trocar o id (%q)", id)
	}
	if cfg.porID("web").Porta != 8090 {
		t.Fatal("a porta nova nao foi gravada")
	}
	if len(cfg.Servicos) != 4 {
		t.Fatalf("editar criou servico novo: %d", len(cfg.Servicos))
	}
}

func TestSalvarServicoDockerPrecisaDoComposeNoDisco(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()
	compose := filepath.Join(dentro, "docker-compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  web:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entrada := EntradaServico{
		Nome: "nginx local", Grupo: "infra", Tipo: TipoDocker,
		Compose: &Compose{Projeto: "teste", Arquivo: compose, Servico: "web"},
		Porta:   8099,
	}
	if _, err := salvarServico(cfg, entrada, t.TempDir(), raizNav); err != nil {
		t.Fatalf("compose existente deveria passar: %v", err)
	}

	entrada.Compose.Arquivo = filepath.Join(dentro, "nao-existe.yml")
	if _, err := salvarServico(cfg, entrada, t.TempDir(), raizNav); err == nil {
		t.Fatal("compose inexistente deveria ser recusado")
	}
}

func TestRemoverServicoLimpaDependenciasEPerfis(t *testing.T) {
	cfg := configTeste()
	if _, err := salvarPerfil(cfg, "", "stack completa", []string{"db", "rabbit", "api", "web"}); err != nil {
		t.Fatal(err)
	}

	if err := removerServico(cfg, "api"); err != nil {
		t.Fatalf("remover: %v", err)
	}
	if cfg.porID("api") != nil {
		t.Fatal("o servico continua no config")
	}
	if contem(cfg.porID("web").Depende, "api") {
		t.Fatalf("o frontend ainda espera por um servico apagado: %v", cfg.porID("web").Depende)
	}
	if contem(cfg.Perfis[0].Servicos, "api") {
		t.Fatalf("o perfil ainda cita o servico apagado: %v", cfg.Perfis[0].Servicos)
	}
	if err := cfg.validar(); err != nil {
		t.Fatalf("config ficou invalido depois da remocao: %v", err)
	}
}

func TestRemoverServicoDesconhecido(t *testing.T) {
	if err := removerServico(configTeste(), "fantasma"); err == nil {
		t.Fatal("esperava erro")
	}
}

// ------------------------------------------------------------------
// mover (arrastar e soltar)
// ------------------------------------------------------------------

func ordemDe(cfg *Config) []string {
	out := []string{}
	for _, s := range cfg.Servicos {
		out = append(out, s.ID+"@"+s.Grupo)
	}
	return out
}

func TestMoverServicoTrocaDeGrupoENaoQuebraDependencia(t *testing.T) {
	cfg := configTeste() // db@infra rabbit@infra api@api web@web

	if err := moverServico(cfg, "web", "infra", ""); err != nil {
		t.Fatalf("mover: %v", err)
	}
	if cfg.porID("web").Grupo != "infra" {
		t.Fatalf("grupo = %q", cfg.porID("web").Grupo)
	}
	// Sem referencia, entra logo depois do ultimo do grupo de destino.
	if !reflect.DeepEqual(ordemDe(cfg), []string{"db@infra", "rabbit@infra", "web@infra", "api@api"}) {
		t.Fatalf("ordem = %v", ordemDe(cfg))
	}
	// Mudar de grupo nao mexe em quem espera quem.
	if !reflect.DeepEqual(cfg.porID("web").Depende, []string{"api"}) {
		t.Fatalf("dependencias mudaram: %v", cfg.porID("web").Depende)
	}
	if err := cfg.validar(); err != nil {
		t.Fatalf("config invalido apos mover: %v", err)
	}
}

func TestMoverServicoPosicionaAntesDoAlvo(t *testing.T) {
	cfg := configTeste()

	// Soltou "api" em cima do "db": entra na frente dele, no grupo do db.
	if err := moverServico(cfg, "api", "infra", "db"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordemDe(cfg), []string{"api@infra", "db@infra", "rabbit@infra", "web@web"}) {
		t.Fatalf("ordem = %v", ordemDe(cfg))
	}
}

func TestMoverServicoReordenaDentroDoMesmoGrupo(t *testing.T) {
	cfg := configTeste()

	if err := moverServico(cfg, "rabbit", "infra", "db"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordemDe(cfg), []string{"rabbit@infra", "db@infra", "api@api", "web@web"}) {
		t.Fatalf("ordem = %v", ordemDe(cfg))
	}
}

func TestMoverServicoEmCimaDeSiMesmoNaoFazNada(t *testing.T) {
	cfg := configTeste()
	antes := ordemDe(cfg)

	if err := moverServico(cfg, "api", "api", "api"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordemDe(cfg), antes) {
		t.Fatalf("ordem mudou: %v", ordemDe(cfg))
	}
}

func TestMoverServicoRecusaAlvoDesconhecido(t *testing.T) {
	cfg := configTeste()
	if err := moverServico(cfg, "api", "fantasma", ""); err == nil {
		t.Fatal("grupo inexistente deveria dar erro")
	}
	if err := moverServico(cfg, "fantasma", "api", ""); err == nil {
		t.Fatal("servico inexistente deveria dar erro")
	}
	if err := moverServico(cfg, "api", "infra", "fantasma"); err == nil {
		t.Fatal("referencia inexistente deveria dar erro")
	}
}

// ------------------------------------------------------------------
// grupos
// ------------------------------------------------------------------

func TestSalvarGrupoCriaERenomeia(t *testing.T) {
	cfg := configTeste()

	id, err := salvarGrupo(cfg, "", "Photonow")
	if err != nil {
		t.Fatal(err)
	}
	if id != "photonow" {
		t.Fatalf("id do grupo = %q", id)
	}

	if _, err := salvarGrupo(cfg, id, "PhotoNow Studio"); err != nil {
		t.Fatal(err)
	}
	if cfg.grupoPorID(id).Nome != "PhotoNow Studio" {
		t.Fatalf("nome = %q", cfg.grupoPorID(id).Nome)
	}
	if _, err := salvarGrupo(cfg, "", "   "); err == nil {
		t.Fatal("grupo sem nome deveria ser recusado")
	}
	if _, err := salvarGrupo(cfg, "fantasma", "x"); err == nil {
		t.Fatal("renomear grupo inexistente deveria dar erro")
	}
}

func TestRemoverGrupoSoQuandoVazio(t *testing.T) {
	cfg := configTeste()

	err := removerGrupo(cfg, "infra")
	if err == nil {
		t.Fatal("grupo com projetos nao pode ser apagado")
	}
	if !strings.Contains(err.Error(), "mova ou apague antes") {
		t.Fatalf("a mensagem deveria dizer o que fazer: %v", err)
	}

	id, _ := salvarGrupo(cfg, "", "vazio")
	if err := removerGrupo(cfg, id); err != nil {
		t.Fatalf("grupo vazio deveria sair: %v", err)
	}
	if cfg.grupoPorID(id) != nil {
		t.Fatal("o grupo continua la")
	}
}

func TestOrdenarGrupos(t *testing.T) {
	cfg := configTeste() // infra, api, web

	if err := ordenarGrupos(cfg, []string{"web", "infra"}); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, g := range cfg.Grupos {
		got = append(got, g.ID)
	}
	// Os citados vem na ordem pedida; o resto mantem a ordem antiga no fim.
	if !reflect.DeepEqual(got, []string{"web", "infra", "api"}) {
		t.Fatalf("ordem = %v", got)
	}
	if err := ordenarGrupos(cfg, []string{"fantasma"}); err == nil {
		t.Fatal("grupo inexistente na ordem deveria dar erro")
	}
}

// ------------------------------------------------------------------
// perfis
// ------------------------------------------------------------------

func TestSalvarEAplicarPerfil(t *testing.T) {
	cfg := configTeste()
	for _, s := range cfg.Servicos {
		s.Selecionado = true
	}

	id, err := salvarPerfil(cfg, "", "so as APIs", []string{"db", "rabbit", "api"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PerfilAtivo != id {
		t.Fatalf("salvar deveria deixar o perfil ativo (%q)", cfg.PerfilAtivo)
	}

	if err := aplicarPerfil(cfg, id); err != nil {
		t.Fatal(err)
	}
	for _, s := range cfg.Servicos {
		querido := s.ID != "web"
		if s.Selecionado != querido {
			t.Fatalf("%s selecionado=%v, queria %v", s.ID, s.Selecionado, querido)
		}
	}
}

func TestSalvarPerfilRecusaVazioESemNome(t *testing.T) {
	cfg := configTeste()
	if _, err := salvarPerfil(cfg, "", "vazio", nil); err == nil {
		t.Fatal("perfil sem servico deveria ser recusado")
	}
	if _, err := salvarPerfil(cfg, "", "  ", []string{"db"}); err == nil {
		t.Fatal("perfil sem nome deveria ser recusado")
	}
}

func TestAtualizarPerfilTrocaALista(t *testing.T) {
	cfg := configTeste()
	id, _ := salvarPerfil(cfg, "", "stack", []string{"db"})

	if _, err := salvarPerfil(cfg, id, "stack", []string{"db", "rabbit", "api"}); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Perfis) != 1 {
		t.Fatalf("atualizar criou perfil novo: %d", len(cfg.Perfis))
	}
	if !reflect.DeepEqual(cfg.perfilPorID(id).Servicos, []string{"db", "rabbit", "api"}) {
		t.Fatalf("lista = %v", cfg.perfilPorID(id).Servicos)
	}
}

func TestRemoverPerfilLimpaOAtivo(t *testing.T) {
	cfg := configTeste()
	id, _ := salvarPerfil(cfg, "", "stack", []string{"db"})

	if err := removerPerfil(cfg, id); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Perfis) != 0 || cfg.PerfilAtivo != "" {
		t.Fatalf("sobrou perfil (%d) ou ativo (%q)", len(cfg.Perfis), cfg.PerfilAtivo)
	}
	if err := removerPerfil(cfg, "fantasma"); err == nil {
		t.Fatal("perfil inexistente deveria dar erro")
	}
}

// ------------------------------------------------------------------
// utilitarios
// ------------------------------------------------------------------

func TestGerarID(t *testing.T) {
	casos := []struct{ nome, querido string }{
		{"AppDaTurma Backend", "appdaturma-backend"},
		{"Cabine Foto  Gestao", "cabine-foto-gestao"},
		{"Configuração Inicial", "configuracao-inicial"},
		{"  ---  ", "servico"},
		{"API 2.0", "api-2-0"},
	}
	for _, caso := range casos {
		if got := gerarID(caso.nome, map[string]bool{}); got != caso.querido {
			t.Fatalf("gerarID(%q) = %q, queria %q", caso.nome, got, caso.querido)
		}
	}

	usados := map[string]bool{"api": true, "api-2": true}
	if got := gerarID("api", usados); got != "api-3" {
		t.Fatalf("desempate = %q, queria api-3", got)
	}
}

func TestDentroDe(t *testing.T) {
	raiz := filepath.FromSlash("C:/Users/Pichau/projetos")
	dentro := []string{
		filepath.FromSlash("C:/Users/Pichau/projetos"),
		filepath.FromSlash("C:/Users/Pichau/projetos/appdaturma"),
		filepath.FromSlash("C:/Users/Pichau/projetos/a/b/c"),
	}
	for _, c := range dentro {
		if !dentroDe(raiz, c) {
			t.Fatalf("%q deveria estar dentro de %q", c, raiz)
		}
	}
	fora := []string{
		filepath.FromSlash("C:/Users/Pichau"),
		filepath.FromSlash("C:/Windows/System32"),
		// Nome que so comeca igual nao conta como estar dentro.
		filepath.FromSlash("C:/Users/Pichau/projetos-antigos/x"),
	}
	for _, c := range fora {
		if dentroDe(raiz, c) {
			t.Fatalf("%q nao deveria contar como dentro de %q", c, raiz)
		}
	}
}

func TestNormalizarCriaGruposDoFormatoAntigo(t *testing.T) {
	// Config da primeira versao: sem grupos, sem perfis, grupo era so um texto no servico.
	cfg := &Config{Servicos: []*Servico{
		{ID: "db", Nome: "Postgres", Grupo: "infra", Tipo: TipoDocker,
			Compose: &Compose{Projeto: "p", Arquivo: "a.yml", Servico: "db"},
			Pronto:  Checagem{Tipo: "porta", Porta: 5432}},
		{ID: "solto", Nome: "Sem grupo", Tipo: TipoApp, Dir: "x", Cmd: "y",
			Pronto: Checagem{Tipo: "porta", Porta: 1}},
	}}
	cfg.normalizar()

	if err := cfg.validar(); err != nil {
		t.Fatalf("config antigo deveria virar valido: %v", err)
	}
	if cfg.grupoPorID("infra") == nil || cfg.grupoPorID("infra").Nome != "infraestrutura" {
		t.Fatalf("grupo infra = %+v", cfg.grupoPorID("infra"))
	}
	if cfg.porID("solto").Grupo != "geral" || cfg.grupoPorID("geral") == nil {
		t.Fatal("servico sem grupo deveria cair em 'geral'")
	}
	if cfg.Perfis == nil {
		t.Fatal("perfis deveria virar lista vazia, nao nil")
	}
}
