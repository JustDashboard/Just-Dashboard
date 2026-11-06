/**
 * `/hub/enter`: where Just Hub opens this server with a sign-in ticket in the address's fragment
 * (docs/internal/backend/hub-link-protocol.md §9.5).
 *
 * The page is a document written by hand and served by a route handler (`app/hub/enter/route.ts`),
 * not a page of the app, because of what §9.5 asks of it:
 *
 * - Its first script takes the ticket out of the address before anything else runs. Next puts its
 *   own chunks at the top of every page's `<head>`, ahead of anything a layout renders, and an
 *   async chunk that is already cached may run before a later inline script; here the inline
 *   script is the first thing in `<head>` after the charset, and the document has no other.
 * - Nothing on this path redirects, and no provider, layout or shell sees the address while the
 *   ticket is in it: none of them runs here.
 * - It loads nothing from anywhere: its style is inline, its mark is inline, and it asks this
 *   server two things only, `GET /api/v1/auth/session` and the redeem, `POST /api/v1/auth/hub/enter`,
 *   whose body is the one place the ticket goes.
 *
 * The ticket is kept in a variable of the script's own closure, never in a global, storage, a
 * URL or a log, and it is dropped as soon as the redeem answers. It is never retried.
 */

/** The address the page leaves in the bar and the history entry. */
export const HUB_ENTER_PATH = "/hub/enter"

/**
 * The ticket in a fragment such as `#ticket=v1.hub-2026-11.….…`, or "". The script below does the
 * same in the browser; this copy is what the unit tests read.
 */
export function ticketFromHash(hash: string): string {
  const match = /(?:^#|&)ticket=([^&]*)/.exec(hash)
  if (!match) return ""
  try {
    return decodeURIComponent(match[1])
  } catch {
    return ""
  }
}

/**
 * The account a ticket names, read for display only: the question "you are signed in as ion" is
 * asked when the browser holds another account's session. The server verifies everything.
 */
export function ticketUserId(ticket: string): number {
  const segment = ticket.split(".")[2]
  if (!segment) return 0
  try {
    let b64 = segment.replace(/-/g, "+").replace(/_/g, "/")
    while (b64.length % 4) b64 += "="
    const payload = JSON.parse(atob(b64)) as { userId?: unknown }
    return typeof payload.userId === "number" ? payload.userId : 0
  } catch {
    return 0
  }
}

/** A `next` the page may navigate to: a path on this server, never another origin. */
export function safeNext(next: unknown): string {
  return typeof next === "string" && next.startsWith("/") && !next.startsWith("//")
    ? next
    : "/dashboard"
}

/**
 * What the page says for each refusal of §9.7, and for the two answers that can come before the
 * redeem runs (the sign-in rate limit of §9.6, and a server error), keyed by the answer's code.
 * The API's own messages are lower-case clauses meant to sit inside other text, and they say
 * "sign in here" beside a button that says "Sign in on this server"; the page says each in a
 * sentence of its own that names that button. A code not listed here is said as the server's
 * message, made a sentence.
 */
export const HUB_ENTER_REFUSALS: Readonly<Record<string, string>> = {
  hub_signin_disabled:
    "Signing in through Just Hub is not turned on for this server. Sign in on this server instead.",
  hub_not_linked:
    "This server is not connected to a Just Hub it can trust right now. Sign in on this server instead.",
  bad_ticket:
    "This sign-in link is not one this server can read. Open the server again from Just Hub.",
  wrong_server:
    "This sign-in link was made for another server, or by another hub. Open this server from the Just Hub it is connected to.",
  ticket_expired:
    "This sign-in link has expired. Open the server again from Just Hub, and if it keeps happening, check this server's clock.",
  mfa_stale:
    "Just Hub did not confirm your authenticator code recently enough. Open the server again from Just Hub.",
  wrong_device:
    "This sign-in link was made for another device or another tailnet login. Open the server from Just Hub on this device.",
  ticket_used:
    "This sign-in link has already been used. Open the server again from Just Hub, or sign in on this server.",
  not_permitted:
    "This account does not let that Just Hub user sign it in. Sign in on this server, and turn it on in Settings → Hub.",
  account_unavailable:
    "This account cannot be signed in through Just Hub right now. Sign in on this server instead.",
  other_account_signed_in:
    "This browser is signed in to another account on this server. Open the server again from Just Hub to choose which account to use.",
  rate_limited:
    "Too many sign-in attempts from this address. Wait a minute, then open the server again from Just Hub.",
  internal:
    "This server could not finish signing you in. Open it again from Just Hub, or sign in on this server.",
}

/**
 * The script, as the browser runs it. It is plain ES2017 so it needs no build step, and it repeats
 * the three helpers above rather than importing them: there is nothing to import from here. The
 * refusals are written into it as data.
 */
export const HUB_ENTER_SCRIPT = `(function () {
  "use strict";
  var refusals = ${JSON.stringify(HUB_ENTER_REFUSALS)};
  var match = /(?:^#|&)ticket=([^&]*)/.exec(location.hash);
  var ticket = "";
  if (match) {
    try { ticket = decodeURIComponent(match[1]); } catch (e) { ticket = ""; }
  }
  history.replaceState(null, "", ${JSON.stringify(HUB_ENTER_PATH)});

  function el(id) { return document.getElementById(id); }
  function show(id) {
    ["working", "ask", "failed"].forEach(function (name) { el(name).hidden = name !== id; });
  }
  function fail(message) {
    ticket = "";
    el("message").textContent = message;
    show("failed");
  }
  // The page's sentence for a refusal's code; any other code is the server's message, as a
  // sentence, and no message at all is the page's own.
  function refusal(error) {
    var code = error && typeof error.code === "string" ? error.code : "";
    if (Object.prototype.hasOwnProperty.call(refusals, code)) return refusals[code];
    var message = error && typeof error.message === "string" ? error.message.trim() : "";
    if (!message) return "This server did not accept the sign-in link. Open it again from Just Hub, or sign in on this server.";
    message = message.charAt(0).toUpperCase() + message.slice(1);
    return /[.!?]$/.test(message) ? message : message + ".";
  }
  function userId(t) {
    var segment = t.split(".")[2];
    if (!segment) return 0;
    try {
      var b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
      while (b64.length % 4) b64 += "=";
      var payload = JSON.parse(atob(b64));
      return typeof payload.userId === "number" ? payload.userId : 0;
    } catch (e) { return 0; }
  }
  function redeem(replace) {
    show("working");
    var body = JSON.stringify(replace ? { ticket: ticket, replace: true } : { ticket: ticket });
    ticket = "";
    fetch("/api/v1/auth/hub/enter", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "X-JD-CSRF": "1" },
      body: body
    }).then(function (response) {
      return response.json().then(function (answer) {
        return { ok: response.ok, answer: answer };
      }, function () { return { ok: false, answer: null }; });
    }).then(function (result) {
      var answer = result.answer || {};
      if (result.ok) {
        var next = typeof answer.next === "string" && answer.next.charAt(0) === "/" &&
          answer.next.charAt(1) !== "/" ? answer.next : "/dashboard";
        location.replace(next);
        return;
      }
      fail(refusal(answer.error));
    }, function () {
      fail("This server could not be reached. Open it again from Just Hub.");
    });
  }
  function ask(username) {
    el("ask-known").hidden = !username;
    el("ask-unknown").hidden = !!username;
    if (username) {
      el("as").textContent = username;
      el("as-again").textContent = username;
    }
    show("ask");
    el("continue").focus();
  }
  // Who this browser is signed in as decides whether to ask. 401 is nobody, and 200 names the
  // account; anything else, or no answer, could be anyone, so the question is asked without a
  // name rather than a session ended unasked. A session still owing its second factor is not
  // signed in; one owing a new password is.
  function signedInAs(session) {
    var user = session && session.user;
    if (!user || typeof user.id !== "number" || session.needsTotp || session.needsEnrollment) return null;
    return session.authenticated || session.needsPasswordChange ? user : null;
  }
  function start() {
    if (!ticket) {
      fail("This address carries no sign-in link. Open the server again from Just Hub.");
      return;
    }
    el("continue").addEventListener("click", function () { redeem(true); });
    el("stay").addEventListener("click", function () {
      ticket = "";
      location.replace("/dashboard");
    });
    fetch("/api/v1/auth/session", { credentials: "same-origin", cache: "no-store" })
      .then(function (response) {
        if (response.status === 401) return { nobody: true };
        if (response.status !== 200) return null;
        return response.json().then(function (session) { return { session: session }; },
          function () { return null; });
      })
      .then(function (read) {
        if (!read) {
          ask("");
          return;
        }
        var user = read.nobody ? null : signedInAs(read.session);
        if (user && user.id !== userId(ticket)) {
          ask(typeof user.username === "string" && user.username ? user.username : "another account");
          return;
        }
        redeem(false);
      }, function () { ask(""); });
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();`

/** The style: the sign-in page's dark surface, tokens copied from globals.css. */
const STYLE = `
:root{color-scheme:dark;--background:oklch(0.16 0 0);--foreground:oklch(1 0 0);--card:oklch(0.183 0 0);
--muted:oklch(0.7118 0.0129 286.0665);--border:oklch(0.275 0 0);--brand:oklch(0.9185 0.0444 238.9);
--signal:oklch(0.78 0.13 238.9);--chart-2:oklch(0.72 0.152 258);--radius:0.5rem}
*{box-sizing:border-box}
html,body{margin:0;min-height:100%;background:var(--background);color:var(--foreground)}
body{font-family:"Source Sans 3",ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;
font-size:15px;line-height:1.5;-webkit-font-smoothing:antialiased;min-height:100svh;display:flex;
background-image:radial-gradient(55% 42% at 50% -6%,color-mix(in oklab,var(--signal) 20%,transparent),transparent 72%),
radial-gradient(46% 52% at 0% 100%,color-mix(in oklab,var(--chart-2) 18%,transparent),transparent 72%)}
main{margin:auto;width:100%;max-width:36rem;padding:24px 16px;display:flex;flex-direction:column;gap:24px}
.mark{display:flex;align-items:center;gap:8px;color:var(--brand)}
.mark svg{height:32px;width:auto}
.mark span{font-weight:600;letter-spacing:-0.01em;color:var(--foreground)}
.eyebrow{margin:0 0 6px;font-size:12px;font-weight:600;letter-spacing:0.06em;text-transform:uppercase;color:var(--muted)}
h1{margin:0;font-size:24px;line-height:1.2;font-weight:600;letter-spacing:-0.02em}
p{margin:0;color:var(--muted)}
p strong{color:var(--foreground);font-weight:600}
.actions{display:flex;flex-wrap:wrap;gap:8px}
button,a.button{font:inherit;font-size:14px;font-weight:500;height:36px;padding:0 14px;border-radius:var(--radius);
border:1px solid var(--border);background:var(--card);color:var(--foreground);cursor:pointer;
display:inline-flex;align-items:center;text-decoration:none}
button.primary{background:var(--foreground);color:var(--background);border-color:var(--foreground)}
button:focus-visible,a.button:focus-visible{outline:2px solid var(--signal);outline-offset:2px}
section{display:flex;flex-direction:column;gap:16px}
section[hidden]{display:none}
`

/** The product mark, inline so the page loads nothing. */
const MARK = `<svg viewBox="0 0 199 256" fill="currentColor" aria-hidden="true" focusable="false"><path d="M198.4 0L198.4 49.6L116.8 96.71L116.8 47.11ZM198.4 62.4L198.4 195.49L93.6 256L0 162.4L70.14 162.4L101.76 194.02L116.8 185.33L116.8 109.51Z"/></svg>`

function attribute(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;")
}

/**
 * The whole document. The nonce is this response's CSP nonce (the proxy's `x-nonce`); the script
 * carrying it is the first in the document and the only one.
 */
export function hubEnterDocument(nonce: string): string {
  const n = attribute(nonce)
  return `<!doctype html>
<html lang="en" class="dark">
<head>
<meta charset="utf-8">
<script nonce="${n}">${HUB_ENTER_SCRIPT}</script>
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="referrer" content="no-referrer">
<title>Signing in · Just Dashboard</title>
<link rel="icon" href="data:,">
<style nonce="${n}">${STYLE}</style>
</head>
<body>
<main>
<div class="mark">${MARK}<span>Just Dashboard</span></div>
<section id="working" aria-live="polite">
<div><p class="eyebrow">Just Hub</p><h1>Signing you in</h1></div>
<p>Checking the sign-in link Just Hub opened this server with.</p>
</section>
<section id="ask" hidden>
<div><p class="eyebrow">Just Hub</p><h1>Continue through Just Hub?</h1></div>
<p id="ask-known">You are signed in as <strong id="as"></strong>. Continuing signs <strong id="as-again"></strong> out in this browser and signs you in as the account Just Hub names.</p>
<p id="ask-unknown" hidden>This server could not say who is signed in in this browser. Continuing signs out whoever is, and signs you in as the account Just Hub names.</p>
<div class="actions"><button id="continue" class="primary" type="button">Continue</button><button id="stay" type="button">Stay</button></div>
</section>
<section id="failed" hidden role="alert">
<div><p class="eyebrow">Just Hub</p><h1>Could not sign you in</h1></div>
<p id="message"></p>
<div class="actions"><a class="button" href="/login">Sign in on this server</a></div>
</section>
</main>
</body>
</html>
`
}
