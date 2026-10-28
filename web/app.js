"use strict";

// Vue 3 (build global, sem passo de build): o binario serve vendor/vue.global.prod.js.
// A tela e reativa - mudar um estado repinta so o cartao afetado, sem redesenhar a lista.
const { createApp } = Vue;

const ROTULO_STATUS = {
  parado: "parado",
  esperando: "na fila",
  subindo: "subindo",
  pronto: "no ar",
  erro: "erro",
  bloqueado: "bloqueado",
};

// Familias de status: e por elas que os contadores do topo contam e filtram.
const FAMILIA_STATUS = {
  parado: "parado",
  esperando: "andando",
  subindo: "andando",
  pronto: "pronto",
  erro: "falha",
  bloqueado: "falha",
};

// Grupo e painel recolhido e preferencia de quem olha a tela, nao do config: fica no navegador.
const CHAVE_RECOLHIDOS = "dev-launcher.recolhidos";
function lerRecolhidos() {
  try {
    return JSON.parse(localStorage.getItem(CHAVE_RECOLHIDOS)) || {};
  } catch {
    return {};
  }
}

const FORM_VAZIO = () => ({
  id: "", nome: "", grupo: "", tipo: "app",
  dir: "", cmd: "", modo: "gerenciado", caminho_livre: false,
  composeArquivo: "", composeProjeto: "", composeServico: "", servicosCompose: [],
  container: "", porta: null, url: "",
  prontoTipo: "porta", prontoDetalhe: "", timeout: 120,
  depende: [], selecionado: true, sugestoes: null, erro: "",
});

// ------------------------------------------------------------------
// diff (painel git)
// ------------------------------------------------------------------

// Acima disso o navegador sofre para desenhar: o resto do diff fica para o terminal.
const MAX_LINHAS_DIFF = 6000;

// ---- cor de sintaxe (highlight.js, servido de vendor/highlight.min.js) ----

// Extensao que o highlight.js nao reconhece sozinho como alias. O resto ele resolve: js, ts,
// tsx, go, py, rb, rs, cs, java, php, sql, yml, json, md, sh, ps1, css, scss, html, xml...
const LINGUAGEM_POR_EXT = {
  vue: "xml", svelte: "xml", htm: "xml", svg: "xml", csproj: "xml", props: "xml", config: "xml",
  mjs: "javascript", cjs: "javascript", jsonc: "json", json5: "json",
  toml: "ini", env: "bash", h: "c", hpp: "cpp",
};

// linguagemDo escolhe a linguagem pelo nome do arquivo. Sem certeza, fica sem cor: adivinhar
// pelo conteudo (highlightAuto) e lento e erra bastante em trecho solto de diff.
function linguagemDo(caminho) {
  if (typeof hljs === "undefined" || !caminho) return null;
  const nome = caminho.split("/").pop().toLowerCase();
  if (nome === "dockerfile" || nome.startsWith("dockerfile.") || nome.endsWith(".dockerfile")) return "dockerfile";
  if (nome === "makefile") return "makefile";
  if (nome === ".env" || nome.startsWith(".env.")) return "bash";
  const ponto = nome.lastIndexOf(".");
  if (ponto < 0) return null;
  const ext = nome.slice(ponto + 1);
  const lang = LINGUAGEM_POR_EXT[ext] || ext;
  return hljs.getLanguage(lang) ? lang : null;
}

function escaparHtml(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// quebrarHtml divide o HTML do highlight.js em uma string por linha. Um token pode atravessar
// linhas (comentario de bloco, string longa): o span e fechado no fim da linha e reaberto na
// seguinte, para cada linha do diff ficar com HTML completo.
function quebrarHtml(html) {
  const linhas = [], abertos = [];
  let atual = "";
  for (const [p] of html.matchAll(/<span[^>]*>|<\/span>|\n|[^<\n]+/g)) {
    if (p === "\n") {
      linhas.push(atual + "</span>".repeat(abertos.length));
      atual = abertos.join("");
    } else {
      if (p === "</span>") abertos.pop();
      else if (p.startsWith("<span")) abertos.push(p);
      atual += p;
    }
  }
  linhas.push(atual + "</span>".repeat(abertos.length));
  return linhas;
}

// colorir pinta um lado (antigo ou novo) de um trecho do diff de uma vez so, e nao linha a
// linha: assim o highlight.js ve o comentario ou a string inteira e nao se perde no meio.
function colorir(itens, indices, lang) {
  if (!indices.length) return;
  let html;
  try {
    html = hljs.highlight(indices.map((i) => itens[i].t).join("\n"), { language: lang, ignoreIllegals: true }).value;
  } catch {
    return; // fica o texto escapado que ja esta no item
  }
  const partes = quebrarHtml(html);
  if (partes.length !== indices.length) return;
  indices.forEach((i, k) => { itens[i].h = partes[k]; });
}

// lerDiff transforma a saida do "git diff"/"git show" em linhas com classe e numero da linha
// no arquivo antigo e no novo, lidos dos cabecalhos "@@ -a,b +c,d @@". Linha de codigo sai
// com o sinal (m) separado do texto (t) e o texto ja em HTML (h), com cor quando a
// linguagem do arquivo e conhecida.
function lerDiff(texto) {
  const itens = [];
  let velho = 0, novo = 0, dentro = false, sobrou = false;
  let lang = null, ladoVelho = [], ladoNovo = [];
  // Fecha o trecho atual: pinta o lado antigo (contexto + removidas) e o novo (contexto +
  // adicionadas). O contexto fica com a cor do lado novo.
  const fecharTrecho = () => {
    if (lang) { colorir(itens, ladoVelho, lang); colorir(itens, ladoNovo, lang); }
    ladoVelho = []; ladoNovo = [];
  };
  const codigo = (c, linha, v, n) => {
    const t = linha.slice(1);
    itens.push({ c, m: linha[0] || " ", t, h: escaparHtml(t), cor: !!lang, v, n });
    return itens.length - 1;
  };
  const linhas = (texto || "").split("\n");
  if (linhas.length && linhas[linhas.length - 1] === "") linhas.pop();
  for (let linha of linhas) {
    if (itens.length >= MAX_LINHAS_DIFF) { sobrou = true; break; }
    if (linha.endsWith("\r")) linha = linha.slice(0, -1);
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(linha);
    if (hunk) {
      fecharTrecho();
      velho = +hunk[1]; novo = +hunk[2]; dentro = true;
      itens.push({ c: "hunk", t: linha, v: "", n: "" });
    } else if (linha.startsWith("diff --git") || linha.startsWith("diff --cc")) {
      fecharTrecho();
      dentro = false;
      const caminho = linha.replace(/^diff --git a\/(.*) b\/.*$/, "$1").replace(/^diff --cc /, "");
      lang = linguagemDo(caminho);
      itens.push({ c: "arquivo", t: caminho, v: "", n: "" });
    } else if (!dentro && /^(index |--- |\+\+\+ )/.test(linha)) {
      continue; // cabecalho tecnico: o nome do arquivo ja aparece na linha de cima
    } else if (!dentro || linha.startsWith("\\")) {
      itens.push({ c: "meta", t: linha, v: "", n: "" });
    } else if (linha.startsWith("+")) {
      ladoNovo.push(codigo("add", linha, "", novo++));
    } else if (linha.startsWith("-")) {
      ladoVelho.push(codigo("del", linha, velho++, ""));
    } else {
      const i = codigo("ctx", linha, velho++, novo++);
      ladoVelho.push(i); ladoNovo.push(i);
    }
  }
  fecharTrecho();
  // Congelado: o Vue nao precisa vigiar milhares de linhas que nunca mudam.
  return Object.freeze({ itens: Object.freeze(itens), sobrou });
}

// Componente proprio para o diff: a tela inteira e um componente so, e sem isso cada tecla
// digitada na mensagem do commit redesenharia todas as linhas do diff.
const VisorDiff = {
  props: { texto: String, cortado: Boolean, vazio: String },
  computed: {
    linhas() { return lerDiff(this.texto); },
  },
  template: `
    <div class="visor-diff">
      <div v-if="!texto" class="diff-vazio">{{ vazio }}</div>
      <template v-else>
        <template v-for="(l, i) in linhas.itens" :key="i">
          <div v-if="l.h !== undefined" class="dl" :class="[l.c, { cor: l.cor }]"><span class="nv">{{ l.v }}</span><span class="nn">{{ l.n }}</span><span class="mk">{{ l.m }}</span><span class="tx" v-html="l.h"></span></div>
          <div v-else class="dl" :class="l.c"><span class="nv">{{ l.v }}</span><span class="nn">{{ l.n }}</span><span class="tx">{{ l.t }}</span></div>
        </template>
        <div v-if="linhas.sobrou || cortado" class="diff-corte">diff grande demais - mostrando so o comeco.</div>
      </template>
    </div>`,
};

// "2 hours ago" do git vira "ha 2 h".
const UNIDADES_GIT = { second: ["s", "s"], minute: ["min", "min"], hour: ["h", "h"], day: ["dia", "dias"],
  week: ["semana", "semanas"], month: ["mes", "meses"], year: ["ano", "anos"] };
function quandoPt(texto) {
  if (!texto) return "";
  const t = texto.replace(/ ago$/, "").replace(/(\d+) (second|minute|hour|day|week|month|year)s?/g,
    (_, n, u) => `${n} ${UNIDADES_GIT[u][n === "1" ? 0 : 1]}`);
  return t === texto ? texto : "ha " + t;
}

const ABAS_GIT = [
  { id: "alteracoes", rotulo: "alteracoes" },
  { id: "historico", rotulo: "historico" },
  { id: "branches", rotulo: "branches" },
  { id: "stash", rotulo: "stash" },
  { id: "prs", rotulo: "pull requests" },
];

createApp({
  data() {
    return {
      raiz: "",
      temWt: false,
      config: { grupos: [], servicos: [], perfis: [], perfil_ativo: "" },
      estados: {},
      plano: {},
      logAtual: null,
      logContainer: null,
      depAberta: null,
      menuAberto: null,
      avisos: [],
      proximoAviso: 1,
      modais: [],
      form: FORM_VAZIO(),
      pastas: { caminho: "", pai: "", raiz: "", itens: [] },
      arraste: { id: null, grupo: null, antes: null },
      salvandoEm: null,
      encerrado: false,
      docker: { rodando: false, iniciando: false, containers: [], mensagem: "verificando..." },
      git: {},
      buscandoGit: false,
      abasGit: ABAS_GIT,
      rascunhos: {}, // mensagem de commit por projeto: fechar o painel nao joga o texto fora
      faltando: [],
      filtro: "",
      filtroStatus: "", // "" | pronto | andando | falha
      recolhidos: lerRecolhidos(),
      agora: Date.now(), // relogio dos cartoes que estao subindo
    };
  },

  computed: {
    selecionados() {
      return this.config.servicos.filter((s) => s.selecionado).map((s) => s.id);
    },
    contagem() {
      const c = { pronto: 0, andando: 0, falha: 0, parado: 0 };
      for (const s of this.servicosNoPerfil) c[this.familia(this.estado(s.id).status)]++;
      return c;
    },
    // Perfil aberto e uma visao: a tela inteira (cartoes, grupos, contadores) passa a mostrar
    // so os projetos dele. Sem perfil aberto, e o TODOS.
    perfilAtivo() {
      return this.config.perfis.find((p) => p.id === this.config.perfil_ativo) || null;
    },
    servicosNoPerfil() {
      const p = this.perfilAtivo;
      if (!p) return this.config.servicos;
      const ids = new Set(p.servicos);
      return this.config.servicos.filter((s) => ids.has(s.id));
    },
    subindo() {
      return this.contagem.andando > 0;
    },
    filtrando() {
      return !!(this.filtro.trim() || this.filtroStatus);
    },
    // Com filtro ligado, grupo sem nenhum cartao que bata some da tela.
    gruposVisiveis() {
      if (!this.filtrando && !this.perfilAtivo) return this.config.grupos;
      return this.config.grupos.filter((g) => this.servicosDe(g.id).length);
    },
    nenhumVisivel() {
      return (this.filtrando || !!this.perfilAtivo) && !this.config.servicos.some(this.passaNoFiltro);
    },
    topo() {
      return this.modais[this.modais.length - 1] || {};
    },
    // Indice do primeiro container que nao esta no config - e onde entra o separador
    // "fora do config" na lista.
    primeiroDeFora() {
      return this.docker.containers.findIndex((c) => !c.do_config);
    },
    textoDocker() {
      if (this.docker.iniciando) return "abrindo o Docker Desktop...";
      if (!this.docker.rodando) return this.docker.mensagem || "engine parado";
      const doConfig = this.docker.containers.filter((c) => c.do_config);
      const noAr = doConfig.filter((c) => c.estado === "running").length;
      const fora = this.docker.containers.length - doConfig.length;
      const base = `${noAr} de ${doConfig.length} no ar`;
      return fora ? `${base} · ${fora} fora do config` : base;
    },
    tituloModal() {
      const t = this.topo;
      if (t.tipo === "servico") return this.form.id ? `editar · ${this.form.nome}` : "novo projeto";
      if (t.tipo === "pasta") return "escolher pasta";
      if (t.tipo === "perfil") return t.perfil ? `perfil · ${t.perfil.nome}` : "novo perfil";
      if (t.tipo === "importar") return t.etapa === "resultado" ? `adicionar projetos · ${t.resultado.nome}` : "adicionar projetos";
      if (t.tipo === "branch") return `trocar de branch · ${t.servico.nome}`;
      if (t.tipo === "git") return `git · ${t.servico.nome}`;
      if (t.tipo === "confirmar") return t.titulo || "confirmar";
      return t.titulo || "";
    },
    // O erro fica dentro do modal, e nao num aviso: aviso some em 6s e o formulario
    // continuaria parado sem dizer por que.
    erroModal() {
      const t = this.topo;
      if (t.tipo === "servico") return this.form.erro;
      return t.erro || "";
    },
  },

  async mounted() {
    try {
      const dados = await this.pedir("/api/estado");
      this.config = dados.config;
      this.raiz = dados.raiz;
      this.temWt = dados.terminal;
      this.faltando = dados.faltando || [];
      for (const e of dados.estados) this.estados[e.id] = e;
    } catch (err) {
      return this.avisar("nao consegui falar com o launcher: " + err.message, "erro");
    }
    this.atualizarPlano();
    // O docker vem depois do primeiro desenho: com o engine parado a checagem demora
    // alguns segundos, e nao da para segurar a tela por causa disso.
    this.atualizarDocker();
    this.atualizarGit(false);
    this.conectarEventos();
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape" && this.modais.length) return this.fecharTopo();
      // "/" pula para o filtro, como em qualquer lista grande - menos quando ja se esta
      // digitando em algum campo.
      const digitando = /^(INPUT|TEXTAREA|SELECT)$/.test(ev.target.tagName);
      if (ev.key === "/" && !digitando && !this.modais.length) {
        ev.preventDefault();
        this.$refs.busca?.focus();
      }
    });
    // O relogio so anda enquanto ha algo subindo: e ele que move o tempo e o pavio dos
    // cartoes, e parado nao custa nenhum redesenho.
    setInterval(() => {
      if (this.subindo) this.agora = Date.now();
    }, 1000);
  },

  methods: {
    // ------------------------------------------------------------------
    // http
    // ------------------------------------------------------------------
    async pedir(url, opcoes) {
      const resp = await fetch(url, opcoes);
      const texto = await resp.text();
      let corpo = {};
      if (texto) {
        try { corpo = JSON.parse(texto); } catch { corpo = { erro: texto }; }
      }
      if (!resp.ok) {
        const erro = new Error(corpo.erro || `HTTP ${resp.status}`);
        erro.corpo = corpo; // o painel git manda o retrato relido junto com o erro
        throw erro;
      }
      return corpo;
    },
    enviar(url, corpo, metodo = "POST") {
      return this.pedir(url, {
        method: metodo,
        headers: { "Content-Type": "application/json" },
        body: corpo === undefined ? undefined : JSON.stringify(corpo),
      });
    },

    // ------------------------------------------------------------------
    // consultas da tela
    // ------------------------------------------------------------------
    estado(id) {
      return this.estados[id] || { status: "parado", mensagem: "" };
    },
    familia(status) {
      return FAMILIA_STATUS[status] || "parado";
    },
    // textoEstado e o selo do cartao. O motivo de erro/bloqueio nao entra aqui: vai na linha
    // propria, que tem espaco para a frase inteira.
    textoEstado(id) {
      const e = this.estado(id);
      const rotulo = ROTULO_STATUS[e.status] || e.status;
      if (e.status === "subindo") return `${rotulo} · ${this.decorrido(id)}`;
      if (e.status === "esperando") return e.mensagem && e.mensagem !== "na fila" ? e.mensagem : rotulo;
      if (this.familia(e.status) === "falha") return rotulo;
      return e.mensagem ? `${rotulo} · ${e.mensagem}` : rotulo;
    },
    // A etiqueta mostra so o essencial ("porta 8080 em uso por X"); a frase inteira fica no
    // tooltip.
    disputaCurta(id) {
      return (this.estado(id).disputa || "").split(" - ")[0];
    },
    motivo(id) {
      const e = this.estado(id);
      return this.familia(e.status) === "falha" ? e.mensagem || "" : "";
    },
    decorrido(id) {
      const desde = Date.parse(this.estado(id).desde);
      if (!desde) return "";
      const seg = Math.max(0, Math.round((this.agora - desde) / 1000));
      return seg < 60 ? `${seg}s` : `${Math.floor(seg / 60)}m${String(seg % 60).padStart(2, "0")}s`;
    },
    // progresso diz quanto do timeout ja foi gasto (0 a 1) - e a largura do pavio do cartao.
    progresso(s) {
      const desde = Date.parse(this.estado(s.id).desde);
      if (!desde) return 0;
      const limite = (s.timeout || 120) * 1000;
      return Math.min(1, Math.max(0, (this.agora - desde) / limite));
    },
    nomeDe(id) {
      return this.config.servicos.find((s) => s.id === id)?.nome || id;
    },
    servicoDe(id) {
      return this.config.servicos.find((s) => s.id === id);
    },
    passaNoFiltro(s) {
      if (!this.noPerfil(s)) return false;
      const texto = this.filtro.trim().toLowerCase();
      if (texto && !`${s.nome} ${s.dir || ""} ${s.porta || ""}`.toLowerCase().includes(texto)) return false;
      if (this.filtroStatus && this.familia(this.estado(s.id).status) !== this.filtroStatus) return false;
      return true;
    },
    // servicosDe e o que aparece na tela (respeita o filtro); todosDe e o grupo inteiro.
    servicosDe(grupo) {
      return this.config.servicos.filter((s) => s.grupo === grupo && this.passaNoFiltro(s));
    },
    todosDe(grupo) {
      return this.config.servicos.filter((s) => s.grupo === grupo && this.noPerfil(s));
    },
    noPerfil(s) {
      return !this.perfilAtivo || this.perfilAtivo.servicos.includes(s.id);
    },
    noArDe(grupo) {
      return this.todosDe(grupo).filter((s) => this.estado(s.id).status === "pronto").length;
    },
    posicaoGrupo(id) {
      return this.config.grupos.findIndex((g) => g.id === id);
    },

    // ------------------------------------------------------------------
    // filtro e grupos recolhidos
    // ------------------------------------------------------------------
    filtrarStatus(familia) {
      this.filtroStatus = this.filtroStatus === familia ? "" : familia;
    },
    limparFiltro() {
      this.filtro = "";
      this.filtroStatus = "";
    },
    recolhido(chave) {
      return !!this.recolhidos[chave];
    },
    // Filtrando, todo grupo abre: nao adianta achar o projeto e ele ficar escondido.
    grupoAberto(id) {
      return this.filtrando || !this.recolhido("grupo:" + id);
    },
    alternarRecolhido(chave) {
      this.recolhidos = { ...this.recolhidos, [chave]: !this.recolhidos[chave] };
      try {
        localStorage.setItem(CHAVE_RECOLHIDOS, JSON.stringify(this.recolhidos));
      } catch {
        // navegador sem armazenamento: so nao lembra na proxima vez
      }
    },
    idsDoGrupo(grupo) {
      return this.servicosDe(grupo).map((s) => s.id);
    },
    outrosServicos(id) {
      return this.config.servicos.filter((s) => s.id !== id);
    },

    // ------------------------------------------------------------------
    // subir / parar / reiniciar
    // ------------------------------------------------------------------
    async subir(ids) {
      if (!ids.length) return this.avisar("nenhum projeto selecionado.", "erro");
      try {
        const plano = await this.enviar("/api/subir", { ids });
        const extras = plano.ids.filter((x) => !ids.includes(x));
        const verbo = extras.length === 1 ? "entrou" : "entraram";
        // Um projeto so, sem dependencia: o cartao ja mostra que esta subindo, dispensa aviso.
        if (extras.length) {
          this.avisar(`subindo ${plano.ids.length} projetos (${extras.map(this.nomeDe).join(", ")} ${verbo} como dependencia).`, "ok");
        } else if (plano.ids.length > 1) {
          this.avisar(`subindo ${plano.ids.length} projetos.`, "ok");
        }
        this.plano = plano;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // subirUm e o botao do cartao: quem sobe um projeto quer ver o log dele.
    subirUm(s) {
      this.mostrarLog(s.id);
      this.subir([s.id]);
    },

    // resumirSubida conta como terminou a subida que o servidor acabou de encerrar.
    resumirSubida(ids) {
      const noAr = ids.filter((id) => this.estado(id).status === "pronto");
      const falhas = ids.filter((id) => this.familia(this.estado(id).status) === "falha");
      if (falhas.length) this.avisar(`nao subiu: ${falhas.map(this.nomeDe).join(", ")}.`, "erro");
      if (!noAr.length) return;
      this.avisar(noAr.length === 1 ? `${this.nomeDe(noAr[0])} no ar.` : `${noAr.length} projetos no ar.`, "ok");
    },

    async parar(ids) {
      if (!ids.length) return this.avisar("nenhum projeto selecionado.", "erro");
      try {
        const r = await this.enviar("/api/parar", { ids });
        if (r.erros?.length) r.erros.forEach((e) => this.avisar(e, "erro"));
        else this.avisar(`parado: ${ids.map(this.nomeDe).join(", ")}.`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    async reiniciar(ids) {
      if (!ids.length) return this.avisar("nenhum projeto selecionado.", "erro");
      try {
        const plano = await this.enviar("/api/reiniciar", { ids });
        if (plano.erros?.length) return plano.erros.forEach((e) => this.avisar(e, "erro"));
        this.plano = plano;
        this.avisar(`reiniciando ${plano.ids.length} projeto(s).`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // ------------------------------------------------------------------
    // git
    // ------------------------------------------------------------------
    async atualizarGit(forcar) {
      try {
        this.git = await this.pedir(`/api/git${forcar ? "?forcar=1" : ""}`);
      } catch (err) {
        this.avisar("git: " + err.message, "erro");
      }
    },

    // buscarGit sem ids busca em todos os projetos.
    async buscarGit(ids) {
      this.buscandoGit = true;
      try {
        await this.enviar("/api/git/buscar", { ids: ids || [] });
        this.avisar(ids ? `buscando ${this.nomeDe(ids[0])}...` : "buscando todos os repositorios...");
      } catch (err) {
        this.avisar(err.message, "erro");
      } finally {
        // O fetch termina em segundo plano; o evento "git" atualiza a tela quando chegar.
        setTimeout(() => (this.buscandoGit = false), 1500);
      }
      this.menuAberto = null;
    },

    mexidos(g) {
      return (g.preparados || 0) + (g.modificados || 0) + (g.nao_rastreados || 0);
    },

    // Trocar de branch so com a copia de trabalho limpa. Aqui e so a dica visual do cartao,
    // que pode estar uns segundos atrasada: quem decide e o servidor, lendo o git na hora.
    podeTrocarBranch(g) {
      return !this.mexidos(g) && !g.conflitos;
    },

    async abrirBranches(s) {
      this.menuAberto = null;
      const g = this.git[s.id] || {};
      this.abrir({
        tipo: "branch", servico: s, atual: g.branch || "", limpo: true,
        branches: [], filtro: "", carregando: true, trocando: "", erro: "",
      });
      const modal = this.topo; // o proxy reativo, nao o objeto cru que foi empilhado
      try {
        const r = await this.pedir(`/api/servicos/${encodeURIComponent(s.id)}/branches`);
        modal.atual = r.atual;
        modal.limpo = r.limpo;
        modal.branches = r.branches || [];
        if (!r.limpo) modal.erro = "o repositorio tem alteracoes locais - faca commit ou stash antes de trocar de branch.";
      } catch (err) {
        modal.erro = err.message;
      } finally {
        modal.carregando = false;
      }
      this.$nextTick(() => this.$refs.entradaFiltro?.focus());
    },

    branchesVisiveis(modal) {
      const filtro = (modal.filtro || "").trim().toLowerCase();
      return modal.branches.filter((b) => !filtro || b.nome.toLowerCase().includes(filtro));
    },

    // Enter no filtro troca direto quando sobrou um branch so na lista.
    trocarUnicoBranch() {
      const visiveis = this.branchesVisiveis(this.topo);
      if (visiveis.length === 1) this.trocarBranch(visiveis[0]);
    },

    async trocarBranch(b) {
      const modal = this.topo;
      if (modal.trocando || !modal.limpo || b.nome === modal.atual) return;
      modal.trocando = b.nome;
      modal.erro = "";
      try {
        const r = await this.enviar(
          `/api/servicos/${encodeURIComponent(modal.servico.id)}/branch`, { branch: b.nome });
        this.git = r.git;
        if (this.topo === modal) this.fecharTopo(true);
        this.avisar(`${modal.servico.nome} agora esta em ${b.nome}.`, "ok");
      } catch (err) {
        modal.erro = err.message;
      } finally {
        modal.trocando = "";
      }
    },

    limpoEEmDia(g) {
      return !this.mexidos(g) && !g.conflitos && !g.frente && !g.atras && g.upstream;
    },

    tituloGit(g) {
      const partes = [`branch ${g.branch || "?"}`];
      if (g.upstream) partes.push(`upstream ${g.upstream}`);
      else partes.push("sem upstream configurado");
      if (g.atras) partes.push(`${g.atras} commit(s) atras do remoto`);
      if (g.frente) partes.push(`${g.frente} commit(s) a frente`);
      if (g.preparados) partes.push(`${g.preparados} no indice`);
      if (g.modificados) partes.push(`${g.modificados} modificado(s)`);
      if (g.nao_rastreados) partes.push(`${g.nao_rastreados} nao rastreado(s)`);
      if (g.conflitos) partes.push(`${g.conflitos} em conflito`);
      if (g.commit && g.commit.hash) {
        partes.push(`ultimo: ${g.commit.hash} ${g.commit.resumo} (${g.commit.autor}, ${g.commit.quando})`);
      }
      if (g.erro) partes.push("erro: " + g.erro);
      return partes.join("\n");
    },

    // ------------------------------------------------------------------
    // painel git (modal grande por projeto)
    // ------------------------------------------------------------------
    async abrirPainelGit(s, aba = "alteracoes") {
      this.menuAberto = null;
      this.abrir({
        tipo: "git", servico: s, aba, painel: null, carregando: true, ocupado: "", erro: "", saida: "",
        sel: null, diff: "", diffCortado: false, carregandoDiff: false,
        mensagem: this.rascunhos[s.id] || "",
        historico: null, refHistorico: "", commitSel: "", commitDiff: "", commitCortado: false, carregandoHist: false,
        branches: null, carregandoBranches: false, novoBranch: "", filtroBranch: "",
        prs: null, erroPrs: "", carregandoPrs: false,
        stashMsg: "",
      });
      const modal = this.topo; // o proxy reativo
      await this.recarregarPainel(modal);
      this.trocarAba(modal, aba);
    },

    urlGit(m, resto = "") {
      return `/api/servicos/${encodeURIComponent(m.servico.id)}/git${resto}`;
    },

    estadoPainel(m) {
      return (m.painel && m.painel.estado) || {};
    },

    async recarregarPainel(m) {
      m.carregando = true;
      try {
        m.painel = await this.pedir(this.urlGit(m));
        m.erro = "";
        this.revalidarSelecao(m);
      } catch (err) {
        m.erro = err.message;
      } finally {
        m.carregando = false;
      }
    },

    // Botao de reler: tudo que ja foi carregado em alguma aba e lido de novo.
    async recarregarTudo(m) {
      await this.recarregarPainel(m);
      m.historico = null;
      m.branches = null;
      m.prs = null;
      m.erroPrs = "";
      this.trocarAba(m, m.aba);
    },

    // Com o painel aberto, um commit feito no editor (ou um arquivo salvo) chega pelo evento
    // "git" do servidor: o painel se rele sozinho, sem precisar clicar em nada.
    sincronizarPainelGit(estados) {
      const m = this.modais.find((x) => x.tipo === "git");
      if (!m || m.ocupado || m.carregando || !m.painel) return;
      const novo = estados[m.servico.id];
      if (!novo) return;
      const chave = (e) => [e.branch, e.upstream, e.frente, e.atras, e.preparados, e.modificados,
        e.nao_rastreados, e.conflitos, e.adicoes, e.remocoes, e.commit && e.commit.hash].join("|");
      if (chave(novo) !== chave(m.painel.estado || {})) {
        this.recarregarPainel(m);
        m.historico = null;
        if (m.aba === "historico") this.carregarHistorico(m);
      }
    },

    trocarAba(m, aba) {
      if (m.aba !== aba) m.erro = ""; // erro de outra aba so confundiria
      m.aba = aba;
      if (aba === "historico") {
        if (!m.historico) this.carregarHistorico(m);
        if (!m.branches) this.carregarBranches(m); // para o seletor de branch
      } else if (aba === "branches" && !m.branches) {
        this.carregarBranches(m);
      } else if (aba === "prs" && !m.prs && !m.carregandoPrs) {
        this.carregarPrs(m);
      }
    },

    contaAba(m, aba) {
      if (!m.painel) return 0;
      if (aba === "alteracoes") return m.painel.arquivos.length;
      if (aba === "stash") return m.painel.stashes.length;
      if (aba === "prs") return m.prs ? m.prs.prs.length : 0;
      return 0;
    },

    // ---- alteracoes ----
    preparados(m) {
      return ((m.painel && m.painel.arquivos) || []).filter((a) => a.indice && !a.conflito);
    },
    naoPreparados(m) {
      return ((m.painel && m.painel.arquivos) || []).filter((a) => a.trabalho);
    },
    nomeArquivo(caminho) {
      return caminho.split("/").pop();
    },
    pastaArquivo(caminho) {
      const i = caminho.lastIndexOf("/");
      return i < 0 ? "" : caminho.slice(0, i);
    },
    // Letra que aparece na lista: a do git, menos para arquivo novo (+) e conflito (!).
    letraArquivo(a, preparado) {
      if (a.conflito) return "!";
      const l = preparado ? a.indice : a.trabalho;
      return l === "?" ? "+" : l;
    },
    classeLetra(a, preparado) {
      const l = this.letraArquivo(a, preparado);
      return { "+": "novo", A: "novo", M: "mod", D: "del", R: "ren", C: "ren", "!": "conf" }[l] || "mod";
    },
    descricaoArquivo(a, preparado) {
      if (a.conflito) return "em conflito";
      const l = preparado ? a.indice : a.trabalho;
      return { "?": "novo, fora do git", A: "adicionado", M: "modificado", D: "apagado", R: "renomeado",
        C: "copiado", T: "mudou de tipo" }[l] || l;
    },
    selecionado(m, a, preparado) {
      return m.sel && m.sel.caminho === a.caminho && m.sel.preparado === preparado;
    },

    async verDiff(m, a, preparado) {
      m.sel = { caminho: a.caminho, preparado };
      m.carregandoDiff = true;
      m.diff = "";
      const pedido = m.sel;
      try {
        const r = await this.pedir(this.urlGit(m,
          `/diff?caminho=${encodeURIComponent(a.caminho)}&preparado=${preparado ? 1 : 0}`));
        // Clique rapido em outro arquivo: a resposta antiga nao pode sobrescrever a nova.
        if (m.sel !== pedido) return;
        m.diff = r.diff;
        m.diffCortado = r.cortado;
      } catch (err) {
        if (m.sel === pedido) m.erro = err.message;
      } finally {
        if (m.sel === pedido) m.carregandoDiff = false;
      }
    },

    // Depois de reler o painel: o arquivo aberto continua do mesmo lado? Rele o diff. Saiu
    // da lista (commitado, descartado)? Fecha.
    revalidarSelecao(m) {
      if (!m.sel) return;
      const lista = m.sel.preparado ? this.preparados(m) : this.naoPreparados(m);
      const outra = m.sel.preparado ? this.naoPreparados(m) : this.preparados(m);
      const a = lista.find((x) => x.caminho === m.sel.caminho);
      // Preparou ou tirou da preparacao: o diff acompanha o arquivo para o outro lado.
      const mudou = !a && outra.find((x) => x.caminho === m.sel.caminho);
      if (a) {
        this.verDiff(m, a, m.sel.preparado);
      } else if (mudou) {
        this.verDiff(m, mudou, !m.sel.preparado);
      } else {
        m.sel = null;
        m.diff = "";
      }
    },

    // acaoGit e o caminho unico das acoes que mudam o repositorio. opcoes.confirmar abre o
    // modal de confirmacao antes; opcoes.ok vira aviso verde no fim.
    async acaoGit(m, acao, corpo = {}, opcoes = {}) {
      if (m.ocupado) return false;
      if (opcoes.confirmar) {
        const ok = await this.confirmar(opcoes.confirmar, { titulo: opcoes.titulo, botao: opcoes.botao });
        if (!ok) return false;
      }
      m.ocupado = acao;
      m.erro = "";
      m.saida = "";
      try {
        const r = await this.enviar(this.urlGit(m, `/${acao}`), corpo);
        m.painel = r.painel;
        if (r.git) this.git = r.git;
        m.saida = r.saida || "";
        if (opcoes.ok) this.avisar(opcoes.ok, "ok");
        return true;
      } catch (err) {
        m.erro = err.message;
        const painel = err.corpo && err.corpo.painel;
        if (painel && painel.estado && painel.estado.repo) m.painel = painel;
        return false;
      } finally {
        m.ocupado = "";
        this.revalidarSelecao(m);
        // O que mudou no repositorio pode mudar historico e branches: rele o que estiver a
        // vista e esquece o resto.
        m.historico = null;
        m.branches = null;
        if (m.aba === "historico" || m.aba === "branches") this.trocarAba(m, m.aba);
      }
    },

    preparar(m, arquivos) {
      return this.acaoGit(m, "preparar", { caminhos: arquivos.map((a) => a.caminho) });
    },
    despreparar(m, arquivos) {
      return this.acaoGit(m, "despreparar", { caminhos: arquivos.map((a) => a.caminho) });
    },

    descartar(m, arquivos) {
      const lista = arquivos.filter((a) => !a.conflito);
      if (!lista.length) return;
      const novos = lista.filter((a) => a.trabalho === "?").length;
      let texto;
      if (lista.length === 1) {
        texto = novos
          ? `${lista[0].caminho} nao esta no git: descartar APAGA o arquivo do disco.`
          : `Descartar as alteracoes de ${lista[0].caminho}? O arquivo volta a ser como esta no ultimo commit (ou no que ja foi preparado).`;
      } else {
        texto = `Descartar as alteracoes de ${lista.length} arquivo(s)?` +
          (novos ? ` ${novos} arquivo(s) novo(s), fora do git, serao APAGADOS do disco.` : "") +
          " O que ja foi preparado continua.";
      }
      return this.acaoGit(m, "descartar", { caminhos: lista.map((a) => a.caminho) }, {
        confirmar: texto + " Isso nao tem volta.", titulo: "descartar alteracoes", botao: "descartar",
      });
    },

    async commitar(m, push) {
      if (!m.mensagem.trim() || !this.preparados(m).length) return;
      const ok = await this.acaoGit(m, "commit", { mensagem: m.mensagem, push }, {
        ok: push ? "commit feito e enviado." : "commit feito.",
      });
      if (ok) {
        m.mensagem = "";
        delete this.rascunhos[m.servico.id];
      }
    },

    // ---- sincronizar com o remoto ----
    gitPull(m, rebase) {
      if (!rebase) return this.acaoGit(m, "pull", {}, { ok: "pull concluido." });
      return this.acaoGit(m, "pull", { rebase: true }, {
        titulo: "pull com rebase",
        botao: "puxar com rebase",
        confirmar: "O rebase reescreve os seus commits locais que ainda nao foram enviados, " +
          "reaplicando cada um em cima do que veio do remoto. Se houver conflito, o rebase fica " +
          "parado esperando voce resolver (ou abortar). Continuar?",
        ok: "pull com rebase concluido.",
      });
    },
    gitPush(m) {
      return this.acaoGit(m, "push", {}, { ok: "push concluido." });
    },
    gitAbortar(m) {
      const tipo = m.painel.em_andamento;
      return this.acaoGit(m, "abortar", {}, {
        titulo: `abortar ${tipo}`,
        botao: `abortar ${tipo}`,
        confirmar: `O repositorio volta ao estado de antes do ${tipo}. O que voce ja resolveu nos conflitos se perde.`,
      });
    },

    // ---- historico ----
    async carregarHistorico(m) {
      m.carregandoHist = true;
      try {
        const ref = m.refHistorico ? `?ref=${encodeURIComponent(m.refHistorico)}` : "";
        m.historico = await this.pedir(this.urlGit(m, `/historico${ref}`));
        const ainda = m.historico.find((c) => c.hash === m.commitSel);
        if (!ainda && m.historico.length) this.verCommit(m, m.historico[0]);
        if (!m.historico.length) { m.commitSel = ""; m.commitDiff = ""; }
      } catch (err) {
        m.erro = err.message;
        m.historico = [];
      } finally {
        m.carregandoHist = false;
      }
    },
    recarregarHistorico(m) {
      m.historico = null;
      m.commitSel = "";
      this.carregarHistorico(m);
    },
    async verCommit(m, c) {
      m.commitSel = c.hash;
      m.commitDiff = "";
      try {
        const r = await this.pedir(this.urlGit(m, `/commits/${c.hash}`));
        if (m.commitSel !== c.hash) return;
        m.commitDiff = r.diff;
        m.commitCortado = r.cortado;
      } catch (err) {
        m.erro = err.message;
      }
    },

    // ---- branches ----
    async carregarBranches(m) {
      m.carregandoBranches = true;
      try {
        m.branches = await this.pedir(this.urlGit(m, "/branches"));
      } catch (err) {
        m.erro = err.message;
        m.branches = [];
      } finally {
        m.carregandoBranches = false;
      }
    },
    branchesFiltrados(m) {
      const f = (m.filtroBranch || "").trim().toLowerCase();
      return (m.branches || []).filter((b) => !f || b.nome.toLowerCase().includes(f));
    },
    rotuloBranch(b) {
      return b.remoto ? `${b.remoto}/${b.nome}` : b.nome;
    },
    async criarBranch(m) {
      const nome = m.novoBranch.trim();
      if (!nome) return;
      const ok = await this.acaoGit(m, "criar-branch", { nome }, { ok: `branch ${nome} criado - voce ja esta nele.` });
      if (ok) m.novoBranch = "";
    },
    async trocarParaBranch(m, b) {
      if (m.ocupado) return;
      m.ocupado = "trocar";
      m.erro = "";
      try {
        const r = await this.enviar(`/api/servicos/${encodeURIComponent(m.servico.id)}/branch`, { branch: b.nome });
        this.git = r.git;
        this.avisar(`${m.servico.nome} agora esta em ${b.nome}.`, "ok");
      } catch (err) {
        m.erro = err.message;
      } finally {
        m.ocupado = "";
      }
      await this.recarregarPainel(m);
      m.historico = null;
      m.refHistorico = "";
      this.carregarBranches(m);
    },
    mergeBranch(m, b) {
      const atual = this.estadoPainel(m).branch;
      return this.acaoGit(m, "merge", { nome: b.nome }, {
        titulo: "merge",
        botao: `integrar ${b.nome}`,
        confirmar: `Integrar ${this.rotuloBranch(b)} em ${atual} (git merge)? Se houver conflito, o merge ` +
          "fica parado esperando voce resolver os arquivos ou abortar.",
        ok: `${b.nome} integrado em ${atual}.`,
      });
    },
    async apagarBranch(m, b) {
      const ok = await this.acaoGit(m, "apagar-branch", { nome: b.nome }, {
        titulo: "apagar branch",
        botao: "apagar branch",
        confirmar: `Apagar o branch local ${b.nome}? O branch no remoto (se existir) nao e tocado.`,
        ok: `branch ${b.nome} apagado.`,
      });
      // Branch com commit que so existe nele: segunda confirmacao, bem explicita.
      if (!ok && m.erro.includes("nao foram integrados")) {
        m.erro = "";
        await this.acaoGit(m, "apagar-branch", { nome: b.nome, forcar: true }, {
          titulo: "apagar mesmo assim?",
          botao: "apagar e perder os commits",
          confirmar: `${b.nome} tem commits que nao estao em nenhum outro branch. Apagar assim faz esses ` +
            "commits sumirem. Tem certeza?",
          ok: `branch ${b.nome} apagado.`,
        });
      }
    },
    verHistoricoDe(m, b) {
      m.refHistorico = b.atual ? "" : b.ref;
      m.historico = null;
      m.commitSel = "";
      this.trocarAba(m, "historico");
    },

    // ---- stash ----
    rotuloStash(s) {
      return `stash@{${s.indice}}`;
    },
    async guardarStash(m) {
      const ok = await this.acaoGit(m, "stash", { mensagem: m.stashMsg }, { ok: "alteracoes guardadas no stash." });
      if (ok) m.stashMsg = "";
    },
    aplicarStash(m, s, remover) {
      return this.acaoGit(m, remover ? "stash-pop" : "stash-aplicar", { indice: s.indice }, {
        ok: remover ? "stash aplicado e removido." : "stash aplicado.",
      });
    },
    apagarStash(m, s) {
      return this.acaoGit(m, "stash-apagar", { indice: s.indice }, {
        titulo: "apagar stash",
        botao: "apagar stash",
        confirmar: `Apagar ${this.rotuloStash(s)} (${s.resumo})? As alteracoes guardadas nele se perdem.`,
        ok: "stash apagado.",
      });
    },

    // ---- pull requests ----
    async carregarPrs(m) {
      m.carregandoPrs = true;
      m.erroPrs = "";
      try {
        m.prs = await this.pedir(this.urlGit(m, "/prs"));
      } catch (err) {
        m.erroPrs = err.message;
      } finally {
        m.carregandoPrs = false;
      }
    },
    prDoBranch(m) {
      const branch = this.estadoPainel(m).branch;
      return m.prs && m.prs.prs.find((p) => p.branch === branch);
    },
    linkNovoPR(m) {
      const branch = this.estadoPainel(m).branch;
      if (!m.prs || !m.prs.url || !branch || branch === m.prs.padrao) return "";
      return `${m.prs.url}/compare/${branch.split("/").map(encodeURIComponent).join("/")}?expand=1`;
    },
    rotuloChecks(c) {
      return { ok: "checks ok", falhou: "checks falharam", pendente: "checks rodando" }[c] || c;
    },
    tempoRelativo(iso) {
      const s = (Date.now() - Date.parse(iso)) / 1000;
      if (!isFinite(s)) return "";
      if (s < 3600) return `ha ${Math.max(1, Math.round(s / 60))} min`;
      if (s < 86400) return `ha ${Math.round(s / 3600)} h`;
      const d = Math.round(s / 86400);
      return `ha ${d} ${d === 1 ? "dia" : "dias"}`;
    },
    quandoPt,

    // ------------------------------------------------------------------
    // docker
    // ------------------------------------------------------------------
    async atualizarDocker() {
      try {
        this.docker = await this.pedir("/api/docker");
      } catch (err) {
        this.docker = { rodando: false, iniciando: false, containers: [], mensagem: err.message };
      }
    },

    async acaoContainer(c, acao) {
      const rotulos = { start: "iniciando", stop: "parando", restart: "reiniciando" };
      this.avisar(`${rotulos[acao]} ${c.nome}...`);
      try {
        await this.enviar(`/api/docker/containers/${encodeURIComponent(c.nome)}/${acao}`);
        await this.atualizarDocker();
        this.avisar(`${c.nome}: ${acao} ok.`, "ok");
      } catch (err) {
        this.avisar(`${c.nome}: ${err.message}`, "erro");
      }
    },

    async trocarPolitica(c, politica) {
      const anterior = c.politica;
      try {
        await this.enviar(`/api/docker/containers/${encodeURIComponent(c.nome)}/politica`, { politica });
        await this.atualizarDocker();
        this.avisar(`${c.nome}: reinicio automatico agora e "${politica}".`, "ok");
      } catch (err) {
        c.politica = anterior; // o select ja mudou na tela; volta ao que o docker tem
        this.avisar(`${c.nome}: ${err.message}`, "erro");
      }
    },

    async mostrarLogContainer(nome) {
      this.logAtual = null;
      this.logContainer = nome;
      const alvo = this.$refs.log;
      alvo.textContent = "";
      try {
        const r = await this.pedir(`/api/docker/containers/${encodeURIComponent(nome)}/logs?linhas=300`);
        const linhas = (r.texto || "").split("\n");
        if (!r.texto) this.acrescentarLog("(container sem log)");
        else linhas.forEach((l) => this.acrescentarLog(l));
      } catch (err) {
        this.acrescentarLog("nao consegui ler o log: " + err.message);
      }
    },

    async abrirDocker() {
      this.docker.iniciando = true;
      try {
        await this.enviar("/api/docker/abrir");
        this.avisar("abrindo o Docker Desktop - isso pode levar um minuto.", "ok");
      } catch (err) {
        this.docker.iniciando = false;
        this.avisar(err.message, "erro");
      }
    },

    // portasCurtas troca "0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp" por "5432".
    portasCurtas(portas) {
      if (!portas) return "";
      const achadas = [...portas.matchAll(/:(\d+)->/g)].map((m) => m[1]);
      return [...new Set(achadas)].join(" ");
    },

    async encerrarLauncher() {
      const ok = await this.confirmar(
        "encerrar o launcher? os projetos em modo gerenciado sao derrubados junto (containers ficam no ar).");
      if (!ok) return;
      try {
        await this.enviar("/api/encerrar");
        this.encerrado = true;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    async abrirNoSistema(s, alvo) {
      this.menuAberto = null;
      try {
        const r = await this.enviar("/api/abrir", { id: s.id, alvo });
        this.avisar(`abrindo ${r.caminho}`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    async copiarComando(s) {
      this.menuAberto = null;
      const texto = s.tipo === "docker"
        ? `docker compose -p ${s.compose.projeto} -f "${s.compose.arquivo}" up -d ${s.compose.servico}`
        : s.cmd;
      try {
        await navigator.clipboard.writeText(texto);
        this.avisar("comando copiado.", "ok");
      } catch {
        this.avisar(texto); // sem permissao de area de transferencia: mostra para copiar na mao
      }
    },

    // ------------------------------------------------------------------
    // selecao, modo e dependencias
    // ------------------------------------------------------------------
    aoSelecionar() {
      this.salvarEdicao();
      this.atualizarPlano();
    },
    trocarModo(s, modo) {
      s.modo = modo;
      this.salvarEdicao();
    },

    // salvarEdicao manda selecao/modo/dependencias com um respiro de 350ms: marcar varios
    // cartoes seguidos vira uma gravacao so.
    salvarEdicao() {
      clearTimeout(this.salvandoEm);
      return new Promise((ok, falhou) => {
        this.salvandoEm = setTimeout(async () => {
          const servicos = this.config.servicos.map((s) => ({
            id: s.id,
            selecionado: !!s.selecionado,
            modo: s.modo || "",
            depende: s.depende,
          }));
          try {
            await this.enviar("/api/config", { servicos });
            ok();
          } catch (err) {
            this.avisar(err.message, "erro");
            // Ciclo recusado pelo servidor: recarrega para a tela voltar ao que esta gravado.
            await this.recarregarConfig();
            falhou(err);
          }
        }, 350);
      });
    },

    async recarregarConfig() {
      const dados = await this.pedir("/api/estado");
      this.config = dados.config;
      this.atualizarPlano();
    },

    async atualizarPlano() {
      if (!this.selecionados.length) {
        this.plano = {};
        return;
      }
      try {
        this.plano = await this.enviar("/api/plano", { ids: this.selecionados });
      } catch (err) {
        this.plano = {};
        this.avisar(err.message, "erro");
      }
    },

    // ------------------------------------------------------------------
    // arrastar e soltar
    // ------------------------------------------------------------------
    iniciarArraste(ev, s) {
      this.arraste.id = s.id;
      this.menuAberto = null;
      ev.dataTransfer.effectAllowed = "move";
      // Firefox so dispara o arraste se algo for escrito no dataTransfer.
      ev.dataTransfer.setData("text/plain", s.id);
    },
    marcarAlvo(grupo, s) {
      if (!this.arraste.id || this.arraste.id === s.id) return;
      this.arraste.grupo = grupo;
      this.arraste.antes = s.id;
    },
    limparArraste() {
      this.arraste = { id: null, grupo: null, antes: null };
    },
    async soltar(grupo, antes) {
      const id = this.arraste.id;
      this.limparArraste();
      if (!id) return;
      const s = this.servicoDe(id);
      if (s && s.grupo === grupo && !antes) return; // soltou no proprio grupo, sem posicao
      try {
        const r = await this.enviar(`/api/servicos/${encodeURIComponent(id)}/mover`, { grupo, antes });
        this.config = r.config;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // ------------------------------------------------------------------
    // grupos
    // ------------------------------------------------------------------
    async novoGrupo() {
      const nome = await this.pedirTexto("novo grupo", "nome do grupo (ex.: uma empresa ou produto)", "");
      if (!nome) return;
      try {
        const r = await this.enviar("/api/grupos", { nome });
        this.config = r.config;
        this.avisar(`grupo "${nome}" criado.`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async renomearGrupo(grupo) {
      const nome = await this.pedirTexto("renomear grupo", "nome do grupo", grupo.nome);
      if (!nome) return;
      try {
        const r = await this.enviar("/api/grupos", { id: grupo.id, nome });
        this.config = r.config;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async apagarGrupo(grupo) {
      if (!(await this.confirmar(`apagar o grupo "${grupo.nome}"?`))) return;
      try {
        const r = await this.enviar(`/api/grupos/${encodeURIComponent(grupo.id)}`, undefined, "DELETE");
        this.config = r.config;
        this.avisar("grupo apagado.", "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async moverGrupo(id, passo) {
      const ordem = this.config.grupos.map((g) => g.id);
      const i = ordem.indexOf(id);
      const j = i + passo;
      if (j < 0 || j >= ordem.length) return;
      [ordem[i], ordem[j]] = [ordem[j], ordem[i]];
      try {
        const r = await this.enviar("/api/grupos/ordem", { ids: ordem });
        this.config = r.config;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // ------------------------------------------------------------------
    // perfis
    // ------------------------------------------------------------------
    // Abrir um perfil: a tela mostra so os projetos dele, ja marcados. "todos" volta a
    // mostrar tudo sem mexer na selecao.
    async aplicarPerfil(p) {
      const id = p ? p.id : "todos";
      try {
        const r = await this.enviar(`/api/perfis/${encodeURIComponent(id)}/aplicar`);
        this.config = r.config;
        this.atualizarPlano();
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // editarPerfil abre o editor: nome e a lista de projetos para marcar. Perfil novo comeca
    // com o que esta selecionado agora.
    editarPerfil(p) {
      this.abrir({
        tipo: "perfil", perfil: p, erro: "",
        nome: p ? p.nome : "",
        marcados: p ? [...p.servicos] : [...this.selecionados],
      });
      this.$nextTick(() => this.$refs.entradaPerfil?.focus());
    },
    alternarGrupoNoPerfil(m, grupo) {
      const ids = this.config.servicos.filter((s) => s.grupo === grupo).map((s) => s.id);
      const todos = ids.every((id) => m.marcados.includes(id));
      m.marcados = todos ? m.marcados.filter((id) => !ids.includes(id)) : [...new Set([...m.marcados, ...ids])];
    },
    marcadosNoGrupo(m, grupo) {
      return this.config.servicos.filter((s) => s.grupo === grupo && m.marcados.includes(s.id)).length;
    },
    async salvarPerfilEditor() {
      const m = this.topo;
      const nome = (m.nome || "").trim();
      if (!nome) return (m.erro = "de um nome ao perfil.");
      if (!m.marcados.length) return (m.erro = "marque pelo menos um projeto.");
      try {
        const r = await this.enviar("/api/perfis", { id: m.perfil ? m.perfil.id : "", nome, servicos: m.marcados });
        this.config = r.config;
        this.atualizarPlano();
        this.fecharTopo(true);
        this.avisar(`perfil "${nome}" salvo com ${m.marcados.length} projeto(s).`, "ok");
      } catch (err) {
        m.erro = err.message;
      }
    },
    async apagarPerfil(p) {
      if (!(await this.confirmar(`Apagar o perfil "${p.nome}"? Os projetos continuam cadastrados - so o atalho some.`,
        { titulo: "apagar perfil", botao: "apagar perfil" }))) return;
      try {
        const r = await this.enviar(`/api/perfis/${encodeURIComponent(p.id)}`, undefined, "DELETE");
        this.config = r.config;
        this.fecharTopo(true);
        this.avisar("perfil apagado.", "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    // ------------------------------------------------------------------
    // cadastro de projeto
    // ------------------------------------------------------------------
    formularioServico(s, grupoPadrao) {
      const f = FORM_VAZIO();
      if (s) {
        Object.assign(f, {
          id: s.id, nome: s.nome, grupo: s.grupo, tipo: s.tipo,
          dir: s.dir || "", cmd: s.cmd || "", modo: s.modo || "gerenciado",
          composeArquivo: s.compose?.arquivo || "", composeProjeto: s.compose?.projeto || "",
          composeServico: s.compose?.servico || "",
          servicosCompose: s.compose?.servico ? [s.compose.servico] : [],
          container: s.container || "", porta: s.porta || null, url: s.url || "",
          prontoTipo: s.pronto.tipo, timeout: s.timeout || 120,
          depende: [...s.depende],
          selecionado: !!s.selecionado,
          prontoDetalhe: s.pronto.tipo === "comando" ? (s.pronto.cmd || []).join(" ")
            : s.pronto.tipo === "http" ? (s.pronto.url || "") : String(s.pronto.porta || ""),
        });
      } else {
        f.grupo = grupoPadrao || this.config.grupos[0]?.id || "";
      }
      this.form = f;
      this.menuAberto = null;
      this.abrir({ tipo: "servico" });
      this.$nextTick(() => this.$refs.entradaNome?.focus());
    },

    trocarTipo(tipo) {
      this.form.tipo = tipo;
      // "processo rodando" so existe para aplicacao: container volta para a checagem padrao.
      if (tipo === "docker" && this.form.prontoTipo === "processo") this.form.prontoTipo = "porta";
    },

    aoTrocarChecagem() {
      // O detalhe de um tipo nao serve para o outro (porta nao e url nem comando).
      this.form.prontoDetalhe = "";
      // Na aba do terminal o launcher nao enxerga o processo, entao esta checagem obriga
      // o modo gerenciado.
      if (this.form.prontoTipo === "processo") this.form.modo = "gerenciado";
    },

    ajudaChecagem() {
      const tipo = this.form.prontoTipo;
      if (tipo === "processo") return "pronto enquanto o processo estiver de pe - para bot, worker ou script que nao abre porta";
      if (tipo === "http") return "url que precisa responder (qualquer resposta abaixo de 500 conta)";
      if (tipo === "comando") return "comando que devolve 0 quando o projeto esta pronto";
      return "em branco = a porta do projeto";
    },

    // validarForm devolve o primeiro problema do cadastro apontando o campo. O servidor
    // valida de novo, mas a mensagem dele fala em nome de campo do config.
    validarForm() {
      const f = this.form;
      if (!(f.nome || "").trim()) return "de um nome ao projeto.";
      if (f.tipo === "app") {
        if (!(f.dir || "").trim()) return "escolha a pasta do projeto.";
        if (!(f.cmd || "").trim()) return "informe o comando que sobe o projeto.";
      } else {
        if (!(f.composeArquivo || "").trim()) return "escolha o arquivo compose.";
        if (!(f.composeProjeto || "").trim()) return "informe o nome do projeto compose.";
        if (!f.composeServico) return "escolha o servico dentro do compose.";
      }
      const c = this.montarChecagem();
      if (c.tipo === "porta" && !(c.porta > 0 && c.porta <= 65535)) {
        return f.tipo === "app"
          ? 'informe a porta do projeto - ou, se ele nao abre porta (bot, worker, script), troque "como saber que ficou pronto" para "processo rodando".'
          : 'informe a porta do container - ou troque "como saber que ficou pronto" para http ou comando.';
      }
      if (c.tipo === "http" && !c.url) return "informe a url da checagem http no detalhe da checagem.";
      if (c.tipo === "comando" && !c.cmd.length) return "informe o comando da checagem no detalhe da checagem.";
      return "";
    },

    async salvarServico() {
      const f = this.form;
      f.erro = this.validarForm();
      if (f.erro) return;
      const entrada = {
        id: f.id, nome: (f.nome || "").trim(), grupo: f.grupo, tipo: f.tipo,
        porta: Number(f.porta) || 0, url: (f.url || "").trim(),
        timeout: Number(f.timeout) || 120, depende: f.depende,
        selecionado: f.selecionado, caminho_livre: f.caminho_livre,
        pronto: this.montarChecagem(),
        // Com um perfil aberto, o projeto novo entra nele - senao sumiria da tela ao salvar.
        perfil: !f.id && this.config.perfil_ativo ? this.config.perfil_ativo : "",
      };
      if (f.tipo === "app") {
        entrada.dir = (f.dir || "").trim();
        entrada.cmd = (f.cmd || "").trim();
        entrada.modo = f.modo;
      } else {
        entrada.compose = {
          projeto: (f.composeProjeto || "").trim(),
          arquivo: (f.composeArquivo || "").trim(),
          servico: f.composeServico,
        };
        entrada.container = (f.container || "").trim();
      }

      // Porta repetida nao impede o cadastro (pode ser que os dois nunca subam juntos), mas
      // avisa: a checagem de "pronto" olha a porta, entao um projeto daria como no ar por
      // causa do outro.
      const conflito = this.config.servicos.find(
        (o) => o.porta && o.porta === entrada.porta && o.id !== entrada.id);

      try {
        const r = await this.enviar("/api/servicos", entrada);
        this.config = r.config;
        this.fecharTopo(true);
        this.atualizarPlano();
        this.avisar(f.id ? "projeto atualizado." : `projeto "${entrada.nome}" cadastrado.`, "ok");
        if (conflito) this.avisar(`atencao: a porta ${entrada.porta} ja e usada por ${conflito.nome}.`);
      } catch (err) {
        f.erro = err.message;
      }
    },

    montarChecagem() {
      const detalhe = (this.form.prontoDetalhe || "").trim();
      if (this.form.prontoTipo === "processo") return { tipo: "processo" };
      if (this.form.prontoTipo === "comando") return { tipo: "comando", cmd: detalhe.split(/\s+/).filter(Boolean) };
      if (this.form.prontoTipo === "http") return { tipo: "http", url: detalhe };
      return { tipo: "porta", porta: Number(detalhe) || Number(this.form.porta) || 0 };
    },

    async apagarServico() {
      const nome = this.form.nome;
      if (!(await this.confirmar(`apagar "${nome}" do launcher? (nao mexe nos arquivos do projeto)`))) return;
      try {
        const r = await this.enviar(`/api/servicos/${encodeURIComponent(this.form.id)}`, undefined, "DELETE");
        this.config = r.config;
        if (this.logAtual === this.form.id) this.logAtual = null;
        this.fecharTopo(true);
        this.atualizarPlano();
        this.avisar("projeto removido do launcher.", "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    usarSugestao(s) {
      this.form.cmd = s.cmd;
      if (s.porta && !this.form.porta) {
        this.form.porta = s.porta;
        if (this.form.prontoTipo === "porta") this.form.prontoDetalhe = String(s.porta);
      }
    },

    // ------------------------------------------------------------------
    // seletor de pastas
    // ------------------------------------------------------------------
    // ------------------------------------------------------------------
    // adicionar projetos a partir de uma pasta (o launcher descobre como rodar cada um)
    // ------------------------------------------------------------------
    abrirImportar(grupo) {
      this.menuAberto = null;
      this.abrir({
        tipo: "importar", etapa: "pasta", erro: "", carregando: false, salvando: false, livre: false,
        pasta: { caminho: "", pai: "", raiz: "", itens: [] },
        resultado: null, linhas: [],
        grupo: grupo || "", grupoNovo: "", noPerfil: !!this.config.perfil_ativo, esperar: true,
      });
      this.navegarImportar(this.topo, "");
    },
    async navegarImportar(m, caminho) {
      m.erro = "";
      try {
        m.pasta = await this.pedir(`/api/pastas?caminho=${encodeURIComponent(caminho || "")}${m.livre ? "&livre=1" : ""}`);
      } catch (err) {
        m.erro = err.message;
      }
    },
    async procurarProjetos(m, caminho) {
      m.carregando = true;
      m.erro = "";
      try {
        const alvo = caminho || m.pasta.caminho;
        const r = await this.pedir(`/api/descobrir?caminho=${encodeURIComponent(alvo)}${m.livre ? "&livre=1" : ""}`);
        m.resultado = r;
        m.linhas = r.projetos.map((d, i) => ({ ...d, chave: i, marcado: !d.cadastrado, porta: d.porta || null, cmd: d.cmd || "" }));
        // Varios sistemas na mesma pasta pedem um grupo proprio com o nome dela; um so vai
        // para o primeiro grupo que ja existe.
        if (!m.grupo || m.grupo === "__novo") {
          const novos = m.linhas.filter((l) => l.marcado).length;
          if (novos > 1 || !this.config.grupos.length || m.grupo === "__novo") {
            m.grupo = "__novo";
            m.grupoNovo = r.nome;
          } else {
            m.grupo = this.config.grupos[0].id;
          }
        }
        m.etapa = "resultado";
        if (!r.projetos.length) m.erro = "nenhum projeto reconhecido nesta pasta - escolha outra ou use o cadastro manual.";
      } catch (err) {
        m.erro = err.message;
      } finally {
        m.carregando = false;
      }
    },
    linhasMarcadas(m) {
      return m.linhas.filter((l) => l.marcado);
    },
    temAppEContainer(m) {
      const ls = this.linhasMarcadas(m);
      return ls.some((l) => l.tipo === "app") && ls.some((l) => l.tipo === "docker");
    },
    marcarTodasLinhas(m, valor) {
      for (const l of m.linhas) if (!valor || !l.cadastrado) l.marcado = valor;
    },
    rotuloTecnologia(t) {
      return { node: "Node", go: "Go", java: "Java", dotnet: ".NET", python: "Python", php: "PHP", ruby: "Ruby",
        rust: "Rust", script: "script", make: "make", compose: "docker" }[t] || t;
    },
    nomeDoCadastrado(id) {
      return (this.config.servicos.find((s) => s.id === id) || {}).nome || id;
    },
    // Porta repetida nao impede (os dois podem nunca subir juntos), mas a tela avisa.
    portaEmUso(l) {
      const porta = Number(l.porta);
      if (!porta) return "";
      const outro = this.config.servicos.find((s) => s.porta === porta && s.id !== l.cadastrado);
      return outro ? outro.nome : "";
    },
    async importarProjetos() {
      const m = this.topo;
      const linhas = this.linhasMarcadas(m);
      if (!linhas.length) return (m.erro = "marque pelo menos um projeto.");
      for (const l of linhas) {
        if (!(l.nome || "").trim()) return (m.erro = "um dos projetos marcados esta sem nome.");
        if (l.tipo === "app" && !(l.cmd || "").trim()) return (m.erro = `informe o comando de ${l.nome}.`);
      }
      if (m.grupo === "__novo" && !(m.grupoNovo || "").trim()) return (m.erro = "de um nome ao grupo novo.");

      const projetos = linhas.map((l) => {
        const porta = Number(l.porta) || 0;
        const base = { nome: l.nome.trim(), tipo: l.tipo, porta, timeout: 120, depende: [], selecionado: false };
        if (l.tipo === "app") {
          return {
            ...base, dir: l.dir, cmd: l.cmd.trim(), modo: "gerenciado",
            url: porta ? `http://localhost:${porta}` : "",
            // Sem porta, "pronto" e o processo continuar de pe (worker, bot, script).
            pronto: porta ? { tipo: "porta", porta } : { tipo: "processo" },
          };
        }
        return {
          ...base, compose: l.compose, container: l.container,
          // Container sem porta publicada: pronto quando esta rodando ("docker top" falha se nao).
          pronto: porta ? { tipo: "porta", porta } : { tipo: "comando", cmd: ["docker", "top", l.container] },
        };
      });

      m.salvando = true;
      m.erro = "";
      try {
        const r = await this.enviar("/api/importar", {
          grupo: m.grupo === "__novo" ? "" : m.grupo,
          grupo_novo: m.grupo === "__novo" ? m.grupoNovo.trim() : "",
          perfil: m.noPerfil ? this.config.perfil_ativo || "" : "",
          esperar_containers: m.esperar && this.temAppEContainer(m),
          caminho_livre: m.livre,
          projetos,
        });
        this.config = r.config;
        this.atualizarPlano();
        this.atualizarGit(false);
        this.fecharTopo(true);
        this.avisar(`${projetos.length} projeto(s) adicionado(s).`, "ok");
      } catch (err) {
        m.erro = err.message;
      } finally {
        m.salvando = false;
      }
    },
    // Caso que a descoberta nao cobre: cai no formulario completo.
    cadastroManual() {
      const m = this.topo;
      const grupo = m.grupo && m.grupo !== "__novo" ? m.grupo : undefined;
      this.fecharTopo(true);
      this.formularioServico(null, grupo);
    },

    escolherPasta(alvo) {
      const inicial = alvo === "compose"
        ? (this.form.composeArquivo || "").replace(/[\\/][^\\/]*$/, "")
        : this.form.dir;
      this.abrir({ tipo: "pasta", alvo });
      this.navegar(inicial);
    },

    async navegar(caminho) {
      const livre = this.form.caminho_livre ? "&livre=1" : "";
      try {
        this.pastas = await this.pedir(`/api/pastas?caminho=${encodeURIComponent(caminho || "")}${livre}`);
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },

    async usarPasta() {
      const alvo = this.topo.alvo;
      const pasta = this.pastas.caminho;
      this.fecharTopo(true);

      let insp;
      try {
        const livre = this.form.caminho_livre ? "&livre=1" : "";
        insp = await this.pedir(`/api/inspecionar?caminho=${encodeURIComponent(pasta)}${livre}`);
      } catch (err) {
        return this.avisar(err.message, "erro");
      }

      if (alvo === "compose") {
        if (!insp.composes.length) return this.avisar("nenhum docker-compose nessa pasta.", "erro");
        const arquivo = insp.composes[0];
        this.form.composeArquivo = `${pasta}\\${arquivo.arquivo}`;
        this.form.servicosCompose = arquivo.servicos;
        this.form.composeServico = arquivo.servicos[0] || "";
        if (!this.form.composeProjeto) {
          this.form.composeProjeto = pasta.split(/[\\/]/).filter(Boolean).pop().replace(/[^a-z0-9]/gi, "").toLowerCase();
        }
        return;
      }

      this.form.dir = pasta;
      if (!this.form.nome) this.form.nome = pasta.split(/[\\/]/).filter(Boolean).pop();
      this.form.sugestoes = insp.sugestoes;
    },

    // ------------------------------------------------------------------
    // modais (pilha: o seletor de pastas abre por cima do formulario)
    // ------------------------------------------------------------------
    abrir(modal) {
      this.modais.push(modal);
      return modal;
    },
    fecharTopo(concluido) {
      const modal = this.modais.pop();
      if (modal && modal.tipo === "git") this.rascunhos[modal.servico.id] = modal.mensagem;
      if (modal && !concluido && modal.resolver) modal.resolver(null);
    },
    // Clique fora fecha os modais leves. O formulario de projeto nao: um clique torto jogaria
    // fora tudo que foi digitado.
    fecharPeloFundo() {
      const t = this.topo;
      // Mesmo cuidado para a lista de projetos encontrados e para o editor de perfil: muita
      // marcacao feita para um clique torto jogar fora.
      if (t.tipo === "servico" || t.tipo === "perfil" || (t.tipo === "importar" && t.etapa === "resultado")) return;
      this.fecharTopo();
    },
    pedirTexto(titulo, rotulo, valor) {
      return new Promise((resolver) => {
        this.abrir({ tipo: "texto", titulo, rotulo, valor: valor || "", resolver });
        this.$nextTick(() => this.$refs.entradaTexto?.select());
      });
    },
    confirmarTexto() {
      const modal = this.topo;
      const valor = (modal.valor || "").trim();
      this.fecharTopo(true);
      modal.resolver(valor || null);
    },
    // opcoes: { titulo, botao } - o botao diz o que vai acontecer ("apagar branch"), em vez
    // de um "confirmar" generico.
    confirmar(texto, opcoes = {}) {
      return new Promise((resolver) => {
        this.abrir({ tipo: "confirmar", texto, titulo: opcoes.titulo, botao: opcoes.botao, resolver: (v) => resolver(!!v) });
      });
    },

    // ------------------------------------------------------------------
    // eventos ao vivo
    // ------------------------------------------------------------------
    conectarEventos() {
      const fonte = new EventSource("/api/eventos");
      fonte.onmessage = (ev) => {
        const evento = JSON.parse(ev.data);
        if (evento.tipo === "estado") {
          for (const e of evento.estados) this.estados[e.id] = e;
          this.agora = Date.now(); // acerta o relogio na hora, sem esperar o proximo segundo
        } else if (evento.tipo === "log") {
          if (evento.id === this.logAtual) this.acrescentarLog(evento.linha);
        } else if (evento.tipo === "config") {
          this.config = evento.config;
          this.faltando = evento.faltando || [];
        } else if (evento.tipo === "docker") {
          this.docker = evento.docker;
        } else if (evento.tipo === "git") {
          this.git = evento.git;
          this.buscandoGit = false;
          this.sincronizarPainelGit(evento.git);
        } else if (evento.tipo === "aviso") {
          this.avisar(evento.texto, evento.erro ? "erro" : "");
        } else if (evento.tipo === "fim") {
          this.resumirSubida(evento.ids || []);
          this.atualizarPlano();
        }
      };
    },

    // ------------------------------------------------------------------
    // log (fora do Vue de proposito: o log e append puro, e reconstruir 500
    // linhas a cada linha nova so gastaria trabalho a toa)
    // ------------------------------------------------------------------
    async mostrarLog(id) {
      this.logAtual = id;
      this.logContainer = null;
      const alvo = this.$refs.log;
      alvo.textContent = "";
      try {
        const r = await this.pedir(`/api/logs?id=${encodeURIComponent(id)}`);
        (r.linhas || []).forEach((l) => this.acrescentarLog(l));
      } catch (err) {
        this.acrescentarLog("nao consegui ler o log: " + err.message);
      }
    },
    acrescentarLog(linha) {
      const alvo = this.$refs.log;
      if (!alvo) return;
      // So rola sozinho se o usuario ja estava no fim - senao atrapalha quem esta lendo o meio.
      const noFim = alvo.scrollHeight - alvo.scrollTop - alvo.clientHeight < 40;
      const no = document.createElement("span");
      if (linha.startsWith("> ")) no.className = "eco";
      else if (/\b(error|erro|exception|panic|fatal|failed|falhou)\b/i.test(linha)) no.className = "ruim";
      else if (/\b(warn|warning|aviso)\b/i.test(linha)) no.className = "atencao";
      no.textContent = linha + "\n";
      alvo.appendChild(no);
      if (noFim) alvo.scrollTop = alvo.scrollHeight;
    },
    // limparLog so limpa a tela: o historico continua no servidor e volta ao reabrir o log.
    limparLog() {
      if (this.$refs.log) this.$refs.log.textContent = "";
    },

    // ------------------------------------------------------------------
    // avisos
    // ------------------------------------------------------------------
    avisar(texto, tipo = "") {
      const id = this.proximoAviso++;
      // No maximo cinco na tela: uma subida grande nao pode cobrir os cartoes de avisos.
      this.avisos = [...this.avisos.slice(-4), { id, texto, tipo }];
      // Erro fica o dobro do tempo - e o que a pessoa precisa conseguir ler.
      setTimeout(() => this.fecharAviso(id), tipo === "erro" ? 12000 : 5000);
    },
    fecharAviso(id) {
      this.avisos = this.avisos.filter((a) => a.id !== id);
    },
  },
}).component("visor-diff", VisorDiff).mount("#app");
