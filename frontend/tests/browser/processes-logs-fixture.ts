import type { Page, Route } from "@playwright/test"

/**
 * The Processes section's logs, as the log routes would answer them: a unit
 * that is crash-looping, a timer's service that failed one night, cron's
 * journal and a PM2 worker's output. The manager's lines carry what the
 * systemd lens reads out of them (`event`, and the journal's invocation and
 * exit fields in `attrs`), in the research's own words
 * (`research-system-logs.md`), so the Runs view groups what systemd writes.
 *
 * Every socket and search is recorded, so a spec can say what a press asked
 * the server for.
 */

const now = Date.now()
const ago = (ms: number) => new Date(now - ms).toISOString()
const MIN = 60_000
const HOUR = 60 * MIN

type Line = {
  text: string
  timestamp?: string
  level?: string
  source?: string
  stream?: string
  event?: string
  attrs?: Record<string, string>
}

const manager = (
  unit: string,
  at: number,
  event: string,
  text: string,
  attrs: Record<string, string> = {},
  level = "info",
): Line => ({
  text,
  timestamp: ago(at),
  level,
  source: "systemd[1]",
  event,
  attrs: { unit, program: "systemd", ...attrs },
})

/** One turn of postgresql@16-main's loop: it starts, exits 1, fails and is restarted. */
function crash(invocation: string, at: number, counter: number): Line[] {
  const unit = "postgresql.service"
  return [
    manager(unit, at, "starting", "Starting postgresql.service - PostgreSQL RDBMS...", {
      invocation,
    }),
    manager(
      unit,
      at - 800,
      "exited",
      "postgresql.service: Main process exited, code=exited, status=1/FAILURE",
      { invocation, exit_code: "exited", exit_status: "1" },
      "error",
    ),
    manager(
      unit,
      at - 810,
      "failed",
      "postgresql.service: Failed with result 'exit-code'.",
      { invocation, result: "exit-code" },
      "error",
    ),
    manager(
      unit,
      at - 5_800,
      "restart_scheduled",
      `postgresql.service: Scheduled restart job, restart counter is at ${counter}.`,
      { invocation, restarts: String(counter) },
      "warn",
    ),
    manager(unit, at - 5_810, "stopped", "Stopped postgresql.service - PostgreSQL RDBMS.", {
      invocation,
    }),
  ]
}

/**
 * The unit's week: a clean stop yesterday, then a loop, then the start
 * limit. systemd refuses the fifth start without starting anything — no new
 * invocation, no "Starting" — so the refusal is written under the run that
 * crashed last.
 */
export const POSTGRES_RUNS: Line[] = [
  manager("postgresql.service", 26 * HOUR, "starting", "Starting postgresql.service...", {
    invocation: "a0",
  }),
  manager("postgresql.service", 26 * HOUR - 1200, "started", "Started postgresql.service.", {
    invocation: "a0",
  }),
  manager(
    "postgresql.service",
    3 * MIN,
    "deactivated",
    "postgresql.service: Deactivated successfully.",
    {
      invocation: "a0",
    },
  ),
  manager("postgresql.service", 3 * MIN - 5, "stopped", "Stopped postgresql.service.", {
    invocation: "a0",
  }),
  manager(
    "postgresql.service",
    3 * MIN - 10,
    "resources",
    "postgresql.service: Consumed 1min 4.210s CPU time, 1.2G memory peak.",
    { invocation: "a0", cpu: "64210", memory: "1288490189" },
  ),
  ...crash("c1", 2 * MIN, 1),
  ...crash("c2", 2 * MIN - 10_000, 2),
  ...crash("c3", 2 * MIN - 20_000, 3),
  ...crash("c4", 2 * MIN - 30_000, 4),
  manager(
    "postgresql.service",
    2 * MIN - 36_000,
    "start_limit",
    "postgresql.service: Start request repeated too quickly.",
    { invocation: "c4" },
    "error",
  ),
  manager(
    "postgresql.service",
    2 * MIN - 36_010,
    "failed",
    "postgresql.service: Failed with result 'start-limit-hit'.",
    { invocation: "c4", result: "start-limit-hit" },
    "error",
  ),
]

/** What postgres printed in the run that hit the limit, for the History a row opens. */
export const POSTGRES_RUN_LINES: Line[] = [
  {
    text: '2026-09-27 10:00:00.140 UTC [4410] FATAL:  could not open file "global/pg_filenode.map": Permission denied',
    timestamp: ago(2 * MIN - 30_400),
    level: "critical",
    source: "postgres[4410]",
    event: "fatal",
    attrs: { unit: "postgresql.service", program: "postgres", pid: "4410", invocation: "c4" },
  },
]

/** certbot's renewals: fine for two nights, one failed, and the first of the week. */
function oneshot(invocation: string, at: number, ok: boolean): Line[] {
  const unit = "certbot.service"
  const lines = [
    manager(unit, at, "starting", "Starting certbot.service - Certbot...", { invocation }),
  ]
  if (ok) {
    lines.push(
      manager(unit, at - 4_300, "deactivated", "certbot.service: Deactivated successfully.", {
        invocation,
      }),
      manager(unit, at - 4_310, "started", "Finished certbot.service - Certbot.", { invocation }),
    )
  } else {
    lines.push(
      manager(
        unit,
        at - 9_100,
        "exited",
        "certbot.service: Main process exited, code=exited, status=1/FAILURE",
        { invocation, exit_code: "exited", exit_status: "1" },
        "error",
      ),
      manager(
        unit,
        at - 9_110,
        "failed",
        "certbot.service: Failed with result 'exit-code'.",
        { invocation, result: "exit-code" },
        "error",
      ),
    )
  }
  lines.push(
    manager(
      unit,
      at - 9_200,
      "resources",
      "certbot.service: Consumed 2.114s CPU time, 61.4M memory peak.",
      { invocation, cpu: "2114", memory: "64382566" },
    ),
  )
  return lines
}

export const CERTBOT_RUNS: Line[] = [
  ...oneshot("r1", 60 * HOUR, true),
  ...oneshot("r2", 36 * HOUR, false),
  ...oneshot("r3", 12 * HOUR, true),
  ...oneshot("r4", 2 * HOUR, true),
]

const cron = (at: number, event: string, text: string, attrs: Record<string, string>): Line => ({
  text,
  timestamp: ago(at),
  level: event === "output_discarded" ? "warn" : "info",
  source: "CRON[3113693]",
  event,
  attrs: { unit: "cron.service", program: "CRON", pid: "3113693", ...attrs },
})

export const CRON_LINES: Line[] = [
  cron(40 * MIN, "run", "(root) CMD (/usr/local/bin/backup)", {
    user: "root",
    command: "/usr/local/bin/backup",
  }),
  cron(17 * MIN, "run", "(root) CMD (cd / && run-parts --report /etc/cron.hourly)", {
    user: "root",
    command: "cd / && run-parts --report /etc/cron.hourly",
  }),
  cron(17 * MIN - 900, "output_discarded", "(CRON) info (No MTA installed, discarding output)", {}),
  cron(10 * MIN, "run", "(root) CMD (command -v debian-sa1 > /dev/null && debian-sa1 1 1)", {
    user: "root",
    command: "command -v debian-sa1 > /dev/null && debian-sa1 1 1",
  }),
]

export const WORKER_LINES: Line[] = [
  {
    text: "worker listening for jobs on queue default",
    timestamp: ago(6 * MIN),
    level: "info",
    stream: "stdout",
    event: "startup",
  },
  {
    text: "Error: connect ECONNREFUSED 127.0.0.1:6379",
    timestamp: ago(5 * MIN),
    level: "error",
    stream: "stderr",
    event: "exception",
    attrs: { error: "Error: connect ECONNREFUSED 127.0.0.1:6379" },
  },
]

/** The host's log index: the journal, and cron as a unit of it. */
export const LOG_INDEX = {
  sources: [
    {
      id: "journal:",
      label: "systemd journal",
      kind: "journal",
      detail: "Every unit on the host — pick one below to narrow it",
      lens: "syslog",
      rotated: false,
    },
  ],
  units: [
    {
      name: "cron.service",
      description: "Regular background program processing daemon",
      active: "active",
      lens: "cron",
    },
    {
      name: "postgresql.service",
      description: "PostgreSQL RDBMS",
      active: "failed",
      lens: "postgres",
    },
    { name: "certbot.service", description: "Certbot", active: "inactive", lens: "certbot" },
  ],
  roots: ["/var/log"],
  missing: {},
}

/** What `GET /logs/source` says of each, as `describeLogSource` would. */
const DESCRIBED: Record<string, object> = {
  "journal:cron.service": { kind: "journal", lens: "cron", status: "active" },
  "journal:postgresql.service": { kind: "journal", lens: "postgres", status: "failed" },
  "journal:certbot.service": { kind: "journal", lens: "certbot", status: "inactive" },
  "journal:nginx.service": { kind: "journal", lens: "nginx-error", status: "active" },
  "pm2:deploy/1/worker": {
    kind: "pm2",
    lens: "pm2",
    path: "/home/deploy/.pm2/logs/worker-out.log",
    detail: "stdout and stderr, merged",
    status: "errored",
  },
}

const LIVE: Record<string, Line[]> = {
  "journal:cron.service": CRON_LINES,
  "journal:postgresql.service": POSTGRES_RUNS,
  "journal:certbot.service": CERTBOT_RUNS,
  "pm2:deploy/1/worker": WORKER_LINES,
}

const RUNS: Record<string, Line[]> = {
  "journal:postgresql.service": POSTGRES_RUNS,
  "journal:certbot.service": CERTBOT_RUNS,
}

export type LogMocks = { sockets: URLSearchParams[]; searches: URLSearchParams[] }

const json = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

const result = (lines: Line[], extra: object = {}) => ({
  lines,
  scanned: lines.length * 40,
  matched: lines.length,
  truncated: false,
  complete: true,
  files: [],
  histogram: [],
  tookMillis: 3,
  ...extra,
})

/**
 * Answers a `/logs/*` request, or returns false for any other. Called from
 * the page's own catch-all route, so the two cannot disagree about order.
 */
export function answerLogs(route: Route, path: string, url: URL, mocks: LogMocks): boolean {
  if (!path.startsWith("/logs/")) return false
  const source = url.searchParams.get("source") ?? ""
  if (path === "/logs/sources") void json(route, LOG_INDEX)
  else if (path === "/logs/source") void json(route, DESCRIBED[source] ?? {})
  else if (path === "/logs/retention")
    void json(route, {
      managed: true,
      summary: "Rotated daily, 7 kept",
      level: "ok",
      available: true,
    })
  else if (path === "/logs/search") {
    mocks.searches.push(url.searchParams)
    if (url.searchParams.get("lens") === "systemd") {
      void json(route, result(RUNS[source] ?? [], { lens: "systemd" }))
    } else if (url.searchParams.get("histogramBy") === "event") {
      // A reading: the cron lens's runs and lost output over the day.
      void json(
        route,
        result([], {
          matched: 145,
          histogram: [4, 6, 5, 7, 6, 8].map((n, i) => ({
            start: ago((6 - i) * 4 * HOUR),
            total: n,
            counts: { run: n },
          })),
          facets: {
            event: {
              values: [
                { value: "run", count: 143, errors: 0 },
                { value: "output_discarded", count: 2, errors: 0 },
              ],
              distinct: 2,
              other: 0,
              missing: 0,
            },
          },
        }),
      )
    } else {
      const narrowed = url.searchParams.getAll("f").some((f) => f.startsWith("invocation:"))
      void json(route, result(narrowed ? POSTGRES_RUN_LINES : (LIVE[source] ?? [])))
    }
  } else void json(route, {})
  return true
}

/** The live tail, which answers each source with its lines. */
export async function mockLogSockets(page: Page, mocks: LogMocks) {
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const params = new URL(socket.url()).searchParams
    mocks.sockets.push(params)
    const source = params.get("source") ?? ""
    const described = DESCRIBED[source] as { lens?: string } | undefined
    socket.send(
      JSON.stringify({
        type: "meta",
        data: { kind: "journal", label: source, lens: described?.lens },
        ts: Date.now(),
      }),
    )
    socket.send(JSON.stringify({ type: "logs", data: LIVE[source] ?? [], ts: Date.now() }))
  })
}
