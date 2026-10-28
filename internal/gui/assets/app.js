// DSDock's interface, for the browser (dsdock gui), the desktop window and
// the menu bar panel (#/panel). Plain JavaScript, no build step. Everything
// shown from the server goes through textContent (see h()), never
// innerHTML: plugin descriptions come from third parties.
"use strict";

// ---------- token ----------
// In a browser the server hands a per-run token in the URL fragment
// (#t=...), which never reaches the network; an optional go=<route> opens a
// page. The desktop app serves the page in-process and needs no token. A
// link pasted into an open tab changes only the fragment, so the router runs
// this again on every hash change.
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

// ---------- tiny DOM helpers ----------
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

// Icons are drawn with DOM calls; the CSP forbids inline markup tricks.
const ICONS = {
  refresh: "M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7",
  open: "M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5",
};
function icon(name) {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.8");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  const p = document.createElementNS(ns, "path");
  p.setAttribute("d", ICONS[name]);
  svg.append(p);
  return svg;
}

// avatar is a letter tile, coloured from the name, like Magpie's agent icons.
const PALETTE = ["#0a84ff", "#30a46c", "#e5484d", "#f59e0b", "#8e4ec6", "#12a594", "#ef5f00", "#3e63dd", "#d6409f"];
function avatar(name, small) {
  let n = 0;
  for (const ch of name) n = (n * 31 + ch.codePointAt(0)) >>> 0;
  const el = h("span", { class: "avatar" + (small ? " small" : "") }, [...name][0] || "?");
  el.style.background = PALETTE[n % PALETTE.length];
  return el;
}

// ---------- API ----------
async function api(path, body) {
  const opts = { headers: {} };
  if (TOKEN) opts.headers["X-DSDock-Token"] = TOKEN;
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

// desktop asks the native shell to do something; only the app has these.
async function desktop(action, body) {
  try { await api("/api/desktop/" + action, body || {}); } catch (e) { toast(e.message, true); }
}

// ---------- state ----------
const state = { status: null, meta: null, versions: null, outdated: {} };

async function refreshStatus() {
  state.status = await api("/api/status");
  return state.status;
}
async function refreshVersions() {
  state.versions = await api("/api/versions");
  return state.versions;
}

function health(ps) {
  if (!ps.healthy) return ["bad", "出错"];
  const warns = (ps.duplicates || []).length + (ps.unmatched || []).length + (ps.skipped || []).length;
  if (warns) return ["warn", "有警告"];
  return ["ok", "正常"];
}

function markNav() {
  const cur = (location.hash.replace(/^#\/?/, "") || "home").split("?")[0];
  document.querySelectorAll("#nav a").forEach((a) => {
    const r = a.dataset.route;
    a.classList.toggle("active", r === cur || (r === "home" && (cur.startsWith("p/") || cur.startsWith("rescue/"))));
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
  openDialog(title, [h("div", { class: "row-flex" }, h("span", { class: "spin" }), "进行中…"), list], []);
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
  openDialog(title, h("div", { class: "row-flex" }, h("span", { class: "spin" }), "检查中…"), []);
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

// ---------- shared pieces ----------

// versionPicker chooses which DSH release runs a profile: following the
// default (no pin) or one installed release (a pin). The change is checked
// like any switch before it happens.
function versionPicker(p, compact) {
  const v = state.versions || { installed: [], default: "" };
  const pinned = p.dshSource === "pin" ? (v.pins || []).find((x) => x.profile === p.name) : null;
  const sel = h("select", { class: "picker", title: "这个 profile 用哪个 DSH 版本" },
    h("option", { value: "" }, compact ? "默认版本" : `默认（${v.default || p.dsh || "?"}）`),
    (v.installed || []).map((i) => h("option", { value: i.name }, i.name === "external" ? `外部 ${i.version}` : i.name)));
  sel.value = pinned ? pinned.name : "";
  sel.addEventListener("change", async () => {
    const want = sel.value;
    if (!want) {
      await api("/api/versions/unpin", { profile: p.name });
      toast(`${p.name} 改为跟随默认版本`);
      return rerender();
    }
    const ok = await switchFlow({ kind: "pin", name: want, profile: p.name }, `${p.name} 使用 DSH ${want}`);
    if (!ok) sel.value = pinned ? pinned.name : "";
    rerender();
  });
  return sel;
}

function statusLine(p) {
  const [cls, word] = health(p);
  let text = `${word} · ${(p.plugins || []).length} 个插件`;
  if ((p.running || []).length) text += " · 运行中";
  return [h("span", { class: "dot " + ((p.running || []).length && cls === "ok" ? "run" : cls) }), h("span", { class: "status-text" }, text)];
}

function versionChips(onPick) {
  const v = state.versions || { installed: [] };
  if (!(v.installed || []).length) {
    const head = state.status && state.status.dsh;
    // DSDock manages no release yet but is already running the user's own
    // dsh (npm i -g): say so, and offer to adopt it rather than "install".
    if (head && head.source === "PATH" && head.version) {
      return h("div", { class: "chips" }, h("span", { class: "chips-label" }, "DSH"),
        h("span", { class: "chip on", title: head.entry }, `终端里的 ${head.version}`, h("span", { class: "mark" })),
        h("button", { class: "chip", title: "由 DSDock 统一管理，原来的安装不会移动或删除", onclick: async () => {
          try { const i = await api("/api/versions/adopt", {}); toast(`已接管 DSH ${i.version}`); rerender(); } catch (e) { toast(e.message, true); }
        } }, "接管"),
        h("a", { class: "chip", href: "#/versions" }, "+ 安装其它版本"));
    }
    return h("div", { class: "chips" }, h("span", { class: "chips-label" }, "DSH"),
      h("a", { class: "chip", href: "#/versions" }, "+ 安装 DSH"));
  }
  return h("div", { class: "chips" }, h("span", { class: "chips-label" }, "DSH"),
    v.installed.map((i) => h("button", { class: "chip" + (i.name === v.default ? " on" : ""), title: i.name === v.default ? "默认版本" : "设为默认版本",
      onclick: () => { if (i.name !== v.default) onPick(i.name); } },
    i.name === "external" ? "外部 " + i.version : i.name, i.name === v.default ? h("span", { class: "mark" }) : null)));
}

let rerender = () => route();

// ---------- pages ----------
async function pageHome(main) {
  const [st] = await Promise.all([refreshStatus(), refreshVersions()]);
  const lead = st.dsh.error
    ? h("div", { class: "lead" }, "还没有可用的 DSH。去 ", h("a", { href: "#/versions" }, "DSH 版本"), " 安装一个。")
    : h("div", { class: "lead" }, "每个 profile 是一套插件组合。右边选它用哪个 DSH 版本；改动插件前 DSDock 会先检查，改坏了自动回滚。");
  const banners = st.dsh.nodeProblem ? h("div", { class: "banner warn" }, "⚠ " + st.dsh.nodeProblem) : null;

  const rows = st.profiles.map((p) => {
    const bad = !p.healthy || health(p)[0] !== "ok";
    return h("div", { class: "item" },
      avatar(p.name),
      h("div", { class: "body" },
        h("div", { class: "title" }, h("a", { href: "#/p/" + encodeURIComponent(p.name) }, p.name), p.web ? h("span", { class: "pill plain" }, "Web 界面") : null),
        h("div", { class: "sub" }, statusLine(p))),
      h("div", { class: "side" },
        bad ? h("a", { class: "btn small danger", href: "#/rescue/" + encodeURIComponent(p.name) }, "救援") : null,
        h("a", { class: "link", href: "#/p/" + encodeURIComponent(p.name) }, `插件 ${(p.plugins || []).length}`),
        versionPicker(p)));
  });
  mount(main, lead, banners,
    h("div", { class: "card" }, rows.length ? rows : h("div", { class: "empty" }, "还没有 profile。在终端里运行 dsh plugin --profile web add <插件> 会创建一个带 Web 界面的 profile。")),
    versionChips((name) => switchFlow({ kind: "use", name }, `把默认版本切换到 ${name}`).then(rerender)));
}

async function smokeTest(name) {
  try {
    const res = await runJob(`启动测试：${name}`, () => api("/api/smoke", { profile: name }));
    const body = [];
    const clean = res.ready && !(res.inactive || []).length;
    if (clean) body.push(h("div", { class: "banner ok" }, `✓ 启动成功，所有插件都已激活（${(res.duration / 1e9).toFixed(1)} 秒）`));
    else if (res.ready) body.push(h("div", { class: "banner warn" }, `启动成功，但有 ${res.inactive.length} 个插件没有激活`));
    else if (res.exited) body.push(h("div", { class: "banner bad" }, `DSH 启动后退出了（退出码 ${res.exitCode}）`));
    else body.push(h("div", { class: "banner bad" }, "超时：DSH 没有就绪"));
    for (const i of res.inactive || []) body.push(h("div", {}, h("code", {}, i.id), "（", i.module, "）：", i.reason));
    const foot = [h("button", { class: "btn primary", onclick: closeDialog }, "好的")];
    if (!clean) foot.unshift(h("a", { class: "btn", href: "#/rescue/" + encodeURIComponent(name), onclick: closeDialog }, "去救援"));
    openDialog(`启动测试：${name}`, body, foot);
  } catch (_) { /* shown */ }
}

async function pageProfile(main, name, tab) {
  tab = tab || "plugins";
  const [st] = await Promise.all([refreshStatus(), refreshVersions()]);
  const p = st.profiles.find((x) => x.name === name);
  if (!p) { mount(main, h("div", { class: "card empty" }, `profile ${name} 不存在`)); return; }
  const head = h("div", { class: "page-head" },
    h("a", { class: "back", href: "#/" }, "‹ 全部"),
    avatar(p.name, true), h("h1", {}, name),
    h("span", { class: "muted small status" }, statusLine(p)),
    h("span", { class: "spacer" }),
    versionPicker(p),
    h("button", { class: "btn", onclick: () => smokeTest(name) }, "启动测试"),
    h("a", { class: "btn", href: "#/rescue/" + encodeURIComponent(name) }, "救援"));
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
  if (p.error) out.push(h("div", { class: "banner bad" }, "无法组装：" + p.error));
  for (const d of p.duplicates || []) out.push(h("div", { class: "banner warn" }, `id “${d.id}” 被 ${d.sources.join("、")} 重复插入，只有 ${d.sources[d.sources.length - 1]} 生效`));
  for (const s of p.skipped || []) out.push(h("div", { class: "banner warn" }, `${s.package} 被 DSH 跳过，没有生效：${s.reason.replace(/^Error: /, "").split(". ")[0]}`));
  for (const u of p.unmatched || []) out.push(h("div", { class: "banner warn" }, `覆盖找不到目标 “${u.id}”（${u.file}）`));
  return out;
}

async function tabPlugins(body, p) {
  const input = h("input", { type: "text", placeholder: "npm 包名[@版本]、本地目录，或 owner/repo（git）" });
  const add = async () => {
    const spec = input.value.trim();
    if (!spec) return;
    if (await planFlow({ kind: "add", profile: p.name, target: spec }, `安装到 ${p.name}`)) { input.value = ""; route(); }
  };
  input.addEventListener("keydown", (e) => { if (e.key === "Enter") add(); });
  const addBar = h("div", { class: "card addbar" }, input, h("button", { class: "btn primary", onclick: add }, "检查并安装"),
    h("a", { class: "btn", href: "#/market?for=" + encodeURIComponent(p.name) }, "去市场找"));

  const list = h("div", { class: "card" });
  mount(body, addBar, h("h2", {}, `已安装 · ${(p.plugins || []).length}`), list);
  if (!(p.plugins || []).length) { mount(list, h("div", { class: "empty" }, "还没有装外部插件")); return; }

  const outdated = state.outdated[p.name] || {};
  // A row whose id another plugin inserts later is silently dropped by dsh.
  const dropped = {};
  for (const d of p.duplicates || []) {
    const winner = d.sources[d.sources.length - 1];
    for (const src of d.sources.slice(0, -1)) dropped[src + "\u0000" + d.id] = winner;
  }
  const render = () => mount(list, p.plugins.flatMap((pl) => {
    const up = outdated[pl.name];
    const head = h("div", { class: "item" },
      avatar(pl.name.replace(/^@[^/]+\//, ""), true),
      h("div", { class: "body" },
        h("div", { class: "title" }, pl.name, up && up.latest ? h("span", { class: "pill info" }, "可更新到 " + up.latest) : null),
        h("div", { class: "sub" }, pl.version || "?", " · ", pl.source === "npm" ? "npm" : h("span", { class: "mono" }, pl.spec))),
      h("div", { class: "side" },
        pl.source === "npm" ? h("button", { class: "btn small" + (up && up.latest ? "" : " ghost"),
          onclick: async () => { if (await planFlow({ kind: "update", profile: p.name, target: pl.name }, `更新 ${pl.name}`)) { delete state.outdated[p.name]; route(); } } }, up && up.latest ? "更新" : "检查更新") : null,
        h("button", { class: "btn small ghost danger", onclick: async () => { if (await planFlow({ kind: "remove", profile: p.name, target: pl.name }, `卸载 ${pl.name}`)) route(); } }, "卸载")));
    // each row the plugin adds, with Magpie's switch
    const rows = (pl.rows || []).map((r) => {
      const lostTo = dropped[pl.name + "\u0000" + r.id];
      let control;
      if (lostTo) control = h("span", { class: "pill bad", title: `${lostTo} 插入了同一个 id，DSH 只加载它` }, "未生效");
      else if (r.disabled === "expr") control = h("span", { class: "pill plain" }, "由表达式决定");
      else {
        const on = r.disabled !== "true";
        control = h("button", { class: "switch" + (on ? " on" : ""), title: on ? "禁用" : "启用", "aria-label": (on ? "禁用 " : "启用 ") + r.id,
          onclick: async () => { if (await planFlow({ kind: on ? "disable" : "enable", profile: p.name, target: r.id }, `${on ? "禁用" : "启用"} ${r.id}`)) route(); } });
      }
      return h("div", { class: "item" },
        h("span", { class: "avatar small" }),
        h("div", { class: "body" }, h("div", { class: "sub" }, "配置行 ", h("code", {}, r.id), r.patched ? " · 你改过它" : "")),
        h("div", { class: "side" }, control));
    });
    return [head, ...rows];
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
  mount(body, h("div", { class: "row-flex" }, h("div", { class: "lead grow" }, "每次改动之前和之后都会自动拍快照。绿点表示那时配置能正常组装。"), take),
    h("div", { class: "card" }, snaps && snaps.length ? snaps.map((s) => h("div", { class: "item" },
      h("span", { class: "dot " + (s.verified ? "ok" : "warn") }),
      h("div", { class: "body" }, h("div", { class: "title" }, s.reason, s.manual ? h("span", { class: "pill plain" }, "手动") : null),
        h("div", { class: "sub" }, fmtTime(s.createdAt), ` · DSH ${s.dshVersion || "?"} · `, h("code", {}, s.id))),
      h("div", { class: "side" }, h("button", { class: "btn small", onclick: () => restoreFlow(s) }, "恢复到这里")))) : h("div", { class: "empty" }, "还没有快照")));
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
  mount(body, h("div", { class: "card" }, ops && ops.length ? ops.map((o) => {
    const [cls, word] = OP_STATUS[o.status] || ["plain", o.status];
    return h("div", { class: "item" },
      h("div", { class: "body" }, h("div", { class: "title" }, OP_KIND[o.kind] || o.kind, " ", h("span", { class: "mono" }, o.spec)),
        h("div", { class: "sub" }, fmtTime(o.startedAt)),
        o.log && o.status !== "applied" ? h("details", {}, h("summary", { class: "small muted" }, "日志"), h("pre", { class: "log mono" }, o.log)) : null),
      h("div", { class: "side" }, h("span", { class: "pill " + cls }, word)));
  }) : h("div", { class: "empty" }, "还没有操作记录")));
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
    h("a", { class: "back", href: "#/p/" + encodeURIComponent(name) }, "‹ " + name), h("h1", {}, "救援")),
  h("div", { class: "lead" }, "DSH 起不来、插件不生效的时候用。诊断只读，不会改动任何东西。"),
  h("div", { class: "card item" },
    h("div", { class: "body" }, h("div", { class: "title" }, "完整诊断"), h("div", { class: "sub" }, "会真正启动一次，能发现加载时才出错的插件，大约 5–20 秒")),
    h("div", { class: "side" }, h("button", { class: "btn", onclick: () => run(false) }, "快速检查"), h("button", { class: "btn primary", onclick: () => run(true) }, "开始诊断"))),
  out);
}

function renderDiagnosis(el, d, name) {
  const items = [];
  if (d.healthy && !(d.problems || []).length) {
    items.push(h("h2", {}, "结果"), h("div", { class: "banner ok" }, "✓ 没有发现问题：配置能正常组装" + (d.booted ? "，启动测试通过，所有插件都已激活" : "")));
  } else {
    items.push(h("h2", {}, "发现的问题"));
    items.push(h("div", { class: "card problems" }, h("ul", { class: "findings" }, (d.problems || []).map((pr) =>
      h("li", {}, h("span", { class: "pill " + (pr.severity === "warn" ? "warn" : "bad") }, pr.severity === "warn" ? "警告" : "问题"),
        h("span", {}, pr.message), pr.fix ? h("span", { class: "fix" }, "建议：" + pr.fix) : null)))));
    if (!d.composed) items.push(h("div", { class: "lead" }, "配置无法组装，所以没有做启动测试。"));
  }
  if (!d.healthy) {
    if (d.lastGood) {
      const lg = d.lastGood;
      items.push(h("h2", {}, "一键恢复"));
      items.push(h("div", { class: "card item" },
        h("div", { class: "body" }, h("div", { class: "title" }, `回到 ${fmtTime(lg.createdAt)} 的状态`), h("div", { class: "sub" }, lg.reason, " · ", h("code", {}, lg.id))),
        h("div", { class: "side" }, h("button", { class: "btn primary", onclick: () => restoreFlow(Object.assign({ profile: name }, lg)) }, "恢复"))));
    } else {
      items.push(h("div", { class: "banner info" }, "没有找到可以回去的正常状态（DSDock 接管之前的改动没有快照）。请按上面的建议逐个处理。"));
    }
  }
  mount(el, items);
}

async function pageVersions(main) {
  const v = await refreshVersions();
  if (!state.status) await refreshStatus().catch(() => {});
  const pinsOf = (n) => (v.pins || []).filter((p) => p.name === n).map((p) => p.profile);
  const installed = h("div", { class: "card" }, (v.installed || []).length ? v.installed.map((i) => h("div", { class: "item" },
    h("div", { class: "body" },
      h("div", { class: "title" }, i.name === "external" ? `外部安装 ${i.version}` : i.name, i.name === v.default ? h("span", { class: "pill ok" }, "默认") : null),
      h("div", { class: "sub" }, `安装于 ${fmtTime(i.installedAt)}`, i.sessionFormat ? ` · 会话格式 v${i.sessionFormat}` : "",
        pinsOf(i.name).length ? ` · 固定在这个版本：${pinsOf(i.name).join("、")}` : "",
        i.name === "external" ? h("span", { class: "mono" }, " · " + i.dir) : null)),
    h("div", { class: "side" },
      i.name !== v.default ? h("button", { class: "btn small", onclick: () => switchFlow({ kind: "use", name: i.name }, `把默认版本切换到 ${i.name}`).then(rerender) }, "设为默认") : null,
      i.name !== v.default ? h("button", { class: "btn small ghost danger", onclick: async () => {
        try { await api("/api/versions/uninstall", { name: i.name }); toast("已卸载 " + i.name); route(); } catch (e) { toast(e.message, true); }
      } }, "卸载") : null))) : h("div", { class: "empty" }, "DSDock 还没有管理任何 DSH 版本。从下面选一个安装。"));

  const shim = h("div", { class: "card item" },
    h("div", { class: "body" }, h("div", { class: "title" }, "终端里的 dsh", v.shimInstalled ? h("span", { class: "pill ok" }, "已安装") : null),
      h("div", { class: "sub" }, "装上转发器后，在终端里敲 dsh 会自动用对应 profile 的版本"),
      v.shimInstalled ? h("pre", { class: "log mono" }, v.pathHint) : null),
    h("div", { class: "side" }, h("button", { class: "btn small", onclick: async () => { const r = await api("/api/shim", {}); toast("转发器已写入 " + r.shim); route(); } }, v.shimInstalled ? "重新安装" : "安装")));

  const avail = h("div", { class: "card" }, h("div", { class: "empty" }, h("span", { class: "spin" }), " 正在读取可安装的版本…"));
  const found = h("div", {});
  mount(main, h("div", { class: "lead" }, "多个版本可以并存。没有固定版本的 profile 跟随", h("strong", {}, "默认版本"), "；在 Profile 页的下拉框里可以给某个 profile 固定版本。"),
    found, installed, h("h2", {}, "转发器"), shim, h("h2", {}, "可以安装的版本"), avail);
  // A dsh installed some other way (npm i -g) can be used as it is.
  api("/api/versions/detect").then((d) => {
    if (!d.found) return;
    mount(found, h("div", { class: "card item" },
      h("div", { class: "body" }, h("div", { class: "title" }, `发现已安装的 DSH ${d.version}`), h("div", { class: "sub mono" }, d.entry)),
      h("div", { class: "side" }, h("button", { class: "btn small primary", onclick: async () => {
        try { const i = await api("/api/versions/adopt", {}); toast(`已接管 DSH ${i.version}，原来的安装没有移动`); route(); } catch (e) { toast(e.message, true); }
      } }, "接管"))), h("div", { class: "lead small" }, "接管后由 DSDock 统一管理，原来的安装不会移动或删除。"));
  }).catch(() => {});
  try {
    const rel = await api("/api/versions/available");
    let showAll = false;
    const draw = () => {
      const shown = rel.filter((r) => !r.deprecated).slice(0, showAll ? 60 : 6);
      mount(avail, shown.map((r) => h("div", { class: "item" },
        h("div", { class: "body" }, h("div", { class: "title" }, r.version, (r.tags || []).map((t) => h("span", { class: "pill info" }, t))),
          h("div", { class: "sub" }, "发布于 " + fmtTime(r.published).slice(0, 5))),
        h("div", { class: "side" }, r.installed ? h("span", { class: "muted small" }, "已安装")
          : h("button", { class: "btn small primary", onclick: async () => {
            try {
              const res = await runJob(`安装 DSH ${r.version}`, () => api("/api/versions/install", { want: r.version }));
              closeDialog();
              toast(`已安装 DSH ${res.installed.version}` + (res.default ? "，并设为默认版本" : ""));
              route();
            } catch (_) { /* shown */ }
          } }, "安装")))),
      !showAll ? h("button", { class: "more", onclick: () => { showAll = true; draw(); } }, `显示全部 ${rel.length} 个 ⌄`) : null);
    };
    draw();
  } catch (e) { mount(avail, h("div", { class: "empty" }, "读取失败：" + e.message)); }
}

// switchFlow checks a release switch with the target release and asks.
async function switchFlow(req, title) {
  openDialog(title, h("div", { class: "row-flex" }, h("span", { class: "spin" }), "用目标版本检查每个 profile…"), []);
  let resp;
  try { resp = await api("/api/versions/plan", req); } catch (e) { showError(title, e); return false; }
  const r = resp.plan.report;
  if (resp.plan.same) { closeDialog(); return true; }
  const body = [h("div", {}, `${r.from || "（无）"} → ${r.to}`)];
  if (r.sessionDowngrade) body.push(h("div", { class: "banner warn" }, `降级：目标版本使用会话格式 v${r.toFormat}，当前是 v${r.fromFormat}。在新版本里创建或继续过的会话，旧版本打不开。`));
  for (const pc of r.profiles || []) {
    const bad = Object.keys(pc.skipped || {}).length || Object.keys(pc.incompatible || {}).length || pc.composeError;
    body.push(h("div", { class: "subcard" }, h("div", { class: "row-flex" }, h("strong", {}, pc.profile), h("span", { class: "pill " + (bad ? "bad" : "ok") }, bad ? "有问题" : "正常")),
      Object.entries(pc.skipped || {}).map(([b, why]) => h("div", { class: "small" }, `${b} 会被 DSH 跳过：${why}`)),
      Object.entries(pc.incompatible || {}).map(([b, req2]) => h("div", { class: "small" }, `${b} 不兼容：${JSON.stringify(req2)}`)),
      pc.composeError ? h("div", { class: "small" }, "用目标版本无法组装：" + pc.composeError) : null));
  }
  if (!(r.profiles || []).length) body.push(h("div", { class: "muted" }, "没有受影响的 profile"));
  // Nothing to warn about: switch without a second click.
  if (!resp.needsConfirmation && req.kind === "pin") {
    try { await api(`/api/versions/plan/${resp.id}/apply`, {}); closeDialog(); toast(`${req.profile} 固定使用 DSH ${req.name}`); return true; } catch (e) { showError(title, e); return false; }
  }
  return new Promise((resolve) => {
    openDialog(title, body, [h("button", { class: "btn", onclick: () => { closeDialog(); resolve(false); } }, "取消"),
      h("button", { class: "btn primary", onclick: async () => {
        try {
          const res = await api(`/api/versions/plan/${resp.id}/apply`, {});
          closeDialog();
          toast("已切换" + ((res.running || []).length ? "；正在运行的 profile 需要重启才会生效" : ""));
          resolve(true);
        } catch (e) { showError(title, e); resolve(false); }
      } }, resp.needsConfirmation ? "仍然切换" : "确认切换")]);
  });
}

async function pageMarket(main, params) {
  const forProfile = params.get("for") || "";
  const q = h("input", { type: "search", placeholder: "搜索 dshfind 收录的插件", value: params.get("q") || "" });
  const sort = h("select", { class: "picker" }, [["", "推荐"], ["stars", "Star 最多"], ["score", "评分最高"], ["updated", "最近更新"]].map(([v, l]) => h("option", { value: v }, l)));
  sort.value = params.get("sort") || "";
  const results = h("div", {});
  const search = () => {
    const sp = new URLSearchParams({ q: q.value.trim(), sort: sort.value });
    if (forProfile) sp.set("for", forProfile);
    location.hash = "#/market?" + sp.toString();
  };
  q.addEventListener("keydown", (e) => { if (e.key === "Enter") search(); });
  sort.addEventListener("change", search);
  mount(main, h("div", { class: "lead" }, "数据来自 dshfind.com。安装前 DSDock 会再做一遍完整检查。"),
    h("div", { class: "searchbar" }, q, sort, h("button", { class: "btn primary", onclick: search }, "搜索")), results);
  mount(results, h("div", { class: "empty" }, h("span", { class: "spin" }), " 搜索中…"));
  let data;
  try {
    data = await api("/api/market?" + new URLSearchParams({ q: q.value.trim(), sort: sort.value }).toString());
  } catch (e) { mount(results, h("div", { class: "card empty" }, "搜索失败：" + e.message)); return; }
  if (!state.status) await refreshStatus();
  const ps = state.status.profiles || [];
  mount(results, h("div", { class: "count" }, `共 ${data.total} 个`), h("div", { class: "grid" }, (data.items || []).map((it) => {
    const target = h("select", { class: "picker soft" }, ps.map((p) => h("option", { value: p.name }, "装到 " + p.name)));
    if (forProfile) target.value = forProfile;
    return h("div", { class: "mcard" },
      h("div", { class: "mtitle" }, h("a", { class: "name", href: "https://dshfind.com/plugins/" + it.fullName, target: "_blank", rel: "noopener noreferrer" }, it.fullName),
        it.grade ? h("span", { class: "pill info" }, it.grade) : null),
      h("div", { class: "desc" }, it.description || "（没有描述）"),
      h("div", { class: "meta" }, `★ ${it.stars}`, it.category ? h("span", { class: "pill plain" }, it.category) : null, h("span", { class: "pill plain" }, it.fromNpm ? "npm" : "git")),
      it.risky ? h("div", { class: "banner bad small" }, "dshfind 标记为有风险：" + (it.riskNote || "")) : null,
      it.archived ? h("div", { class: "banner warn small" }, "仓库已归档") : null,
      ps.length ? h("div", { class: "install" }, target, h("button", { class: "btn small primary", onclick: async () => {
        if (await planFlow({ kind: "add", profile: target.value, target: it.spec }, `安装 ${it.fullName} 到 ${target.value}`)) toast("已安装到 " + target.value);
      } }, "安装")) : h("div", { class: "muted small" }, "先创建一个 profile 才能安装"));
  })));
}

// ---------- menu bar panel ----------
async function pagePanel(main) {
  document.body.classList.add("panel");
  const [st] = await Promise.all([refreshStatus(), refreshVersions()]);
  const open = (route) => (state.meta && state.meta.desktop ? desktop("open", { route }) : (location.hash = "#" + route));
  const rows = st.profiles.map((p) => {
    const bad = !p.healthy || health(p)[0] !== "ok";
    return h("div", { class: "item" },
      avatar(p.name, true),
      h("div", { class: "body" },
        h("div", { class: "title" }, h("a", { href: "#", onclick: (e) => { e.preventDefault(); open("/p/" + encodeURIComponent(p.name)); } }, p.name)),
        h("div", { class: "sub" }, statusLine(p))),
      h("div", { class: "side" },
        bad ? h("button", { class: "btn small danger", onclick: () => open("/rescue/" + encodeURIComponent(p.name)) }, "救援") : null,
        versionPicker(p, true)));
  });
  const ups = [];
  mount(main, h("div", { class: "pn" },
    h("div", { class: "pn-head" },
      h("div", { class: "brand" }, h("img", { src: "icon.svg", alt: "" }), h("span", {}, "DSDock")),
      h("span", { class: "spacer" }),
      h("button", { class: "icon-btn", title: "刷新", onclick: () => route() }, icon("refresh")),
      h("button", { class: "icon-btn", title: "打开 DSDock", onclick: () => open("/") }, icon("open"))),
    st.dsh.error ? h("div", { class: "banner info" }, "还没有可用的 DSH。") : null,
    h("div", { class: "card" }, rows.length ? rows : h("div", { class: "empty" }, "还没有 profile")),
    versionChips((name) => switchFlow({ kind: "use", name }, `把默认版本切换到 ${name}`).then(rerender)),
    h("div", { class: "pn-note muted", id: "pn-updates" }, ups),
    h("div", { class: "pn-foot" },
      h("button", { class: "btn ghost", onclick: () => open("/") }, "打开 DSDock"),
      state.meta && state.meta.desktop ? h("button", { class: "btn ghost", onclick: () => desktop("quit") }, "退出") : null)));

  fitPanel();
  // updates are a network round per plugin: count them after the panel shows
  let total = 0;
  await Promise.all(st.profiles.map((p) => api("/api/outdated?profile=" + encodeURIComponent(p.name))
    .then((list) => { total += (list || []).filter((u) => u.latest).length; }).catch(() => {})));
  if (total) mount($("#pn-updates"), h("a", { href: "#", onclick: (e) => { e.preventDefault(); open("/"); } }, `${total} 个插件有更新 ›`));
  fitPanel();
}

// fitPanel asks the native panel to be as tall as its content.
function fitPanel() {
  const pn = document.querySelector(".pn");
  if (!pn || !(state.meta && state.meta.desktop)) return;
  requestAnimationFrame(() => desktop("resize", { height: Math.ceil(pn.getBoundingClientRect().height) }));
}

// ---------- router ----------
async function route() {
  takeToken();
  const main = $("#main");
  const raw = location.hash.replace(/^#\/?/, "");
  const [path, query] = raw.split("?");
  const params = new URLSearchParams(query || "");
  markNav();
  if (!state.meta) {
    try { state.meta = await api("/api/meta"); } catch (e) {
      mount(main, h("div", { class: "banner bad" }, TOKEN ? e.message : "缺少访问令牌。请从终端里 dsdock gui 打印的地址重新打开。"));
      return;
    }
    if (state.meta.desktop) document.body.classList.add("desktop");
  }
  if (path !== "panel") document.body.classList.remove("panel");
  try {
    if (!path) await pageHome(main);
    else if (path === "panel") await pagePanel(main);
    else if (path === "versions") await pageVersions(main);
    else if (path === "market") await pageMarket(main, params);
    else if (path.startsWith("p/")) await pageProfile(main, decodeURIComponent(path.slice(2)), query);
    else if (path.startsWith("rescue/")) await pageRescue(main, decodeURIComponent(path.slice(7)));
    else mount(main, h("div", { class: "card empty" }, "页面不存在"));
  } catch (e) {
    mount(main, h("div", { class: "banner bad" }, e.message));
  }
  markNav();
}

window.addEventListener("hashchange", route);
$("#refresh").append(icon("refresh"));
$("#refresh").addEventListener("click", () => route());
route();
