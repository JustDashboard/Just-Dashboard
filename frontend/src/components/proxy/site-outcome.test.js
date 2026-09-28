import { describe, expect, test } from "bun:test"
import { failureHeadline, nginxNotRunning, reloadFailure, reloadOutput } from "./site-outcome"

const refused = {
  valid: false,
  command: "nginx -t",
  output:
    'nginx: [warn] conflicting server name "a" on 0.0.0.0:80, ignored\nnginx: [emerg] unknown directive "foo" in /etc/nginx/sites-available/app:3\nnginx: configuration file /etc/nginx/nginx.conf test failed',
  diagnostics: [
    { level: "warn", message: 'conflicting server name "a" on 0.0.0.0:80, ignored' },
    {
      level: "emerg",
      message: 'unknown directive "foo"',
      file: "/etc/nginx/sites-available/app",
      line: 3,
    },
  ],
}

/** A test refused on a missing include: an open() that fails, but not the pid file's. */
const brokenInclude = {
  valid: false,
  command: "nginx -t",
  output:
    'nginx: [emerg] open() "/etc/nginx/sites-enabled/ghost" failed (2: No such file or directory) in /etc/nginx/nginx.conf:60\nnginx: configuration file /etc/nginx/nginx.conf test failed',
  diagnostics: [],
}

describe("failureHeadline", () => {
  test("names nginx's first error where it points, past the warnings", () => {
    expect(failureHeadline(refused)).toBe(
      'unknown directive "foo" in /etc/nginx/sites-available/app:3',
    )
  })

  test("keeps an error that names no file as it is", () => {
    expect(
      failureHeadline({ ...refused, diagnostics: [{ level: "emerg", message: "no events" }] }),
    ).toBe("no events")
  })

  test("falls back to the first line of output nginx printed", () => {
    expect(
      failureHeadline({ ...refused, diagnostics: [], output: "\n  something odd\nmore" }),
    ).toBe("something odd")
  })
})

describe("reloadFailure", () => {
  test("is nothing when the reload happened", () => {
    expect(
      reloadFailure({
        reload: { validation: { ...refused, valid: true }, reloaded: true, output: "" },
      }),
    ).toBe(undefined)
  })

  test("replaces the delete's 'configuration failed validation' with nginx's reason", () => {
    expect(
      reloadFailure({
        reloadError: "configuration failed validation",
        reload: { validation: refused, reloaded: false, output: "" },
      }),
    ).toBe('nginx -t failed: unknown directive "foo" in /etc/nginx/sites-available/app:3')
  })

  test("keeps a reason that already says it, and one from the reload itself", () => {
    const said = 'nginx -t failed: unknown directive "foo" in /etc/nginx/sites-available/app:3'
    expect(
      reloadFailure({
        reloadError: said,
        reload: { validation: refused, reloaded: false, output: "" },
      }),
    ).toBe(said)
    expect(
      reloadFailure({
        reloadError: 'reload failed: nginx: [error] invalid PID number "" in "/run/nginx.pid"',
        reload: {
          validation: { ...refused, valid: true },
          reloaded: false,
          output: 'nginx: [error] invalid PID number "" in "/run/nginx.pid"',
        },
      }),
    ).toBe('reload failed: nginx: [error] invalid PID number "" in "/run/nginx.pid"')
  })
})

describe("reloadOutput", () => {
  test("is the test's output when the test refused, the reload's otherwise", () => {
    expect(reloadOutput({ reload: { validation: refused, reloaded: false, output: "" } })).toBe(
      refused.output,
    )
    expect(
      reloadOutput({
        reload: { validation: { ...refused, valid: true }, reloaded: false, output: "pid gone" },
      }),
    ).toBe("pid gone")
    expect(reloadOutput({})).toBe(undefined)
  })
})

describe("nginxNotRunning", () => {
  const passed = { ...refused, valid: true }
  const failedWith = (output) => ({ reload: { validation: passed, reloaded: false, output } })

  // Verbatim from nginx 1.26.3's `nginx -s reload`, the prefix path shortened.
  test("reads nginx's words for no process to signal, in both forms it prints them", () => {
    for (const output of [
      'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)',
      '2026/09/27 23:40:45 [notice] 3139977#3139977: signal process started\n2026/09/27 23:40:45 [error] 3139977#3139977: open() "/run/nginx.pid" failed (2: No such file or directory)',
      'nginx: [error] invalid PID number "" in "/run/nginx.pid"',
      "nginx: [alert] kill(999999, 1) failed (3: No such process)",
    ]) {
      expect(nginxNotRunning(failedWith(output))).toBe(true)
    }
  })

  test("is not a pid file nginx may not read, or a process it may not signal", () => {
    for (const output of [
      'nginx: [error] open() "/run/nginx.pid" failed (13: Permission denied)',
      "nginx: [alert] kill(1, 1) failed (1: Operation not permitted)",
      "signal: killed",
    ]) {
      expect(nginxNotRunning(failedWith(output))).toBe(false)
    }
  })

  test("is not a missing include the test refused, or a reload that happened", () => {
    expect(
      nginxNotRunning({ reload: { validation: brokenInclude, reloaded: false, output: "" } }),
    ).toBe(false)
    expect(
      nginxNotRunning({
        reload: {
          validation: passed,
          reloaded: true,
          output: 'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)',
        },
      }),
    ).toBe(false)
    expect(nginxNotRunning({})).toBe(false)
  })
})
