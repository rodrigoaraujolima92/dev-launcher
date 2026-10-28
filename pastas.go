package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ItemPasta e uma subpasta no seletor. As marcas ("go", "node", "maven", "compose", "git")
// viram etiquetas na tela para voce reconhecer o projeto sem abrir.
type ItemPasta struct {
	Nome    string   `json:"nome"`
	Caminho string   `json:"caminho"`
	Marcas  []string `json:"marcas"`
}

type Listagem struct {
	Caminho string      `json:"caminho"`
	Pai     string      `json:"pai,omitempty"`
	Raiz    string      `json:"raiz"`
	Itens   []ItemPasta `json:"itens"`
}

// pastasIgnoradas nao ajudam a achar projeto e so poluem a lista.
var pastasIgnoradas = map[string]bool{
	"node_modules": true,
	"target":       true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"bin":          true,
	"obj":          true,
	"__pycache__":  true,
}

func listarPastas(caminho string) (*Listagem, error) {
	caminho = filepath.Clean(caminho)
	info, err := os.Stat(caminho)
	if err != nil {
		return nil, fmt.Errorf("pasta nao encontrada: %s", caminho)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("nao e uma pasta: %s", caminho)
	}

	entradas, err := os.ReadDir(caminho)
	if err != nil {
		return nil, err
	}
	itens := []ItemPasta{}
	for _, e := range entradas {
		if !e.IsDir() {
			continue
		}
		nome := e.Name()
		if strings.HasPrefix(nome, ".") || pastasIgnoradas[strings.ToLower(nome)] {
			continue
		}
		completo := filepath.Join(caminho, nome)
		itens = append(itens, ItemPasta{Nome: nome, Caminho: completo, Marcas: marcasDaPasta(completo)})
	}
	sort.Slice(itens, func(i, j int) bool {
		return strings.ToLower(itens[i].Nome) < strings.ToLower(itens[j].Nome)
	})

	listagem := &Listagem{Caminho: caminho, Itens: itens}
	if pai := filepath.Dir(caminho); pai != caminho {
		listagem.Pai = pai
	}
	return listagem, nil
}

func marcasDaPasta(dir string) []string {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	marcas := []string{}
	visto := map[string]bool{}
	anotar := func(m string) {
		if !visto[m] {
			visto[m] = true
			marcas = append(marcas, m)
		}
	}
	for _, e := range entradas {
		nome := strings.ToLower(e.Name())
		switch {
		case nome == ".git":
			anotar("git")
		case nome == "package.json":
			anotar("node")
		case nome == "go.mod":
			anotar("go")
		case nome == "pom.xml":
			anotar("maven")
		case strings.HasPrefix(nome, "docker-compose") && strings.HasSuffix(nome, ".yml"):
			anotar("compose")
		case strings.HasSuffix(nome, ".ps1"):
			anotar("script")
		}
	}
	return marcas
}

// ------------------------------------------------------------------
// sugestoes de comando
// ------------------------------------------------------------------

type Sugestao struct {
	Rotulo string `json:"rotulo"`
	Cmd    string `json:"cmd"`
	Porta  int    `json:"porta,omitempty"`
	// preferido marca o script que tem cara de "sobe o projeto" (run-local.ps1, start.bat).
	preferido bool
}

type ArquivoCompose struct {
	Arquivo  string   `json:"arquivo"`
	Servicos []string `json:"servicos"`
}

type Inspecao struct {
	Caminho   string           `json:"caminho"`
	Sugestoes []Sugestao       `json:"sugestoes"`
	Composes  []ArquivoCompose `json:"composes"`
}

// inspecionar olha a pasta e propoe como rodar aquilo: scripts do package.json, go run,
// maven, .ps1 solto na raiz e os servicos de cada docker-compose encontrado.
func inspecionar(dir string) (*Inspecao, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("pasta nao encontrada: %s", dir)
	}

	insp := &Inspecao{Caminho: dir, Sugestoes: []Sugestao{}, Composes: []ArquivoCompose{}}

	// Mesmo detector da importacao por pasta, sem o filtro de "tem certeza que roda": aqui a
	// pessoa ja escolheu a pasta, entao qualquer palpite ajuda.
	insp.Sugestoes = append(insp.Sugestoes, detectarApp(dir, nomesDaPasta(entradas)).Sugestoes...)
	if c := nomesDaPasta(entradas); c.tem("go.mod") && len(sugestoesGo(dir, c, 0)) == 0 {
		insp.Sugestoes = append(insp.Sugestoes, Sugestao{Rotulo: "go run .", Cmd: "go run ."})
	}

	for _, e := range entradas {
		nome := e.Name()
		minusculo := strings.ToLower(nome)
		if e.IsDir() || !strings.HasSuffix(minusculo, ".yml") && !strings.HasSuffix(minusculo, ".yaml") {
			continue
		}
		if !strings.Contains(minusculo, "compose") {
			continue
		}
		dados, err := os.ReadFile(filepath.Join(dir, nome))
		if err != nil {
			continue
		}
		insp.Composes = append(insp.Composes, ArquivoCompose{
			Arquivo:  nome,
			Servicos: servicosDoCompose(string(dados)),
		})
	}
	return insp, nil
}

var reScript = regexp.MustCompile(`--port[= ](\d{2,5})`)

// sugestoesDoPackageJSON pega os scripts que costumam subir a aplicacao e tenta adivinhar
// a porta a partir de um --port no proprio script.
func sugestoesDoPackageJSON(dados []byte) []Sugestao {
	var teste map[string]any
	if json.Unmarshal(dados, &teste) != nil {
		return nil
	}
	_, out := sugestoesNode("", dados, "npm", 0)
	return out
}

// servicosDoCompose devolve os nomes sob "services:" sem depender de biblioteca de YAML:
// le a indentacao das chaves do primeiro nivel dentro do bloco. Da conta dos compose
// simples que usamos aqui; formato exotico so nao gera sugestao, nada quebra.
func servicosDoCompose(conteudo string) []string {
	linhas := strings.Split(conteudo, "\n")
	dentro := false
	indentServico := -1
	out := []string{}

	for _, bruta := range linhas {
		linha := strings.TrimRight(bruta, "\r")
		semEspaco := strings.TrimSpace(linha)
		if semEspaco == "" || strings.HasPrefix(semEspaco, "#") {
			continue
		}
		indent := len(linha) - len(strings.TrimLeft(linha, " \t"))

		if !dentro {
			if indent == 0 && strings.HasPrefix(semEspaco, "services:") {
				dentro = true
			}
			continue
		}
		if indent == 0 { // comecou outro bloco de primeiro nivel (networks:, volumes:)
			break
		}
		if indentServico == -1 {
			indentServico = indent
		}
		if indent != indentServico {
			continue // propriedade do servico, nao um servico novo
		}
		nome := strings.TrimSpace(strings.SplitN(semEspaco, ":", 2)[0])
		if nome != "" && !strings.HasPrefix(nome, "-") {
			out = append(out, nome)
		}
	}
	return out
}
