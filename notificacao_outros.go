//go:build !windows

package main

import "errors"

// O launcher e feito para Windows (taskkill, netstat, wt). Fora dele o lembrete continua
// aparecendo na tela; so a notificacao do sistema fica de fora.
func avisoDoSistema(AvisoSistema) error {
	return errors.New("notificacao do sistema so no Windows")
}
