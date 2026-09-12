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

const FORM_VAZIO = () => ({
  id: "", nome: "", grupo: "", tipo: "app",
  dir: "", cmd: "", modo: "gerenciado", caminho_livre: false,
  composeArquivo: "", composeProjeto: "", composeServico: "", servicosCompose: [],
  container: "", porta: null, url: "",
  prontoTipo: "porta", prontoDetalhe: "", timeout: 120,
  depende: [], selecionado: true, sugestoes: null,
});

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
    };
  },

  computed: {
    selecionados() {
      return this.config.servicos.filter((s) => s.selecionado).map((s) => s.id);
    },
    contagem() {
      const c = { pronto: 0, andando: 0, falha: 0 };
      for (const s of this.config.servicos) {
        const status = this.estado(s.id).status;
        if (status === "pronto") c.pronto++;
        else if (status === "subindo" || status === "esperando") c.andando++;
        else if (status === "erro" || status === "bloqueado") c.falha++;
      }
      return c;
    },
    subindo() {
      return this.contagem.andando > 0;
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
      if (t.tipo === "perfil") return `perfil · ${t.perfil.nome}`;
      if (t.tipo === "confirmar") return "confirmar";
      return t.titulo || "";
    },
  },

  async mounted() {
    try {
      const dados = await this.pedir("/api/estado");
      this.config = dados.config;
      this.raiz = dados.raiz;
      this.temWt = dados.terminal;
      for (const e of dados.estados) this.estados[e.id] = e;
    } catch (err) {
      return this.avisar("nao consegui falar com o launcher: " + err.message, "erro");
    }
    this.atualizarPlano();
    // O docker vem depois do primeiro desenho: com o engine parado a checagem demora
    // alguns segundos, e nao da para segurar a tela por causa disso.
    this.atualizarDocker();
    this.conectarEventos();
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape" && this.modais.length) this.fecharTopo();
    });
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
      if (!resp.ok) throw new Error(corpo.erro || `HTTP ${resp.status}`);
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
    textoEstado(id) {
      const e = this.estado(id);
      return e.mensagem ? `${ROTULO_STATUS[e.status]} · ${e.mensagem}` : ROTULO_STATUS[e.status];
    },
    nomeDe(id) {
      return this.config.servicos.find((s) => s.id === id)?.nome || id;
    },
    servicoDe(id) {
      return this.config.servicos.find((s) => s.id === id);
    },
    servicosDe(grupo) {
      return this.config.servicos.filter((s) => s.grupo === grupo);
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
        this.avisar(extras.length
          ? `subindo ${plano.ids.length} projetos (${extras.map(this.nomeDe).join(", ")} ${verbo} como dependencia).`
          : `subindo ${plano.ids.length} projeto(s).`, "ok");
        this.plano = plano;
      } catch (err) {
        this.avisar(err.message, "erro");
      }
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
      const nome = await this.pedirTexto("novo grupo", "nome do grupo (ex.: photonow)", "");
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
    async aplicarPerfil(p) {
      try {
        const r = await this.enviar(`/api/perfis/${encodeURIComponent(p.id)}/aplicar`);
        this.config = r.config;
        this.atualizarPlano();
        this.avisar(`perfil "${p.nome}": ${p.servicos.length} projeto(s) marcados.`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async salvarPerfilAtual() {
      if (!this.selecionados.length) return this.avisar("marque os projetos do perfil antes de salvar.", "erro");
      const nome = await this.pedirTexto("salvar perfil", "nome do perfil", "");
      if (!nome) return;
      try {
        const r = await this.enviar("/api/perfis", { nome, servicos: this.selecionados });
        this.config = r.config;
        this.avisar(`perfil "${nome}" salvo com ${this.selecionados.length} projeto(s).`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async gravarPerfilAtual(p) {
      if (!this.selecionados.length) return this.avisar("nenhum projeto marcado.", "erro");
      try {
        const r = await this.enviar("/api/perfis", { id: p.id, nome: p.nome, servicos: this.selecionados });
        this.config = r.config;
        this.fecharTopo(true);
        this.avisar(`perfil "${p.nome}" agora tem ${this.selecionados.length} projeto(s).`, "ok");
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async renomearPerfil(p) {
      const nome = await this.pedirTexto("renomear perfil", "nome do perfil", p.nome);
      if (!nome) return;
      try {
        const r = await this.enviar("/api/perfis", { id: p.id, nome, servicos: p.servicos });
        this.config = r.config;
        this.fecharTopo(true);
      } catch (err) {
        this.avisar(err.message, "erro");
      }
    },
    async apagarPerfil(p) {
      if (!(await this.confirmar(`apagar o perfil "${p.nome}"?`))) return;
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

    async salvarServico() {
      const f = this.form;
      const entrada = {
        id: f.id, nome: (f.nome || "").trim(), grupo: f.grupo, tipo: f.tipo,
        porta: Number(f.porta) || 0, url: (f.url || "").trim(),
        timeout: Number(f.timeout) || 120, depende: f.depende,
        selecionado: f.selecionado, caminho_livre: f.caminho_livre,
        pronto: this.montarChecagem(),
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
        this.avisar(err.message, "erro");
      }
    },

    montarChecagem() {
      const detalhe = (this.form.prontoDetalhe || "").trim();
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
      if (modal && !concluido && modal.resolver) modal.resolver(null);
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
    confirmar(texto) {
      return new Promise((resolver) => {
        this.abrir({ tipo: "confirmar", texto, resolver: (v) => resolver(!!v) });
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
        } else if (evento.tipo === "log") {
          if (evento.id === this.logAtual) this.acrescentarLog(evento.linha);
        } else if (evento.tipo === "config") {
          this.config = evento.config;
        } else if (evento.tipo === "docker") {
          this.docker = evento.docker;
        } else if (evento.tipo === "aviso") {
          this.avisar(evento.texto, evento.erro ? "erro" : "");
        } else if (evento.tipo === "fim") {
          this.avisar("subida encerrada.", "ok");
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
      no.textContent = linha + "\n";
      alvo.appendChild(no);
      if (noFim) alvo.scrollTop = alvo.scrollHeight;
    },

    // ------------------------------------------------------------------
    // avisos
    // ------------------------------------------------------------------
    avisar(texto, tipo = "") {
      const id = this.proximoAviso++;
      this.avisos.push({ id, texto, tipo });
      setTimeout(() => {
        this.avisos = this.avisos.filter((a) => a.id !== id);
      }, 6000);
    },
  },
}).mount("#app");
