"use strict";

const app = {
  config: null,
  estados: {},
  temWt: false,
  logAtual: null,
  subindo: false,
};

const $ = (sel, base = document) => base.querySelector(sel);
const $$ = (sel, base = document) => [...base.querySelectorAll(sel)];

const NOMES_GRUPO = { infra: "infraestrutura", api: "apis", web: "front-ends" };
const ROTULO_STATUS = {
  parado: "parado",
  esperando: "na fila",
  subindo: "subindo",
  pronto: "no ar",
  erro: "erro",
  bloqueado: "bloqueado",
};

// ------------------------------------------------------------------
// carga inicial
// ------------------------------------------------------------------

async function iniciar() {
  try {
    const dados = await pedir("/api/estado");
    app.config = dados.config;
    app.temWt = dados.terminal;
    $("#subtitulo").textContent = dados.raiz;
    for (const e of dados.estados) app.estados[e.id] = e;
  } catch (err) {
    avisar("nao consegui falar com o launcher: " + err.message, "erro");
    return;
  }
  desenharServicos();
  aplicarEstados();
  atualizarPlano();
  conectarEventos();
  ligarBotoesTopo();
}

async function pedir(url, opcoes) {
  const resp = await fetch(url, opcoes);
  const texto = await resp.text();
  let corpo = {};
  if (texto) {
    try { corpo = JSON.parse(texto); } catch { corpo = { erro: texto }; }
  }
  if (!resp.ok) throw new Error(corpo.erro || `HTTP ${resp.status}`);
  return corpo;
}

const enviarJSON = (url, corpo) =>
  pedir(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(corpo),
  });

// ------------------------------------------------------------------
// desenho dos servicos
// ------------------------------------------------------------------

function desenharServicos() {
  const alvo = $("#servicos");
  const modelo = $("#modelo-servico");
  alvo.innerHTML = "";

  let grupoAtual = null;
  app.config.servicos.forEach((s, i) => {
    if (s.grupo !== grupoAtual) {
      grupoAtual = s.grupo;
      const titulo = document.createElement("div");
      titulo.className = "grupo-titulo";
      titulo.textContent = NOMES_GRUPO[s.grupo] || s.grupo || "servicos";
      alvo.appendChild(titulo);
    }

    const no = modelo.content.firstElementChild.cloneNode(true);
    no.dataset.id = s.id;
    no.style.animationDelay = `${Math.min(i, 12) * 28}ms`;

    $(".nome", no).textContent = s.nome;
    $(".porta", no).textContent = s.porta ? `:${s.porta}` : s.tipo;
    const caixa = $(".chk-selecao", no);
    caixa.checked = !!s.selecionado;
    caixa.setAttribute("aria-label", `incluir ${s.nome} na subida`);
    no.classList.toggle("selecionado", !!s.selecionado);
    $(".detalhe", no).textContent = s.tipo === "docker"
      ? `docker compose · ${s.compose.servico} (${s.container || "-"})`
      : `${s.dir} · ${s.cmd}`;

    const link = $(".link-url", no);
    if (s.url) {
      link.href = s.url;
      link.textContent = "abrir";
      link.hidden = false;
    }

    const modo = $(".modo", no);
    if (s.tipo !== "app") {
      modo.classList.add("oculto");
    } else {
      $$(".modo-opcao", modo).forEach((b) => {
        const ativo = (s.modo || "gerenciado") === b.dataset.modo;
        b.setAttribute("aria-pressed", ativo ? "true" : "false");
        if (b.dataset.modo === "terminal" && !app.temWt) {
          b.title = "Windows Terminal nao encontrado - cai para modo gerenciado";
        }
      });
    }

    desenharDependencias(no, s);
    alvo.appendChild(no);
  });

  alvo.onclick = aoClicar;
  alvo.onchange = aoMudar;
}

function desenharDependencias(no, s) {
  const chips = $(".chips", no);
  chips.innerHTML = "";
  if (!s.depende.length) {
    const vazio = document.createElement("span");
    vazio.className = "chip vazio";
    vazio.textContent = "ninguem";
    chips.appendChild(vazio);
  } else {
    for (const d of s.depende) {
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.textContent = nomeDe(d);
      chips.appendChild(chip);
    }
  }

  const opcoes = $(".opcoes-dep", no);
  opcoes.innerHTML = "";
  for (const outro of app.config.servicos) {
    if (outro.id === s.id) continue;
    const label = document.createElement("label");
    const caixa = document.createElement("input");
    caixa.type = "checkbox";
    caixa.className = "chk-dep";
    caixa.dataset.dep = outro.id;
    caixa.checked = s.depende.includes(outro.id);
    caixa.setAttribute("aria-label", `${s.nome} espera ${outro.nome}`);
    label.append(caixa, document.createTextNode(outro.nome));
    opcoes.appendChild(label);
  }
}

const nomeDe = (id) => app.config.servicos.find((s) => s.id === id)?.nome || id;
const servicoDe = (id) => app.config.servicos.find((s) => s.id === id);
const selecionados = () => app.config.servicos.filter((s) => s.selecionado).map((s) => s.id);

// ------------------------------------------------------------------
// interacoes
// ------------------------------------------------------------------

function aoMudar(ev) {
  const cartao = ev.target.closest(".cartao");
  if (!cartao) return;
  const s = servicoDe(cartao.dataset.id);

  if (ev.target.classList.contains("chk-selecao")) {
    s.selecionado = ev.target.checked;
    cartao.classList.toggle("selecionado", s.selecionado);
    salvar();
    atualizarPlano();
    return;
  }

  if (ev.target.classList.contains("chk-dep")) {
    const marcados = $$(".chk-dep", cartao).filter((c) => c.checked).map((c) => c.dataset.dep);
    const anterior = s.depende;
    s.depende = marcados;
    desenharDependencias(cartao, s);
    atualizarPlano();
    // Ciclo (A espera B, B espera A) e recusado pelo servidor: volta ao que era e avisa.
    salvar().catch(() => {
      s.depende = anterior;
      desenharDependencias(cartao, s);
      atualizarPlano();
    });
  }
}

function aoClicar(ev) {
  const cartao = ev.target.closest(".cartao");
  if (!cartao) return;
  const id = cartao.dataset.id;
  const s = servicoDe(id);

  const opcaoModo = ev.target.closest(".modo-opcao");
  if (opcaoModo) {
    s.modo = opcaoModo.dataset.modo;
    $$(".modo-opcao", cartao).forEach((b) =>
      b.setAttribute("aria-pressed", b.dataset.modo === s.modo ? "true" : "false"));
    salvar();
    return;
  }

  if (ev.target.classList.contains("acao-subir")) {
    subir([id]);
    return;
  }
  if (ev.target.classList.contains("acao-parar")) {
    parar([id]);
    return;
  }
  if (ev.target.classList.contains("acao-log")) {
    mostrarLog(id);
  }
}

function ligarBotoesTopo() {
  $("#btn-subir").onclick = () => subir(selecionados());
  $("#btn-parar").onclick = () => parar(selecionados());

  // O <details> das dependencias nao fecha sozinho: sem isto fica um menu aberto por cima
  // dos cartoes depois de editar.
  document.addEventListener("click", (ev) => {
    for (const editor of $$(".editor-dep[open]")) {
      if (!editor.contains(ev.target)) editor.open = false;
    }
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") $$(".editor-dep[open]").forEach((e) => (e.open = false));
  });
}

let salvamento = null;
function salvar() {
  clearTimeout(salvamento);
  return new Promise((ok, falhou) => {
    salvamento = setTimeout(async () => {
      const servicos = app.config.servicos.map((s) => ({
        id: s.id,
        selecionado: !!s.selecionado,
        modo: s.modo || "",
        depende: s.depende,
      }));
      try {
        await enviarJSON("/api/config", { servicos });
        ok();
      } catch (err) {
        avisar(err.message, "erro");
        falhou(err);
      }
    }, 350);
  });
}

async function subir(ids) {
  if (!ids.length) return avisar("nenhum servico selecionado.", "erro");
  try {
    const plano = await enviarJSON("/api/subir", { ids });
    const extras = plano.ids.filter((x) => !ids.includes(x));
    const verbo = extras.length === 1 ? "entrou" : "entraram";
    avisar(extras.length
      ? `subindo ${plano.ids.length} servicos (${extras.map(nomeDe).join(", ")} ${verbo} como dependencia).`
      : `subindo ${plano.ids.length} servico(s).`, "ok");
    desenharPlano(plano);
  } catch (err) {
    avisar(err.message, "erro");
  }
}

async function parar(ids) {
  if (!ids.length) return avisar("nenhum servico selecionado.", "erro");
  try {
    const r = await enviarJSON("/api/parar", { ids });
    if (r.erros?.length) r.erros.forEach((e) => avisar(e, "erro"));
    else avisar(`parado: ${ids.map(nomeDe).join(", ")}.`, "ok");
  } catch (err) {
    avisar(err.message, "erro");
  }
}

// ------------------------------------------------------------------
// plano de subida
// ------------------------------------------------------------------

async function atualizarPlano() {
  const ids = selecionados();
  if (!ids.length) {
    $("#plano").innerHTML = '<p class="plano-vazio">nada marcado.</p>';
    $("#contador-subir").textContent = "0";
    return;
  }
  try {
    desenharPlano(await enviarJSON("/api/plano", { ids }));
  } catch (err) {
    $("#plano").innerHTML = `<p class="plano-vazio">${err.message}</p>`;
  }
}

function desenharPlano(plano) {
  $("#contador-subir").textContent = String(plano.ids.length);
  const alvo = $("#plano");
  alvo.innerHTML = "";
  plano.ondas.forEach((onda, i) => {
    const bloco = document.createElement("div");
    bloco.className = "onda";
    const num = document.createElement("div");
    num.className = "onda-num";
    num.textContent = i === 0 ? "1a" : `${i + 1}a`;
    const itens = document.createElement("div");
    itens.className = "onda-itens";
    for (const id of onda) {
      const item = document.createElement("span");
      item.className = "onda-item";
      item.dataset.id = id;
      item.dataset.status = app.estados[id]?.status || "parado";
      item.textContent = nomeDe(id);
      itens.appendChild(item);
    }
    bloco.append(num, itens);
    alvo.appendChild(bloco);
  });
}

// ------------------------------------------------------------------
// estado ao vivo
// ------------------------------------------------------------------

function conectarEventos() {
  const fonte = new EventSource("/api/eventos");
  fonte.onmessage = (ev) => {
    const evento = JSON.parse(ev.data);
    if (evento.tipo === "estado") {
      for (const e of evento.estados) app.estados[e.id] = e;
      aplicarEstados();
    } else if (evento.tipo === "log") {
      if (evento.id === app.logAtual) acrescentarLog(evento.linha);
    } else if (evento.tipo === "config") {
      app.config = evento.config;
      desenharServicos();
      aplicarEstados();
      atualizarPlano();
    } else if (evento.tipo === "fim") {
      app.subindo = false;
      atualizarBotaoSubir();
      avisar("subida encerrada.", "ok");
      // Durante a subida o painel mostra o plano daquela rodada; terminou, volta a mostrar
      // o plano do que esta marcado na tela.
      atualizarPlano();
    }
  };
  fonte.onerror = () => {
    // EventSource reconecta sozinho; so registra para o usuario nao achar que travou.
    $("#log-alvo").dataset.conexao = "reconectando";
  };
}

function aplicarEstados() {
  const contagem = { pronto: 0, andando: 0, falha: 0 };

  for (const s of app.config.servicos) {
    const e = app.estados[s.id] || { status: "parado", mensagem: "" };
    if (e.status === "pronto") contagem.pronto++;
    else if (e.status === "subindo" || e.status === "esperando") contagem.andando++;
    else if (e.status === "erro" || e.status === "bloqueado") contagem.falha++;

    const cartao = $(`.cartao[data-id="${s.id}"]`);
    if (!cartao) continue;
    cartao.dataset.status = e.status;
    $(".led", cartao).dataset.status = e.status;
    const texto = $(".estado-texto", cartao);
    texto.className = `estado-texto ${e.status}`;
    texto.textContent = e.mensagem ? `${ROTULO_STATUS[e.status]} · ${e.mensagem}` : ROTULO_STATUS[e.status];
  }

  for (const item of $$(".onda-item")) {
    item.dataset.status = app.estados[item.dataset.id]?.status || "parado";
  }

  app.subindo = contagem.andando > 0;
  atualizarBotaoSubir();

  $("#medidores").innerHTML = `
    <div class="medidor pronto"><b>${contagem.pronto}</b><span>no ar</span></div>
    <div class="medidor andando"><b>${contagem.andando}</b><span>subindo</span></div>
    <div class="medidor falha"><b>${contagem.falha}</b><span>falha</span></div>`;
}

function atualizarBotaoSubir() {
  $("#btn-subir").classList.toggle("ocupado", app.subindo);
}

// ------------------------------------------------------------------
// log
// ------------------------------------------------------------------

async function mostrarLog(id) {
  app.logAtual = id;
  $("#log-alvo").textContent = nomeDe(id);
  $$(".cartao").forEach((c) => c.classList.toggle("em-foco", c.dataset.id === id));
  const alvo = $("#log");
  alvo.textContent = "";
  try {
    const r = await pedir(`/api/logs?id=${encodeURIComponent(id)}`);
    (r.linhas || []).forEach(acrescentarLog);
  } catch (err) {
    acrescentarLog("nao consegui ler o log: " + err.message);
  }
}

function acrescentarLog(linha) {
  const alvo = $("#log");
  // So rola sozinho se o usuario ja estava no fim - senao atrapalha quem esta lendo o meio.
  const noFim = alvo.scrollHeight - alvo.scrollTop - alvo.clientHeight < 40;
  const no = document.createElement("span");
  if (linha.startsWith("> ")) no.className = "eco";
  no.textContent = linha + "\n";
  alvo.appendChild(no);
  if (noFim) alvo.scrollTop = alvo.scrollHeight;
}

// ------------------------------------------------------------------
// avisos
// ------------------------------------------------------------------

function avisar(texto, tipo = "") {
  const no = document.createElement("div");
  no.className = `aviso ${tipo}`;
  no.textContent = texto;
  $("#avisos").appendChild(no);
  setTimeout(() => no.remove(), 6000);
}

iniciar();
