// Uses an installed dashboard the way a person would, from inside the machine
// it was installed on: run.sh starts it in that machine's network namespace,
// where the dashboard listens. Node 22 or later; nothing to install.
//
//   node check.mjs <port> <username> <password> <routes file> [phase]
//
// It signs in through the proxy, opens every page's data (each GET route that
// takes no parameter, from the routes file), opens a terminal on the host and
// runs a command in it, and prints one JSON object. Phase "reboot" does the
// same after the machine has restarted. A route answering 5xx is a failure,
// except a 503 that names what this server lacks; a route that takes longer
// than the timeout to send its headers is reported as slow, because a few
// stream or reach the internet.

import { readFileSync } from "node:fs";

const [port, username, password, routesFile, phase = "install"] = process.argv.slice(2);
// The proxy serves the site by the name the installer gave it (localhost for
// an SSH tunnel) and answers any other host with nothing.
const base = `http://localhost:${port}`;
const routeTimeoutMs = 20_000;
const result = { phase, login: null, session: null, routes: {}, terminal: null, failures: [] };
let cookie = "";

async function call(path, { method = "GET", body, timeout = routeTimeoutMs } = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  try {
    const res = await fetch(base + path, {
      method,
      redirect: "manual",
      signal: controller.signal,
      headers: {
        ...(cookie ? { cookie } : {}),
        ...(body ? { "content-type": "application/json" } : {}),
        "x-jd-csrf": "1",
        origin: `http://localhost:${port}`,
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const type = res.headers.get("content-type") || "";
    // A stream answers its headers and then holds the body open.
    if (type.includes("text/event-stream")) {
      controller.abort();
      return { status: res.status, stream: true, res };
    }
    const text = await res.text();
    return { status: res.status, text, res };
  } catch (err) {
    return { status: 0, error: err.name === "AbortError" ? "timeout" : String(err.cause?.code || err.message) };
  } finally {
    clearTimeout(timer);
  }
}

const page = await call("/login");
if (page.status !== 200) result.failures.push(`GET /login answered ${page.status}`);

const login = await call("/api/v1/auth/login", { method: "POST", body: { username, password } });
result.login = login.status;
if (login.status !== 200) {
  result.failures.push(`sign-in answered ${login.status}: ${(login.text || login.error || "").slice(0, 300)}`);
  console.log(JSON.stringify(result));
  process.exit(1);
}
cookie = (login.res.headers.getSetCookie?.() || [])
  .map((c) => c.split(";")[0])
  .join("; ");

const session = await call("/api/v1/auth/session");
try {
  const s = JSON.parse(session.text);
  result.session = { authenticated: s.authenticated, needsPasswordChange: s.needsPasswordChange };
  if (!s.authenticated) result.failures.push("the session after sign-in is not authenticated");
} catch {
  result.failures.push(`GET /api/v1/auth/session answered ${session.status}`);
}

// The API allows each account a budget a minute, so the walk is paced by it:
// a 429 waits for as long as Retry-After says and asks again.
const routes = readFileSync(routesFile, "utf8").split("\n").map((l) => l.trim()).filter((l) => l && !l.includes("{"));
async function paced(route) {
  for (let attempt = 0; ; attempt++) {
    const r = await call(route);
    if (r.status !== 429 || attempt === 20) return r;
    const wait = Number(r.res?.headers.get("retry-after")) || 5;
    await new Promise((resolve) => setTimeout(resolve, wait * 1000));
  }
}
let next = 0;
async function worker() {
  while (next < routes.length) {
    const route = routes[next++];
    const r = await paced(route);
    const entry = { status: r.status };
    if (r.stream) entry.stream = true;
    if (r.error) entry.error = r.error;
    if (r.status >= 500) {
      entry.body = (r.text || "").slice(0, 300);
      try {
        entry.code = JSON.parse(r.text).error.code;
      } catch {}
    }
    result.routes[route] = entry;
  }
}
await Promise.all(Array.from({ length: 3 }, worker));
// A 503 that names what is missing is a page saying "not on this server"
// (fail2ban not installed, no stream bridge), which run.sh compares with the
// baseline rather than failing. Anything else at 5xx is the dashboard broken.
for (const [route, r] of Object.entries(result.routes)) {
  if (r.status >= 500 && !(r.status === 503 && r.code)) {
    result.failures.push(`GET ${route} answered ${r.status}: ${r.body}`);
  }
  if (r.status === 0 && r.error !== "timeout") result.failures.push(`GET ${route} failed: ${r.error}`);
  if (r.status === 429) result.failures.push(`GET ${route} was still rate limited after twenty waits`);
}

// The terminal is a shell on the host, so it is where a distribution's own
// shell, accounts and namespaces show.
const created = await call("/api/v1/terminal/", { method: "POST", body: { rows: 24, cols: 100 } });
if (created.status !== 201) {
  result.failures.push(`opening a terminal answered ${created.status}: ${(created.text || created.error || "").slice(0, 300)}`);
} else {
  const term = JSON.parse(created.text);
  result.terminal = { shell: term.shell, user: term.user };
  const output = await new Promise((resolve) => {
    let seen = "";
    const ws = new WebSocket(`ws://localhost:${port}/api/v1/terminal/${term.id}/attach?rows=24&cols=100`, {
      headers: { cookie, origin: `http://localhost:${port}` },
    });
    ws.binaryType = "arraybuffer";
    const done = (value) => {
      clearTimeout(timer);
      try { ws.close(); } catch {}
      resolve(value);
    };
    const timer = setTimeout(() => done(seen), 30_000);
    ws.onopen = () => {
      setTimeout(() => {
        ws.send(JSON.stringify({ type: "input", data: '. /etc/os-release; echo "JDV_ANSWER=$((6*7)) JDV_OS=$ID"\r' }));
      }, 1500);
    };
    ws.onmessage = (event) => {
      seen += typeof event.data === "string" ? "" : Buffer.from(event.data).toString("utf8");
      if (/JDV_ANSWER=42 JDV_OS=\S+/.test(seen)) done(seen);
    };
    ws.onerror = (event) => done(seen + `\n[websocket error: ${event.message || "closed"}]`);
  });
  const match = /JDV_ANSWER=42 JDV_OS=(\S+)/.exec(output);
  result.terminal.ran = Boolean(match);
  result.terminal.os = match?.[1] ?? null;
  if (!match) result.failures.push(`the terminal did not run a command; it showed: ${output.slice(-400)}`);
}

result.counts = Object.values(result.routes).reduce((acc, r) => {
  const key = r.status === 0 ? r.error : String(r.status)[0] + "xx";
  acc[key] = (acc[key] || 0) + 1;
  return acc;
}, {});
console.log(JSON.stringify(result));
process.exit(result.failures.length ? 1 : 0);
