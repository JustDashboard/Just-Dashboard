// Compares each distribution's results with the baseline's, so a page that
// works on the baseline and not on a distribution shows even where it is not
// a 5xx (a 400 for a tool that is missing, a 503 the baseline does not give).
//
//   node compare.mjs <results directory> [baseline name]
//
// For each distribution: the verdict, the installer's warnings, and every
// route whose answer differs from the baseline's after either phase.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const [dir, baseline = "ubuntu-24.04"] = process.argv.slice(2);
const read = (name, file) => {
  const path = join(dir, name, file);
  return existsSync(path) ? readFileSync(path, "utf8") : null;
};
const json = (name, file) => {
  const text = read(name, file);
  try {
    return text ? JSON.parse(text) : null;
  } catch {
    return null;
  }
};
// An answer, as a page shows it: works, a named state, refused, or broken.
const kind = (r) => {
  if (!r) return "missing";
  if (r.status >= 200 && r.status < 300) return "ok";
  if (r.status === 503 && r.code) return `503 ${r.code}`;
  if (r.status === 0) return r.error;
  return String(r.status);
};

const base = { install: json(baseline, "check-install.json"), reboot: json(baseline, "check-reboot.json") };
for (const name of readdirSync(dir).sort()) {
  const verdict = read(name, "verdict");
  if (!verdict) continue;
  console.log(`\n## ${name}: ${verdict.trim()}`);
  const facts = read(name, "facts");
  if (facts) console.log(facts.trim().replace(/^/gm, "   "));
  const warnings = (read(name, "install.log") || "").split("\n").filter((l) => l.includes("[!]"));
  for (const w of warnings) console.log(`   warning: ${w.replace(/^\s*\[!\]\s*/, "")}`);
  for (const phase of ["install", "reboot"]) {
    const mine = json(name, `check-${phase}.json`);
    if (!mine) continue;
    for (const f of mine.failures || []) console.log(`   ${phase} failure: ${f.slice(0, 240)}`);
    if (mine.terminal) console.log(`   ${phase} terminal: ${mine.terminal.shell} as ${mine.terminal.user}, ran: ${mine.terminal.ran}`);
    if (name === baseline || !base[phase]) continue;
    const routes = new Set([...Object.keys(base[phase].routes), ...Object.keys(mine.routes || {})]);
    for (const route of [...routes].sort()) {
      const a = kind(base[phase].routes[route]);
      const b = kind(mine.routes?.[route]);
      if (a !== b) console.log(`   ${phase} differs: ${route}  ${baseline}=${a}  here=${b}`);
    }
  }
}
