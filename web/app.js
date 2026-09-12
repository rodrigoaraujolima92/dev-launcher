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
// el("div", {className, dataset, onclick...}, filhos...) - dataset entra com Object.assign
// porque element.dataset e somente leitura (atribuir direto estoura em modo estrito), e o
// que nao for propriedade do elemento (aria-*, title em option) vira atributo.
function el(tag, props = {}, ...filhos) {
  const no = document.createElement(tag);
  for (const [chave, valor] of Object.entries(props)) {
    if (valor == null) continue;
    if (chave === "dataset") Object.assign(no.dataset, valor);
    else if (chave in no) no[chave] = valor;
    else no.setAttribute(chave, valor);
  }
  for (const filho of filhos.flat()) if (filho != null) no.append(filho);
  return no;
}

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
  desenharTudo();
  conectarEventos();
  ligarBotoes();
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

const enviarJSON = (url, corpo, metodo = "POST") =>
  pedir(url, {
    method: metodo,
    headers: { "Content-Type": "application/json" },
    body: corpo === undefined ? undefined : JSON.stringify(corpo),
  });

function desenharTudo() {
  desenharPerfis();
  desenharServicos();
  aplicarEstados();
  atualizarPlano();
}

const nomeDe = (id) => app.config.servicos.find((s) => s.id === id)?.nome || id;
const servicoDe = (id) => app.config.servicos.find((s) => s.id === id);
const selecionados = () => app.config.servicos.filter((s) => s.selecionado).map((s) => s.id);

// ------------------------------------------------------------------
// perfis
// ------------------------------------------------------------------

function desenharPerfis() {
  const alvo = $("#perfis");
  alvo.innerHTML = "";
  for (const p of app.config.perfis || []) {
    const ativo = app.config.perfil_ativo === p.id;
    // Div por fora com dois botoes dentro: botao dentro de botao e HTML invalido e o
    // navegador desmonta a estrutura na hora de renderizar.
    const chip = el("div", {
      className: `perfil${ativo ? " ativo" : ""}`,
      dataset: { id: p.id },
      title: p.servicos.map(nomeDe).join(", "),
    },
      el("button", { type: "button", className: "aplicar", textContent: p.nome }),
      el("span", { className: "qtd", textContent: `${p.servicos.length}` }),
      el("button", { type: "button", className: "editar-perfil", textContent: "⋯", title: "editar perfil" }),
    );
    alvo.appendChild(chip);
  }

  alvo.onclick = async (ev) => {
    const chip = ev.target.closest(".perfil");
    if (!chip) return;
    const perfil = (app.config.perfis || []).find((p) => p.id === chip.dataset.id);
    if (ev.target.closest(".editar-perfil")) return menuPerfil(perfil);
    if (!ev.target.closest(".aplicar")) return;
    try {
      const r = await enviarJSON(`/api/perfis/${encodeURIComponent(perfil.id)}/aplicar`);
      app.config = r.config;
      desenharTudo();
      avisar(`perfil "${perfil.nome}": ${perfil.servicos.length} projeto(s) marcados.`, "ok");
    } catch (err) {
      avisar(err.message, "erro");
    }
  };
}

async function salvarPerfilAtual() {
  const marcados = selecionados();
  if (!marcados.length) return avisar("marque os projetos do perfil antes de salvar.", "erro");
  const nome = await pedirTexto("salvar perfil", "nome do perfil", "");
  if (!nome) return;
  try {
    const r = await enviarJSON("/api/perfis", { nome, servicos: marcados });
    app.config = r.config;
    desenharTudo();
    avisar(`perfil "${nome}" salvo com ${marcados.length} projeto(s).`, "ok");
  } catch (err) {
    avisar(err.message, "erro");
  }
}

function menuPerfil(perfil) {
  abrirModal({
    titulo: `perfil · ${perfil.nome}`,
    corpo: [
      el("p", { className: "ajuda", textContent: `${perfil.servicos.length} projeto(s): ${perfil.servicos.map(nomeDe).join(", ")}` }),
    ],
    acoes: [
      { texto: "apagar", classe: "perigo esquerda", acao: async () => {
        if (!(await confirmar(`apagar o perfil "${perfil.nome}"?`))) return false;
        const r = await enviarJSON(`/api/perfis/${encodeURIComponent(perfil.id)}`, undefined, "DELETE");
        app.config = r.config;
        desenharTudo();
        avisar("perfil apagado.", "ok");
      } },
      { texto: "renomear", acao: async () => {
        const nome = await pedirTexto("renomear perfil", "nome do perfil", perfil.nome);
        if (!nome) return false;
        const r = await enviarJSON("/api/perfis", { id: perfil.id, nome, servicos: perfil.servicos });
        app.config = r.config;
        desenharTudo();
      } },
      { texto: "gravar selecao atual", primario: true, acao: async () => {
        const marcados = selecionados();
        if (!marcados.length) { avisar("nenhum projeto marcado.", "erro"); return false; }
        const r = await enviarJSON("/api/perfis", { id: perfil.id, nome: perfil.nome, servicos: marcados });
        app.config = r.config;
        desenharTudo();
        avisar(`perfil "${perfil.nome}" agora tem ${marcados.length} projeto(s).`, "ok");
      } },
    ],
  });
}

// ------------------------------------------------------------------
// grupos e cartoes
// ------------------------------------------------------------------

function desenharServicos() {
  const alvo = $("#servicos");
  const modelo = $("#modelo-servico");
  alvo.innerHTML = "";

  app.config.grupos.forEach((grupo, indice) => {
    const cabecalho = el("div", { className: "grupo-titulo" },
      el("span", { textContent: grupo.nome }),
      el("div", { className: "acoes-grupo" },
        el("button", { type: "button", dataset: { acao: "novo-projeto", grupo: grupo.id }, textContent: "+ projeto" }),
        el("button", { type: "button", dataset: { acao: "renomear", grupo: grupo.id }, textContent: "renomear" }),
        el("button", { type: "button", dataset: { acao: "subir-grupo", grupo: grupo.id }, textContent: "↑", title: "mover grupo para cima", disabled: indice === 0 }),
        el("button", { type: "button", dataset: { acao: "descer-grupo", grupo: grupo.id }, textContent: "↓", title: "mover grupo para baixo", disabled: indice === app.config.grupos.length - 1 }),
        el("button", { type: "button", dataset: { acao: "apagar-grupo", grupo: grupo.id }, textContent: "apagar" }),
      ),
    );
    alvo.appendChild(cabecalho);

    const doGrupo = app.config.servicos.filter((s) => s.grupo === grupo.id);
    if (!doGrupo.length) {
      alvo.appendChild(el("p", { className: "grupo-vazio", textContent: "nenhum projeto neste grupo ainda." }));
      return;
    }

    doGrupo.forEach((s, i) => {
      const no = modelo.content.firstElementChild.cloneNode(true);
      no.dataset.id = s.id;
      no.style.animationDelay = `${Math.min(i, 10) * 26}ms`;

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
        link.hidden = false;
      }

      const modo = $(".modo", no);
      if (s.tipo !== "app") {
        modo.classList.add("oculto");
      } else {
        $$(".modo-opcao", modo).forEach((b) => {
          b.setAttribute("aria-pressed", (s.modo || "gerenciado") === b.dataset.modo ? "true" : "false");
          if (b.dataset.modo === "terminal" && !app.temWt) {
            b.title = "Windows Terminal nao encontrado - cai para modo gerenciado";
          }
        });
      }

      desenharDependencias(no, s);
      alvo.appendChild(no);
    });
  });

  alvo.onclick = aoClicar;
  alvo.onchange = aoMudar;
}

function desenharDependencias(no, s) {
  const chips = $(".chips", no);
  chips.innerHTML = "";
  if (!s.depende.length) {
    chips.appendChild(el("span", { className: "chip vazio", textContent: "ninguem" }));
  } else {
    for (const d of s.depende) chips.appendChild(el("span", { className: "chip", textContent: nomeDe(d) }));
  }

  const opcoes = $(".opcoes-dep", no);
  opcoes.innerHTML = "";
  for (const outro of app.config.servicos) {
    if (outro.id === s.id) continue;
    const caixa = el("input", { type: "checkbox", className: "chk-dep", dataset: { dep: outro.id } });
    caixa.checked = s.depende.includes(outro.id);
    caixa.setAttribute("aria-label", `${s.nome} espera ${outro.nome}`);
    opcoes.appendChild(el("label", {}, caixa, outro.nome));
  }
}

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
  const botaoGrupo = ev.target.closest(".acoes-grupo button");
  if (botaoGrupo) return acaoDeGrupo(botaoGrupo.dataset.acao, botaoGrupo.dataset.grupo);

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

  if (ev.target.classList.contains("acao-subir")) return subir([id]);
  if (ev.target.classList.contains("acao-parar")) return parar([id]);
  if (ev.target.classList.contains("acao-log")) return mostrarLog(id);
  if (ev.target.classList.contains("acao-editar")) return formularioServico(s);
}

async function acaoDeGrupo(acao, id) {
  const grupo = app.config.grupos.find((g) => g.id === id);
  try {
    if (acao === "novo-projeto") return formularioServico(null, id);

    if (acao === "renomear") {
      const nome = await pedirTexto("renomear grupo", "nome do grupo", grupo.nome);
      if (!nome) return;
      const r = await enviarJSON("/api/grupos", { id, nome });
      app.config = r.config;
      return desenharTudo();
    }

    if (acao === "apagar-grupo") {
      if (!(await confirmar(`apagar o grupo "${grupo.nome}"?`))) return;
      const r = await enviarJSON(`/api/grupos/${encodeURIComponent(id)}`, undefined, "DELETE");
      app.config = r.config;
      avisar("grupo apagado.", "ok");
      return desenharTudo();
    }

    if (acao === "subir-grupo" || acao === "descer-grupo") {
      const ordem = app.config.grupos.map((g) => g.id);
      const i = ordem.indexOf(id);
      const j = acao === "subir-grupo" ? i - 1 : i + 1;
      if (j < 0 || j >= ordem.length) return;
      [ordem[i], ordem[j]] = [ordem[j], ordem[i]];
      const r = await enviarJSON("/api/grupos/ordem", { ids: ordem });
      app.config = r.config;
      return desenharTudo();
    }
  } catch (err) {
    avisar(err.message, "erro");
  }
}

async function novoGrupo() {
  const nome = await pedirTexto("novo grupo", "nome do grupo (ex.: photonow)", "");
  if (!nome) return;
  try {
    const r = await enviarJSON("/api/grupos", { nome });
    app.config = r.config;
    desenharTudo();
    avisar(`grupo "${nome}" criado.`, "ok");
  } catch (err) {
    avisar(err.message, "erro");
  }
}

// ------------------------------------------------------------------
// formulario de projeto
// ------------------------------------------------------------------

function formularioServico(servico, grupoPadrao) {
  const novo = !servico;
  const dados = servico
    ? JSON.parse(JSON.stringify(servico))
    : {
        nome: "", grupo: grupoPadrao || app.config.grupos[0]?.id || "", tipo: "app",
        dir: "", cmd: "", compose: { projeto: "", arquivo: "", servico: "" }, container: "",
        porta: 0, url: "", pronto: { tipo: "porta" }, timeout: 120, modo: "gerenciado",
        depende: [], selecionado: true,
      };
  dados.compose = dados.compose || { projeto: "", arquivo: "", servico: "" };
  dados.caminho_livre = false;

  const campos = el("div", { className: "campos" });
  const bloco = (rotulo, conteudo, largo = false, ajuda = "") =>
    el("div", { className: `campo${largo ? " largo" : ""}` },
      el("span", { className: "rotulo-campo", textContent: rotulo }),
      conteudo,
      ajuda ? el("span", { className: "ajuda", textContent: ajuda }) : null,
    );

  const entNome = el("input", { type: "text", value: dados.nome, placeholder: "appdaturma-backend" });
  const selGrupo = el("select", {},
    ...app.config.grupos.map((g) => el("option", { value: g.id, textContent: g.nome, selected: g.id === dados.grupo })));

  const btnApp = el("button", { type: "button", textContent: "aplicacao / script" });
  const btnDocker = el("button", { type: "button", textContent: "container docker" });
  const seletorTipo = el("div", { className: "seletor-tipo" }, btnApp, btnDocker);

  const entDir = el("input", { type: "text", value: dados.dir, placeholder: "permissions-api" });
  const btnProcurarDir = el("button", { type: "button", className: "mini", textContent: "procurar" });
  const chkLivre = el("input", { type: "checkbox" });
  const entCmd = el("input", { type: "text", value: dados.cmd, placeholder: "npm run dev" });
  const sugestoes = el("div", { className: "sugestoes" });
  const modoApp = el("select", {},
    el("option", { value: "gerenciado", textContent: "log aqui (gerenciado)", selected: dados.modo !== "terminal" }),
    el("option", { value: "terminal", textContent: "aba do Windows Terminal", selected: dados.modo === "terminal" }));

  const entArquivo = el("input", { type: "text", value: dados.compose.arquivo, placeholder: "docker appdaturma/docker-compose.yml" });
  const btnProcurarCompose = el("button", { type: "button", className: "mini", textContent: "procurar" });
  const entProjeto = el("input", { type: "text", value: dados.compose.projeto, placeholder: "dockerappdaturma" });
  const selServicoCompose = el("select", {}, el("option", { value: dados.compose.servico || "", textContent: dados.compose.servico || "(escolha o compose)" }));
  const entContainer = el("input", { type: "text", value: dados.container, placeholder: "db" });

  const entPorta = el("input", { type: "number", value: dados.porta || "", min: "0", max: "65535", placeholder: "7003" });
  const entURL = el("input", { type: "text", value: dados.url, placeholder: "http://localhost:7003" });
  const selPronto = el("select", {},
    el("option", { value: "porta", textContent: "porta atendendo", selected: dados.pronto.tipo === "porta" }),
    el("option", { value: "http", textContent: "http respondendo", selected: dados.pronto.tipo === "http" }),
    el("option", { value: "comando", textContent: "comando com saida 0", selected: dados.pronto.tipo === "comando" }));
  const entProntoDetalhe = el("input", {
    type: "text",
    value: dados.pronto.tipo === "comando" ? (dados.pronto.cmd || []).join(" ")
      : dados.pronto.tipo === "http" ? (dados.pronto.url || "") : (dados.pronto.porta || ""),
  });
  const entTimeout = el("input", { type: "number", value: dados.timeout || 120, min: "1", placeholder: "120" });

  const listaDep = el("div", { className: "lista-dep" });
  for (const outro of app.config.servicos) {
    if (servico && outro.id === servico.id) continue;
    const caixa = el("input", { type: "checkbox", dataset: { dep: outro.id } });
    caixa.checked = dados.depende.includes(outro.id);
    listaDep.appendChild(el("label", {}, caixa, outro.nome));
  }

  const campoDir = bloco("pasta do projeto",
    el("div", {},
      el("div", { className: "linha-campo" }, entDir, btnProcurarDir),
      el("label", { className: "opcao-livre" }, chkLivre, "usar caminho fora da pasta de projetos"),
    ), true);
  const campoCmd = bloco("comando", el("div", {}, entCmd, sugestoes), true,
    "roda no pwsh, dentro da pasta acima");
  const campoModo = bloco("modo", modoApp);
  const campoArquivo = bloco("arquivo compose", el("div", { className: "linha-campo" }, entArquivo, btnProcurarCompose), true);
  const campoProjeto = bloco("projeto compose", entProjeto, false, "mantenha o nome ja usado na maquina");
  const campoServicoCompose = bloco("servico no compose", selServicoCompose);
  const campoContainer = bloco("nome do container", entContainer);

  campos.append(
    bloco("nome", entNome),
    bloco("grupo", selGrupo),
    bloco("tipo", seletorTipo, true),
    campoDir, campoCmd, campoModo,
    campoArquivo, campoProjeto, campoServicoCompose, campoContainer,
    bloco("porta", entPorta),
    bloco("url (botao abrir)", entURL),
    bloco("como saber que ficou pronto", selPronto),
    bloco("detalhe da checagem", entProntoDetalhe, false, "porta, url ou comando conforme a opcao ao lado"),
    bloco("timeout (segundos)", entTimeout),
    bloco("espera por", listaDep, true, "o projeto so arranca quando estes estiverem no ar"),
  );

  function aplicarTipo() {
    const app_ = dados.tipo === "app";
    btnApp.setAttribute("aria-pressed", app_ ? "true" : "false");
    btnDocker.setAttribute("aria-pressed", app_ ? "false" : "true");
    for (const c of [campoDir, campoCmd, campoModo]) c.hidden = !app_;
    for (const c of [campoArquivo, campoProjeto, campoServicoCompose, campoContainer]) c.hidden = app_;
  }
  btnApp.onclick = () => { dados.tipo = "app"; aplicarTipo(); };
  btnDocker.onclick = () => { dados.tipo = "docker"; aplicarTipo(); };
  aplicarTipo();

  selPronto.onchange = () => {
    entProntoDetalhe.value = selPronto.value === "porta" ? (entPorta.value || "") : "";
  };

  btnProcurarDir.onclick = async () => {
    const escolhida = await escolherPasta(entDir.value, chkLivre.checked);
    if (!escolhida) return;
    entDir.value = escolhida;
    if (!entNome.value) entNome.value = escolhida.split(/[\\/]/).filter(Boolean).pop();
    await carregarSugestoes(escolhida, chkLivre.checked);
  };

  btnProcurarCompose.onclick = async () => {
    const escolhida = await escolherPasta(entArquivo.value.replace(/[\\/][^\\/]*$/, ""), chkLivre.checked);
    if (!escolhida) return;
    await carregarSugestoes(escolhida, chkLivre.checked, true);
  };

  async function carregarSugestoes(pasta, livre, paraCompose = false) {
    sugestoes.innerHTML = "";
    let insp;
    try {
      insp = await pedir(`/api/inspecionar?caminho=${encodeURIComponent(pasta)}${livre ? "&livre=1" : ""}`);
    } catch (err) {
      return avisar(err.message, "erro");
    }
    if (paraCompose) {
      if (!insp.composes.length) return avisar("nenhum docker-compose nessa pasta.", "erro");
      const arquivo = insp.composes[0];
      entArquivo.value = `${pasta}\\${arquivo.arquivo}`;
      selServicoCompose.innerHTML = "";
      for (const nome of arquivo.servicos) {
        selServicoCompose.appendChild(el("option", { value: nome, textContent: nome }));
      }
      if (!entProjeto.value) entProjeto.value = pasta.split(/[\\/]/).filter(Boolean).pop().replace(/[^a-z0-9]/gi, "").toLowerCase();
      return;
    }
    for (const s of insp.sugestoes) {
      const chip = el("button", { type: "button", className: "sugestao", textContent: s.rotulo });
      chip.onclick = () => {
        entCmd.value = s.cmd;
        if (s.porta && !entPorta.value) {
          entPorta.value = s.porta;
          if (selPronto.value === "porta") entProntoDetalhe.value = s.porta;
        }
      };
      sugestoes.appendChild(chip);
    }
    if (!insp.sugestoes.length) sugestoes.appendChild(el("span", { className: "ajuda", textContent: "nenhuma sugestao automatica - escreva o comando." }));
  }

  const acoes = [
    { texto: "salvar", primario: true, acao: async () => {
      const entrada = {
        id: servico ? servico.id : "",
        nome: entNome.value.trim(),
        grupo: selGrupo.value,
        tipo: dados.tipo,
        porta: Number(entPorta.value) || 0,
        url: entURL.value.trim(),
        timeout: Number(entTimeout.value) || 120,
        depende: $$("input[data-dep]", listaDep).filter((c) => c.checked).map((c) => c.dataset.dep),
        selecionado: servico ? !!servico.selecionado : true,
        caminho_livre: chkLivre.checked,
        pronto: montarChecagem(selPronto.value, entProntoDetalhe.value, Number(entPorta.value) || 0),
      };
      if (dados.tipo === "app") {
        entrada.dir = entDir.value.trim();
        entrada.cmd = entCmd.value.trim();
        entrada.modo = modoApp.value;
      } else {
        entrada.compose = {
          projeto: entProjeto.value.trim(),
          arquivo: entArquivo.value.trim(),
          servico: selServicoCompose.value,
        };
        entrada.container = entContainer.value.trim();
      }
      // Porta repetida nao impede o cadastro (pode ser que os dois nunca subam juntos), mas
      // avisa: a checagem de "pronto" olha a porta, entao um servico daria como no ar por
      // causa do outro.
      const conflito = app.config.servicos.find(
        (outro) => outro.porta && outro.porta === entrada.porta && outro.id !== entrada.id);

      const r = await enviarJSON("/api/servicos", entrada);
      app.config = r.config;
      desenharTudo();
      avisar(novo ? `projeto "${entrada.nome}" cadastrado.` : "projeto atualizado.", "ok");
      if (conflito) avisar(`atencao: a porta ${entrada.porta} ja e usada por ${conflito.nome}.`);
    } },
  ];
  if (servico) {
    acoes.unshift({ texto: "apagar", classe: "perigo esquerda", acao: async () => {
      if (!(await confirmar(`apagar "${servico.nome}" do launcher? (nao mexe nos arquivos do projeto)`))) return false;
      const r = await enviarJSON(`/api/servicos/${encodeURIComponent(servico.id)}`, undefined, "DELETE");
      app.config = r.config;
      if (app.logAtual === servico.id) app.logAtual = null;
      desenharTudo();
      avisar("projeto removido do launcher.", "ok");
    } });
  }

  abrirModal({ titulo: novo ? "novo projeto" : `editar · ${servico.nome}`, corpo: [campos], acoes });
  entNome.focus();
}

function montarChecagem(tipo, detalhe, portaCampo) {
  detalhe = (detalhe || "").trim();
  if (tipo === "comando") return { tipo, cmd: detalhe.split(/\s+/).filter(Boolean) };
  if (tipo === "http") return { tipo, url: detalhe };
  return { tipo: "porta", porta: Number(detalhe) || portaCampo };
}

// ------------------------------------------------------------------
// navegador de pastas
// ------------------------------------------------------------------

function escolherPasta(inicial, livre) {
  return new Promise((resolver) => {
    const lista = el("div", { className: "lista-pastas" });
    const trilha = el("div", { className: "trilha" });
    let atual = "";

    async function navegar(caminho) {
      let dados;
      try {
        dados = await pedir(`/api/pastas?caminho=${encodeURIComponent(caminho || "")}${livre ? "&livre=1" : ""}`);
      } catch (err) {
        avisar(err.message, "erro");
        return;
      }
      atual = dados.caminho;
      trilha.innerHTML = "";
      trilha.append("pasta atual: ", el("b", { textContent: dados.caminho }));
      lista.innerHTML = "";
      if (dados.pai && (livre || dados.caminho !== dados.raiz)) {
        const acima = el("button", { type: "button", className: "item-pasta acima" },
          el("span", { className: "nome-pasta", textContent: ".. subir um nivel" }));
        acima.onclick = () => navegar(dados.pai);
        lista.appendChild(acima);
      }
      for (const item of dados.itens) {
        const no = el("button", { type: "button", className: "item-pasta" },
          el("span", { className: "nome-pasta", textContent: item.nome }),
          el("span", { className: "marcas" },
            ...(item.marcas || []).map((m) => el("span", { className: "marca", textContent: m }))),
        );
        no.onclick = () => navegar(item.caminho);
        lista.appendChild(no);
      }
    }

    abrirModal({
      titulo: "escolher pasta",
      corpo: [trilha, lista],
      aoFechar: () => resolver(null),
      acoes: [
        { texto: "usar esta pasta", primario: true, acao: () => { resolver(atual); } },
      ],
    });
    navegar(inicial);
  });
}

// ------------------------------------------------------------------
// modal generico
// ------------------------------------------------------------------

// Pilha, e nao um modal so: o seletor de pastas abre por cima do formulario de projeto.
// Os nos do corpo ficam guardados na pilha, entao ao voltar o formulario reaparece com tudo
// que ja estava digitado - sao os mesmos elementos, nao uma copia.
const pilhaModais = [];

function abrirModal(config) {
  pilhaModais.push(config);
  renderizarModal();
  return (concluido) => fecharModal(config, concluido);
}

function renderizarModal() {
  const caixa = $("#modal");
  if (!pilhaModais.length) {
    caixa.hidden = true;
    $("#modal-corpo").innerHTML = "";
    return;
  }
  const atual = pilhaModais[pilhaModais.length - 1];
  $("#modal-titulo").textContent = atual.titulo;

  const alvo = $("#modal-corpo");
  alvo.innerHTML = "";
  for (const parte of atual.corpo) alvo.appendChild(parte);

  const rodape = $("#modal-acoes");
  rodape.innerHTML = "";
  rodape.appendChild(el("button", {
    type: "button", className: "botao", textContent: "cancelar",
    onclick: () => fecharModal(atual),
  }));
  for (const a of atual.acoes || []) {
    const botao = el("button", {
      type: "button",
      className: `botao ${a.primario ? "primario" : ""} ${a.classe || ""}`.trim(),
      textContent: a.texto,
    });
    botao.onclick = async () => {
      botao.disabled = true;
      try {
        // acao devolvendo false mantem o modal aberto (cancelou uma confirmacao, por ex.).
        if ((await a.acao()) !== false) fecharModal(atual, true);
      } catch (err) {
        avisar(err.message, "erro");
      } finally {
        botao.disabled = false;
      }
    };
    rodape.appendChild(botao);
  }
  caixa.hidden = false;
}

function fecharModal(config, concluido) {
  const i = pilhaModais.lastIndexOf(config);
  if (i < 0) return;
  pilhaModais.splice(i, 1);
  if (!concluido && config.aoFechar) config.aoFechar();
  renderizarModal();
}

function fecharTopo() {
  if (pilhaModais.length) fecharModal(pilhaModais[pilhaModais.length - 1]);
}

function pedirTexto(titulo, rotulo, valor) {
  return new Promise((resolver) => {
    const entrada = el("input", { type: "text", value: valor || "" });
    const campo = el("div", { className: "campo largo" },
      el("span", { className: "rotulo-campo", textContent: rotulo }), entrada);
    const fechar = abrirModal({
      titulo,
      corpo: [campo],
      aoFechar: () => resolver(null),
      acoes: [{ texto: "confirmar", primario: true, acao: () => { resolver(entrada.value.trim() || null); } }],
    });
    entrada.onkeydown = (ev) => {
      if (ev.key === "Enter") { resolver(entrada.value.trim() || null); fechar(true); }
    };
    entrada.focus();
    entrada.select();
  });
}

function confirmar(texto) {
  return new Promise((resolver) => {
    abrirModal({
      titulo: "confirmar",
      corpo: [el("p", { className: "ajuda", textContent: texto })],
      aoFechar: () => resolver(false),
      acoes: [{ texto: "confirmar", primario: true, classe: "perigo", acao: () => { resolver(true); } }],
    });
  });
}

// ------------------------------------------------------------------
// acoes gerais
// ------------------------------------------------------------------

function ligarBotoes() {
  $("#btn-subir").onclick = () => subir(selecionados());
  $("#btn-parar").onclick = () => parar(selecionados());
  $("#btn-novo-perfil").onclick = salvarPerfilAtual;
  $("#btn-novo-grupo").onclick = novoGrupo;
  $("#btn-novo-projeto").onclick = () => formularioServico(null);

  $$("[data-fechar]").forEach((no) => (no.onclick = fecharTopo));

  // O <details> das dependencias nao fecha sozinho: sem isto fica um menu aberto por cima
  // dos cartoes depois de editar.
  document.addEventListener("click", (ev) => {
    for (const editor of $$(".editor-dep[open]")) {
      if (!editor.contains(ev.target)) editor.open = false;
    }
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key !== "Escape") return;
    if (pilhaModais.length) return fecharTopo();
    $$(".editor-dep[open]").forEach((e) => (e.open = false));
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
    const itens = el("div", { className: "onda-itens" },
      ...onda.map((id) => el("span", {
        className: "onda-item",
        textContent: nomeDe(id),
        dataset: { id, status: app.estados[id]?.status || "parado" },
      })));
    alvo.appendChild(el("div", { className: "onda" },
      el("div", { className: "onda-num", textContent: `${i + 1}a` }), itens));
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
      desenharTudo();
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
  const no = el("span", { textContent: linha + "\n", className: linha.startsWith("> ") ? "eco" : "" });
  alvo.appendChild(no);
  if (noFim) alvo.scrollTop = alvo.scrollHeight;
}

// ------------------------------------------------------------------
// avisos
// ------------------------------------------------------------------

function avisar(texto, tipo = "") {
  const no = el("div", { className: `aviso ${tipo}`, textContent: texto });
  $("#avisos").appendChild(no);
  setTimeout(() => no.remove(), 6000);
}

iniciar();
