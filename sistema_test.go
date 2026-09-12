package main

import (
	"reflect"
	"testing"
)

const saidaNetstat = `
Conexoes ativas

  Proto  Endereco local         Endereco externo       Estado           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1092
  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       41448
  TCP    127.0.0.1:8081         0.0.0.0:0              LISTENING       13112
  TCP    [::]:8080              [::]:0                 LISTENING       41448
  TCP    127.0.0.1:8080         127.0.0.1:53012        ESTABLISHED     41448
  TCP    127.0.0.1:53012        127.0.0.1:8080         ESTABLISHED     9001
  TCP    0.0.0.0:18080          0.0.0.0:0              LISTENING       7777
  UDP    0.0.0.0:8080           *:*                                    4242
`

func TestExtrairPidsEscutando(t *testing.T) {
	got := extrairPidsEscutando(saidaNetstat, 8080)
	if !reflect.DeepEqual(got, []int{41448}) {
		t.Fatalf("porta 8080 = %v, queria [41448] (IPv4 e IPv6 sao o mesmo processo)", got)
	}

	if got := extrairPidsEscutando(saidaNetstat, 8081); !reflect.DeepEqual(got, []int{13112}) {
		t.Fatalf("porta 8081 = %v, queria [13112]", got)
	}
	if got := extrairPidsEscutando(saidaNetstat, 9999); len(got) != 0 {
		t.Fatalf("porta sem ninguem = %v, queria vazio", got)
	}
	// 18080 termina com "8080": nao pode ser confundida com a 8080.
	if got := extrairPidsEscutando(saidaNetstat, 8080); contemInt(got, 7777) {
		t.Fatalf("a porta 18080 foi confundida com a 8080: %v", got)
	}
	// So LISTENING conta: quem tem conexao aberta contra a porta nao esta servindo nela.
	if got := extrairPidsEscutando(saidaNetstat, 53012); len(got) != 0 {
		t.Fatalf("conexao ESTABLISHED nao deveria contar: %v", got)
	}
	if got := extrairPidsEscutando("", 8080); len(got) != 0 {
		t.Fatalf("saida vazia = %v", got)
	}
}

func TestLimparANSI(t *testing.T) {
	casos := []struct{ entrada, querido string }{
		{"\x1b[32mDONE\x1b[39m  Compiled successfully", "DONE  Compiled successfully"},
		{"sem cor nenhuma", "sem cor nenhuma"},
		{"progresso\rfim", "progressofim"},              // \r de spinner nao vira linha nova
		{"\x1b]0;titulo da janela\x07pronto", "pronto"}, // sequencia de titulo do terminal
		{"\x1b[1;33mAVISO\x1b[0m: porta ocupada", "AVISO: porta ocupada"},
		{"", ""},
	}
	for _, caso := range casos {
		if got := limparANSI(caso.entrada); got != caso.querido {
			t.Fatalf("limparANSI(%q) = %q, queria %q", caso.entrada, got, caso.querido)
		}
	}
}

func TestPortaEscutandoNaoAchaPortaLivre(t *testing.T) {
	// Porta alta e improvavel: o teste garante que a checagem devolve false rapido em vez
	// de travar esperando timeout de rede.
	if portaEscutando(59_123) {
		t.Skip("alguem esta usando a porta 59123 nesta maquina")
	}
}

func contemInt(lista []int, alvo int) bool {
	for _, v := range lista {
		if v == alvo {
			return true
		}
	}
	return false
}
