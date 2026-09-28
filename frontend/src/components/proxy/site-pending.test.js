import { describe, expect, test } from "bun:test"
import {
  changeVerb,
  configName,
  keptLoad,
  loadKnown,
  loadedSince,
  notLiveLabel,
  outputHeadline,
  pendingTitle,
  siteChanges,
} from "./site-pending"

const site = (over = {}) => ({
  name: "app.test",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.test",
  enabledPath: "/etc/nginx/sites-enabled/app.test",
  enabled: true,
  layout: "sites-available",
  formEditable: true,
  serverNames: ["app.test"],
  listen: ["80"],
  upstreams: [],
  tls: false,
  modified: "2026-09-28T10:00:00Z",
  size: 100,
  ...over,
})

const pending = (files, over = {}) => ({
  running: true,
  lastReload: "2026-09-28T09:00:00Z",
  generation: "100-5000",
  files,
  ...over,
})

const change = (over) => ({
  path: "/etc/nginx/sites-enabled/app.test",
  site: "app.test",
  layout: "sites-available",
  change: "changed",
  modified: "2026-09-28T10:00:00Z",
  ...over,
})

describe("siteChanges", () => {
  test("a card's changes are those naming its entry", () => {
    const answer = pending([
      change(),
      change({ site: "app.test", layout: "conf.d", path: "/etc/nginx/conf.d/app.test" }),
      change({ site: "other.test", path: "/etc/nginx/sites-enabled/other.test" }),
      change({ site: undefined, layout: undefined, path: "/etc/nginx/nginx.conf" }),
    ])
    expect(siteChanges(answer, site())).toEqual([change()])
    expect(siteChanges(answer, site({ layout: "conf.d" }))).toHaveLength(1)
  })

  test("nothing is compared without a running nginx whose load is known", () => {
    expect(siteChanges(undefined, site())).toEqual([])
    expect(siteChanges(pending([change()], { running: false }), site())).toEqual([])
    expect(siteChanges(pending([change()], { lastReload: undefined }), site())).toEqual([])
    expect(loadKnown(pending([]))).toBe(true)
  })

  test("a Caddy entry of the same name is not nginx's", () => {
    expect(siteChanges(pending([change()]), site({ kind: "caddy", layout: undefined }))).toEqual([])
  })
})

describe("notLiveLabel", () => {
  test("says what nginx is still running", () => {
    expect(notLiveLabel(site(), [])).toBeUndefined()
    expect(notLiveLabel(site(), [change()])).toBe("saved, not live")
    expect(notLiveLabel(site(), [change({ change: "added" })])).toBe("enabled, not live")
    expect(notLiveLabel(site(), [change(), change({ change: "added" })])).toBe("enabled, not live")
    expect(notLiveLabel(site({ enabled: false }), [change({ change: "removed" })])).toBe(
      "disabled, not live",
    )
    // Another name for the file taken out, the site still enabled by its own.
    expect(notLiveLabel(site(), [change({ change: "removed" })])).toBe("changed, not live")
  })

  test("a disabled site nginx never loaded has nothing waiting", () => {
    expect(notLiveLabel(site({ enabled: false }), [change()])).toBeUndefined()
  })
})

describe("strip words", () => {
  test("counts", () => {
    expect(pendingTitle(1)).toBe("1 change on disk is not live yet")
    expect(pendingTitle(3)).toBe("3 changes on disk are not live yet")
    expect(pendingTitle(1200)).toBe("1,200 changes on disk are not live yet")
  })

  test("names a file from the nginx directory down", () => {
    expect(configName("/etc/nginx/sites-enabled/app", "/etc/nginx")).toBe("sites-enabled/app")
    expect(configName("/etc/nginx/sites-enabled/app", "/etc/nginx/")).toBe("sites-enabled/app")
    expect(configName("/etc/nginx2/app", "/etc/nginx")).toBe("/etc/nginx2/app")
    expect(configName("/etc/nginx/nginx.conf", undefined)).toBe("/etc/nginx/nginx.conf")
  })

  test("one word per change", () => {
    expect(changeVerb(change())).toBe("saved")
    expect(changeVerb(change({ change: "added" }))).toBe("linked")
    expect(changeVerb(change({ change: "removed" }))).toBe("taken out")
  })
})

describe("loadedSince", () => {
  test("a reload landed when nginx runs another load than the one seen before it", () => {
    expect(loadedSince("100-5000", pending([], { generation: "100-6000" }))).toBe(true)
    expect(loadedSince("100-5000", pending([], { generation: "200-10" }))).toBe(true)
    expect(loadedSince("100-5000", pending([change()]))).toBe(false)
  })

  test("is never claimed without both loads", () => {
    expect(loadedSince(undefined, pending([], { generation: "100-6000" }))).toBe(false)
    expect(loadedSince("100-5000", undefined)).toBe(false)
    expect(loadedSince("100-5000", pending([], { running: false, generation: undefined }))).toBe(
      false,
    )
  })
})

describe("outputHeadline", () => {
  test("is nginx's first error without its prefix, in either form nginx prints", () => {
    expect(
      outputHeadline(
        'nginx: [warn] conflicting server name "a" on 0.0.0.0:80, ignored\n' +
          'nginx: [emerg] unknown directive "foo" in /etc/nginx/sites-enabled/app:3\n' +
          "nginx: configuration file /etc/nginx/nginx.conf test failed",
      ),
    ).toBe('unknown directive "foo" in /etc/nginx/sites-enabled/app:3')
    expect(
      outputHeadline(
        "2026/09/28 03:18:14 [emerg] 3942780#3942780: bind() to 127.0.0.1:18412 failed (98: Address already in use)",
      ),
    ).toBe("bind() to 127.0.0.1:18412 failed (98: Address already in use)")
  })

  test("falls back to the first line", () => {
    expect(outputHeadline("\nsomething else went wrong\nmore")).toBe("something else went wrong")
  })
})

describe("keptLoad", () => {
  test("says when nginx loaded what it still runs, and where to look", () => {
    const hourAgo = new Date(Date.now() - 3_600_000).toISOString()
    expect(keptLoad(hourAgo)).toBe(
      "The reload was sent, but nginx is still running what it loaded 1h ago. Its error log says why — often a port another process holds, or a file it cannot open.",
    )
  })
})
