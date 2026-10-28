package main

// Descoberta de projetos: dada uma pasta, acha o que da para rodar nela e nas subpastas -
// um sistema so, um monorepo com front e back, uma pasta de empresa com dez repositorios - e
// propoe, para cada um, o nome, o comando que sobe local e a porta. A tela mostra a lista e
// a pessoa so marca o que quer cadastrar.
//
// Tudo aqui e palpite a partir dos arquivos do projeto (package.json, go.mod, pom.xml...):
// nada e executado. Palpite errado se corrige na tela antes de cadastrar.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Descoberta struct {
	Nome       string     `json:"nome"`
	Tipo       string     `json:"tipo"`       // app | docker
	Tecnologia string     `json:"tecnologia"` // node, go, java, dotnet, python, php, ruby, rust, script, compose
	Dir        string     `json:"dir"`        // pasta absoluta do projeto (ou do compose)
	Relativo   string     `json:"relativo"`   // em relacao a pasta escolhida; "." e ela mesma
	Cmd        string     `json:"cmd,omitempty"`
	Comandos   []Sugestao `json:"comandos,omitempty"`
	Porta      int        `json:"porta,omitempty"`
	Compose    *Compose   `json:"compose,omitempty"`
	Container  string     `json:"container,omitempty"`
	Imagem     string     `json:"imagem,omitempty"`
	Cadastrado string     `json:"cadastrado,omitempty"` // id do projeto que ja aponta para ca
}

type ResultadoDescoberta struct {
	Caminho  string       `json:"caminho"`
	Nome     string       `json:"nome"` // nome da pasta: vira a sugestao de grupo
	Projetos []Descoberta `json:"projetos"`
	Cortado  bool         `json:"cortado,omitempty"`
}

const (
	profundidadeDescoberta = 4    // pasta/empresa/repo/apps/web
	maxPastasDescoberta    = 1500 // pasta enorme (C:\) nao pode travar a tela
	maxDescobertas         = 120
)

// Alem das pastasIgnoradas do seletor: pastas que nunca guardam um projeto proprio.
var ignoradasDescoberta = map[string]bool{
	"coverage": true, "venv": true, "migrations": true, "public": true, "static": true,
	"assets": true, "docs": true, "test": true, "tests": true, "e2e": true, "cypress": true,
	"out": true, "tmp": true, "temp": true, "logs": true, "storybook-static": true,
	"resources": true, "properties": true,
}

func descobrirProjetos(raiz string) (*ResultadoDescoberta, error) {
	raiz = filepath.Clean(raiz)
	info, err := os.Stat(raiz)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("pasta nao encontrada: %s", raiz)
	}
	res := &ResultadoDescoberta{Caminho: raiz, Nome: filepath.Base(raiz), Projetos: []Descoberta{}}

	type item struct {
		dir   string
		nivel int
	}
	fila := []item{{raiz, 0}}
	visitadas := 0
	for len(fila) > 0 {
		atual := fila[0]
		fila = fila[1:]
		if visitadas++; visitadas > maxPastasDescoberta {
			res.Cortado = true
			break
		}
		entradas, err := os.ReadDir(atual.dir)
		if err != nil {
			continue
		}
		relativo, err := filepath.Rel(raiz, atual.dir)
		if err != nil {
			relativo = atual.dir
		}
		for _, d := range detectarPasta(atual.dir, entradas, true) {
			d.Relativo = filepath.ToSlash(relativo)
			res.Projetos = append(res.Projetos, d)
		}
		if len(res.Projetos) >= maxDescobertas {
			res.Projetos = res.Projetos[:maxDescobertas]
			res.Cortado = true
			break
		}
		if atual.nivel >= profundidadeDescoberta {
			continue
		}
		for _, e := range entradas {
			nome := e.Name()
			minusculo := strings.ToLower(nome)
			if !e.IsDir() || strings.HasPrefix(nome, ".") || pastasIgnoradas[minusculo] || ignoradasDescoberta[minusculo] {
				continue
			}
			fila = append(fila, item{filepath.Join(atual.dir, nome), atual.nivel + 1})
		}
	}
	// Ordem de leitura: pela pasta, e dentro da pasta os apps antes dos containers.
	sort.SliceStable(res.Projetos, func(i, j int) bool {
		a, b := res.Projetos[i], res.Projetos[j]
		if a.Relativo != b.Relativo {
			return strings.ToLower(a.Relativo) < strings.ToLower(b.Relativo)
		}
		return a.Tipo == TipoApp && b.Tipo != TipoApp
	})
	return res, nil
}

// detectarPasta olha uma pasta so. estrito=true e a varredura: so conta como projeto o que
// tem evidencia de ser rodavel (uma biblioteca Go sem main nao entra).
func detectarPasta(dir string, entradas []os.DirEntry, estrito bool) []Descoberta {
	nomes := nomesDaPasta(entradas)
	out := []Descoberta{}
	app := detectarApp(dir, nomes)
	if estrito && app.Tecnologia == "script" {
		// Pasta que so tem script: so entra se o script tem cara de "sobe o projeto".
		filtradas := []Sugestao{}
		for _, s := range app.Sugestoes {
			if s.preferido {
				filtradas = append(filtradas, s)
			}
		}
		app.Sugestoes = filtradas
	}
	if len(app.Sugestoes) > 0 && (app.Certeza || !estrito) {
		d := Descoberta{
			Nome: app.Nome, Tipo: TipoApp, Tecnologia: app.Tecnologia, Dir: dir,
			Cmd: app.Sugestoes[0].Cmd, Comandos: app.Sugestoes, Porta: app.Sugestoes[0].Porta,
		}
		if d.Nome == "" {
			d.Nome = filepath.Base(dir)
		}
		out = append(out, d)
	}
	out = append(out, composesDaPasta(dir, nomes)...)
	return out
}

// nomesDaPasta mapeia nome em minusculo -> nome real, separando arquivo de pasta.
type conteudoPasta struct {
	arquivos map[string]string
	pastas   map[string]string
}

func nomesDaPasta(entradas []os.DirEntry) conteudoPasta {
	c := conteudoPasta{arquivos: map[string]string{}, pastas: map[string]string{}}
	for _, e := range entradas {
		if e.IsDir() {
			c.pastas[strings.ToLower(e.Name())] = e.Name()
		} else {
			c.arquivos[strings.ToLower(e.Name())] = e.Name()
		}
	}
	return c
}

func (c conteudoPasta) tem(nome string) bool {
	_, ok := c.arquivos[nome]
	return ok
}

func (c conteudoPasta) temPasta(nome string) bool {
	_, ok := c.pastas[nome]
	return ok
}

// comSufixo devolve os arquivos que terminam com o sufixo, em ordem.
func (c conteudoPasta) comSufixo(sufixo string) []string {
	out := []string{}
	for minusculo, real := range c.arquivos {
		if strings.HasSuffix(minusculo, sufixo) {
			out = append(out, real)
		}
	}
	sort.Strings(out)
	return out
}

// ------------------------------------------------------------------
// aplicacoes
// ------------------------------------------------------------------

type deteccaoApp struct {
	Tecnologia string
	Nome       string
	Sugestoes  []Sugestao
	Certeza    bool // ha evidencia de que e rodavel (e nao so uma biblioteca)
}

// detectarApp propoe os comandos de uma pasta, o mais provavel primeiro. Script de subida
// que o proprio projeto traz (run-local.ps1, start.bat) vem na frente: se alguem se deu ao
// trabalho de escrever, e esse o jeito certo de rodar.
func detectarApp(dir string, c conteudoPasta) deteccaoApp {
	det := deteccaoApp{}
	anotar := func(tec, nome string, certeza bool, sugestoes ...Sugestao) {
		if len(sugestoes) == 0 {
			return
		}
		if det.Tecnologia == "" {
			det.Tecnologia, det.Nome = tec, nome
		}
		det.Certeza = det.Certeza || certeza
		det.Sugestoes = append(det.Sugestoes, sugestoes...)
	}

	envPorta := portaDoEnv(dir)

	if c.tem("package.json") {
		if dados, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
			nome, sugestoes := sugestoesNode(dir, dados, gerenciadorNode(c), envPorta)
			anotar("node", nome, true, sugestoes...)
		}
	}
	if c.tem("go.mod") {
		anotar("go", "", true, sugestoesGo(dir, c, envPorta)...)
	}
	if c.tem("pom.xml") {
		certeza, sugestoes := sugestoesMaven(dir, c, envPorta)
		anotar("java", "", certeza, sugestoes...)
	}
	if c.tem("build.gradle") || c.tem("build.gradle.kts") {
		certeza, sugestoes := sugestoesGradle(dir, c, envPorta)
		anotar("java", "", certeza, sugestoes...)
	}
	if projetos := c.comSufixo(".csproj"); len(projetos) > 0 {
		nome, certeza, sugestoes := sugestoesDotnet(dir, projetos[0], envPorta)
		anotar("dotnet", nome, certeza, sugestoes...)
	}
	if s := sugestoesPython(dir, c, envPorta); len(s) > 0 {
		anotar("python", "", true, s...)
	}
	if c.tem("artisan") {
		anotar("php", "", true, Sugestao{Rotulo: "php artisan serve", Cmd: "php artisan serve", Porta: portaOu(envPorta, 8000)})
	}
	if c.tem("gemfile") && c.temPasta("bin") {
		if _, err := os.Stat(filepath.Join(dir, "bin", "rails")); err == nil {
			anotar("ruby", "", true, Sugestao{Rotulo: "rails server", Cmd: "ruby bin/rails server", Porta: portaOu(envPorta, 3000)})
		}
	}
	if c.tem("cargo.toml") {
		if _, err := os.Stat(filepath.Join(dir, "src", "main.rs")); err == nil {
			anotar("rust", "", true, Sugestao{Rotulo: "cargo run", Cmd: "cargo run", Porta: envPorta})
		}
	}

	// Scripts soltos na raiz. O de subida (run-local.ps1, start.bat) passa a frente de tudo.
	preferidos, outros := []Sugestao{}, []Sugestao{}
	for _, sufixo := range []string{".ps1", ".bat", ".cmd"} {
		for _, nome := range c.comSufixo(sufixo) {
			s := Sugestao{Rotulo: nome, Cmd: fmt.Sprintf("& './%s'", nome), Porta: envPorta}
			if reScriptSubida.MatchString(strings.ToLower(nome)) {
				s.preferido = true
				preferidos = append(preferidos, s)
			} else {
				outros = append(outros, s)
			}
		}
	}
	if len(preferidos) > 0 {
		// Herdam a porta que o detector da tecnologia achou: o script sobe o mesmo sistema.
		for i := range preferidos {
			if preferidos[i].Porta == 0 && len(det.Sugestoes) > 0 {
				preferidos[i].Porta = det.Sugestoes[0].Porta
			}
		}
		if det.Tecnologia == "" {
			det.Tecnologia = "script"
		}
		det.Certeza = true
		det.Sugestoes = append(preferidos, det.Sugestoes...)
	}
	if len(outros) > 0 {
		if det.Tecnologia == "" {
			det.Tecnologia = "script"
		}
		det.Sugestoes = append(det.Sugestoes, outros...)
	}
	if c.tem("makefile") {
		if alvo := alvoDoMakefile(dir, c.arquivos["makefile"]); alvo != "" {
			if det.Tecnologia == "" {
				det.Tecnologia = "make"
			}
			det.Sugestoes = append(det.Sugestoes, Sugestao{Rotulo: "make " + alvo, Cmd: "make " + alvo, Porta: envPorta})
		}
	}
	return det
}

var reScriptSubida = regexp.MustCompile(`^(run|start|dev|serve|up|iniciar|rodar|subir)`)

func portaOu(porta, padrao int) int {
	if porta > 0 {
		return porta
	}
	return padrao
}

var rePortaEnv = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:PORT|APP_PORT|SERVER_PORT|HTTP_PORT|API_PORT|VITE_PORT)\s*=\s*["']?(\d{2,5})`)

// portaDoEnv le a porta dos .env do projeto - e onde a maioria dos projetos guarda.
func portaDoEnv(dir string) int {
	for _, nome := range []string{".env.local", ".env.development.local", ".env.development", ".env"} {
		dados, err := os.ReadFile(filepath.Join(dir, nome))
		if err != nil {
			continue
		}
		if achado := rePortaEnv.FindSubmatch(dados); len(achado) == 2 {
			if p, err := strconv.Atoi(string(achado[1])); err == nil && p > 0 && p <= 65535 {
				return p
			}
		}
	}
	return 0
}

// ---- node ----

func gerenciadorNode(c conteudoPasta) string {
	switch {
	case c.tem("pnpm-lock.yaml"):
		return "pnpm"
	case c.tem("yarn.lock"):
		return "yarn"
	case c.tem("bun.lockb"), c.tem("bun.lock"):
		return "bun"
	}
	return "npm"
}

var scriptsDeSubida = []string{"dev", "start:dev", "serve", "start", "develop", "host"}

var (
	rePortaScript  = regexp.MustCompile(`(?:--port|-p)[= ](\d{2,5})\b`)
	rePortaVarEnv  = regexp.MustCompile(`\bPORT=(\d{2,5})\b`)
	rePortaConfig  = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})`)
	rePortaAngular = regexp.MustCompile(`"port"\s*:\s*(\d{2,5})`)
)

// sugestoesNode le o package.json: nome do pacote e os scripts que sobem a aplicacao, com a
// porta tirada (nesta ordem) do proprio script, do arquivo de config do framework, do .env
// ou do padrao do framework.
func sugestoesNode(dir string, dados []byte, gerenciador string, envPorta int) (string, []Sugestao) {
	var pacote struct {
		Nome    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(dados, &pacote); err != nil {
		return "", nil
	}
	nome := pacote.Nome
	if i := strings.LastIndex(nome, "/"); i >= 0 { // @empresa/front -> front
		nome = nome[i+1:]
	}
	portaConfig := 0
	if dir != "" {
		portaConfig = portaDoConfigNode(dir)
	}
	out := []Sugestao{}
	for _, script := range scriptsDeSubida {
		conteudo, ok := pacote.Scripts[script]
		if !ok {
			continue
		}
		s := Sugestao{Rotulo: comandoNode(gerenciador, script), Cmd: comandoNode(gerenciador, script)}
		switch {
		case rePortaScript.MatchString(conteudo):
			s.Porta, _ = strconv.Atoi(rePortaScript.FindStringSubmatch(conteudo)[1])
		case rePortaVarEnv.MatchString(conteudo):
			s.Porta, _ = strconv.Atoi(rePortaVarEnv.FindStringSubmatch(conteudo)[1])
		case portaConfig > 0:
			s.Porta = portaConfig
		case envPorta > 0:
			s.Porta = envPorta
		default:
			s.Porta = portaPadraoNode(conteudo)
		}
		out = append(out, s)
	}
	return nome, out
}

func comandoNode(gerenciador, script string) string {
	switch gerenciador {
	case "yarn", "pnpm":
		return gerenciador + " " + script
	case "bun":
		return "bun run " + script
	}
	return "npm run " + script
}

// portaPadraoNode e a porta que o framework usa quando ninguem diz outra.
func portaPadraoNode(script string) int {
	switch {
	case strings.Contains(script, "vue-cli-service serve"), strings.Contains(script, "webpack serve"),
		strings.Contains(script, "webpack-dev-server"):
		return 8080
	case strings.Contains(script, "ng serve"):
		return 4200
	case strings.Contains(script, "astro"):
		return 4321
	case strings.Contains(script, "vite"):
		return 5173
	case strings.Contains(script, "next"), strings.Contains(script, "nuxt"), strings.Contains(script, "react-scripts"),
		strings.Contains(script, "nest start"), strings.Contains(script, "remix"):
		return 3000
	}
	return 0
}

func portaDoConfigNode(dir string) int {
	for _, nome := range []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vue.config.js", "nuxt.config.ts", "nuxt.config.js"} {
		if dados, err := os.ReadFile(filepath.Join(dir, nome)); err == nil {
			if achado := rePortaConfig.FindSubmatch(dados); len(achado) == 2 {
				p, _ := strconv.Atoi(string(achado[1]))
				return p
			}
		}
	}
	if dados, err := os.ReadFile(filepath.Join(dir, "angular.json")); err == nil {
		if achado := rePortaAngular.FindSubmatch(dados); len(achado) == 2 {
			p, _ := strconv.Atoi(string(achado[1]))
			return p
		}
	}
	return 0
}

// ---- go ----

var rePortaGo = regexp.MustCompile(`"(?:localhost|0\.0\.0\.0|127\.0\.0\.1)?:(\d{4,5})"`)

// sugestoesGo: "go run ." quando ha main.go na raiz, um "go run ./cmd/x" para cada programa
// em cmd/. Biblioteca (sem nenhum dos dois) nao gera sugestao.
func sugestoesGo(dir string, c conteudoPasta, envPorta int) []Sugestao {
	alvos := []string{}
	if c.tem("main.go") {
		alvos = append(alvos, ".")
	}
	if c.temPasta("cmd") {
		if subs, err := os.ReadDir(filepath.Join(dir, c.pastas["cmd"])); err == nil {
			for _, s := range subs {
				if s.IsDir() {
					alvos = append(alvos, "./cmd/"+s.Name())
				}
			}
		}
	}
	// Programa chamado api/server/app primeiro: e o que costuma ser "o" sistema.
	sort.SliceStable(alvos, func(i, j int) bool { return pesoAlvoGo(alvos[i]) < pesoAlvoGo(alvos[j]) })
	out := []Sugestao{}
	for _, alvo := range alvos {
		porta := envPorta
		if porta == 0 {
			porta = portaNoCodigoGo(filepath.Join(dir, filepath.FromSlash(alvo)))
		}
		out = append(out, Sugestao{Rotulo: "go run " + alvo, Cmd: "go run " + alvo, Porta: porta})
	}
	if c.tem(".air.toml") && len(out) > 0 {
		out = append(out, Sugestao{Rotulo: "air (hot reload)", Cmd: "air", Porta: out[0].Porta})
	}
	return out
}

func pesoAlvoGo(alvo string) int {
	switch filepath.Base(alvo) {
	case "api", "server", "app", "web":
		return 0
	case ".":
		return 1
	}
	return 2
}

func portaNoCodigoGo(dir string) int {
	arquivos, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, a := range arquivos {
		if a.IsDir() || !strings.HasSuffix(a.Name(), ".go") || strings.HasSuffix(a.Name(), "_test.go") {
			continue
		}
		if dados, err := os.ReadFile(filepath.Join(dir, a.Name())); err == nil {
			if achado := rePortaGo.FindSubmatch(dados); len(achado) == 2 {
				p, _ := strconv.Atoi(string(achado[1]))
				return p
			}
		}
	}
	return 0
}

// ---- java ----

var (
	rePortaProperties = regexp.MustCompile(`(?m)^\s*(?:server\.port|quarkus\.http\.port)\s*[=:]\s*(?:\$\{[^:}]+:)?(\d{2,5})`)
	rePortaYaml       = regexp.MustCompile(`(?m)^server:\s*\n(?:[ \t]+.*\n)*?[ \t]+port:\s*(?:\$\{[^:}]+:)?(\d{2,5})`)
)

func portaSpring(dir string, envPorta int) int {
	if envPorta > 0 {
		return envPorta
	}
	pasta := filepath.Join(dir, "src", "main", "resources")
	for _, nome := range []string{"application-local.properties", "application-dev.properties", "application.properties"} {
		if dados, err := os.ReadFile(filepath.Join(pasta, nome)); err == nil {
			if achado := rePortaProperties.FindSubmatch(dados); len(achado) == 2 {
				p, _ := strconv.Atoi(string(achado[1]))
				return p
			}
		}
	}
	for _, nome := range []string{"application-local.yml", "application-dev.yml", "application.yml", "application.yaml"} {
		if dados, err := os.ReadFile(filepath.Join(pasta, nome)); err == nil {
			texto := strings.ReplaceAll(string(dados), "\r", "") + "\n"
			if achado := rePortaYaml.FindStringSubmatch(texto); len(achado) == 2 {
				p, _ := strconv.Atoi(achado[1])
				return p
			}
		}
	}
	return 8080
}

func sugestoesMaven(dir string, c conteudoPasta, envPorta int) (bool, []Sugestao) {
	dados, err := os.ReadFile(filepath.Join(dir, c.arquivos["pom.xml"]))
	if err != nil {
		return false, nil
	}
	pom := string(dados)
	// pom pai de projeto multi-modulo nao roda nada: os modulos sao achados nas subpastas.
	if strings.Contains(pom, "<modules>") {
		return false, nil
	}
	mvn := "mvn"
	if c.tem("mvnw.cmd") || c.tem("mvnw") {
		mvn = `.\mvnw`
	}
	switch {
	case strings.Contains(pom, "spring-boot"):
		return true, []Sugestao{{Rotulo: "spring-boot:run", Cmd: mvn + " spring-boot:run", Porta: portaSpring(dir, envPorta)}}
	case strings.Contains(pom, "quarkus"):
		return true, []Sugestao{{Rotulo: "quarkus:dev", Cmd: mvn + " quarkus:dev", Porta: portaSpring(dir, envPorta)}}
	}
	return false, []Sugestao{{Rotulo: "exec:java", Cmd: mvn + " compile exec:java", Porta: envPorta}}
}

func sugestoesGradle(dir string, c conteudoPasta, envPorta int) (bool, []Sugestao) {
	nome := c.arquivos["build.gradle"]
	if nome == "" {
		nome = c.arquivos["build.gradle.kts"]
	}
	dados, err := os.ReadFile(filepath.Join(dir, nome))
	if err != nil {
		return false, nil
	}
	gradle := "gradle"
	if c.tem("gradlew.bat") || c.tem("gradlew") {
		gradle = `.\gradlew`
	}
	texto := string(dados)
	switch {
	case strings.Contains(texto, "org.springframework.boot"):
		return true, []Sugestao{{Rotulo: "bootRun", Cmd: gradle + " bootRun", Porta: portaSpring(dir, envPorta)}}
	case strings.Contains(texto, "application"):
		return true, []Sugestao{{Rotulo: "run", Cmd: gradle + " run", Porta: envPorta}}
	}
	return false, nil
}

// ---- .NET ----

// Prefere a url http: abre no navegador sem aviso de certificado.
var (
	reURLDotnetHTTP = regexp.MustCompile(`"applicationUrl"\s*:\s*"[^"]*?http://(?:localhost|0\.0\.0\.0|\*|\+):(\d{2,5})`)
	reURLDotnet     = regexp.MustCompile(`"applicationUrl"\s*:\s*"[^"]*?https?://(?:localhost|0\.0\.0\.0|\*|\+):(\d{2,5})`)
)

func sugestoesDotnet(dir, csproj string, envPorta int) (string, bool, []Sugestao) {
	nome := strings.TrimSuffix(csproj, filepath.Ext(csproj))
	dados, err := os.ReadFile(filepath.Join(dir, csproj))
	if err != nil {
		return nome, false, nil
	}
	texto := string(dados)
	web := strings.Contains(texto, "Microsoft.NET.Sdk.Web")
	exe := web || strings.Contains(texto, "Microsoft.NET.Sdk.Worker") || strings.Contains(texto, "<OutputType>Exe</OutputType>")
	if !exe {
		return nome, false, nil // biblioteca de classes
	}
	porta := envPorta
	if porta == 0 {
		if ls, err := os.ReadFile(filepath.Join(dir, "Properties", "launchSettings.json")); err == nil {
			for _, re := range []*regexp.Regexp{reURLDotnetHTTP, reURLDotnet} {
				if achado := re.FindSubmatch(ls); len(achado) == 2 {
					porta, _ = strconv.Atoi(string(achado[1]))
					break
				}
			}
		}
	}
	if porta == 0 && web {
		porta = 5000
	}
	return nome, true, []Sugestao{
		{Rotulo: "dotnet run", Cmd: "dotnet run", Porta: porta},
		{Rotulo: "dotnet watch", Cmd: "dotnet watch", Porta: porta},
	}
}

// ---- python ----

func sugestoesPython(dir string, c conteudoPasta, envPorta int) []Sugestao {
	py := "python"
	for _, venv := range []string{".venv", "venv", "env"} {
		if _, err := os.Stat(filepath.Join(dir, venv, "Scripts", "python.exe")); err == nil {
			py = `.\` + venv + `\Scripts\python.exe`
			break
		}
	}
	if c.tem("manage.py") {
		return []Sugestao{{Rotulo: "django runserver", Cmd: py + " manage.py runserver", Porta: portaOu(envPorta, 8000)}}
	}
	deps := ""
	for _, nome := range []string{"requirements.txt", "pyproject.toml", "pipfile"} {
		if real, ok := c.arquivos[nome]; ok {
			if dados, err := os.ReadFile(filepath.Join(dir, real)); err == nil {
				deps += strings.ToLower(string(dados))
			}
		}
	}
	if deps == "" && !c.tem("main.py") && !c.tem("app.py") {
		return nil
	}
	_, errAppMain := os.Stat(filepath.Join(dir, "app", "main.py"))
	switch {
	case strings.Contains(deps, "fastapi") || strings.Contains(deps, "uvicorn"):
		modulo := "main:app"
		if !c.tem("main.py") && errAppMain == nil {
			modulo = "app.main:app"
		}
		porta := portaOu(envPorta, 8000)
		return []Sugestao{{Rotulo: "uvicorn " + modulo, Cmd: fmt.Sprintf("%s -m uvicorn %s --reload --port %d", py, modulo, porta), Porta: porta}}
	case strings.Contains(deps, "streamlit"):
		for _, arq := range []string{"app.py", "main.py"} {
			if c.tem(arq) {
				return []Sugestao{{Rotulo: "streamlit run " + arq, Cmd: py + " -m streamlit run " + arq, Porta: portaOu(envPorta, 8501)}}
			}
		}
	case strings.Contains(deps, "flask") && (c.tem("app.py") || c.tem("wsgi.py")):
		porta := portaOu(envPorta, 5000)
		return []Sugestao{{Rotulo: "flask run", Cmd: fmt.Sprintf("%s -m flask run --port %d", py, porta), Porta: porta}}
	}
	for _, arq := range []string{"main.py", "app.py"} {
		if c.tem(arq) {
			return []Sugestao{{Rotulo: "python " + arq, Cmd: py + " " + arq, Porta: envPorta}}
		}
	}
	return nil
}

// ---- make ----

var reAlvoMake = regexp.MustCompile(`(?m)^(run|dev|start|serve|up):`)

func alvoDoMakefile(dir, nome string) string {
	dados, err := os.ReadFile(filepath.Join(dir, nome))
	if err != nil {
		return ""
	}
	if achado := reAlvoMake.FindSubmatch(dados); len(achado) == 2 {
		return string(achado[1])
	}
	return ""
}

// ------------------------------------------------------------------
// docker compose
// ------------------------------------------------------------------

// ServicoCompose e o que interessa de um servico do compose para cadastrar.
type ServicoCompose struct {
	Nome      string
	Container string
	Imagem    string
	Porta     int // primeira porta publicada no host
}

func ehArquivoCompose(nome string) bool {
	n := strings.ToLower(nome)
	if !strings.HasSuffix(n, ".yml") && !strings.HasSuffix(n, ".yaml") {
		return false
	}
	return strings.HasPrefix(n, "docker-compose") || strings.HasPrefix(n, "compose")
}

func composesDaPasta(dir string, c conteudoPasta) []Descoberta {
	arquivos := []string{}
	for _, real := range c.arquivos {
		if ehArquivoCompose(real) {
			arquivos = append(arquivos, real)
		}
	}
	sort.Strings(arquivos)
	out := []Descoberta{}
	for _, arquivo := range arquivos {
		// Override (docker-compose.override.yml, .prod.yml) completa o principal - cadastrar
		// os servicos dele de novo so duplicaria.
		if len(arquivos) > 1 && strings.Count(strings.ToLower(arquivo), ".") > 1 {
			continue
		}
		dados, err := os.ReadFile(filepath.Join(dir, arquivo))
		if err != nil {
			continue
		}
		projeto, servicos := lerCompose(string(dados))
		if projeto == "" {
			projeto = nomeProjetoCompose(filepath.Base(dir))
		}
		for _, s := range servicos {
			container := s.Container
			if container == "" {
				container = projeto + "-" + s.Nome + "-1" // nome que o compose v2 da
			}
			out = append(out, Descoberta{
				Nome: s.Nome, Tipo: TipoDocker, Tecnologia: "compose", Dir: dir,
				Compose:   &Compose{Projeto: projeto, Arquivo: filepath.Join(dir, arquivo), Servico: s.Nome},
				Container: container, Imagem: s.Imagem, Porta: s.Porta,
			})
		}
	}
	return out
}

// nomeProjetoCompose repete a regra do docker compose para o nome padrao do projeto: o nome
// da pasta em minusculo, so com letras, numeros, "-" e "_".
func nomeProjetoCompose(pasta string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(pasta) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "-_")
}

var (
	reVarComPadrao = regexp.MustCompile(`\$\{[^:}]+:-([^}]*)\}`)
	reSoDigitos    = regexp.MustCompile(`^\d{1,5}$`)
)

// lerCompose le o nome do projeto e os servicos de um compose, sem biblioteca de YAML: pela
// indentacao, como servicosDoCompose. Pega container_name, image e a primeira porta
// publicada (nas duas sintaxes de ports).
func lerCompose(conteudo string) (projeto string, servicos []ServicoCompose) {
	servicos = []ServicoCompose{}
	dentro := false
	indentServico, indentProp := -1, -1
	emPortas := false
	var atual *ServicoCompose

	for _, bruta := range strings.Split(conteudo, "\n") {
		linha := strings.TrimRight(bruta, "\r")
		texto := strings.TrimSpace(linha)
		if texto == "" || strings.HasPrefix(texto, "#") {
			continue
		}
		indent := len(linha) - len(strings.TrimLeft(linha, " \t"))
		if indent == 0 {
			dentro = strings.HasPrefix(texto, "services:")
			if v, ok := strings.CutPrefix(texto, "name:"); ok {
				projeto = limparValorYaml(v)
			}
			atual = nil
			continue
		}
		if !dentro {
			continue
		}
		if indentServico == -1 {
			indentServico = indent
		}
		if indent == indentServico {
			nome := strings.TrimSpace(strings.SplitN(texto, ":", 2)[0])
			servicos = append(servicos, ServicoCompose{Nome: nome})
			atual = &servicos[len(servicos)-1]
			indentProp, emPortas = -1, false
			continue
		}
		if atual == nil || indent < indentServico {
			continue
		}
		if indentProp == -1 {
			indentProp = indent
		}
		if indent == indentProp {
			emPortas = false
			chave, valor, _ := strings.Cut(texto, ":")
			switch strings.TrimSpace(chave) {
			case "container_name":
				atual.Container = limparValorYaml(valor)
			case "image":
				atual.Imagem = limparValorYaml(valor)
			case "ports":
				emPortas = true
				// ports: ["5432:5432"] numa linha so
				if v := strings.TrimSpace(valor); strings.HasPrefix(v, "[") && atual.Porta == 0 {
					for _, item := range strings.Split(strings.Trim(v, "[]"), ",") {
						if p := portaPublicada(item); p > 0 {
							atual.Porta = p
							break
						}
					}
				}
			}
			continue
		}
		if emPortas && atual.Porta == 0 {
			item := strings.TrimSpace(strings.TrimPrefix(texto, "-"))
			if v, ok := strings.CutPrefix(item, "published:"); ok {
				atual.Porta = portaPublicada(v)
			} else if strings.HasPrefix(texto, "-") && !strings.Contains(item, "target:") {
				atual.Porta = portaPublicada(item)
			}
		}
	}
	return projeto, servicos
}

func limparValorYaml(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return strings.Trim(v, `"'`)
}

// portaPublicada tira a porta do host de "5432:5432", "127.0.0.1:8080:80/tcp",
// "${DB_PORT:-5433}:5432" ou "5432" (sintaxe longa, published). Porta so do container
// ("80") nao publica nada no host: zero.
func portaPublicada(item string) int {
	item = limparValorYaml(item)
	item = reVarComPadrao.ReplaceAllString(item, "$1")
	if i := strings.Index(item, "/"); i >= 0 {
		item = item[:i]
	}
	partes := strings.Split(item, ":")
	var host string
	switch len(partes) {
	case 1:
		host = partes[0]
		if !reSoDigitos.MatchString(host) {
			return 0
		}
		// "published: 5432" chega aqui com uma parte so; "- 80" (so container) tambem, mas
		// esse caso vem filtrado pelo chamador... na duvida, aceita: e a porta do servico.
	case 2:
		host = partes[0]
	default:
		host = partes[len(partes)-2]
	}
	if i := strings.Index(host, "-"); i >= 0 { // faixa "8000-8010"
		host = host[:i]
	}
	p, err := strconv.Atoi(strings.TrimSpace(host))
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

// ------------------------------------------------------------------
// importacao
// ------------------------------------------------------------------

// PedidoImportar e o que a tela manda depois de marcar os projetos descobertos.
type PedidoImportar struct {
	Grupo             string           `json:"grupo"`
	GrupoNovo         string           `json:"grupo_novo"`
	Perfil            string           `json:"perfil"`
	EsperarContainers bool             `json:"esperar_containers"`
	CaminhoLivre      bool             `json:"caminho_livre"`
	Projetos          []EntradaServico `json:"projetos"`
}

// importarProjetos cadastra todos de uma vez: ou entram todos, ou nenhum - importacao pela
// metade deixaria a pessoa caçando o que entrou.
func importarProjetos(cfg *Config, p PedidoImportar, raizProjeto, raizNavegacao string) ([]string, error) {
	if len(p.Projetos) == 0 {
		return nil, fmt.Errorf("nenhum projeto marcado para adicionar")
	}
	novo := clonarConfig(cfg)
	grupo := strings.TrimSpace(p.Grupo)
	if nome := strings.TrimSpace(p.GrupoNovo); nome != "" {
		id, err := salvarGrupo(novo, "", nome)
		if err != nil {
			return nil, err
		}
		grupo = id
	}
	var perfil *Perfil
	if p.Perfil != "" {
		if perfil = novo.perfilPorID(p.Perfil); perfil == nil {
			return nil, fmt.Errorf("perfil desconhecido: %s", p.Perfil)
		}
	}

	ids, apps, containers := []string{}, []string{}, []string{}
	for _, e := range p.Projetos {
		e.ID = ""
		e.Perfil = ""
		e.Grupo = grupo
		e.CaminhoLivre = e.CaminhoLivre || p.CaminhoLivre
		// No perfil ativo, o que acabou de entrar ja aparece marcado como o resto do perfil.
		e.Selecionado = perfil != nil
		id, err := salvarServico(novo, e, raizProjeto, raizNavegacao)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Nome, err)
		}
		ids = append(ids, id)
		if e.Tipo == TipoDocker {
			containers = append(containers, id)
		} else {
			apps = append(apps, id)
		}
	}
	// Os apps da mesma pasta costumam precisar do banco/fila do compose dela no ar antes.
	if p.EsperarContainers {
		for _, id := range apps {
			s := novo.porID(id)
			s.Depende = limparLista(append(s.Depende, containers...))
		}
	}
	if perfil != nil {
		perfil = novo.perfilPorID(p.Perfil)
		perfil.Servicos = limparLista(append(perfil.Servicos, ids...))
	}
	if err := novo.validar(); err != nil {
		return nil, err
	}
	*cfg = *novo
	return ids, nil
}

// MarcarCadastrados aponta, em cada descoberta, o projeto do config que ja usa aquela pasta
// (app) ou aquele servico do compose (docker) - para a tela nao sugerir cadastrar de novo.
func marcarCadastrados(res *ResultadoDescoberta, cfg *Config, raiz string) {
	for i := range res.Projetos {
		d := &res.Projetos[i]
		for _, s := range cfg.Servicos {
			switch {
			case d.Tipo == TipoApp && s.Tipo == TipoApp && mesmoCaminho(caminhoAbsoluto(raiz, s.Dir), d.Dir):
				d.Cadastrado = s.ID
			case d.Tipo == TipoDocker && s.Tipo == TipoDocker && s.Compose != nil && d.Compose != nil &&
				s.Compose.Servico == d.Compose.Servico && mesmoCaminho(caminhoAbsoluto(raiz, s.Compose.Arquivo), d.Compose.Arquivo):
				d.Cadastrado = s.ID
			}
			if d.Cadastrado != "" {
				break
			}
		}
	}
}

func mesmoCaminho(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// ------------------------------------------------------------------
// gerente e rotas
// ------------------------------------------------------------------

func (g *Gerente) DescobrirProjetos(caminho string) (*ResultadoDescoberta, error) {
	res, err := descobrirProjetos(caminho)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	marcarCadastrados(res, g.cfg, g.raiz)
	g.mu.Unlock()
	return res, nil
}

func (g *Gerente) ImportarProjetos(p PedidoImportar) ([]string, error) {
	var ids []string
	err := g.mutar(func(cfg *Config) error {
		var err error
		ids, err = importarProjetos(cfg, p, g.raiz, g.raizNavegacao)
		return err
	})
	return ids, err
}

func rotasDescoberta(mux *http.ServeMux, g *Gerente) {
	mux.HandleFunc("GET /api/descobrir", func(w http.ResponseWriter, r *http.Request) {
		caminho, _, err := caminhoPedido(g, r)
		if err != nil {
			responderErro(w, http.StatusForbidden, err)
			return
		}
		res, err := g.DescobrirProjetos(caminho)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("POST /api/importar", func(w http.ResponseWriter, r *http.Request) {
		var p PedidoImportar
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		ids, err := g.ImportarProjetos(p)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"ids": ids, "config": g.Config()})
	})
}
