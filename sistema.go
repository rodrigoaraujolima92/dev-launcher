package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// portaEscutando e a checagem mais barata de "ja subiu": abre um TCP em 127.0.0.1.
// Nao usa localhost de proposito - em maquina com IPv6 o localhost pode resolver para ::1
// primeiro e dar timeout em servico que so escuta em IPv4.
func portaEscutando(porta int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(porta), 700*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func httpRespondendo(ctx context.Context, url string) bool {
	ctx, cancelar := context.WithTimeout(ctx, 3*time.Second)
	defer cancelar()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Qualquer resposta HTTP serve: 401/404 ja provam que tem alguem atendendo.
	return resp.StatusCode < 500
}

func rodarComando(ctx context.Context, prazo time.Duration, nome string, args ...string) (string, error) {
	ctx, cancelar := context.WithTimeout(ctx, prazo)
	defer cancelar()
	cmd := exec.CommandContext(ctx, nome, args...)
	var saida bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &saida
	err := cmd.Run()
	return strings.TrimSpace(saida.String()), err
}

func comandoExiste(nome string) bool {
	_, err := exec.LookPath(nome)
	return err == nil
}

func dockerDisponivel(ctx context.Context) error {
	if !comandoExiste("docker") {
		return fmt.Errorf("docker nao encontrado no PATH")
	}
	if _, err := rodarComando(ctx, 20*time.Second, "docker", "info", "--format", "{{.ServerVersion}}"); err != nil {
		return fmt.Errorf("docker nao esta respondendo (abra o Docker Desktop)")
	}
	return nil
}

func containersRodando(ctx context.Context) (map[string]bool, error) {
	saida, err := rodarComando(ctx, 20*time.Second, "docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	nomes := map[string]bool{}
	for _, linha := range strings.Split(saida, "\n") {
		if l := strings.TrimSpace(linha); l != "" {
			nomes[l] = true
		}
	}
	return nomes, nil
}

func garantirRede(ctx context.Context, rede string) error {
	if rede == "" {
		return nil
	}
	saida, err := rodarComando(ctx, 20*time.Second, "docker", "network", "ls", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	for _, linha := range strings.Split(saida, "\n") {
		if strings.TrimSpace(linha) == rede {
			return nil
		}
	}
	// Os compose de db/adminer/rabbit declaram a rede como external: sem ela o "up" falha
	// antes de criar qualquer container.
	_, err = rodarComando(ctx, 30*time.Second, "docker", "network", "create", rede)
	return err
}

func pidsNaPorta(ctx context.Context, porta int) []int {
	saida, err := rodarComando(ctx, 15*time.Second, "netstat", "-ano", "-p", "TCP")
	if err != nil && saida == "" {
		return nil
	}
	return extrairPidsEscutando(saida, porta)
}

// extrairPidsEscutando le a saida do netstat -ano e devolve os PIDs que escutam na porta.
// Separado do exec para poder ser testado com saida fixa.
func extrairPidsEscutando(saida string, porta int) []int {
	sufixo := ":" + strconv.Itoa(porta)
	vistos := map[int]bool{}
	out := []int{}
	for _, linha := range strings.Split(saida, "\n") {
		campos := strings.Fields(linha)
		if len(campos) < 4 {
			continue
		}
		if !strings.EqualFold(campos[0], "TCP") {
			continue
		}
		if !strings.HasSuffix(campos[1], sufixo) {
			continue
		}
		if !strings.EqualFold(campos[3], "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(campos[len(campos)-1])
		if err != nil || pid <= 0 || vistos[pid] {
			continue
		}
		vistos[pid] = true
		out = append(out, pid)
	}
	return out
}

// matarArvore derruba o processo e os filhos. Precisa ser a arvore inteira: "npm run serve"
// e um pwsh que vira node que vira outro node - matar so o pai deixa o dev server segurando
// a porta e a proxima subida acha que ja esta tudo no ar.
func matarArvore(ctx context.Context, pid int) error {
	_, err := rodarComando(ctx, 20*time.Second, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	return err
}

// limparANSI tira as sequencias de escape do log (cores do vue-cli, spinners do maven)
// para o texto chegar legivel no navegador.
func limparANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && (s[j] == '[' || s[j] == ']') {
				fim := s[j]
				j++
				for j < len(s) {
					c := s[j]
					if fim == '[' && c >= '@' && c <= '~' {
						j++
						break
					}
					if fim == ']' && (c == 0x07 || c == 0x1b) {
						j++
						break
					}
					j++
				}
				i = j
				continue
			}
			i++
			continue
		}
		if s[i] == '\r' {
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
