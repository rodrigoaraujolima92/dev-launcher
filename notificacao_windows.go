//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// avisoDoSistema mostra uma notificacao do Windows (a que aparece no canto e fica na
// central de notificacoes).
func avisoDoSistema(a AvisoSistema) error {
	ctx, cancelar := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelar()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-EncodedCommand", comandoCodificado(scriptToast))
	cmd.Env = append(os.Environ(), "DL_TOAST_XML="+montarToast(a))
	// Sem janela: com o launcher aberto por atalho (sem console), cada aviso piscaria um
	// terminal preto na tela.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	saida, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(saida))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", primeiraLinha(msg))
	}
	return nil
}
