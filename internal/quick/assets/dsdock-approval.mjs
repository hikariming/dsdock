// DSDock's approval answerer, loaded into a headless dsh through a --patch
// overlay. headless has no answerer of its own, so every action that needs
// approval would be rejected; this one asks DSDock instead.
//
// Protocol (line-based, no network):
//   to DSDock   (stderr): dsdock:approval {"id","toolName","callId","reason"}
//   from DSDock (stdin):  {"id","outcome"}   outcome: "allowed-once" | "rejected"
// Anything else, a closed stdin or an aborted request, answers "rejected" or
// "cancelled": approval fails closed, as dsh's own answerers do.
import { randomUUID } from "node:crypto";

export const name = "dsdock-approval";

export function apply(ctx) {
  const pending = new Map();
  let buffer = "";
  const onData = (chunk) => {
    buffer += chunk;
    let nl;
    while ((nl = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, nl);
      buffer = buffer.slice(nl + 1);
      let msg;
      try { msg = JSON.parse(line); } catch { continue; }
      const resolve = pending.get(msg && msg.id);
      if (!resolve) continue;
      pending.delete(msg.id);
      idle();
      resolve(msg.outcome === "allowed-once" ? "allowed-once" : "rejected");
    }
  };
  // stdin keeps the process alive only while a question is open; otherwise
  // dsh could not exit when the task is done.
  const idle = () => { if (pending.size === 0) process.stdin.unref?.(); };
  const onEnd = () => {
    for (const resolve of pending.values()) resolve("rejected");
    pending.clear();
  };
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", onData);
  process.stdin.on("end", onEnd);
  // Waiting for an answer must not be what keeps the process alive.
  process.stdin.unref?.();

  ctx.on("approval/request", (request) => new Promise((resolve) => {
    const id = randomUUID();
    pending.set(id, resolve);
    request.signal?.addEventListener("abort", () => {
      if (pending.delete(id)) { idle(); resolve("cancelled"); }
    }, { once: true });
    process.stdin.ref?.();
    process.stderr.write("dsdock:approval " + JSON.stringify({
      id, toolName: request.toolName, callId: request.callId, reason: request.displayReason ?? request.reason,
    }) + "\n");
  }));

  ctx.effect(() => () => {
    process.stdin.off("data", onData);
    process.stdin.off("end", onEnd);
    onEnd();
  });
}
