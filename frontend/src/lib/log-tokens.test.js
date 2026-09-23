import { describe, expect, test } from "bun:test"
import { leadingTime, pieces, statusClass, tokenize } from "./log-tokens"

// Every line below was read off a real server — syslog, auth.log, the
// dashboard's own JSON, Caddy, uwsgi, memos, nginx — because the shapes are
// the spec, and a made-up line proves only the shape somebody imagined.

/** The spans of a line as `kind:text`, in order. */
function read(text) {
  return tokenize(text)
    .sort((a, b) => a.start - b.start)
    .map((s) => `${s.kind}:${text.slice(s.start, s.end)}`)
}

describe("syslog", () => {
  const line =
    "2026-09-23T05:22:31.211829+00:00 vps-07749119-vps-ovh-net nordvpnd[3257959]: 2026/09/23 05:22:31.211751 main.go:176: [Error] failed to cleanup config: Empty config read, aborting load of config"

  test("the prefix is the time, the host, the program and its pid", () => {
    expect(read(line).slice(0, 5)).toEqual([
      "time:2026-09-23T05:22:31.211829+00:00",
      "host:vps-07749119-vps-ovh-net",
      "proc:nordvpnd",
      "pid:[3257959]",
      "punct::",
    ])
  })

  test("the message keeps its own time, source, level and verdicts", () => {
    const rest = read(line).slice(5)
    expect(rest).toContain("time:2026/09/23 05:22:31.211751")
    expect(rest).toContain("path:main.go:176")
    expect(rest).toContain("level:[Error]")
    expect(rest).toContain("bad:failed")
    expect(rest).toContain("bad:aborting")
  })

  test("a console that shows the time in a column skips the line's own", () => {
    expect(line.slice(leadingTime(tokenize(line)))).toStartWith("vps-07749119")
    expect(leadingTime(tokenize("no time here"))).toBe(0)
  })

  test("and this host's own name, where it is this host", () => {
    const spans = tokenize(line)
    expect(line.slice(leadingTime(spans, line, "vps-07749119-vps-ovh-net"))).toStartWith("nordvpnd")
    expect(line.slice(leadingTime(spans, line, "VPS-07749119-vps-ovh-net.example"))).toStartWith(
      "nordvpnd",
    )
    // A line forwarded from somewhere else keeps the name of where it came from.
    expect(line.slice(leadingTime(spans, line, "atlas"))).toStartWith("vps-07749119")
  })

  test("a systemd unit and its verdict", () => {
    const spans = read(
      "2026-09-23T05:22:31.215620+00:00 vps-07749119-vps-ovh-net systemd[1]: nordvpnd-killswitch.service: Failed with result 'exit-code'.",
    )
    expect(spans).toContain("proc:systemd")
    expect(spans).toContain("bad:Failed")
  })
})

describe("auth.log", () => {
  test("a failed password names the address it came from", () => {
    const spans = read(
      "2026-09-23T05:21:42.600655+00:00 vps-07749119-vps-ovh-net sshd-session[3254114]: Failed password for root from 62.60.130.253 port 59026 ssh2",
    )
    expect(spans).toContain("proc:sshd-session")
    expect(spans).toContain("bad:Failed")
    expect(spans).toContain("ip:62.60.130.253")
  })

  test("pam's key=value tail is read as pairs", () => {
    const spans = read(
      "pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=92.118.39.14  user=root",
    )
    expect(spans).toContain("bad:failure")
    expect(spans).toContain("key:rhost")
    expect(spans).toContain("ip:92.118.39.14")
    expect(spans).toContain("key:user")
  })

  test("an accepted key is good news", () => {
    expect(read("Accepted publickey for ubuntu from 100.84.53.82 port 50122 ssh2")).toContain(
      "good:Accepted",
    )
  })
})

describe("web servers", () => {
  test("nginx's combined format", () => {
    const spans = read(
      '92.118.39.14 - - [23/Sep/2026:05:20:04 +0000] "PUT /.env HTTP/1.1" 404 31383 "-" "curl/8.5.0" rt=0.081',
    )
    expect(spans.slice(0, 6)).toEqual([
      "ip:92.118.39.14",
      "time:[23/Sep/2026:05:20:04 +0000]",
      "method:PUT",
      "path:/.env",
      "punct:HTTP/1.1",
      "status:404",
    ])
    expect(spans).toContain('string:"curl/8.5.0"')
    expect(spans).toContain("key:rt")
  })

  test("uwsgi's request line and its status in parentheses", () => {
    const spans = read(
      "[pid: 21|app: 0|req: 2125/3391] ::1 () {28 vars in 300 bytes} [Wed Sep 23 05:03:26 2026] GET /health => generated 42 bytes in 3 msecs (HTTP/1.1 200) 8 headers in 251 bytes (1 switches on core 1)",
    )
    expect(spans).toContain("ip:::1")
    expect(spans).toContain("method:GET")
    expect(spans).toContain("path:/health")
    expect(spans).toContain("number:3 msecs")
    expect(spans).toContain("status:200")
  })

  test("a request logger that leads with the status", () => {
    expect(read("200 GET /api/health-check with 15 bytes took 1ms")).toEqual([
      "status:200",
      "method:GET",
      "path:/api/health-check",
      "number:15 bytes",
      "number:1ms",
    ])
  })

  test("a status is read by its class", () => {
    expect([200, 301, 404, 502].map(statusClass)).toEqual(["ok", "redirect", "client", "server"])
  })
})

describe("structured lines", () => {
  test("logfmt reads its level, its time and leaves the message alone", () => {
    const text =
      'time=2026-09-22T00:47:14.310Z level=INFO msg="initializing new database with latest schema" file=migration/sqlite/LATEST.sql'
    const spans = tokenize(text)
    const level = spans.find((s) => s.kind === "level")
    expect(text.slice(level.start, level.end)).toBe("INFO")
    expect(level.level).toBe("info")
    expect(read(text)).toContain("time:2026-09-22T00:47:14.310Z")
    // The sentence is the one thing on the line meant to be read.
    expect(read(text).some((s) => s.includes("initializing"))).toBe(false)
  })

  test("JSON keys are muted and each value is read by its key", () => {
    const text =
      '{"time":"2026-09-23T05:11:01.532192041Z","level":"INFO","msg":"audit","user":"wayy","ip":"100.84.53.82","status":204,"success":true}'
    const spans = read(text)
    expect(spans).toContain('key:"level"')
    expect(spans).toContain('level:"INFO"')
    expect(spans).toContain('time:"2026-09-23T05:11:01.532192041Z"')
    expect(spans).toContain('ip:"100.84.53.82"')
    expect(spans).toContain("status:204")
    expect(spans).toContain("good:true")
    expect(spans).not.toContain('string:"audit"')
  })

  test("Caddy's JSON with a numeric timestamp", () => {
    const spans = read(
      '{"level":"warn","ts":1790140145.504238,"logger":"admin","msg":"admin endpoint disabled"}',
    )
    expect(spans).toContain('level:"warn"')
    expect(spans).toContain("time:1790140145.504238")
  })
})

describe("what is left alone", () => {
  test("a word is a level only when it is shouted", () => {
    expect(read("the info you asked for")).toEqual([])
    expect(read("INFO starting")).toEqual(["level:INFO"])
  })

  test("a clock is not an IPv6 address, and a version is not a path", () => {
    expect(read("at 05:22:31 on v1.2.3")).toEqual(["time:05:22:31"])
    expect(read("HTTP/1.1 and 2026/09/23")).toEqual([])
  })
})

describe("pieces", () => {
  test("a search hit splits the span it falls in and keeps its kind", () => {
    const text = "from 62.60.130.253 port"
    const out = pieces(text, tokenize(text), [[8, 12]])
    expect(out.map((p) => [p.text, p.span?.kind ?? "", p.hit])).toEqual([
      ["from ", "", false],
      ["62.", "ip", false],
      ["60.1", "ip", true],
      ["30.253", "ip", false],
      [" port", "", false],
    ])
  })

  test("drawing can start past the line's own timestamp", () => {
    const text = "2026-09-23T05:22:31Z host proc: hi"
    const out = pieces(text, tokenize(text), undefined, leadingTime(tokenize(text)))
    expect(out.map((p) => p.text).join("")).toBe("host proc: hi")
  })
})
