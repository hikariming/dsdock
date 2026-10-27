// DSDock GUI. Plain JavaScript, no build step. Everything shown from the
// server goes through textContent (see h()), never innerHTML: plugin
// descriptions come from third parties.
"use strict";

// ---------- token ----------
// The server hands the per-run token in the URL fragment (#t=...), which is
// never sent over the network. Keep it for this tab and clean the address.
// An optional go=<route> opens a specific page (dsdock gui links use it).
// A link pasted into an open tab changes only the fragment, so the router
// runs this again on every hash change.
function takeToken() {
  const m = location.hash.match(/(?:^#|&)t=([0-9a-f]+)/);
  if (m) {
    sessionStorage.setItem("dsdock-token", m[1]);
    TOKEN = m[1];
    const go = new URLSearchParams(location.hash.slice(1)).get("go") || "/";
    history.replaceState(null, "", location.pathname + "#" + (go.startsWith("/") ? go : "/" + go));
  }
}
let TOKEN = sessionStorage.getItem("dsdock-token") || "";
takeToken();

// ---------- tiny DOM helper ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}
const $ = (sel) => document.querySelector(sel);
function mount(el, ...kids) { el.replaceChildren(...kids.flat(Infinity).filter(Boolean)); }

// ---------- API ----------
async function api(path, body) {
  const opts = { headers: { "X-DSDock-Token": TOKEN } };
  if (body !== undefined) {
    opts.method = "POST";
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  let data = null;
  try { data = await res.json(); } catch (_) { /* empty */ }
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

// waitJob polls a background job, feeding progress lines to onProgress.
async function waitJob(job, onProgress) {
  for (;;) {
    const j = await api("/api/jobs/" + job.id);
    if (onProgress) onProgress(j.progress || []);
    if (j.status !== "running") {
      if (j.status === "failed") throw new Error(j.error);
      return j.result;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
}

function toast(msg, bad) {
  const t = h("div", { class: "toast" + (bad ? " bad" : "") }, msg);
  $("#toasts").append(t);
  setTimeout(() => t.remove(), bad ? 7000 : 3500);
}

const fmtTime = (s) => {
  if (!s) return "";
  const d = new Date(s);
  const p = (n) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};
const SOURCE = {
  pin: "这个 profile 固定了版本", default: "默认版本", external: "接管的外部安装",
  DSDOCK_DSH_VERSION: "环境变量指定", DSDOCK_DSH: "环境变量指定", PATH: "PATH 里的 dsh",
};

// ---------- state ----------
const state = { status: null, meta: null, outdated: {} };

async function refreshStatus() {
  state.status = await api("/api/status");
  renderSideProfiles();
  return state.status;
}

function health(ps) {
  if (!ps.healthy) return ["bad", "出错"];
  const warns = (ps.duplicates || []).length + (ps.unmatched || []).length + (ps.skipped || []).length;
  if (warns) return ["warn", "有警告"];
  return ["ok", "正常"];
}

function renderSideProfiles() {
  const nav = $("#profile-nav");
  const ps = (state.status && state.status.profiles) || [];
  mount(nav, ps.length ? ps.map((p) => {
    const [cls] = health(p);
    return h("a", { href: "#/p/" + encodeURIComponent(p.name), "data-route": "p/" + p.name },
      h("span", { class: "dot " + ((p.running || []).length ? "run" : cls) }), p.name);
  }) : h("div", { class: "muted small pad" }, "还没有 profile"));
  markNav();
}

function markNav() {
  const cur = location.hash.replace(/^#\/?/, "") || "home";
  document.querySelectorAll(".nav a").forEach((a) => {
    const r = a.dataset.route;
    a.classList.toggle("active", r === cur || (r !== "home" && cur.startsWith(r + "/")) || (r === cur.split("?")[0]));
  });
}

// ---------- dialogs ----------
const dlg = () => $("#dialog");
function openDialog(title, body, foot) {
  const d = dlg();
  mount(d, h("div", { class: "dlg-head" }, title), h("div", { class: "dlg-body" }, body), h("div", { class: "dlg-foot" }, foot));
  if (!d.open) d.showModal();
}
function closeDialog() { if (dlg().open) dlg().close(); }

const LEVEL = { block: ["bad", "阻止"], warn: ["warn", "警告"], info: ["info", "提示"] };
function findingList(findings) {
  if (!findings || !findings.length) return h("div", { class: "banner ok" }, "✓ 检查全部通过");
  return h("ul", { class: "findings" }, findings.map((f) => {
    const [cls, word] = LEVEL[f.level] || ["plain", f.level];
    return h("li", {}, h("span", { class: "pill " + cls }, word + (f.rule ? " " + f.rule : "")), h("span", {}, f.message));
  }));
}

// runJob shows a progress dialog until the job ends and returns its result.
async function runJob(title, start) {
  const list = h("ul", { class: "progress" });
  openDialog(title, [h("div", { class: "row" }, h("span", { class: "spin" }), "进行中…"), list], []);
  try {
    const job = await start();
    return await waitJob(job, (lines) => mount(list, lines.map((l) => h("li", {}, l))));
  } catch (e) {
    showError(title, e);
    throw e;
  }
}

function showError(title, e) {
  openDialog(title, h("div", { class: "banner bad" }, e.message || String(e)),
    h("button", { class: "btn", onclick: closeDialog }, "关闭"));
}

// showOutcome renders a txn outcome and resolves when closed.
function showOutcome(title, out) {
  const body = [];
  if (out.applied) body.push(h("div", { class: "banner ok" }, "✓ 完成。改动前的状态已存为快照 ", h("code", {}, out.before.id), "，可以随时恢复"));
  else if (out.rolledBack) body.push(h("div", { class: "banner warn" }, "已自动回滚：" + out.reason));
  else body.push(h("div", { class: "banner bad" }, "失败：" + out.reason));
  for (const w of out.warnings || []) body.push(h("div", { class: "banner warn" }, "⚠ " + w));
  if (out.smoke && out.applied) body.push(h("div", { class: "muted" }, `启动测试通过（${(out.smoke.duration / 1e9).toFixed(1)} 秒）`));
  for (const r of out.running || []) body.push(h("div", { class: "banner info" }, `${r.profile} 正在运行（PID ${r.pid}），重启后才会生效`));
  if (!out.applied && out.op && out.op.log) body.push(h("details", {}, h("summary", {}, "详细日志"), h("pre", { class: "log mono" }, out.op.log)));
  return new Promise((resolve) => {
    openDialog(title, body, h("button", { class: "btn primary", onclick: () => { closeDialog(); resolve(); } }, "好的"));
  });
}

// planFlow: ask the server for a plan, show it, apply on confirm.
async function planFlow(req, title) {
  let resp;
  openDialog(title, h("div", { class: "row" }, h("span", { class: "spin" }), "检查中…"), []);
  try { resp = await api("/api/plan", req); } catch (e) { showError(title, e); return false; }
  const plan = resp.plan;
  if (plan.noChange) {
    openDialog(title, h("div", { class: "banner info" }, plan.reason), h("button", { class: "btn", onclick: closeDialog }, "关闭"));
    return false;
  }
  const body = [];
  const r = plan.report;
  if (r && r.candidate) {
    const c = r.candidate;
    body.push(h("div", {}, h("strong", {}, c.name ? `${c.name}${c.version ? "@" + c.version : ""}` : c.spec.raw), " ", h("span", { class: "pill plain" }, c.spec.kind)));
  }
  if (plan.kind === "update") body.push(h("div", {}, `版本：${plan.from} → ${plan.to}`));
  if (plan.kind === "remove") body.push(h("div", {}, `卸载 ${plan.target}` + (plan.from ? `（${plan.from}）` : "")));
  if (plan.kind === "disable" || plan.kind === "enable") body.push(h("div", {}, `${plan.kind === "disable" ? "禁用" : "启用"} ${plan.target}（只修改你的 cordis.patch.yml，不卸载）`));
  if (r) body.push(findingList(r.findings));
  for (const n of plan.notes || []) body.push(h("div", { class: "banner warn" }, "⚠ " + n));

  const smoke = h("input", { type: "checkbox", id: "smoke" });
  if (plan.kind === "add" || plan.kind === "update") smoke.checked = true;
  const force = h("input", { type: "checkbox", id: "force" });
  body.push(h("label", { class: "check" }, smoke, "改完后真正启动一次检查（更稳妥，多花几秒）"));
  if (resp.blocked) body.push(h("label", { class: "check" }, force, "我知道风险，仍然继续（不推荐）"));
  body.push(h("div", { class: "muted small" }, "改动之前会自动拍快照；改坏了会自动回滚。"));

  return new Promise((resolve) => {
    const go = h("button", { class: "btn primary", disabled: resp.blocked }, resp.needsConfirmation ? "仍然继续" : "确认");
    force.addEventListener("change", () => { go.disabled = !force.checked; });
    go.addEventListener("click", async () => {
      try {
        const out = await runJob(title, () => api(`/api/plan/${resp.id}/apply`, { smoke: smoke.checked, force: force.checked }));
        await showOutcome(title, out);
        resolve(out.applied);
      } catch (_) { resolve(false); }
    });
    openDialog(title, body, [h("button", { class: "btn", onclick: () => { closeDialog(); resolve(false); } }, "取消"), go]);
  });
}

// ---------- pages ----------
async function pageHome(main) {
  const st = await refreshStatus();
  const head = h("div", { class: "page-head" },
    h("div", {}, h("h1", {}, "总览"),
      h("div", { class: "sub" }, st.dsh.error ? st.dsh.error : `DSH ${st.dsh.version} · ${SOURCE[st.dsh.source] || st.dsh.source} · ${st.dsh.home}`)),
    h("button", { class: "btn", onclick: () => route() }, "刷新"));
  const banners = [];
  if (st.dsh.nodeProblem) banners.push(h("div", { class: "banner warn" }, "⚠ " + st.dsh.nodeProblem));
  if (st.dsh.error) banners.push(h("div", { class: "banner info" }, "还没有可用的 DSH。去 ", h("a", { href: "#/versions" }, "DSH 版本"), " 安装一个。"));
  const cards = st.profiles.map((p) => {
    const [cls, word] = health(p);
    const running = (p.running || []).length;
    return h("div", { class: "panel card" },
      h("h3", {}, h("a", { href: "#/p/" + encodeURIComponent(p.name) }, p.name), p.web ? h("span", { class: "pill plain" }, "web") : null,
        h("span", { class: "spacer" }), h("span", { class: "pill " + cls }, word)),
      h("div", { class: "facts" },
        h("span", {}, `${(p.plugins || []).length} 个插件`),
        h("span", {}, `DSH ${p.dsh || "?"}${p.dshSource === "pin" ? "（固定）" : ""}`),
        running ? h("span", {}, h("span", { class: "dot run" }), " 正在运行") : null,
        p.lastSnapshot ? h("span", {}, "快照 " + p.lastSnapshot) : null),
      p.error ? h("div", { class: "banner bad small" }, p.error) : null,
      h("div", { class: "actions" },
        h("a", { class: "btn small", href: "#/p/" + encodeURIComponent(p.name) }, "管理插件"),
        h("button", { class: "btn small", onclick: () => smokeTest(p.name) }, "启动测试"),
        h("a", { class: "btn small" + (p.healthy && !word.includes("警告") ? "" : " danger"), href: "#/rescue/" + encodeURIComponent(p.name) }, "救援")));
  });
  mount(main, head, banners, st.profiles.length ? h("div", { class: "grid" }, cards)
    : h("div", { class: "panel empty" }, "还没有 profile。在终端里运行 dsh plugin --profile web add <插件> 会创建一个带 Web 界面的 profile。"));
}

async function smokeTest(name) {
  try {
    const res = await runJob(`启动测试：${name}`, () => api("/api/smoke", { profile: name }));
    const body = [];
    if (res.ready && !(res.inactive || []).length) body.push(h("div", { class: "banner ok" }, `✓ 启动成功，所有插件都已激活（${(res.duration / 1e9).toFixed(1)} 秒）`));
    else if (res.ready) body.push(h("div", { class: "banner warn" }, `启动成功，但有 ${res.inactive.length} 个插件没有激活`));
    else if (res.exited) body.push(h("div", { class: "banner bad" }, `DSH 启动后退出了（退出码 ${res.exitCode}）`));
    else body.push(h("div", { class: "banner bad" }, "超时：DSH 没有就绪"));
    for (const i of res.inactive || []) body.push(h("div", {}, h("code", {}, i.id), "（", i.module, "）：", i.reason));
    if (!(res.ready && !(res.inactive || []).length)) body.push(h("a", { href: "#/rescue/" + encodeURIComponent(name), class: "btn", onclick: closeDialog }, "去救援"));
    openDialog(`启动测试：${name}`, body, h("button", { class: "btn primary", onclick: closeDialog }, "好的"));
  } catch (_) { /* shown */ }
}

async function pageProfile(main, name, tab) {
  tab = tab || "plugins";
  const st = await refreshStatus();
  const p = st.profiles.find((x) => x.name === name);
  if (!p) { mount(main, h("div", { class: "panel empty" }, `profile ${name} 不存在`)); return; }
  const [cls, word] = health(p);
  const head = h("div", { class: "page-head" },
    h("div", {}, h("h1", {}, name, " ", h("span", { class: "pill " + cls }, word)),
      h("div", { class: "sub" }, `DSH ${p.dsh} · ${SOURCE[p.dshSource] || p.dshSource}`, p.web ? " · 带 Web 界面" : "",
        (p.running || []).length ? ` · 正在运行（PID ${p.running.map((r) => r.pid).join(", ")}）` : "")),
    h("div", { class: "row" },
      h("button", { class: "btn", onclick: () => smokeTest(name) }, "启动测试"),
      h("a", { class: "btn", href: "#/rescue/" + encodeURIComponent(name) }, "救援")));
  const tabs = h("div", { class: "tabs" }, [["plugins", "插件"], ["snaps", "快照"], ["ops", "操作记录"]].map(([k, label]) =>
    h("button", { class: tab === k ? "active" : "", onclick: () => { location.hash = `#/p/${encodeURIComponent(name)}?${k}`; } }, label)));
  const body = h("div", {});
  mount(main, head, problems(p), tabs, body);
  if (tab === "plugins") await tabPlugins(body, p);
  if (tab === "snaps") await tabSnaps(body, p);
  if (tab === "ops") await tabOps(body, p);
}

function problems(p) {
  const out = [];
  if (p.error) out.push(h("div", { class: "banner bad" }, "✗ 无法组装：" + p.error));
  for (const d of p.duplicates || []) out.push(h("div", { class: "banner warn" }, `⚠ id “${d.id}” 被 ${d.sources.join("、")} 重复插入，只有 ${d.sources[d.sources.length - 1]} 生效`));
  for (const s of p.skipped || []) out.push(h("div", { class: "banner warn" }, `⚠ ${s.package} 被 DSH 跳过，没有生效：${s.reason.replace(/^Error: /, "").split(". ")[0]}`));
  for (const u of p.unmatched || []) out.push(h("div", { class: "banner warn" }, `⚠ 覆盖找不到目标 “${u.id}”（${u.file}）`));
  return out;
}

async function tabPlugins(body, p) {
  const input = h("input", { type: "text", class: "grow", placeholder: "npm 包名[@版本]、本地目录，或 owner/repo（git）" });
  const add = async () => {
    const spec = input.value.trim();
    if (!spec) return;
    if (await planFlow({ kind: "add", profile: p.name, target: spec }, `安装到 ${p.name}`)) { input.value = ""; route(); }
  };
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") add(); });
  const addBar = h("div", { class: "panel pad row" }, input, h("button", { class: "btn primary", onclick: add }, "检查并安装"),
    h("a", { class: "btn", href: "#/market?for=" + encodeURIComponent(p.name) }, "去市场找"));

  const tbody = h("tbody", {});
  const table = h("div", { class: "panel" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "插件"), h("th", {}, "版本"), h("th", {}, "配置行"), h("th", {}))), tbody));
  mount(body, addBar, h("h2", {}, `已安装（${(p.plugins || []).length}）`), table);
  if (!(p.plugins || []).length) { mount(tbody, h("tr", {}, h("td", { colspan: "4", class: "empty" }, "还没有装外部插件"))); return; }

  const outdated = state.outdated[p.name] || {};
  // A row whose id another plugin inserts later is silently dropped by dsh.
  const dropped = {};
  for (const d of p.duplicates || []) {
    const winner = d.sources[d.sources.length - 1];
    for (const src of d.sources.slice(0, -1)) dropped[src + "\u0000" + d.id] = winner;
  }
  const render = () => mount(tbody, p.plugins.map((pl) => {
    const up = outdated[pl.name];
    const rows = (pl.rows || []).map((r) => {
      const lostTo = dropped[pl.name + "\u0000" + r.id];
      if (lostTo) return h("span", { class: "pill bad", title: `${lostTo} 插入了同一个 id，DSH 只加载它` }, r.id + "：未生效");
      if (r.disabled === "expr") return h("span", { class: "pill plain", title: "由表达式决定" }, r.id + "（表达式）");
      const on = r.disabled !== "true";
      return h("button", { class: "switch " + (on ? "on" : "off"), title: on ? "点击禁用" : "点击启用",
        onclick: async () => { if (await planFlow({ kind: on ? "disable" : "enable", profile: p.name, target: r.id }, `${on ? "禁用" : "启用"} ${r.id}`)) route(); } }, r.id);
    });
    return h("tr", {},
      h("td", {}, h("div", {}, h("strong", {}, pl.name)), h("div", { class: "muted small mono" }, pl.source === "npm" ? "npm" : pl.spec)),
      h("td", {}, pl.version || "?", up && up.latest ? h("div", {}, h("span", { class: "pill info" }, "可更新到 " + up.latest)) : null),
      h("td", {}, h("div", { class: "rows" }, rows.length ? rows : h("span", { class: "muted small" }, "—"))),
      h("td", { class: "actions" },
        pl.source === "npm" ? h("button", { class: "btn small", onclick: async () => { if (await planFlow({ kind: "update", profile: p.name, target: pl.name }, `更新 ${pl.name}`)) { delete state.outdated[p.name]; route(); } } }, up && up.latest ? "更新" : "检查更新") : null, " ",
        h("button", { class: "btn small danger", onclick: async () => { if (await planFlow({ kind: "remove", profile: p.name, target: pl.name }, `卸载 ${pl.name}`)) route(); } }, "卸载")));
  }));
  render();
  if (!state.outdated[p.name]) {
    api("/api/outdated?profile=" + encodeURIComponent(p.name)).then((ups) => {
      const m = {};
      for (const u of ups || []) m[u.name] = u;
      state.outdated[p.name] = m;
      Object.assign(outdated, m);
      render();
    }).catch(() => {});
  }
}

async function tabSnaps(body, p) {
  const snaps = await api("/api/snapshots?profile=" + encodeURIComponent(p.name));
  const take = h("button", { class: "btn", onclick: async () => {
    try { const s = await api("/api/snapshots", { profile: p.name }); toast("已拍快照 " + s.id); route(); } catch (e) { toast(e.message, true); }
  } }, "手动拍快照");
  mount(body, h("div", { class: "row" }, h("div", { class: "muted" }, "每次改动之前和之后都会自动拍快照。✓ 表示那时配置能正常组装。"), h("span", { class: "spacer" }), take),
    h("h2", {}, "快照"),
    h("div", { class: "panel" }, snaps && snaps.length ? h("ul", { class: "timeline" }, snaps.map((s) => h("li", {},
      h("span", { class: "when" }, fmtTime(s.createdAt)),
      h("span", { class: "dot " + (s.verified ? "ok" : "warn"), title: s.verified ? "当时能正常组装" : "当时无法组装" }),
      h("span", { class: "grow" }, s.reason, s.manual ? h("span", { class: "pill plain" }, "手动") : null, h("div", { class: "muted small" }, `DSH ${s.dshVersion || "?"} · `, h("code", {}, s.id))),
      h("button", { class: "btn small", onclick: () => restoreFlow(s) }, "恢复到这里")))) : h("div", { class: "empty" }, "还没有快照")));
}

async function restoreFlow(s) {
  const title = "恢复快照";
  const smoke = h("input", { type: "checkbox" });
  openDialog(title, [h("div", {}, `把 ${s.profile} 恢复到 ${fmtTime(s.createdAt)} 的状态（${s.reason}）？`),
    h("div", { class: "muted small" }, "恢复前会先给现在的状态拍快照，所以恢复本身也可以撤销。"),
    h("label", { class: "check" }, smoke, "恢复后真正启动一次检查")],
    [h("button", { class: "btn", onclick: closeDialog }, "取消"),
      h("button", { class: "btn primary", onclick: async () => {
        try { const out = await runJob(title, () => api("/api/restore", { snapshot: s.id, smoke: smoke.checked })); await showOutcome(title, out); route(); } catch (_) { /* shown */ }
      } }, "恢复")]);
}

const OP_STATUS = { applied: ["ok", "完成"], "rolled-back": ["warn", "已回滚"], failed: ["bad", "失败"], running: ["info", "进行中"], refused: ["plain", "未执行"] };
const OP_KIND = { add: "安装", remove: "卸载", update: "更新", disable: "禁用", enable: "启用", restore: "恢复" };
async function tabOps(body, p) {
  const ops = await api("/api/ops?profile=" + encodeURIComponent(p.name) + "&limit=100");
  mount(body, h("div", { class: "panel" }, ops && ops.length ? h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "时间"), h("th", {}, "操作"), h("th", {}, "对象"), h("th", {}, "结果"))),
    h("tbody", {}, ops.map((o) => {
      const [cls, word] = OP_STATUS[o.status] || ["plain", o.status];
      return h("tr", {}, h("td", { class: "muted" }, fmtTime(o.startedAt)), h("td", {}, OP_KIND[o.kind] || o.kind),
        h("td", {}, h("span", { class: "mono" }, o.spec), o.log && o.status !== "applied" ? h("details", {}, h("summary", { class: "small" }, "日志"), h("pre", { class: "log mono" }, o.log)) : null),
        h("td", {}, h("span", { class: "pill " + cls }, word)));
    }))) : h("div", { class: "empty" }, "还没有操作记录")));
}

async function pageRescue(main, name) {
  const out = h("div", {});
  const run = async (boot) => {
    try {
      const d = await runJob(`诊断 ${name}`, () => api("/api/diagnose", { profile: name, boot }));
      closeDialog();
      renderDiagnosis(out, d, name);
    } catch (_) { /* shown */ }
  };
  mount(main, h("div", { class: "page-head" },
    h("div", {}, h("h1", {}, "救援：", name), h("div", { class: "sub" }, "DSH 起不来、插件不生效的时候用。诊断只读，不会改动任何东西。")),
    h("a", { class: "btn", href: "#/p/" + encodeURIComponent(name) }, "返回")),
  h("div", { class: "panel pad row" },
    h("button", { class: "btn primary", onclick: () => run(true) }, "完整诊断（会真正启动一次）"),
    h("button", { class: "btn", onclick: () => run(false) }, "快速检查"),
    h("span", { class: "muted small" }, "完整诊断能发现加载时才出错的插件，大约需要 5–20 秒")),
  out);
}

function renderDiagnosis(el, d, name) {
  const items = [];
  if (d.healthy && !(d.problems || []).length) {
    items.push(h("div", { class: "banner ok" }, "✓ 没有发现问题：配置能正常组装" + (d.booted ? "，启动测试通过，所有插件都已激活" : "")));
  } else {
    items.push(h("h2", {}, "发现的问题"));
    items.push(h("div", { class: "panel pad" }, h("ul", { class: "findings" }, (d.problems || []).map((pr) =>
      h("li", {}, h("span", { class: "pill " + (pr.severity === "warn" ? "warn" : "bad") }, pr.severity === "warn" ? "警告" : "问题"),
        h("span", {}, pr.message), pr.fix ? h("span", { class: "fix" }, "建议：" + pr.fix) : null)))));
    if (!d.composed) items.push(h("div", { class: "muted small" }, "配置无法组装，所以没有做启动测试。"));
  }
  if (!d.healthy) {
    if (d.lastGood) {
      const lg = d.lastGood;
      items.push(h("h2", {}, "一键恢复"));
      items.push(h("div", { class: "panel pad row" },
        h("div", { class: "grow" }, h("strong", {}, `回到 ${fmtTime(lg.createdAt)} 的状态`), h("div", { class: "muted small" }, lg.reason, " · ", h("code", {}, lg.id))),
        h("button", { class: "btn primary", onclick: () => restoreFlow(Object.assign({ profile: name }, lg)) }, "恢复")));
    } else {
      items.push(h("div", { class: "banner info" }, "没有找到可以回去的正常状态（DSDock 接管之前的改动没有快照）。请按上面的建议逐个处理。"));
    }
  }
  mount(el, items);
}

async function pageVersions(main) {
  const v = await api("/api/versions");
  const head = h("div", { class: "page-head" }, h("div", {}, h("h1", {}, "DSH 版本"),
    h("div", { class: "sub" }, "多个版本可以并存。没有固定版本的 profile 跟随默认版本。")));
  const pinsOf = (n) => (v.pins || []).filter((p) => p.name === n).map((p) => p.profile);
  const installed = h("div", { class: "panel" }, (v.installed || []).length ? h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "版本"), h("th", {}, "安装时间"), h("th", {}, "会话格式"), h("th", {}, "固定的 profile"), h("th", {}))),
    h("tbody", {}, v.installed.map((i) => h("tr", {},
      h("td", {}, h("strong", {}, i.name), i.name === v.default ? h("span", { class: "pill ok" }, "默认") : null, i.name === "external" ? h("div", { class: "muted small mono" }, i.dir) : null),
      h("td", { class: "muted" }, fmtTime(i.installedAt)),
      h("td", {}, i.sessionFormat ? "v" + i.sessionFormat : "?"),
      h("td", {}, pinsOf(i.name).map((p) => h("span", { class: "pill plain" }, p, " ", h("a", { href: "#", onclick: async (e) => { e.preventDefault(); await api("/api/versions/unpin", { profile: p }); toast(`${p} 不再固定版本`); route(); } }, "×")))),
      h("td", { class: "actions" },
        i.name !== v.default ? h("button", { class: "btn small", onclick: () => switchFlow({ kind: "use", name: i.name }, `把默认版本切换到 ${i.name}`) }, "设为默认") : null, " ",
        h("button", { class: "btn small", onclick: () => pinPick(i.name) }, "固定到…"), " ",
        i.name !== v.default ? h("button", { class: "btn small danger", onclick: async () => {
          try { await api("/api/versions/uninstall", { name: i.name }); toast("已卸载 " + i.name); route(); } catch (e) { toast(e.message, true); }
        } }, "卸载") : null))))) : h("div", { class: "empty" }, "DSDock 还没有管理任何 DSH 版本。从下面选一个安装。"));

  const shim = h("div", { class: "panel pad" },
    h("div", { class: "row" }, h("strong", {}, "终端里的 dsh"), v.shimInstalled ? h("span", { class: "pill ok" }, "转发器已安装") : h("span", { class: "pill plain" }, "未安装"),
      h("span", { class: "spacer" }), h("button", { class: "btn small", onclick: async () => { const r = await api("/api/shim", {}); toast("转发器已写入 " + r.shim); route(); } }, v.shimInstalled ? "重新安装" : "安装转发器")),
    h("div", { class: "muted small" }, "装上后，在终端里敲 dsh 会自动用对应 profile 的版本。"),
    v.shimInstalled ? h("pre", { class: "log mono" }, v.pathHint) : null);

  const avail = h("div", { class: "panel" }, h("div", { class: "empty" }, h("span", { class: "spin" }), " 正在读取可安装的版本…"));
  mount(main, head, installed, h("h2", {}, "转发器"), shim, h("h2", {}, "可以安装的版本"), avail);
  try {
    const rel = await api("/api/versions/available");
    const shown = rel.filter((r) => !r.deprecated).slice(0, 14);
    mount(avail, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "版本"), h("th", {}, "发布日期"), h("th", {}, "渠道"), h("th", {}))),
      h("tbody", {}, shown.map((r) => h("tr", {}, h("td", {}, r.version), h("td", { class: "muted" }, fmtTime(r.published).slice(0, 5)),
        h("td", {}, (r.tags || []).map((t) => h("span", { class: "pill info" }, t))),
        h("td", { class: "actions" }, r.installed ? h("span", { class: "muted small" }, "已安装")
          : h("button", { class: "btn small primary", onclick: async () => {
            try {
              const res = await runJob(`安装 DSH ${r.version}`, () => api("/api/versions/install", { want: r.version }));
              closeDialog();
              toast(`已安装 DSH ${res.installed.version}` + (res.default ? "，并设为默认版本" : ""));
              route();
            } catch (_) { /* shown */ }
          } }, "安装")))))));
  } catch (e) { mount(avail, h("div", { class: "empty" }, "读取失败：" + e.message)); }
}

function pinPick(name) {
  const ps = (state.status && state.status.profiles) || [];
  const sel = h("select", {}, ps.map((p) => h("option", { value: p.name }, p.name)));
  openDialog(`固定使用 DSH ${name}`, [h("div", {}, "选择 profile："), sel],
    [h("button", { class: "btn", onclick: closeDialog }, "取消"),
      h("button", { class: "btn primary", onclick: () => switchFlow({ kind: "pin", name, profile: sel.value }, `把 ${sel.value} 固定到 DSH ${name}`) }, "下一步")]);
}

async function switchFlow(req, title) {
  openDialog(title, h("div", { class: "row" }, h("span", { class: "spin" }), "用目标版本检查每个 profile…"), []);
  let resp;
  try { resp = await api("/api/versions/plan", req); } catch (e) { showError(title, e); return; }
  const r = resp.plan.report;
  if (resp.plan.same) { openDialog(title, h("div", { class: "banner info" }, "已经是这个版本了"), h("button", { class: "btn", onclick: closeDialog }, "关闭")); return; }
  const body = [h("div", {}, `${r.from || "（无）"} → ${r.to}`)];
  if (r.sessionDowngrade) body.push(h("div", { class: "banner warn" }, `⚠ 降级：目标版本使用会话格式 v${r.toFormat}，当前是 v${r.fromFormat}。在新版本里创建或继续过的会话，旧版本打不开。`));
  for (const pc of r.profiles || []) {
    const bad = Object.keys(pc.skipped || {}).length || Object.keys(pc.incompatible || {}).length || pc.composeError;
    body.push(h("div", { class: "panel pad" }, h("div", { class: "row" }, h("strong", {}, pc.profile), h("span", { class: "pill " + (bad ? "bad" : "ok") }, bad ? "有问题" : "正常")),
      Object.entries(pc.skipped || {}).map(([b, why]) => h("div", { class: "small" }, `${b} 会被 DSH 跳过：${why}`)),
      Object.entries(pc.incompatible || {}).map(([b, req]) => h("div", { class: "small" }, `${b} 不兼容：${JSON.stringify(req)}`)),
      pc.composeError ? h("div", { class: "small" }, "用目标版本无法组装：" + pc.composeError) : null));
  }
  if (!(r.profiles || []).length) body.push(h("div", { class: "muted" }, "没有受影响的 profile"));
  openDialog(title, body, [h("button", { class: "btn", onclick: closeDialog }, "取消"),
    h("button", { class: "btn primary", onclick: async () => {
      try {
        const res = await api(`/api/versions/plan/${resp.id}/apply`, {});
        closeDialog();
        toast("已切换" + ((res.running || []).length ? "；正在运行的 profile 需要重启才会生效" : ""));
        route();
      } catch (e) { showError(title, e); }
    } }, resp.needsConfirmation ? "仍然切换" : "确认切换")]);
}

async function pageMarket(main, params) {
  const forProfile = params.get("for") || "";
  const q = h("input", { type: "search", class: "grow", placeholder: "搜索 dshfind 收录的插件", value: params.get("q") || "" });
  const sort = h("select", {}, [["", "推荐"], ["stars", "Star 最多"], ["score", "评分最高"], ["updated", "最近更新"]].map(([v, l]) => h("option", { value: v }, l)));
  sort.value = params.get("sort") || "";
  const results = h("div", {});
  const search = () => {
    const sp = new URLSearchParams({ q: q.value.trim(), sort: sort.value });
    if (forProfile) sp.set("for", forProfile);
    location.hash = "#/market?" + sp.toString();
  };
  q.addEventListener("keydown", (e) => { if (e.key === "Enter") search(); });
  sort.addEventListener("change", search);
  mount(main, h("div", { class: "page-head" }, h("div", {}, h("h1", {}, "插件市场"),
    h("div", { class: "sub" }, "数据来自 dshfind.com。安装前 DSDock 会再做一遍完整检查。"))),
  h("div", { class: "panel pad row" }, q, sort, h("button", { class: "btn primary", onclick: search }, "搜索")), results);
  mount(results, h("div", { class: "empty" }, h("span", { class: "spin" }), " 搜索中…"));
  let data;
  try {
    data = await api("/api/market?" + new URLSearchParams({ q: q.value.trim(), sort: sort.value }).toString());
  } catch (e) { mount(results, h("div", { class: "panel empty" }, "搜索失败：" + e.message)); return; }
  if (!state.status) await refreshStatus();
  const ps = state.status.profiles || [];
  mount(results, h("div", { class: "muted small count" }, `共 ${data.total} 个`), h("div", { class: "grid" }, (data.items || []).map((it) => {
    const target = h("select", {}, ps.map((p) => h("option", { value: p.name }, p.name)));
    if (forProfile) target.value = forProfile;
    return h("div", { class: "panel market-card" },
      h("div", { class: "row title" }, h("a", { class: "name", href: "https://dshfind.com/plugins/" + it.fullName, target: "_blank", rel: "noopener noreferrer" }, it.fullName),
        it.grade ? h("span", { class: "pill info" }, it.grade) : null),
      h("div", { class: "desc" }, it.description || "（没有描述）"),
      h("div", { class: "row small muted" }, `★ ${it.stars}`, it.category ? h("span", { class: "pill plain" }, it.category) : null, it.fromNpm ? h("span", { class: "pill plain" }, "npm") : h("span", { class: "pill plain" }, "git")),
      it.risky ? h("div", { class: "banner bad small" }, "dshfind 标记为有风险：" + (it.riskNote || "")) : null,
      it.archived ? h("div", { class: "banner warn small" }, "仓库已归档") : null,
      ps.length ? h("div", { class: "row" }, target, h("button", { class: "btn small primary", onclick: async () => {
        if (await planFlow({ kind: "add", profile: target.value, target: it.spec }, `安装 ${it.fullName} 到 ${target.value}`)) toast("已安装到 " + target.value);
      } }, "安装")) : h("div", { class: "muted small" }, "先创建一个 profile 才能安装"));
  })));
}

// ---------- router ----------
async function route() {
  takeToken();
  const main = $("#main");
  const raw = location.hash.replace(/^#\/?/, "");
  const [path, query] = raw.split("?");
  const params = new URLSearchParams(query || "");
  markNav();
  if (!TOKEN) {
    mount(main, h("div", { class: "banner bad" }, "缺少访问令牌。请从终端里 dsdock gui 打印的地址重新打开。"));
    return;
  }
  try {
    if (!path) await pageHome(main);
    else if (path === "versions") await pageVersions(main);
    else if (path === "market") await pageMarket(main, params);
    else if (path.startsWith("p/")) await pageProfile(main, decodeURIComponent(path.slice(2)), query);
    else if (path.startsWith("rescue/")) await pageRescue(main, decodeURIComponent(path.slice(7)));
    else mount(main, h("div", { class: "panel empty" }, "页面不存在"));
  } catch (e) {
    mount(main, h("div", { class: "banner bad" }, e.message));
  }
  markNav();
}

window.addEventListener("hashchange", route);
(async function boot() {
  if (TOKEN) {
    refreshStatus().catch(() => {});
    try {
      state.meta = await api("/api/meta");
      mount($("#side-foot"), h("div", {}, "DSDock " + state.meta.version), h("div", { class: "mono" }, state.meta.home));
    } catch (_) { /* route() shows the error */ }
  }
  route();
})();
