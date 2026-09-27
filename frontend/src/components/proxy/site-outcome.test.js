import { describe, expect, test } from "bun:test"
import { failureHeadline, reloadFailure, reloadOutput } from "./site-outcome"

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
