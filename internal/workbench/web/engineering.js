// The workbench consumes the same public IED/SCL services as Go applications.
// This module owns engineering and online service panels, not protocol state.
export function createEngineeringUI({api, state, esc, guard, notify, locale, readRemote}) {
  const $ = (s) => document.querySelector(s);
  const text = (en, zh) => locale() === "zh" ? zh : en;
  const name = (n) => n.Domain + "/" + n.Item;
  let directory = null;
  let directoryID = "";
  let discoveryTicket = 0;
  let historyBusy = false;
  let historyMark = "";
  const endpoint = (id, suffix) => "/connections/" + id + suffix;

  function download(filename, value) {
    const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], {type:"application/json"}));
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  function selectedConnection() {
    return $("#service-connection").value;
  }
  function connectionsChanged() {
    const select = $("#service-connection");
    if (!select) return;
    const previous = select.value || state.clientId;
    select.innerHTML = state.connections.filter(c => c.status === "connected").map(c =>
      '<option value="' + esc(c.id) + '">' + esc(c.address) + '</option>'
    ).join("");
    if ([...select.options].some(o => o.value === previous)) select.value = previous;
    if (directoryID && !state.connections.some(c => c.id === directoryID)) {
      directory = null;
      directoryID = "";
      $("#online-services").innerHTML = "";
      $("#report-history").innerHTML = "";
    }
  }
  async function discover() {
    const id = selectedConnection();
    if (!id) throw new Error(text("Connect an IED first.", "先连接一台 IED。"));
    const ticket = ++discoveryTicket;
    $("#service-status").textContent = text("Reading online services…", "正在读取在线服务…");
    const result = await api(endpoint(id, "/discover"));
    if (ticket !== discoveryTicket || id !== selectedConnection()) return;
    directory = result;
    directoryID = id;
    $("#service-status").textContent =
      result.Objects.length + text(" objects", " 个对象") +
      (result.Truncated ? text(" · discovery limit reached", " · 已达到发现数量上限") : "");
    renderServices();
    await refreshHistory(true);
  }
  function reportCards() {
    return (directory.Reports || []).map((r, i) =>
      '<article class="stream-card"><h3>' + (r.Buffered ? "BRCB " : "URCB ") + esc(name(r.Name)) +
      '</h3><p>' + esc(r.DataSet) + ' · ConfRev ' + r.ConfRev + '</p>' +
      (r.Error ? '<p class="service-error">' + esc(r.Error) + '</p>' : "") +
      (r.Buffered ? '<label>Resume EntryID<input data-resume="' + i + '" placeholder="hex"></label>' : "") +
      '<div class="service-actions"><button class="button primary" data-report="subscribe" data-index="' + i + '">' +
      text("Subscribe + GI", "订阅并总召") + '</button><button class="button secondary" data-report="snapshot" data-index="' + i + '">' +
      text("Read current values", "读取当前值") + '</button><button class="button secondary" data-report="stop" data-index="' + i + '">' +
      text("Stop", "停止") + '</button></div></article>'
    ).join("");
  }
  function controlPanel() {
    const controls = directory.Objects.filter(o => o.FC === "CO" && o.Name.Item.endsWith("$Oper"));
    if (!controls.length) return "";
    return '<article class="stream-card"><h3>' + text("Control procedure", "控制过程") +
      '</h3><label>' + text("Control object", "控制对象") + '<select id="online-control">' +
      controls.map(o => '<option value="' + esc(name(o.Name)) + '">' + esc(name(o.Name)) + '</option>').join("") +
      '</select></label><label>ctlVal · JSON<textarea id="online-control-value" class="value-editor">true</textarea></label>' +
      '<div class="service-actions"><label><input type="checkbox" id="control-test"> Test</label>' +
      '<label><input type="checkbox" id="control-interlock" checked> InterlockCheck</label>' +
      '<label><input type="checkbox" id="control-synchro"> SynchroCheck</label></div>' +
      '<div class="service-actions">' + ["select","operate","cancel"].map(a =>
        '<button class="button secondary" data-control="' + a + '">' + esc(a) + '</button>').join("") +
      '</div><pre id="online-control-result" aria-live="polite"></pre></article>';
  }
  function settingsPanel() {
    const groups = directory.SettingGroups || [];
    if (!groups.length) return "";
    return '<article class="stream-card"><h3>' + text("Setting groups", "定值组") +
      '</h3><label>SGCB<select id="online-settings-domain">' + groups.map(g =>
        '<option value="' + esc(g.Domain) + '">' + esc(name(g)) + '</option>').join("") +
      '</select></label><div class="service-actions"><button class="button secondary" id="settings-read">' +
      text("Read group state", "读取组状态") + '</button><label>' + text("Group", "组号") +
      '<input id="settings-group" type="number" min="1" max="255" value="2"></label></div>' +
      '<label>' + text("SE values by relative reference · JSON", "按相对引用填写 SE 值 · JSON") +
      '<textarea id="settings-values" class="value-editor">{}</textarea></label><div class="service-actions">' +
      '<button class="button secondary" id="settings-confirm">' + text("Edit and confirm", "编辑并确认") +
      '</button><button class="button secondary" id="settings-activate">' + text("Activate existing group", "激活已有组") +
      '</button></div><pre id="online-settings-result" aria-live="polite"></pre></article>';
  }
  function renderServices() {
    const box = $("#online-services");
    if (!directory || directoryID !== selectedConnection()) { box.innerHTML = ""; return; }
    const diagnostics = directory.Diagnostics.map(d => '<p class="service-error">' + esc(d.Reference + " · " + d.Message) + '</p>').join("");
    box.innerHTML = diagnostics + '<div class="stream-grid">' + reportCards() + controlPanel() + settingsPanel() + '</div>' +
      '<details class="stream-card"><summary>' + text("Online object directory", "在线对象目录") + '</summary><div class="remote-names">' +
      directory.Objects.map((o, i) => '<button data-online-object="' + i + '">' + esc(name(o.Name)) + '</button>').join("") +
      '</div></details>';
    box.querySelectorAll("[data-report]").forEach(button => {
      button.onclick = guard(async () => {
        const block = directory.Reports[Number(button.dataset.index)];
        const action = button.dataset.report;
        const body = {Domain:block.Name.Domain, Item:block.Name.Item};
        const base = endpoint(directoryID, "/reports");
        if (action === "stop") {
          await api(base + "?domain=" + encodeURIComponent(body.Domain) + "&item=" + encodeURIComponent(body.Item), {method:"DELETE"});
        } else if (action === "snapshot") {
          await api(endpoint(directoryID, "/snapshot"), {method:"POST", body});
        } else {
          body.Options = {GI:true};
          const cursor = box.querySelector('[data-resume="' + button.dataset.index + '"]')?.value.trim();
          if (cursor) {
            if (!/^(?:[0-9a-fA-F]{2})+$/.test(cursor)) throw new Error("EntryID must be complete hexadecimal octets.");
            body.Options.ResumeEntryID = btoa(String.fromCharCode(...cursor.match(/../g).map(v => parseInt(v,16))));
          }
          await api(base, {method:"POST", body});
        }
        notify(text("Report operation completed", "报告操作已完成"));
        await refreshHistory(true);
      });
    });
    box.querySelectorAll("[data-online-object]").forEach(button => {
      button.onclick = guard(async () => {
        const o = directory.Objects[Number(button.dataset.onlineObject)];
        state.clientId = directoryID;
        state.domain = o.Name.Domain;
        await readRemote(o.Name.Item);
      });
    });
    box.querySelectorAll("[data-control]").forEach(button => {
      button.onclick = guard(async () => {
        const full = $("#online-control").value;
        const [domain, item] = full.split("/");
        const parts = item.split("$");
        const reference = domain + "/" + parts[0] + "." + parts.slice(2,-1).join(".");
        const reply = await api(endpoint(directoryID, "/controls"), {
          method:"POST", body:{Reference:reference, Action:button.dataset.control,
            Value:JSON.parse($("#online-control-value").value),
            Options:{Test:$("#control-test").checked, InterlockCheck:$("#control-interlock").checked, SynchroCheck:$("#control-synchro").checked}}
        });
        $("#online-control-result").textContent = JSON.stringify(reply,null,2);
      });
    });
    if ($("#settings-read")) {
      $("#settings-read").onclick = guard(async () => {
        const result = await api(endpoint(directoryID, "/settings?domain=" + encodeURIComponent($("#online-settings-domain").value)));
        $("#online-settings-result").textContent = JSON.stringify(result,null,2);
      });
      for (const action of ["confirm","activate"]) {
        $("#settings-" + action).onclick = guard(async () => {
          const result = await api(endpoint(directoryID, "/settings"), {method:"POST", body:{
            Domain:$("#online-settings-domain").value, Group:Number($("#settings-group").value),
            Values:JSON.parse($("#settings-values").value), ActivateOnly:action === "activate"
          }});
          $("#online-settings-result").textContent = JSON.stringify(result,null,2);
        });
      }
    }
  }
  async function refreshHistory(force = false) {
    if (!directoryID || historyBusy || (!force && (document.hidden || state.tab !== "client"))) return;
    const id = directoryID;
    historyBusy = true;
    try {
      const page = await api(endpoint(id, "/report-history"));
      if (id !== directoryID) return;
      const mark = id + "/" + page.Last + "/" + locale();
      if (!force && historyMark === mark) return;
      historyMark = mark;
      $("#report-history").innerHTML = '<div class="service-actions"><h3>' + text("Reports and current snapshots", "报告与现值快照") +
        '</h3><button class="button secondary" id="report-export">' + text("Export JSON", "导出 JSON") +
        '</button></div>' + '<p class="form-note">' +
        text("Buffered reports may contain history. Confirmed reads are current snapshots. This session keeps the latest 500 records.",
          "缓冲报告可能包含历史事件，确认读取提供现值快照。本会话保留最近 500 条记录。") + '</p>' +
        page.records.slice().reverse().map(r => '<details class="report-record"><summary>#' + r.index +
          ' · ' + esc(r.source) + ' · ' + esc(r.block) + ' · ' + esc(r.received_at) +
          (r.gap ? ' · ' + text("HISTORY GAP", "历史缺口") : "") + '</summary><p>EntryID ' +
          esc(r.entry_id || "—") + ' · seq ' + (r.sequence ?? "—") + ' · ' + r.segments + ' segments</p>' +
          '<p>' + esc(r.time_of_entry) + '</p><table><thead><tr><th>Reference</th><th>Value</th><th>Reason</th></tr></thead><tbody>' +
          r.values.map(v => '<tr><td>' + esc(v.reference) + '</td><td>' + esc(v.display) +
            '</td><td>' + esc(v.reason) + '</td></tr>').join("") + '</tbody></table></details>').join("");
      $("#report-export").onclick = () => download("iedlab-reports.json",page);
    } finally { historyBusy = false; }
  }
  async function project(container, modelRef) {
    if (!modelRef) return;
    const id = modelRef.Model;
    const result = await api("/models/" + id + "/project");
    if (state.catalogRef?.Model !== id || !container.isConnected) return;
    const section = document.createElement("section");
    section.className = "project-panel";
    section.innerHTML = '<h3>' + text("Engineering networks and inputs", "工程网络与输入") + '</h3>' +
      '<div class="stream-grid">' + result.Networks.map(n => '<article class="stream-card"><h4>' +
        esc(n.Name) + '</h4><p>' + esc(n.Type) + '</p>' + n.AccessPoints.map(p =>
        '<p>' + esc(p.IED + "/" + p.AP + (p.ServerAP && p.ServerAP !== p.AP ? " → " + p.ServerAP : "")) + '</p>').join("") + '</article>').join("") +
      '</div>' + result.Diagnostics.map(d => '<p class="service-error">' + esc(d.Reference + " · " + d.Message) + '</p>').join("") +
      '<label>' + text("Compare with imported revision", "与已导入版本对照") +
      '<select class="compare-before">' + state.documents.filter(d => d.id !== id).map(d => '<option value="' +
        esc(d.id) + '">' + esc(d.name) + '</option>').join("") + '</select></label>' +
      '<button class="button secondary compare-project">' + text("Compare selected IED/AP", "比较所选 IED/AP") +
      '</button><pre class="compare-result"></pre>';
    container.prepend(section);
    section.querySelector(".compare-project").onclick = guard(async () => {
      const before = section.querySelector(".compare-before").value;
      if (!before) throw new Error(text("Import another revision first.", "先导入另一个版本。"));
      const difference = await api("/models/compare",{method:"POST",body:{Before:before,After:id,IED:modelRef.IED,AP:modelRef.AP}});
      section.querySelector(".compare-result").textContent = difference?.length ? JSON.stringify(difference,null,2) : text("No model differences.", "模型没有差异。");
    });
  }
  $("#service-discover").onclick = guard(discover);
  $("#service-connection").onchange = () => {
    ++discoveryTicket;
    if (directoryID !== selectedConnection()) {
      directory = null;
      directoryID = "";
      $("#report-history").innerHTML = "";
    }
    renderServices();
  };
  const languageChanged = () => { renderServices(); refreshHistory(true).catch(error => notify(error.message,true)); };
  return {connectionsChanged, refreshHistory, project, languageChanged};
}
