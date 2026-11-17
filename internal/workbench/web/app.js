// SPDX-License-Identifier: GPL-3.0-or-later
// This frontend has no build step. Put breakpoints in api(), selectDevice(),
// savePoint() and operate() to follow the HTTP -> Go -> MMS -> feedback path.
import { createEngineeringUI } from "./engineering.js";
const $ = (selector) => document.querySelector(selector);
const esc = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const dictionaries = {
  en: {
    import: "Import SCL",
    workspace: "WORKSPACE",
    sample: "Load sample",
    stationSample: "Station sample",
    stationStart: "Start station",
    stationStop: "Stop station",
    stationHint:
      "Shared software bus · SV publishes current MU model values · unsynchronized",
    local: "Models and sessions stay in this process.",
    start: "Start simulator",
    stop: "Stop simulator",
    connectSim: "Connect client",
    model: "Data model",
    goose: "Datasets & GOOSE",
    client: "MMS client",
    structures: "Structures",
    reference: "OBJECT REFERENCE",
    type: "TYPE",
    value: "VALUE",
    auto: "Auto-refresh · 2s",
    events: "Event journal",
    clear: "Clear view",
    endpoint: "MMS ENDPOINT",
    connect: "Connect",
    clientHint: "Browse a simulator or connect to an existing IED.",
    listen: "MMS listen address",
    emptyTitle: "One model. A complete debugging workspace.",
    emptyText:
      "Import an SCD, ICD or CID file, or load the included bay model. Explore the model, edit process values and test real MMS control.",
    save: "Apply value",
    edit: "LOCAL ACQUISITION",
    wire: "MMS WIRE ADDRESS",
    controls: "SIMULATED CONTROL",
    interlock: "Block controls",
    selected: "Select an attribute to inspect its value and wire address.",
    connected: "Workspace online",
    engineering: "Engineering & reports",
    equipment: "Primary equipment",
    catalog: "Export point catalog",
    checkModel: "Check against connected IED",
    subscribe: "Subscribe + GI",
    stopReport: "Stop subscription",
    serviceConnection: "Connection",
    discoverServices: "Discover services",
    reportHint:
      "Report values and EntryID appear in the event journal. A BRCB resume cursor uses hexadecimal bytes.",
    playback: "Recordings",
    journalEvents: "Events",
    journalControls: "Controls",
    export: "Export JSON",
    simSection: "VALUE SIMULATION",
    simApply: "Start / update",
    simStop: "Stop",
    simPause: "Pause",
    simResume: "Resume",
    simRunning: "running",
    simPaused: "paused",
    simIdle: "stopped",
    generators: "ACTIVE GENERATORS",
    stopAll: "Stop all",
    recUpload: "Import capture",
    recEmpty:
      "Import a COMTRADE cfg + dat pair, then map a channel onto a model point for playback.",
    recChannels: "Channels",
    recMap: "Map channel to point",
    recPoint: "Model point",
    recInterval: "Interval ms",
    recDecimation: "Decimation",
    recLoop: "Loop",
    recActive: "Playback generators",
    recPlayback: "Play",
    recDelete: "Delete",
    recDuration: "duration",
  },
  zh: {
    import: "导入 SCL",
    workspace: "工作空间",
    sample: "载入示例",
    stationSample: "整站示例",
    stationStart: "启动整站",
    stationStop: "停止整站",
    stationHint: "共享软件总线 · SV 发布当前 MU 模型值 · 未同步",
    local: "模型和会话保存在当前进程内。",
    start: "启动模拟设备",
    stop: "停止模拟设备",
    connectSim: "连接 MMS 客户端",
    model: "数据模型",
    goose: "数据集与 GOOSE",
    client: "MMS 客户端",
    structures: "显示结构",
    reference: "对象引用",
    type: "类型",
    value: "当前值",
    auto: "自动刷新 · 2 秒",
    events: "事件日志",
    clear: "清空视图",
    endpoint: "MMS 设备地址",
    connect: "连接",
    clientHint: "浏览模拟设备或连接现有 IED。",
    listen: "MMS 监听地址",
    emptyTitle: "从一份模型开始，调试完整设备流程。",
    emptyText:
      "导入 SCD、ICD、CID 文件，或载入间隔设备示例。浏览模型、修改采集值，再通过实际 MMS 命令验证控制与反馈。",
    save: "应用采集值",
    edit: "本地采集更新",
    wire: "MMS 报文中的对象名",
    controls: "模拟控制",
    interlock: "闭锁控制",
    selected: "选择一个属性，查看类型、当前值和 MMS 地址。",
    connected: "工作空间已连接",
    engineering: "工程选点与报告",
    equipment: "一次设备",
    catalog: "导出模型点表",
    checkModel: "与已连接 IED 核对",
    subscribe: "订阅并总召",
    stopReport: "停止订阅",
    serviceConnection: "连接",
    discoverServices: "发现在线服务",
    reportHint:
      "报告值和 EntryID 显示在事件日志中。BRCB 续传标识填写十六进制字节。",
    playback: "录波回放",
    journalEvents: "事件",
    journalControls: "控制记录",
    export: "导出 JSON",
    simSection: "数值模拟",
    simApply: "启动 / 更新",
    simStop: "停止",
    simPause: "暂停",
    simResume: "恢复",
    simRunning: "运行中",
    simPaused: "已暂停",
    simIdle: "已停止",
    generators: "活跃模拟任务",
    stopAll: "全部停止",
    recUpload: "导入录波",
    recEmpty:
      "导入 COMTRADE 的 cfg + dat 文件对，再把通道映射到模型点位即可回放。",
    recChannels: "通道",
    recMap: "通道映射到点位",
    recPoint: "模型点位",
    recInterval: "间隔 ms",
    recDecimation: "抽点",
    recLoop: "循环",
    recActive: "回放任务",
    recPlayback: "回放",
    recDelete: "删除",
    recDuration: "时长",
  },
};
let language = localStorage.getItem("iedlab-language") || "en";
const tr = (key) => dictionaries[language][key] || key;
const state = {
  documents: [],
  devices: [],
  connections: [],
  stations: [],
  events: [],
  detail: null,
  deviceId: "",
  selected: "",
  tab: "model",
  fc: "",
  search: "",
  structures: false,
  editorDirty: false,
  simDirty: false,
  clientId: "",
  domain: "",
  remoteItem: "",
  remotePoint: null,
  eventFloor: 0,
  journalView: "events",
  recordings: [],
  remoteNames: [],
  receiver: null,
  catalog: null,
  catalogRef: null,
  equipment: "",
};
let detailTicket = 0;
let stationTicket = 0;
let refreshTimer;
let toastTimer;
let lastRows = "";
let lastInspector = "";

// Only api() performs requests. Failed operations remain visible instead of
// silently painting a successful state before the server has accepted them.
async function api(path, options = {}) {
  const config = { ...options };
  if (config.body && !(config.body instanceof Blob)) {
    config.headers = { "Content-Type": "application/json", ...config.headers };
    config.body = JSON.stringify(config.body);
  }
  const response = await fetch("/api" + path, config);
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || response.statusText);
  return result;
}
function notify(message, error = false) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.className = "toast" + (error ? " error" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(
    () => toast.classList.add("hidden"),
    error ? 6500 : 3000,
  );
}

const engineeringUI = createEngineeringUI({api, state, esc, guard, notify, locale:() => language, readRemote});
function guard(fn) {
  return async (event) => {
    try {
      await fn(event);
    } catch (error) {
      notify(error.message, true);
    }
  };
}
function applyLanguage() {
  engineeringUI.languageChanged();
  document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
  document.querySelectorAll("[data-i18n]").forEach((element) => {
    element.textContent = tr(element.dataset.i18n);
  });
  $("#language").textContent = language === "en" ? "中文" : "EN";
  $("#live-state").innerHTML = "<i></i>" + esc(tr("connected"));
  $("#search").placeholder =
    language === "zh"
      ? "查找逻辑节点、属性或 MMS 名称…"
      : "Find a logical node, attribute or MMS name…";
  renderSummary();
  renderStation();
  renderInspector(true);
  if (state.tab === "playback") renderPlayback();
}
async function refreshState() {
  const result = await api("/state");
  state.documents = result.documents;
  state.devices = result.devices;
  state.connections = result.connections;
  state.events = result.events || [];
  state.stations = result.stations || [];
  stationTicket++;
  renderTree();
  renderEvents();
  renderStation();
  if (state.clientId && !state.connections.some((c) => c.id === state.clientId))
    state.clientId = "";
}
function renderStation() {
  const model = state.detail?.device?.model;
  const station = state.stations.find((s) => s.model === model);
  const button = $("#station-toggle");
  button.classList.toggle("hidden", !model);
  button.textContent = tr(station ? "stationStop" : "stationStart");
  $("#simulation-toggle").disabled = !!state.detail?.device?.station;
  const target = $("#station-inputs");
  target.classList.toggle("hidden", !station);
  if (!station) {
    target.innerHTML = "";
    return;
  }
  target.innerHTML =
    "<p>" +
    esc(tr("stationHint")) +
    " · " +
    esc(station.status) +
    (station.error ? " · " + esc(station.error) : "") +
    '</p><div class="station-grid">' +
    station.inputs
      .map(
        (input) =>
          '<div class="station-reading ' + (input.valid ? "valid" : "invalid") + '"><strong>' +
          esc(input.receiver + "/" + input.input) +
          "</strong><span>" +
          esc(input.service) +
          " · " +
          esc(input.value) +
          "</span><small>" +
          esc(input.error || input.source) +
          " · #" +
          (input.service === "GOOSE" ? input.state_number + "/" + input.sequence_number : input.counter) +
          " · " + esc(input.state) + " · " + esc(input.network) +
          (input.has_quality ? " · q=" + input.quality : "") +
          (input.service === "SV" ? " · smpSynch=" + input.sample_sync : "") +
          "</small><small>" + esc(input.received_at) +
          "</small></div>",
      )
      .join("") +
    "</div>";
}
async function refreshStation() {
  const ticket = ++stationTicket;
  const stations = await api("/stations");
  if (ticket !== stationTicket) return;
  state.stations = stations;
  renderStation();
}
function renderTree() {
  const tree = $("#model-tree");
  tree.innerHTML = state.documents
    .map(
      (doc) =>
        '<div class="tree-document"><div class="file-label"><span class="file-icon">SCL</span><span>' +
        esc(doc.name.split(/[\\/]/).pop()) +
        "</span></div>" +
        doc.accessPoints
          .map((ap, index) => {
            const current = state.detail?.device;
            const active =
              current?.model === doc.id &&
              current.ied === ap.ied &&
              current.ap === ap.ap;
            const running = state.devices.some(
              (d) =>
                d.model === doc.id &&
                d.ied === ap.ied &&
                d.ap === ap.ap &&
                d.status === "running",
            );
            const nodes = active
              ? [
                  ...new Set(
                    state.detail.points.map(
                      (p) => p.reference.split("/")[1].split(".")[0],
                    ),
                  ),
                ]
              : [];
            const branches = nodes.length
              ? '<div class="logical-tree">' +
                nodes
                  .map(
                    (ln) =>
                      '<button data-ln="' +
                      esc(ln) +
                      '"><span>└</span><span>⌑</span>' +
                      esc(ln) +
                      "</button>",
                  )
                  .join("") +
                "</div>"
              : "";
            return (
              '<button class="tree-ied' +
              (active ? " active" : "") +
              '" data-doc="' +
              doc.id +
              '" data-ap="' +
              index +
              '"><span class="node-icon">⌑</span><span><strong>' +
              esc(ap.ied) +
              "</strong><small>" +
              esc(ap.ap) +
              " · " +
              (ap.server ? "MMS server" : "Client AP") +
              "</small></span>" +
              (running ? '<i class="status-dot"></i>' : "") +
              "</button>" +
              branches
            );
          })
          .join("") +
        "</div>",
    )
    .join("");
  tree.querySelectorAll("[data-doc]").forEach((button) => {
    button.onclick = guard(async () => {
      const doc = state.documents.find((d) => d.id === button.dataset.doc);
      const ap = doc.accessPoints[Number(button.dataset.ap)];
      if (!ap.server) {
        const inputs = await api(
          "/models/" +
            doc.id +
            "/inputs?ied=" +
            encodeURIComponent(ap.ied) +
            "&ap=" +
            encodeURIComponent(ap.ap),
        );
        state.receiver = { name: ap.ied, inputs };
        showTab("goose");
        renderStreams();
        return;
      }
      await selectDevice(doc, ap);
    });
  });
  tree.querySelectorAll("[data-ln]").forEach((button) => {
    button.onclick = () => {
      state.search = button.dataset.ln + ".";
      $("#search").value = state.search;
      state.receiver = null;
      showTab("model");
      renderRows();
    };
  });
}
async function selectDevice(doc, ap) {
  state.catalogRef = { Model: doc.id, IED: ap.ied, AP: ap.ap };
  state.catalog = await api(
    "/models/" +
      doc.id +
      "/catalog?ied=" +
      encodeURIComponent(ap.ied) +
      "&ap=" +
      encodeURIComponent(ap.ap),
  );
  state.equipment = "";
  renderEngineering();
  let selected;
  try {
    selected = await api("/devices", {
      method: "POST",
      body: { Model: doc.id, IED: ap.ied, AP: ap.ap },
    });
  } catch (error) {
    state.deviceId = "";
    state.detail = null;
    for (const id of ["metrics", "simulation-toggle", "connect-sim"])
      $("#" + id).classList.add("hidden");
    showTab("engineering");
    notify(error.message, true);
    return;
  }
  state.deviceId = selected.id;
  state.selected = "";
  state.editorDirty = false;
  state.receiver = null;
  state.search = "";
  state.fc = "";
  $("#search").value = "";
  renderFilters();
  await refreshDetail();
  await refreshState();
  showTab("model");
}
async function refreshDetail() {
  if (!state.deviceId) return;
  const id = state.deviceId;
  const ticket = ++detailTicket;
  const detail = await api("/devices/" + id);
  // Switching devices can complete out of order. Older responses cannot replace
  // the user's current selection or overwrite an in-progress editor.
  if (ticket !== detailTicket || id !== state.deviceId) return;
  state.detail = detail;
  if (!state.selected) {
    const first =
      detail.points.find((p) => p.reference.endsWith(".Pos.stVal")) ||
      detail.points.find((p) => p.kind !== "structure");
    if (first) state.selected = pointKey(first);
  }
  $("#empty-state").classList.add("hidden");
  $("#data-workspace").classList.remove("hidden");
  $("#metrics").classList.remove("hidden");
  $("#simulation-toggle").classList.remove("hidden");
  renderSummary();
  renderRows();
  renderInspector();
  if (state.tab === "goose" && !state.receiver) renderStreams();
  if (state.tab === "playback") renderPlayback();
}
function pointKey(p) {
  return p.reference + "|" + p.fc;
}
function renderSummary() {
  if (!state.detail) return;
  const d = state.detail.device;
  $("#breadcrumb").textContent = "WORKSPACE / " + d.ied + " / " + d.ap;
  $("#device-title").textContent = d.ied;
  $("#device-subtitle").textContent =
    language === "zh"
      ? "SCL 工程模型 · 测量、控制与反馈"
      : "SCL engineering model · measurements, control and feedback";
  const leaves = state.detail.points.filter(
    (p) => p.kind !== "structure" && p.kind !== "array",
  ).length;
  const generators = state.detail.generators || [];
  $("#metrics").innerHTML = [
    '<div class="metric"><label>' +
      (language === "zh" ? "模型属性" : "MODEL ATTRIBUTES") +
      "</label><strong>" +
      leaves +
      "</strong></div>",
    '<div class="metric"><label>' +
      (language === "zh" ? "模拟设备" : "SIMULATOR") +
      '</label><span class="status-label ' +
      (d.status === "running" ? "" : "stopped") +
      '"><i class="status-dot"></i>' +
      esc(d.status) +
      "</span></div>",
    '<div class="metric"><label>' +
      esc(tr("generators")) +
      "</label><strong>" +
      generators.filter((g) => g.Running).length +
      "/" +
      generators.length +
      (generators.length
        ? ' <button id="stop-generators" class="text-button">' + esc(tr("stopAll")) + "</button>"
        : "") +
      "</strong></div>",
    '<div class="metric"><label>MMS ENDPOINT</label><strong class="metric-small">' +
      esc(d.address || "—") +
      "</strong></div>",
  ].join("");
  const stopAll = $("#stop-generators");
  if (stopAll)
    stopAll.onclick = guard(async () => {
      await api("/devices/" + state.deviceId + "/generators", { method: "DELETE" });
      await refreshDetail();
    });
  const button = $("#simulation-toggle");
  button.textContent = d.status === "running" ? tr("stop") : tr("start");
  button.classList.toggle("secondary", d.status === "running");
  $("#connect-sim").classList.toggle("hidden", d.status !== "running");
  if (d.error) $("#device-subtitle").textContent = d.error;
}
function renderFilters() {
  $("#fc-filters").innerHTML = ["", "ST", "MX", "CO", "CF", "DC"]
    .map(
      (fc) =>
        '<button class="chip ' +
        (state.fc === fc ? "active" : "") +
        '" data-fc="' +
        fc +
        '">' +
        (fc || "All") +
        "</button>",
    )
    .join("");
  $("#fc-filters")
    .querySelectorAll("button")
    .forEach((b) => {
      b.onclick = () => {
        state.fc = b.dataset.fc;
        renderFilters();
        renderRows();
      };
    });
}
function renderRows() {
  if (!state.detail) return;
  const query = state.search.toLowerCase();
  const rows = state.detail.points.filter(
    (p) =>
      (!state.equipment ||
        (state.catalog?.Points || []).some(
          (m) =>
            m.Reference === p.reference &&
            (m.Equipment || []).includes(state.equipment),
        )) &&
      (!state.fc || p.fc === state.fc) &&
      (state.structures || (p.kind !== "structure" && p.kind !== "array")) &&
      (p.reference + " " + p.item).toLowerCase().includes(query),
  );
  const fingerprint =
    JSON.stringify(
      rows.map((p) => [pointKey(p), p.kind, p.display, p.simulation]),
    ) +
    state.deviceId +
    state.selected +
    language;
  if (fingerprint === lastRows) return;
  lastRows = fingerprint;
  $("#point-rows").innerHTML = rows
    .map((p, index) => {
      const path = p.reference.split("/").slice(1).join("/");
      const dot = path.indexOf(".");
      const label =
        dot < 0
          ? esc(path)
          : '<span class="ln">' +
            esc(path.slice(0, dot)) +
            "</span>" +
            esc(path.slice(dot));
      const position =
        p.fc === "ST" && p.reference.endsWith(".Pos.stVal")
          ? { "00": "Intermediate", "01": "Open", 10: "Closed", 11: "Bad" }[
              p.value
            ]
          : "";
      const value = position
        ? position + " · " + p.value
        : p.kind === "structure"
          ? "{ " + (p.value?.length || 0) + " members }"
          : p.kind === "utc-time"
            ? String(p.value).replace("T", " ").replace("Z", "")
            : p.display;
      return (
        '<tr data-row="' +
        index +
        '" class="' +
        (pointKey(p) === state.selected ? "selected" : "") +
        '" tabindex="0"><td class="point-path" title="' +
        esc(p.reference) +
        '">' +
        label +
        (p.simulation
          ? ' <span class="sim-badge" title="' + esc(p.simulation) + '">SIM·' + esc(p.simulation) + "</span>"
          : "") +
        '</td><td><span class="fc ' +
        esc(p.fc) +
        '">' +
        esc(p.fc) +
        '</span></td><td class="point-type">' +
        esc(p.kind) +
        '</td><td class="value-text ' +
        (p.kind === "boolean" ? "bool" : "") +
        '" title="' +
        esc(value) +
        '">' +
        esc(value) +
        '</td><td style="color:#a4b3c1">›</td></tr>'
      );
    })
    .join("");
  $("#row-count").textContent =
    rows.length + " " + (language === "zh" ? "个属性" : "attributes");
  $("#point-rows")
    .querySelectorAll("tr")
    .forEach((row) => {
      const choose = () => {
        state.selected = pointKey(rows[Number(row.dataset.row)]);
        state.editorDirty = false;
        renderRows();
        renderInspector(true);
      };
      row.onclick = choose;
      row.onkeydown = (e) => {
        if (e.key === "Enter") choose();
      };
    });
}
function renderInspector(force = false) {
  const p = state.detail?.points.find(
    (point) => pointKey(point) === state.selected,
  );
  if (!p) {
    $("#inspector").innerHTML =
      '<div class="inspector-empty">' + esc(tr("selected")) + "</div>";
    return;
  }
  if ((state.editorDirty || state.simDirty) && !force) return;
  const controls = state.detail.controls.filter((c) =>
    p.reference.startsWith(c.reference.replace(/\.Oper$/, "") + "."),
  );
  const control = controls[0];
  const gen = (state.detail.generators || []).find(
    (g) => g.Reference === p.reference && g.FC === p.fc,
  );
  const d = state.detail.device;
  const fingerprint = JSON.stringify([
    d.id,
    pointKey(p),
    p.value,
    d.status,
    d.interlocked,
    controls,
    language,
    gen && [gen.Mode, gen.Running, gen.Paused, gen.Last, gen.SequenceIndex],
  ]);
  if (!force && fingerprint === lastInspector) return;
  lastInspector = fingerprint;
  $("#inspector").innerHTML =
    '<div class="inspector-top"><span class="fc ' +
    esc(p.fc) +
    '">' +
    esc(p.fc) +
    "</span><h3>" +
    esc(p.reference.split("/").pop()) +
    "</h3><p>" +
    esc(p.kind) +
    " · " +
    esc(p.domain) +
    '</p></div><div class="inspector-section"><div class="field-caption">' +
    esc(tr("edit")) +
    '</div><textarea id="value-editor" class="value-editor" spellcheck="false" aria-label="JSON process value" ' +
    (p.editable ? "" : "readonly") +
    ">" +
    esc(JSON.stringify(p.value, null, 2)) +
    '</textarea><div class="editor-actions"><span>JSON · ' +
    esc(p.kind) +
    '</span><button id="save-point" class="button primary" ' +
    (p.editable ? "" : "disabled") +
    ">" +
    esc(tr("save")) +
    '</button></div></div>' +
    (p.editable ? simulationHTML(p, gen) : "") +
    '<div class="inspector-section"><div class="field-caption">' +
    esc(tr("wire")) +
    '</div><div class="address-row"><label>DOMAIN</label><code>' +
    esc(p.domain) +
    '</code></div><div class="address-row"><label>ITEM</label><code>' +
    esc(p.item) +
    "</code></div></div>" +
    (control
      ? '<div class="inspector-section"><div class="field-caption">' +
        esc(tr("controls")) +
        '</div><code style="font-size:10px;color:#8191a2">' +
        esc(
          [
            "Status only",
            "Direct / normal security",
            "SBO / normal security",
            "Direct / enhanced security",
            "SBO / enhanced security",
          ][control.model] || "Unknown control model",
        ) +
        '</code><div class="controls"><button id="control-open" class="button secondary" ' +
        (!control.supported || d.status !== "running" ? "disabled" : "") +
        '>○ OPEN</button><button id="control-close" class="button primary" ' +
        (!control.supported || d.status !== "running" ? "disabled" : "") +
        '>● CLOSE</button></div><label class="interlock"><input id="block-control" type="checkbox" ' +
        (d.interlocked ? "checked" : "") +
        "> " +
        esc(tr("interlock")) +
        "</label>" +
        (control.reason
          ? '<p class="form-note">' + esc(control.reason) + "</p>"
          : "") +
        "</div>"
      : "");
  $("#value-editor").oninput = () => {
    state.editorDirty = true;
  };
  $("#save-point").onclick = guard(() => savePoint(p));
  if (p.editable) wireSimulation(p, gen);
  if (control) {
    $("#control-open").onclick = guard(() => operate(control.reference, false));
    $("#control-close").onclick = guard(() => operate(control.reference, true));
    $("#block-control").onchange = guard(async (e) => {
      await api("/devices/" + state.deviceId + "/interlock", {
        method: "POST",
        body: { Blocked: e.target.checked },
      });
      await refreshDetail();
    });
  }
}

// A generator animates this point through the local acquisition path. The
// form posts the whole configuration, so the same button starts and updates.
function simulationHTML(p, gen) {
  const mode = gen ? gen.Mode : "off";
  const field = (id, label, value, extra = "") =>
    '<label class="sim-field"><span>' +
    esc(label) +
    '</span><input id="' +
    id +
    '" type="number" step="any" value="' +
    (value ?? "") +
    '" ' +
    extra +
    "></label>";
  const modes = [
    ["off", "— off —"],
    ["random", language === "zh" ? "范围随机" : "Random range"],
    ["ramp", language === "zh" ? "累加 / 累减" : "Ramp ±"],
    ["sine", language === "zh" ? "正弦波" : "Sine"],
    ["sequence", language === "zh" ? "序列回放" : "Sequence"],
  ];
  const show = (m) => (mode === m ? " show" : "");
  const sequenceText = gen?.Values?.length
    ? gen.Values.map((v) => (typeof v === "string" ? JSON.stringify(v) : String(v))).join("\n")
    : "";
  return (
    '<div class="inspector-section" id="sim-root"><div class="field-caption">' +
    esc(tr("simSection")) +
    '</div><div class="sim-grid"><select id="sim-mode" aria-label="Simulation mode">' +
    modes
      .map(([v, l]) => '<option value="' + v + '"' + (mode === v ? " selected" : "") + ">" + l + "</option>")
      .join("") +
    "</select>" +
    field("sim-interval", language === "zh" ? "间隔 ms" : "Interval ms", gen ? gen.IntervalMS : 1000, 'min="100" step="100"') +
    "</div>" +
    '<div class="sim-fields' +
    show("random") +
    '" data-for="random ramp">' +
    field("sim-min", "Min", gen?.Min) +
    field("sim-max", "Max", gen?.Max) +
    "</div>" +
    '<div class="sim-fields' +
    show("ramp") +
    '" data-for="ramp">' +
    field("sim-step", "Step", gen?.Step) +
    '<label class="small-check"><input id="sim-wrap" type="checkbox"' +
    (gen?.Wrap ? " checked" : "") +
    "><span>" +
    (language === "zh" ? "达界回绕（不勾选则钳停在边界）" : "Wrap at bounds") +
    "</span></label></div>" +
    '<div class="sim-fields' +
    show("sine") +
    '" data-for="sine">' +
    field("sim-base", "Base", gen?.Base) +
    field("sim-amp", "Amplitude", gen?.Amplitude) +
    field("sim-freq", "Frequency Hz", gen?.FrequencyHz) +
    field("sim-phase", "Phase °", gen?.PhaseDeg) +
    "</div>" +
    '<div class="sim-fields' +
    show("sequence") +
    '" data-for="sequence">' +
    '<textarea id="sim-values" class="sim-values" rows="4" spellcheck="false" placeholder="' +
    (language === "zh" ? "每行一个 JSON 值，如：&#10;12.5&#10;13&#10;14.5" : "one JSON value per line, e.g.&#10;12.5&#10;13&#10;14.5") +
    '">' +
    esc(sequenceText) +
    '</textarea><label class="small-check"><input id="sim-loop" type="checkbox"' +
    (gen?.Loop ? " checked" : "") +
    "><span>" +
    esc(tr("recLoop")) +
    "</span></label><p>" +
    (language === "zh"
      ? "逐行输出；录波文件在“录波回放”标签页映射。"
      : "Values play one per interval; map recordings in the Recordings tab.") +
    "</p></div>" +
    '<div class="sim-actions"><button id="sim-apply" class="button secondary">' +
    esc(tr("simApply")) +
    "</button>" +
    (gen
      ? '<span class="sim-state' + (gen.Running ? " on" : "") + '">' +
        esc(gen.Running ? tr("simRunning") : gen.Paused ? tr("simPaused") : tr("simIdle")) +
        (gen.SequenceIndex ? " · #" + gen.SequenceIndex : "") +
        (gen.Last ? " · " + esc(gen.Last) : "") +
        '</span><button id="sim-pause" class="button secondary">' +
        esc(gen.Paused ? tr("simResume") : tr("simPause")) +
        '</button><button id="sim-stop" class="button secondary">' +
        esc(tr("simStop")) +
        "</button>"
      : "") +
    "</div></div>"
  );
}

function wireSimulation(p, gen) {
  const root = $("#sim-root");
  if (!root) return;
  // Any edit keeps the current form on screen across the 2 s refresh.
  root.querySelectorAll("input,select,textarea").forEach((el) => {
    el.oninput = () => {
      state.simDirty = true;
    };
  });
  $("#sim-mode").onchange = (event) => {
    state.simDirty = true;
    const mode = event.target.value;
    root.querySelectorAll(".sim-fields").forEach((group) => {
      group.classList.toggle("show", group.dataset.for.split(" ").includes(mode));
    });
  };
  $("#sim-apply").onclick = guard(async () => {
    const number = (id) => {
      const v = $("#" + id).value.trim();
      return v === "" ? undefined : Number(v);
    };
    const mode = $("#sim-mode").value;
    if (mode === "off") {
      await api("/devices/" + state.deviceId + "/generators?reference=" + encodeURIComponent(p.reference) + "&fc=" + p.fc, { method: "DELETE" });
      state.simDirty = false;
      await refreshDetail();
      return;
    }
    const body = {
      Reference: p.reference,
      FC: p.fc,
      Mode: mode,
      IntervalMS: Math.max(100, Math.round(Number($("#sim-interval").value) || 1000)),
    };
    if (mode === "random") {
      body.Min = number("sim-min");
      body.Max = number("sim-max");
    }
    if (mode === "ramp") {
      body.Step = number("sim-step");
      body.Min = number("sim-min");
      body.Max = number("sim-max");
      body.Wrap = $("#sim-wrap").checked;
    }
    if (mode === "sine") {
      body.Base = number("sim-base");
      body.Amplitude = number("sim-amp");
      body.FrequencyHz = number("sim-freq");
      body.PhaseDeg = number("sim-phase");
    }
    if (mode === "sequence") {
      const lines = $("#sim-values").value.split("\n").map((l) => l.trim()).filter(Boolean);
      if (!lines.length && !gen?.Recording) {
        notify(language === "zh" ? "序列模式需要至少一个值" : "Sequence mode needs at least one value", true);
        return;
      }
      if (lines.length)
        body.Values = lines.map((l) => {
          try {
            return JSON.parse(l);
          } catch {
            return l;
          }
        });
      body.Loop = $("#sim-loop").checked;
    }
    await api("/devices/" + state.deviceId + "/generators", { method: "POST", body });
    state.simDirty = false;
    await refreshDetail();
    notify(language === "zh" ? "模拟任务已更新" : "Simulation updated");
  });
  if (gen) {
    $("#sim-pause").onclick = guard(async () => {
      const body = generatorBody(gen);
      body.Paused = !gen.Paused;
      await api("/devices/" + state.deviceId + "/generators", { method: "POST", body });
      await refreshDetail();
    });
    $("#sim-stop").onclick = guard(async () => {
      await api("/devices/" + state.deviceId + "/generators?reference=" + encodeURIComponent(p.reference) + "&fc=" + p.fc, { method: "DELETE" });
      state.simDirty = false;
      await refreshDetail();
    });
  }
}

// Resend exactly what the status carries, so pause toggles do not restart the
// animation state (the backend compares configurations).
function generatorBody(gen) {
  return {
    Reference: gen.Reference,
    FC: gen.FC,
    Mode: gen.Mode,
    IntervalMS: gen.IntervalMS,
    Min: gen.Min,
    Max: gen.Max,
    Step: gen.Step,
    Wrap: gen.Wrap,
    Base: gen.Base,
    Amplitude: gen.Amplitude,
    FrequencyHz: gen.FrequencyHz,
    PhaseDeg: gen.PhaseDeg,
    Values: gen.Values?.length ? gen.Values : undefined,
    Loop: gen.Loop,
    Recording: gen.Recording || undefined,
    Channel: gen.Channel,
    Decimation: gen.Decimation,
  };
}
async function savePoint(point) {
  const value = JSON.parse($("#value-editor").value);
  await api("/devices/" + state.deviceId + "/values", {
    method: "POST",
    body: { Reference: point.reference, FC: point.fc, Value: value },
  });
  state.editorDirty = false;
  await refreshDetail();
  notify(language === "zh" ? "采集值已应用" : "Process value applied");
}
async function operate(reference, value) {
  await api("/devices/" + state.deviceId + "/operate", {
    method: "POST",
    body: { Reference: reference, Value: value },
  });
  state.editorDirty = false;
  await refreshDetail();
  notify(value ? "MMS close accepted" : "MMS open accepted");
}
function mmsName(n) {
  return (n.Domain ? n.Domain + "/" : "") + n.Item;
}
function renderStreams() {
  if (state.receiver) {
    $("#stream-content").innerHTML =
      '<div class="receiver-card"><h3>' +
      esc(state.receiver.name) +
      " · GOOSE inputs</h3>" +
      inputRows(state.receiver.inputs) +
      "</div>";
    return;
  }
  if (!state.detail) {
    $("#stream-content").innerHTML =
      '<div class="empty-state"><p>Import and select an IED to inspect datasets and GOOSE bindings.</p></div>';
    return;
  }
  const cards = state.detail.streams.map(
    (s) =>
      '<article class="stream-card"><div class="eyebrow">GOOSE CONTROL BLOCK</div><h3>' +
      esc(s.Reference) +
      "</h3><dl><dt>Dataset</dt><dd>" +
      esc(s.DataSet) +
      "</dd><dt>APPID / confRev</dt><dd>0x" +
      Number(s.APPID).toString(16).padStart(4, "0") +
      " / " +
      s.ConfRev +
      "</dd><dt>Destination</dt><dd>" +
      s.Destination.map((b) => b.toString(16).padStart(2, "0")).join(":") +
      "</dd><dt>VLAN / PCP</dt><dd>" +
      (s.VLAN ? s.VLAN.ID + " / " + s.VLAN.Priority : "untagged") +
      "</dd><dt>Retransmission</dt><dd>" +
      s.MinTime / 1000000 +
      "…" +
      s.MaxTime / 1000000 +
      ' ms</dd></dl><div class="field-caption">ORDERED ALLDATA</div>' +
      s.Members.map(
        (n, i) =>
          '<div class="member"><span>' +
          String(i).padStart(2, "0") +
          "</span>" +
          esc(mmsName(n)) +
          "</div>",
      ).join("") +
      "</article>",
  );
  const sets = state.detail.datasets.map(
    (set) =>
      '<article class="stream-card"><div class="eyebrow">STATIC MMS DATASET</div><h3>' +
      esc(mmsName(set.Name)) +
      "</h3>" +
      set.Variables.map(
        (v, i) =>
          '<div class="member"><span>' +
          i +
          "</span>" +
          esc(mmsName(v.Name)) +
          "</div>",
      ).join("") +
      "</article>",
  );
  $("#stream-content").innerHTML =
    '<div class="stream-grid">' +
    cards.join("") +
    sets.join("") +
    '<article class="stream-card"><div class="eyebrow">SUBSCRIBER EXTREF</div><h3>Engineered inputs</h3>' +
    inputRows(state.detail.inputs || []) +
    "</article></div>";
}
function inputRows(inputs) {
  return inputs.length
    ? inputs
        .map(
          (i) =>
            '<div class="member"><strong>' +
            esc((i.IntAddr || i.ReceiverLN + "/" + i.Index) + " · " + i.Service) +
            "</strong><p>" +
            esc(
              i.SourceIED +
                " / " +
                i.ControlLD +
                " / " +
                i.ControlLN +
                "." +
                i.ControlBlock,
            ) +
            "</p><span>" +
            esc(i.DataLN + "." + i.DataPath) +
            "</span></div>",
        )
        .join("")
    : '<p class="form-note">No ExtRef declarations in this access point.</p>';
}
function showTab(tab) {
  state.tab = tab;
  document
    .querySelectorAll("[data-tab]")
    .forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
  for (const name of ["model", "goose", "client", "playback", "engineering"])
    $("#" + name + "-view").classList.toggle("hidden", name !== tab);
  if (tab === "goose") renderStreams();
  if (tab === "client") renderConnections();
  if (tab === "playback") refreshRecordings().catch(() => renderPlayback());
  if (tab === "engineering") renderEngineering();
}

// Engineering metadata comes from the same Go SCL compiler used by the SDK.
// The browser only renders it and submits explicit online operations.
function renderEngineering() {
  const box = $("#engineering-content");
  if (!state.catalog) {
    box.textContent =
      language === "zh"
        ? "导入并选择工程中的 IED。"
        : "Import and select an engineered IED.";
    return;
  }
  const c = state.catalog;
  box.innerHTML =
    '<div class="stream-grid"><article class="stream-card"><h3>' +
    tr("equipment") +
    '</h3><button id="equipment-all" class="button secondary">' +
    (language === "zh" ? "查看全部测点" : "Show all points") +
    "</button>" +
    (c.Equipment || [])
      .map(
        (e, i) =>
          '<button class="button secondary equipment-pick" data-index="' +
          i +
          '">' +
          esc(e.Path) +
          "</button><p>" +
          esc(
            (e.Nodes || [])
              .map(
                (n) =>
                  n.IED + "/" + n.LD + "/" + n.Prefix + n.Class + n.Instance,
              )
              .join(", "),
          ) +
          "</p>",
      )
      .join("") +
    '</article><article class="stream-card"><h3>SCL ' +
    esc([c.Version, c.Revision, c.Release].filter(Boolean).join(" ")) +
    '</h3><button id="catalog-export" class="button secondary">' +
    tr("catalog") +
    '</button> <button id="catalog-check" class="button secondary">' +
    tr("checkModel") +
    '</button><pre id="catalog-result"></pre>' +
    (c.Diagnostics || [])
      .map(
        (d) =>
          '<p class="form-error">' +
          esc(d.Reference) +
          " · " +
          esc(d.Message) +
          "</p>",
      )
      .join("") +
    "</article></div>" +
    '<div class="stream-grid">' +
    (state.detail?.reports || [])
      .map(
        (r, i) =>
          '<article class="stream-card"><h3>' +
          (r.Buffered ? "BRCB " : "URCB ") +
          esc(mmsName(r.Name)) +
          "</h3><p>" +
          esc(mmsName(r.DataSet)) +
          " · ConfRev " +
          r.ConfRev +
          "</p>" +
          (r.Buffered
            ? '<label>Resume EntryID<input class="report-cursor" data-index="' +
              i +
              '" placeholder="hex (optional)" maxlength="256"></label>'
            : "") +
          '<button class="button primary report-start" data-index="' +
          i +
          '">' +
          tr("subscribe") +
          '</button> <button class="button secondary report-stop" data-index="' +
          i +
          '">' +
          tr("stopReport") +
          "</button></article>",
      )
      .join("") +
    '</div><p class="form-note">' +
    tr("reportHint") +
    "</p>" +
    '<div class="table-scroll"><table><thead><tr><th>Reference</th><th>FC / CDC</th><th>Type</th><th>Quality / time</th></tr></thead><tbody>' +
    (c.Points || [])
      .map(
        (p) =>
          "<tr><td>" +
          esc(p.Reference) +
          "</td><td>" +
          esc(p.FC + " / " + p.CDC) +
          "</td><td>" +
          esc(p.BasicType || p.Type.Kind) +
          "</td><td>" +
          esc(
            [p.Quality?.Item, p.Timestamp?.Item].filter(Boolean).join(" / "),
          ) +
          "</td></tr>",
      )
      .join("") +
    "</tbody></table></div>";
  box.querySelectorAll(".equipment-pick").forEach(
    (b) =>
      (b.onclick = () => {
        state.equipment = c.Equipment[Number(b.dataset.index)].Path;
        state.search = "";
        $("#search").value = "";
        showTab("model");
        renderRows();
        notify(state.equipment);
      }),
  );
  $("#equipment-all").onclick = () => {
    state.equipment = "";
    showTab("model");
    renderRows();
  };
  $("#catalog-export").onclick = () => {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(c, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = "iec61850-points.json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  $("#catalog-check").onclick = guard(async () => {
    if (!state.clientId)
      throw new Error(
        language === "zh"
          ? "先连接 MMS 客户端。"
          : "Connect an MMS client first.",
      );
    const issues = await api("/connections/" + state.clientId + "/check", {
      method: "POST",
      body: state.catalogRef,
    });
    $("#catalog-result").textContent = issues?.length
      ? issues.map((d) => d.Reference + " · " + d.Message).join("\n")
      : language === "zh"
        ? "在线类型和数据集顺序一致。"
        : "Online types and dataset order match.";
  });
  for (const action of ["start", "stop"])
    box.querySelectorAll(".report-" + action).forEach(
      (b) =>
        (b.onclick = guard(async () => {
          const r = state.detail.reports[Number(b.dataset.index)];
          if (!state.clientId) {
            const address = state.detail.device.address;
            if (!address) throw new Error("Connect an MMS client first.");
            await connect(address);
            showTab("engineering");
          }
          const endpoint = "/connections/" + state.clientId + "/reports";
          if (action === "stop")
            await api(
              endpoint +
                "?domain=" +
                encodeURIComponent(r.Name.Domain) +
                "&item=" +
                encodeURIComponent(r.Name.Item),
              { method: "DELETE" },
            );
          else {
            const hex =
              box
                .querySelector(
                  '.report-cursor[data-index="' + b.dataset.index + '"]',
                )
                ?.value.trim() || "";
            if (hex && (!/^[0-9a-f]+$/i.test(hex) || hex.length % 2))
              throw new Error(
                "EntryID must contain complete hexadecimal bytes.",
              );
            const options = { GI: !hex };
            if (hex)
              options.ResumeEntryID = btoa(
                String.fromCharCode(
                  ...hex.match(/../g).map((v) => parseInt(v, 16)),
                ),
              );
            await api(endpoint, {
              method: "POST",
              body: {
                Domain: r.Name.Domain,
                Item: r.Name.Item,
                Options: options,
              },
            });
          }
          notify(
            action === "start"
              ? "Report subscription enabled"
              : "Report subscription stopped",
          );
        })),
    );
  engineeringUI.project(box,state.catalogRef).catch(error => notify(error.message,true));
}
function renderEvents() {
  if (state.journalView === "controls") {
    renderControls();
    return;
  }
  const filter = $("#event-filter").value;
  const events = state.events.filter(
    (e) => e.id > state.eventFloor && (!filter || e.kind.includes(filter)),
  );
  $("#event-count").textContent = events.length;
  const target = $("#event-log");
  const nearEnd =
    target.scrollTop + target.clientHeight >= target.scrollHeight - 35;
  target.innerHTML = events
    .map(
      (e) =>
        '<div class="event-line ' +
        (e.kind === "error" || e.kind.includes("denied") ? "error" : "") +
        '"><time>' +
        new Date(e.time).toLocaleTimeString("en-GB") +
        '</time><span class="event-kind">' +
        esc(e.kind) +
        '</span><span class="event-message">' +
        esc(e.message) +
        "</span></div>",
    )
    .join("");
  if (nearEnd) target.scrollTop = target.scrollHeight;
}

// Structured control audit rides the same journal channel: control.record
// events carry the Control payload next to the readable message line.
function controlRecords() {
  return state.events.filter(
    (e) => e.id > state.eventFloor && e.kind === "control.record" && e.control,
  );
}
function renderControls() {
  const records = controlRecords();
  $("#event-count").textContent = records.length;
  const target = $("#event-log");
  const nearEnd =
    target.scrollTop + target.clientHeight >= target.scrollHeight - 35;
  const headings = ["", "DEVICE", "REFERENCE", "ACTION", "VALUE", "ORIGIN", "CTL#", "RESULT"];
  target.innerHTML =
    '<table class="controls-table"><thead><tr>' +
    headings.map((h) => "<th>" + h + "</th>").join("") +
    "</tr></thead><tbody>" +
    records
      .map((e) => {
        const c = e.control;
        // Show friendly names: IED name for local simulators, address for peers.
        const who =
          state.devices.find((d) => d.id === c.device)?.ied ||
          state.connections.find((x) => x.id === c.device)?.address ||
          c.device;
        return (
          '<tr class="' +
          (c.result ? "denied" : "") +
          '"><td><time>' +
          new Date(e.time).toLocaleTimeString("en-GB") +
          "</time></td><td>" +
          esc(who) +
          '</td><td class="point-path" title="' +
          esc(c.reference) +
          '">' +
          esc(c.reference) +
          '</td><td><span class="event-kind">' +
          esc(c.action) +
          "</span></td><td>" +
          esc(c.value || "—") +
          "</td><td>" +
          esc(c.origin || c.via || "—") +
          "</td><td>" +
          (c.ctlNum || "—") +
          "</td><td>" +
          esc(c.result || "ok") +
          "</td></tr>"
        );
      })
      .join("") +
    "</tbody></table>";
  if (nearEnd) target.scrollTop = target.scrollHeight;
}
function downloadJSON(name, value) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
}

// --- Recordings: COMTRADE import, channel mapping and playback -------------

async function refreshRecordings() {
  state.recordings = (await api("/recordings")) || [];
  if (state.tab === "playback") renderPlayback();
}

// Analog channels target numeric points; digital channels target boolean,
// integer and Dbpos status points. The backend re-validates with precise
// errors, this filter only keeps the list short.
function mappablePoints(digital) {
  const kinds = digital
    ? ["boolean", "integer", "unsigned", "bit-string"]
    : ["float", "integer", "unsigned"];
  return (state.detail?.points || []).filter(
    (p) => p.editable && kinds.includes(p.kind),
  );
}

function renderPlayback() {
  const box = $("#playback-content");
  if (!state.detail) {
    box.innerHTML =
      '<div class="empty-state"><p>' + esc(tr("recEmpty")) + "</p></div>";
    return;
  }
  const gens = (state.detail.generators || []).filter((g) => g.Recording);
  box.innerHTML =
    '<div class="stream-grid"><article class="stream-card rec-import"><h3>' +
    esc(tr("recUpload")) +
    '</h3><div class="rec-files"><label><span>.cfg</span><input type="file" id="rec-cfg" accept=".cfg,.CFG"></label>' +
    '<label><span>.dat</span><input type="file" id="rec-dat" accept=".dat,.DAT"></label>' +
    '<button id="rec-upload" class="button primary">' +
    esc(tr("recUpload")) +
    "</button></div><p>" +
    (language === "zh"
      ? "COMTRADE 1991/1999/2013 · ASCII、BINARY、BINARY32、FLOAT32。仅存于进程内存。"
      : "COMTRADE 1991/1999/2013 · ASCII, BINARY, BINARY32, FLOAT32. Kept in memory only.") +
    "</p></article>" +
    state.recordings.map(recordingCard).join("") +
    '<article class="stream-card"><h3>' +
    esc(tr("recActive")) +
    "</h3>" +
    (gens.length
      ? gens
          .map(
            (g) =>
              '<div class="rec-play"><span class="sim-state' +
              (g.Running ? " on" : "") +
              '">' +
              (g.Running ? tr("simRunning") : g.Paused ? tr("simPaused") : tr("simIdle")) +
              "</span><strong>" +
              esc(g.Reference) +
              "</strong><small>ch #" +
              g.Channel +
              " · " +
              g.IntervalMS +
              "ms" +
              (g.Decimation > 1 ? " · ÷" + g.Decimation : "") +
              (g.Loop ? " · loop" : "") +
              '</small><button class="button secondary rec-stop" data-ref="' +
              esc(g.Reference) +
              '" data-fc="' +
              esc(g.FC) +
              '">' +
              esc(tr("simStop")) +
              "</button></div>",
          )
          .join("")
      : '<p class="form-note">—</p>') +
    "</article></div>";
  $("#rec-upload").onclick = guard(async () => {
    const cfg = $("#rec-cfg").files[0];
    const dat = $("#rec-dat").files[0];
    if (!cfg || !dat) {
      notify(language === "zh" ? "请选择 cfg 和 dat 两个文件" : "Pick both a cfg and a dat file", true);
      return;
    }
    const form = new FormData();
    form.append("name", cfg.name.replace(/\.cfg$/i, ""));
    form.append("cfg", cfg);
    form.append("dat", dat);
    const response = await fetch("/api/recordings", { method: "POST", body: form });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || response.statusText);
    notify(language === "zh" ? "录波已导入" : "Recording imported");
    await refreshRecordings();
  });
  box.querySelectorAll("[data-rec]").forEach((card) => {
    wireRecordingCard(card.dataset.rec);
  });
  box.querySelectorAll(".rec-stop").forEach((button) => {
    button.onclick = guard(async () => {
      await api(
        "/devices/" +
          state.deviceId +
          "/generators?reference=" +
          encodeURIComponent(button.dataset.ref) +
          "&fc=" +
          button.dataset.fc,
        { method: "DELETE" },
      );
      await refreshDetail();
      renderPlayback();
    });
  });
}

function recordingCard(rec) {
  const channels = rec.channels || [];
  return (
    '<article class="stream-card" data-rec="' +
    esc(rec.id) +
    '"><h3>' +
    esc(rec.name) +
    "</h3><p>" +
    esc(rec.format) +
    " · " +
    (rec.rateHz || "?") +
    " Hz · " +
    rec.samples +
    " " +
    (language === "zh" ? "点" : "samples") +
    " · " +
    new Date(rec.start).toLocaleString() +
    "</p>" +
    '<table class="rec-table"><thead><tr><th>#</th><th>ID</th><th>PH</th><th>UNIT</th><th>MIN · MAX</th></tr></thead><tbody>' +
    channels
      .map(
        (c) =>
          "<tr><td>" +
          c.index +
          "</td><td>" +
          esc(c.id) +
          (c.digital ? ' <span class="fc ST">D</span>' : "") +
          "</td><td>" +
          esc(c.phase || "—") +
          "</td><td>" +
          esc(c.unit || "—") +
          "</td><td>" +
          (c.digital ? "0 · 1" : esc(fmtNum(c.min)) + " · " + esc(fmtNum(c.max))) +
          "</td></tr>",
      )
      .join("") +
    "</tbody></table>" +
    '<div class="rec-map"><span class="field-caption">' +
    esc(tr("recMap")) +
    "</span>" +
    '<div class="rec-map-row"><select class="rec-channel">' +
    channels
      .map((c) => '<option value="' + c.index + '">' + c.index + " · " + esc(c.id) + (c.digital ? " (D)" : "") + "</option>")
      .join("") +
    '</select><select class="rec-point"></select></div>' +
    '<div class="rec-map-row"><label>' +
    esc(tr("recInterval")) +
    '<input class="rec-interval" type="number" min="100" step="100" value="500"></label>' +
    "<label>" +
    esc(tr("recDecimation")) +
    '<input class="rec-decimation" type="number" min="1" step="1" value="1"></label>' +
    '<label class="small-check"><input class="rec-loop" type="checkbox" checked><span>' +
    esc(tr("recLoop")) +
    "</span></label>" +
    '<button class="button primary rec-play-btn">' +
    esc(tr("recPlayback")) +
    '</button><button class="button secondary rec-delete">' +
    esc(tr("recDelete")) +
    "</button></div>" +
    '<p class="form-note rec-duration"></p></div></article>'
  );
}

function fmtNum(v) {
  if (v === undefined || v === null || Number.isNaN(v)) return "—";
  return String(Math.round(v * 1000) / 1000);
}

// Fill the point picker per selected channel and keep the decimation hint in
// sync with the native sampling rate.
function wireRecordingCard(id) {
  const card = document.querySelector('[data-rec="' + CSS.escape(id) + '"]');
  if (!card) return;
  const rec = state.recordings.find((r) => r.id === id);
  if (!rec) return;
  const channelSelect = card.querySelector(".rec-channel");
  const pointSelect = card.querySelector(".rec-point");
  const interval = card.querySelector(".rec-interval");
  const decimation = card.querySelector(".rec-decimation");
  const duration = card.querySelector(".rec-duration");
  const fill = () => {
    const channel = (rec.channels || [])[Number(channelSelect.value)] || rec.channels[0];
    pointSelect.innerHTML = mappablePoints(channel?.digital)
      .map((p) => '<option value="' + esc(pointKey(p)) + '">' + esc(p.reference) + " [" + p.fc + "]</option>")
      .join("");
    updateHint();
  };
  const updateHint = () => {
    const intervalMS = Math.max(100, Number(interval.value) || 500);
    const dec = Math.max(1, Math.round(Number(decimation.value) || 1));
    const played = Math.ceil((rec.samples || 0) / dec);
    const seconds = (played * intervalMS) / 1000;
    duration.textContent =
      (language === "zh" ? "回放" : "plays") +
      " " +
      played +
      (language === "zh" ? " 点 · " : " values · ") +
      (seconds >= 60 ? Math.round(seconds / 60) + " min" : Math.round(seconds * 10) / 10 + " s") +
      (rec.rateHz
        ? (language === "zh" ? " · 原始 " : " · native ") + rec.rateHz + " Hz" +
          (dec === 1 && rec.rateHz * intervalMS / 1000 > 1
            ? (language === "zh" ? " · 建议抽点 ≥ " : " · suggest decimation ≥ ") + Math.round((rec.rateHz * intervalMS) / 1000)
            : "")
        : "");
  };
  channelSelect.onchange = fill;
  interval.oninput = updateHint;
  decimation.oninput = updateHint;
  fill();
  card.querySelector(".rec-play-btn").onclick = guard(async () => {
    const key = pointSelect.value.split("|");
    await api("/devices/" + state.deviceId + "/generators", {
      method: "POST",
      body: {
        Reference: key[0],
        FC: key[1],
        Mode: "sequence",
        IntervalMS: Math.max(100, Math.round(Number(interval.value) || 500)),
        Recording: rec.id,
        Channel: Number(channelSelect.value),
        Decimation: Math.max(1, Math.round(Number(decimation.value) || 1)),
        Loop: card.querySelector(".rec-loop").checked,
      },
    });
    await refreshDetail();
    renderPlayback();
    notify(language === "zh" ? "回放任务已启动" : "Playback started");
  });
  card.querySelector(".rec-delete").onclick = guard(async () => {
    await api("/recordings/" + rec.id, { method: "DELETE" });
    await refreshRecordings();
    await refreshDetail();
    renderPlayback();
  });
}
async function importDocument(file) {
  const doc = await api("/models", {
    method: "POST",
    headers: { "X-File-Name": encodeURIComponent(file.name) },
    body: file,
  });
  await refreshState();
  const first = doc.accessPoints.find((ap) => ap.server);
  if (first) await selectDevice(doc, first);
  notify("Imported " + doc.name);
}
async function loadSample() {
  const doc = await api("/models/sample", { method: "POST" });
  await refreshState();
  await selectDevice(
    doc,
    doc.accessPoints.find((ap) => ap.server),
  );
}
async function toggleSimulation() {
  if (!state.detail) return;
  if (state.detail.device.status === "running") {
    await api("/devices/" + state.deviceId + "/simulation", {
      method: "DELETE",
    });
    await refreshDetail();
    await refreshState();
  } else {
    $("#simulation-error").textContent = "";
    $("#simulation-dialog").showModal();
  }
}
async function connect(address) {
  const connection = await api("/connections", {
    method: "POST",
    body: { Address: address, Device: state.deviceId },
  });
  state.clientId = connection.id;
  $("#remote-address").value = address;
  await refreshState();
  showTab("client");
  await renderConnections();
}
async function renderConnections() {
  engineeringUI.connectionsChanged();
  const tree = $("#remote-tree");
  if (!state.connections.length) {
    tree.innerHTML = "<p>No active MMS connections.</p>";
    return;
  }
  tree.innerHTML = state.connections
    .map(
      (c) =>
        '<div class="remote-connection"><strong>' +
        esc(c.address) +
        '</strong><span class="remote-note">' +
        esc(c.identity.Vendor) +
        " · " +
        esc(c.status) +
        '</span><div id="domains-' +
        c.id +
        '"></div><button class="text-button disconnect" data-id="' +
        c.id +
        '">Disconnect</button></div>',
    )
    .join("");
  for (const c of state.connections) {
    if (c.status !== "connected") continue;
    try {
      const domains = await api("/connections/" + c.id + "/domains");
      const container = $("#domains-" + c.id);
      if (!container) continue;
      container.innerHTML = domains
        .map(
          (name, index) =>
            '<button class="remote-domain" data-index="' +
            index +
            '">' +
            esc(name) +
            "</button>",
        )
        .join("");
      container.querySelectorAll("button").forEach((b) => {
        b.onclick = guard(() => browse(c.id, domains[Number(b.dataset.index)]));
      });
    } catch (error) {
      notify(error.message, true);
    }
  }
  tree.querySelectorAll(".disconnect").forEach((b) => {
    b.onclick = guard(async () => {
      await api("/connections/" + b.dataset.id, { method: "DELETE" });
      await refreshState();
      await renderConnections();
      $("#remote-value").innerHTML = "";
      $("#remote-names").innerHTML = "";
    });
  });
}
async function browse(id, domain) {
  state.clientId = id;
  state.domain = domain;
  const names = await api(
    "/connections/" + id + "/names?domain=" + encodeURIComponent(domain),
  );
  state.remoteNames = names;
  $("#remote-breadcrumb").textContent = "REMOTE IED / " + domain;
  $("#remote-names").innerHTML = names
    .map(
      (name, index) =>
        '<button data-name="' + index + '">' + esc(name) + "</button>",
    )
    .join("");
  $("#remote-names")
    .querySelectorAll("button")
    .forEach((b) => {
      b.onclick = guard(() => readRemote(names[Number(b.dataset.name)]));
    });
}
async function readRemote(item) {
  state.remoteItem = item;
  const p = await api(
    "/connections/" +
      state.clientId +
      "/value?domain=" +
      encodeURIComponent(state.domain) +
      "&item=" +
      encodeURIComponent(item),
  );
  state.remotePoint = p;
  $("#remote-value").innerHTML =
    '<section class="remote-result"><h3>' +
    esc(p.reference) +
    '</h3><p class="remote-note">' +
    esc(p.kind) +
    ' · peer-declared type</p><textarea id="remote-editor" class="value-editor" spellcheck="false" aria-label="Remote MMS value">' +
    esc(JSON.stringify(p.value, null, 2)) +
    '</textarea><button id="remote-write" class="button secondary">Write to remote IED</button></section>';
  $("#remote-write").onclick = guard(async () => {
    await api("/connections/" + state.clientId + "/value", {
      method: "POST",
      body: {
        Domain: state.domain,
        Item: state.remoteItem,
        Value: JSON.parse($("#remote-editor").value),
      },
    });
    await readRemote(state.remoteItem);
    notify("MMS write completed");
  });
}

// Wire UI events once. Polling only replaces data regions, preserving textarea
// edits until the user applies the change or selects another attribute.
$("#language").onclick = () => {
  language = language === "en" ? "zh" : "en";
  localStorage.setItem("iedlab-language", language);
  state.editorDirty = false;
  applyLanguage();
};
$("#import-top").onclick = $("#import-empty").onclick = () =>
  $("#file-input").click();
$("#file-input").onchange = guard(async (event) => {
  const file = event.target.files[0];
  if (file) await importDocument(file);
  event.target.value = "";
});
$("#sample").onclick = $("#sample-empty").onclick = guard(loadSample);
$("#station-sample").onclick = guard(async () => {
  const doc = await api("/models/station-sample", { method: "POST" });
  await refreshState();
  await selectDevice(
    doc,
    doc.accessPoints.find((ap) => ap.ied === "BayIED") ||
      doc.accessPoints.find((ap) => ap.server),
  );
});
$("#station-toggle").onclick = guard(async () => {
  const model = state.detail?.device?.model;
  if (!model) return;
  const running = state.stations.some((s) => s.model === model);
  await api("/models/" + model + "/station", {
    method: running ? "DELETE" : "POST",
  });
  await refreshDetail();
  await refreshState();
});
$("#search").oninput = (event) => {
  state.search = event.target.value;
  renderRows();
};
$("#structures").onchange = (event) => {
  state.structures = event.target.checked;
  renderRows();
};
$("#refresh").onclick = guard(refreshDetail);
$("#simulation-toggle").onclick = guard(toggleSimulation);
$("#connect-sim").onclick = guard(async () => {
  const address = state.detail.device.address
    .replace(/^0\.0\.0\.0:/, "127.0.0.1:")
    .replace(/^\[::\]:/, "[::1]:");
  await connect(address);
});
$("#close-dialog").onclick = () => $("#simulation-dialog").close();
$("#goose-mode").onchange = async (event) => {
  const ethernet = event.target.value === "ethernet";
  $("#interface-field").classList.toggle("hidden", !ethernet);
  if (!ethernet) return;
  try {
    const interfaces = await api("/interfaces");
    $("#interface-options").innerHTML = (interfaces || [])
      .map(
        (item) =>
          '<option value="' +
          esc(item.Name) +
          '">' +
          esc(item.Description || item.Name) +
          "</option>",
      )
      .join("");
    $("#interface-help").textContent =
      "Choose an explicit Ethernet adapter. The SCL supplies destination MAC and VLAN.";
  } catch (error) {
    $("#interface-help").textContent = error.message;
  }
};
$("#simulation-form").onsubmit = async (event) => {
  event.preventDefault();
  const button = event.submitter;
  button.disabled = true;
  try {
    await api("/devices/" + state.deviceId + "/simulation", {
      method: "POST",
      body: {
        listen: $("#listen-address").value,
        goose: $("#goose-mode").value,
        interface: $("#network-interface").value,
        controls: $("#enable-controls").checked,
      },
    });
    $("#simulation-dialog").close();
    await refreshDetail();
    await refreshState();
  } catch (error) {
    $("#simulation-error").textContent = error.message;
  } finally {
    button.disabled = false;
  }
};
$("#connect-form").onsubmit = guard(async (event) => {
  event.preventDefault();
  await connect($("#remote-address").value);
});
$("#event-filter").onchange = renderEvents;
$("#clear-log").onclick = () => {
  state.eventFloor = state.events.at(-1)?.id || 0;
  renderEvents();
};
const setJournalView = (view) => {
  state.journalView = view;
  $("#journal-events").classList.toggle("active", view === "events");
  $("#journal-controls").classList.toggle("active", view === "controls");
  $("#event-filter").hidden = view === "controls";
  $("#control-export").hidden = view !== "controls";
  renderEvents();
};
$("#journal-events").onclick = () => setJournalView("events");
$("#journal-controls").onclick = () => setJournalView("controls");
$("#control-export").onclick = () =>
  downloadJSON("iedlab-controls.json", controlRecords().map((e) => e.control));
document.querySelectorAll("[data-tab]").forEach((button) => {
  button.onclick = () => {
    state.receiver = null;
    showTab(button.dataset.tab);
  };
});
document.body.addEventListener("dragover", (event) => event.preventDefault());
document.body.addEventListener(
  "drop",
  guard(async (event) => {
    event.preventDefault();
    const file = event.dataTransfer.files[0];
    if (file) await importDocument(file);
  }),
);
applyLanguage();
renderFilters();
try {
  await refreshState();
  if (state.devices.length) {
    state.deviceId = state.devices[0].id;
    await refreshDetail();
  } else if (state.documents.length) {
    const doc = state.documents[0];
    const ap = doc.accessPoints.find((entry) => entry.server);
    if (ap) await selectDevice(doc, ap);
  }
} catch (error) {
  notify(error.message, true);
}
const events = new EventSource("/api/events");
events.onmessage = (message) => {
  const event = JSON.parse(message.data);
  if (!state.events.some((entry) => entry.id === event.id))
    state.events.push(event);
  state.events = state.events.slice(-500);
  renderEvents();
  if (
    event.kind.startsWith("model.") ||
    event.kind.startsWith("control.") ||
    event.kind.startsWith("simulation.")
  ) {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(
      () => refreshDetail().catch((error) => notify(error.message, true)),
      150,
    );
  }
  if (event.kind.startsWith("recording.") && state.tab === "playback") {
    refreshRecordings().catch((error) => notify(error.message, true));
  }
};
events.onopen = () => {
  $("#live-state").innerHTML = "<i></i>" + esc(tr("connected"));
  // A slow/disconnected browser can miss journal entries. Rehydrate the
  // bounded history after reconnect, while leaving its editor untouched.
  refreshState().catch((error) => notify(error.message, true));
};
events.onerror = () => {
  $("#live-state").textContent = "Reconnecting…";
};
setInterval(() => {
  engineeringUI.refreshHistory().catch(error => notify(error.message,true));
  if (state.deviceId && !document.hidden) refreshDetail().catch(() => {});
  if (state.stations.length && !document.hidden)
    refreshStation().catch(() => {});
}, 2000);
