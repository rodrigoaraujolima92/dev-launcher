package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolverRaizRespeitaAPrecedencia(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "dev-launcher", "config.json")

	// Sem nada informado: a pasta acima do config (o comportamento antigo continua valendo
	// para quem nunca configurou raiz).
	raiz, origem := resolverRaiz("", "", "", cfg)
	if raiz != filepath.Clean(base) || origem != RaizPadrao {
		t.Fatalf("padrao = %q (%s), queria %q", raiz, origem, base)
	}

	// Config manda no padrao; flag manda em todos.
	raiz, origem = resolverRaiz("", "", filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "doConfig") || origem != RaizDeConfig {
		t.Fatalf("config = %q (%s)", raiz, origem)
	}
	raiz, origem = resolverRaiz("", filepath.Join(base, "doAmbiente"), filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "doAmbiente") || origem != RaizDeAmbiente {
		t.Fatalf("ambiente = %q (%s)", raiz, origem)
	}
	raiz, origem = resolverRaiz(filepath.Join(base, "daFlag"), filepath.Join(base, "doAmbiente"), filepath.Join(base, "doConfig"), cfg)
	if raiz != filepath.Join(base, "daFlag") || origem != RaizDeFlag {
		t.Fatalf("flag = %q (%s)", raiz, origem)
	}
}

func TestResolverRaizRelativaUsaAPastaDoConfig(t *testing.T) {
	base := t.TempDir()
	cfg := filepath.Join(base, "dev-launcher", "config.json")

	// ".." e o caso do launcher morando dentro da pasta de projetos - e o que faz o mesmo
	// config abrir em qualquer maquina, sem caminho absoluto de ninguem.
	raiz, _ := resolverRaiz("", "", "..", cfg)
	if raiz != filepath.Clean(base) {
		t.Fatalf("raiz = %q, queria %q", raiz, base)
	}

	// Relativo NAO pode depender do diretorio de trabalho: o launcher e chamado por atalho.
	anterior, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(anterior) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	outra, _ := resolverRaiz("", "", "..", cfg)
	if outra != raiz {
		t.Fatalf("mudar de diretorio mudou a raiz: %q vs %q", outra, raiz)
	}
}

func TestTornarRelativoMantemOConfigPortavel(t *testing.T) {
	raiz := filepath.FromSlash("C:/Users/fulano/projetos")

	dentro := filepath.Join(raiz, "appdaturma", "appdaturma-backend")
	if got := tornarRelativo(raiz, dentro); got != "appdaturma/appdaturma-backend" {
		t.Fatalf("dentro da raiz = %q, queria caminho relativo com barra normal", got)
	}
	// Fora da raiz continua absoluto: virar "../../.." seria pior de ler e de manter.
	fora := filepath.FromSlash("D:/outro/lugar")
	if got := tornarRelativo(raiz, fora); got != fora {
		t.Fatalf("fora da raiz = %q, queria o absoluto", got)
	}
	// Ja relativo so normaliza a barra.
	if got := tornarRelativo(raiz, filepath.FromSlash("photonow/front")); got != "photonow/front" {
		t.Fatalf("ja relativo = %q", got)
	}
	if got := tornarRelativo("", "qualquer"); got != "qualquer" {
		t.Fatalf("sem raiz = %q", got)
	}
}

func TestCaminhosFaltandoAcusaConfigDeOutraMaquina(t *testing.T) {
	g, _, cfg := gerenteTeste(t, nil)

	// A config de teste aponta para "backend" e "front", que nao existem no tempdir.
	faltando := g.CaminhosFaltando()
	if !contem(faltando, "api") || !contem(faltando, "web") {
		t.Fatalf("deveria acusar os dois projetos app: %v", faltando)
	}

	// Criada a pasta, o aviso some.
	if err := os.MkdirAll(filepath.Join(g.raiz, cfg.porID("api").Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if faltando := g.CaminhosFaltando(); contem(faltando, "api") {
		t.Fatalf("com a pasta criada nao deveria mais acusar: %v", faltando)
	}
}

func TestSalvarServicoGuardaCaminhoRelativo(t *testing.T) {
	raizNav, dentro, _ := pastasTeste(t)
	cfg := configTeste()

	// O seletor de pastas devolve caminho absoluto; o config tem que guardar relativo.
	id, err := salvarServico(cfg, EntradaServico{
		Nome: "cabine", Grupo: "web", Tipo: TipoApp, Dir: dentro, Cmd: "npm run dev", Porta: 5173,
	}, raizNav, raizNav)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.porID(id).Dir; got != "photonow/cabine-foto-front" {
		t.Fatalf("dir gravado = %q, queria relativo a raiz", got)
	}
}
