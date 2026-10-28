// Drives a headless Chromium through the DSDock GUI and saves screenshots,
// so changes to the interface can be looked at without a desktop.
//
//   node scripts/gui-shots.mjs <gui-url-with-token> <out-dir> <steps.json>
//
// CHROME points at a Chromium binary (chrome-headless-shell works). steps is
// a list of {go}, {fill: [selector, value]}, {click: selector or text},
// {wait: selector or ms}, {viewport: [w, h]}, {shot: name}. Needs Node >= 22 (global WebSocket).
import { spawn } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const [url, outDir, stepsFile] = process.argv.slice(2);
const chrome = process.env.CHROME;
if (!url || !outDir || !stepsFile || !chrome) {
  console.error("usage: CHROME=<chromium> node scripts/gui-shots.mjs <url> <out-dir> <steps.json>");
  process.exit(2);
}
mkdirSync(outDir, { recursive: true });
const steps = JSON.parse(readFileSync(stepsFile, "utf8"));
const base = url.split("#")[0];
const token = (url.match(/t=([0-9a-f]+)/) || [])[1];

const port = 9300 + Math.floor(Math.random() * 500);
// Its own process group, so the whole tree (zygotes included) can be stopped.
const proc = spawn(chrome, ["--no-sandbox", "--disable-gpu", "--hide-scrollbars", `--remote-debugging-port=${port}`, "--window-size=1280,860", "about:blank"], { stdio: "ignore", detached: true });
const stopBrowser = () => { try { process.kill(-proc.pid, "SIGTERM"); } catch { /* already gone */ } };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let target;
for (let i = 0; i < 50 && !target; i++) {
  await sleep(200);
  try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((t) => t.type === "page"); } catch { /* starting */ }
}
if (!target) { stopBrowser(); throw new Error("chromium did not start"); }

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r, { once: true }));
let seq = 0;
const pending = new Map();
ws.addEventListener("message", (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
});
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++seq;
  pending.set(id, (m) => (m.error ? reject(new Error(m.error.message)) : resolve(m.result)));
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expr) => (await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true })).result.value;

await send("Page.enable");
await send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 860, deviceScaleFactor: 1, mobile: false });

// finder locates an element by CSS selector or by its visible text: among
// clickable elements for clicks, among all elements when waiting.
const finder = (q, any) => `(() => {
  const q = ${JSON.stringify(q)};
  try { const el = document.querySelector(q); if (el) return el; } catch (_) {}
  const sel = ${any ? '"body *"' : '"button, a, summary, label"'};
  const all = [...document.querySelectorAll(sel)].filter((e) => e.offsetParent !== null || e.closest("dialog[open]"));
  return all.reverse().find((e) => e.textContent.trim() === q) || all.find((e) => e.children.length === 0 && e.textContent.includes(q)) || null;
})()`;

try {
for (const step of steps) {
  if (step.viewport) {
    const [width, height] = step.viewport;
    await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: step.scale || 1, mobile: false });
  } else if (step.go !== undefined) {
    await send("Page.navigate", { url: `${base}#t=${token}&go=${encodeURIComponent(step.go)}` });
    await sleep(step.settle || 2500);
  } else if (step.fill) {
    const [sel, value] = step.fill;
    await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(sel)}); el.value = ${JSON.stringify(value)}; el.dispatchEvent(new Event("input")); return true; })()`);
  } else if (step.click) {
    const ok = await evaluate(`(() => { const el = ${finder(step.click)}; if (!el) return false; el.click(); return true; })()`);
    if (!ok) throw new Error("not found: " + step.click);
    await sleep(step.settle || 400);
  } else if (step.wait !== undefined) {
    if (typeof step.wait === "number") { await sleep(step.wait); continue; }
    const deadline = Date.now() + (step.timeout || 60000);
    while (!(await evaluate(`!!(${finder(step.wait, true)})`))) {
      if (Date.now() > deadline) throw new Error("timed out waiting for " + step.wait);
      await sleep(300);
    }
    await sleep(300);
  } else if (step.shot) {
    const { data } = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(join(outDir, step.shot + ".png"), Buffer.from(data, "base64"));
    console.log("saved", step.shot);
  }
}
} finally {
  ws.close();
  stopBrowser(); // the whole group, never just the parent (LESSONS 1)
}
