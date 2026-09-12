package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var conteudoWeb embed.FS

func main() {
	var (
		caminhoCfg  = flag.String("config", "", "caminho do config.json (padrao: ao lado do binario ou ./dev-launcher/config.json)")
		porta       = flag.Int("porta", 0, "porta da interface web (sobrepoe a do config)")
		semNavegado = flag.Bool("sem-navegador", false, "nao abrir o navegador automaticamente")
	)
	flag.Parse()

	cfgPath, err := acharConfig(*caminhoCfg)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg, err := carregarConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// A raiz dos projetos e a pasta acima do config (dev-launcher/config.json -> appdaturma).
	raiz := filepath.Dir(filepath.Dir(cfgPath))

	if *porta > 0 {
		cfg.PortaUI = *porta
	}
	if cfg.PortaUI == 0 {
		cfg.PortaUI = 7010
	}

	exe := NovoExecutorSO(raiz, cfg.RedeDocker)
	g := NovoGerente(raiz, cfgPath, cfg, exe)
	exe.aoLogar = g.RegistrarLog
	exe.aoTerPID = func(id string, pid int) { g.definirPID(id, pid) }
	exe.aoEncerra = func(id, mensagem string) {
		g.RegistrarLog(id, mensagem)
		g.definirPID(id, 0)
		// Nao marca erro direto: um "npm run serve" que morre com a porta ainda em uso
		// (outro processo) confundiria a tela. A varredura ajusta o status em ate 4s.
	}

	ctx, cancelar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelar()

	go g.Vigiar(ctx, 4*time.Second)

	endereco := "127.0.0.1:" + strconv.Itoa(cfg.PortaUI)
	servidor := &http.Server{
		Addr:              endereco,
		Handler:           somenteLocal(rotas(g, raiz)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ouvinte, err := net.Listen("tcp", endereco)
	if err != nil {
		log.Fatalf("nao consegui abrir %s: %v", endereco, err)
	}

	url := "http://" + endereco
	fmt.Println()
	fmt.Println("  dev-launcher do appdaturma")
	fmt.Println("  raiz:    ", raiz)
	fmt.Println("  config:  ", cfgPath)
	fmt.Println("  interface:", url)
	fmt.Println()
	fmt.Println("  Ctrl+C encerra o launcher. Servicos em modo gerenciado morrem junto.")
	fmt.Println()

	if !*semNavegado {
		abrirNavegador(url)
	}

	go func() {
		<-ctx.Done()
		ctxParada, cancelarParada := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelarParada()
		_ = servidor.Shutdown(ctxParada)
	}()

	if err := servidor.Serve(ouvinte); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func acharConfig(informado string) (string, error) {
	if informado != "" {
		return filepath.Abs(informado)
	}
	candidatos := []string{}
	if exe, err := os.Executable(); err == nil {
		candidatos = append(candidatos, filepath.Join(filepath.Dir(exe), "config.json"))
	}
	candidatos = append(candidatos, "config.json", filepath.Join("dev-launcher", "config.json"))
	for _, c := range candidatos {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("config.json nao encontrado (use -config)")
}

// somenteLocal barra requisicao que nao venha de localhost. O servidor executa comandos da
// maquina: mesmo escutando so em 127.0.0.1, conferir o Host evita que uma pagina qualquer
// aberta no navegador use um dominio apontando para 127.0.0.1 para falar com o launcher.
func somenteLocal(prox http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch host {
		case "localhost", "127.0.0.1", "::1", "[::1]":
			prox.ServeHTTP(w, r)
		default:
			http.Error(w, "acesso permitido apenas de localhost", http.StatusForbidden)
		}
	})
}

func rotas(g *Gerente, raiz string) http.Handler {
	mux := http.NewServeMux()

	arquivos, err := fs.Sub(conteudoWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(arquivos)))

	mux.HandleFunc("GET /api/estado", func(w http.ResponseWriter, r *http.Request) {
		responderJSON(w, http.StatusOK, map[string]any{
			"raiz":     raiz,
			"config":   g.Config(),
			"estados":  g.Estados(),
			"terminal": comandoExiste("wt"),
		})
	})

	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		var corpo struct {
			Servicos []EdicaoServico `json:"servicos"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&corpo); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		if err := g.Editar(corpo.Servicos); err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"config": g.Config()})
	})

	mux.HandleFunc("POST /api/plano", func(w http.ResponseWriter, r *http.Request) {
		ids, err := lerIDs(w, r)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		responderJSON(w, http.StatusOK, g.Planejar(ids))
	})

	mux.HandleFunc("POST /api/subir", func(w http.ResponseWriter, r *http.Request) {
		ids, err := lerIDs(w, r)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		plano, err := g.Subir(context.Background(), ids)
		if err != nil {
			codigo := http.StatusBadRequest
			if errors.Is(err, ErrSubidaEmAndamento) {
				codigo = http.StatusConflict
			}
			responderErro(w, codigo, err)
			return
		}
		responderJSON(w, http.StatusOK, plano)
	})

	mux.HandleFunc("POST /api/parar", func(w http.ResponseWriter, r *http.Request) {
		ids, err := lerIDs(w, r)
		if err != nil {
			responderErro(w, http.StatusBadRequest, err)
			return
		}
		if erros := g.Parar(r.Context(), ids); len(erros) > 0 {
			msgs := make([]string, 0, len(erros))
			for _, e := range erros {
				msgs = append(msgs, e.Error())
			}
			responderJSON(w, http.StatusOK, map[string]any{"erros": msgs})
			return
		}
		responderJSON(w, http.StatusOK, map[string]any{"erros": []string{}})
	})

	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		responderJSON(w, http.StatusOK, map[string]any{"id": id, "linhas": g.Logs(id)})
	})

	mux.HandleFunc("GET /api/eventos", func(w http.ResponseWriter, r *http.Request) {
		fluxo, ok := w.(http.Flusher)
		if !ok {
			responderErro(w, http.StatusInternalServerError, errors.New("streaming indisponivel"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		fluxo.Flush()

		inscricao, ch := g.Inscrever()
		defer g.Desinscrever(inscricao)

		// Primeiro evento ja manda o retrato atual - a tela nao fica esperando algo mudar.
		enviarSSE(w, fluxo, mustJSON(map[string]any{"tipo": "estado", "estados": g.Estados()}))

		pulso := time.NewTicker(20 * time.Second)
		defer pulso.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case dados, aberto := <-ch:
				if !aberto {
					return
				}
				enviarSSE(w, fluxo, dados)
			case <-pulso.C:
				fmt.Fprint(w, ": ping\n\n") // segura a conexao viva atras de proxy/antivirus
				fluxo.Flush()
			}
		}
	})

	return mux
}

func enviarSSE(w http.ResponseWriter, fluxo http.Flusher, dados []byte) {
	fmt.Fprintf(w, "data: %s\n\n", dados)
	fluxo.Flush()
}

func mustJSON(v any) []byte {
	dados, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return dados
}

func lerIDs(w http.ResponseWriter, r *http.Request) ([]string, error) {
	var corpo struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&corpo); err != nil {
		return nil, err
	}
	limpos := []string{}
	for _, id := range corpo.IDs {
		if id = strings.TrimSpace(id); id != "" {
			limpos = append(limpos, id)
		}
	}
	if len(limpos) == 0 {
		return nil, errors.New("nenhum servico informado")
	}
	return limpos, nil
}

func responderJSON(w http.ResponseWriter, codigo int, corpo any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codigo)
	_ = json.NewEncoder(w).Encode(corpo)
}

func responderErro(w http.ResponseWriter, codigo int, err error) {
	responderJSON(w, codigo, map[string]string{"erro": err.Error()})
}

func abrirNavegador(url string) {
	// rundll32 evita a briga de aspas do "cmd /c start" com URLs.
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		log.Printf("nao consegui abrir o navegador: %v", err)
	}
}
