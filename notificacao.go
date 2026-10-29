package main

import (
	"encoding/base64"
	"encoding/xml"
	"strings"
	"unicode/utf16"
)

// montarToast monta o XML da notificacao do Windows. Todo texto passa pelo escape do XML:
// titulo de tarefa e digitado pela pessoa e pode ter "<" e "&".
func montarToast(a AvisoSistema) string {
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<toast`)
	if a.Abrir != "" {
		b.WriteString(` activationType="protocol" launch="` + esc(a.Abrir) + `"`)
	}
	if a.Fixo {
		// "reminder" fica na tela ate ser dispensado - mas o Windows so respeita o cenario
		// quando o aviso tem botao, por isso as acoes abaixo.
		b.WriteString(` scenario="reminder"`)
	}
	b.WriteString(`><visual><binding template="ToastGeneric">`)
	b.WriteString(`<text>` + esc(a.Titulo) + `</text>`)
	if a.Texto != "" {
		b.WriteString(`<text>` + esc(a.Texto) + `</text>`)
	}
	b.WriteString(`<text placement="attribution">dev launcher</text>`)
	b.WriteString(`</binding></visual>`)
	if a.Fixo {
		b.WriteString(`<actions>`)
		if a.Abrir != "" {
			b.WriteString(`<action content="abrir" activationType="protocol" arguments="` + esc(a.Abrir) + `"/>`)
		}
		b.WriteString(`<action content="dispensar" activationType="system" arguments="dismiss"/>`)
		b.WriteString(`</actions>`)
		b.WriteString(`<audio src="ms-winsoundevent:Notification.Reminder"/>`)
	} else {
		b.WriteString(`<audio src="ms-winsoundevent:Notification.Default"/>`)
	}
	b.WriteString(`</toast>`)
	return b.String()
}

// scriptToast mostra o XML que chega pela variavel DL_TOAST_XML. O texto nunca entra no
// script: assim titulo nenhum consegue virar comando do PowerShell.
//
// Precisa ser o Windows PowerShell (5.1): o pwsh 7 nao carrega mais as classes WinRT. O
// AppID e o do proprio PowerShell, que ja vem registrado no Windows - um AppID inventado
// faz o Windows engolir a notificacao sem erro.
const scriptToast = `$ErrorActionPreference = 'Stop'
[void][Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
[void][Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($env:DL_TOAST_XML)
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show($toast)
`

// comandoCodificado e o formato do -EncodedCommand: base64 de UTF-16LE.
func comandoCodificado(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
