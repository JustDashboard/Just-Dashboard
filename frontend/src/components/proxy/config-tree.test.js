import { describe, expect, test } from "bun:test"
import {
  authorship,
  diffStat,
  directiveText,
  editorPath,
  entryNote,
  entryTags,
  fileLines,
  filterCounts,
  folderOf,
  groupByFolder,
  MAX_MATCHES,
  readCount,
  readLabel,
  readState,
  relativePath,
  searchEffective,
  unreadCount,
  visibleFiles,
} from "./config-tree"

const root = "/etc/nginx"
const file = (path, extra = {}) => ({
  path: `${root}/${path}`,
  kind: "file",
  size: 1024,
  modified: "2026-09-27T10:00:00Z",
  included: false,
  managed: false,
  protected: false,
  ...extra,
})

const files = [
  file("nginx.conf", { kind: "main", included: true }),
  file("conf.d/app.conf", {
    included: true,
    managed: true,
    includedBy: { file: `${root}/nginx.conf`, line: 14 },
  }),
  file("sites-available/default", {
    included: true,
    includedBy: { file: `${root}/nginx.conf`, line: 15, via: `${root}/sites-enabled/default` },
  }),
  file("sites-available/old"),
  file("sites-enabled/default", {
    kind: "link",
    included: true,
    target: `${root}/sites-available/default`,
  }),
  file("sites-enabled/default.bak", { included: true, backup: true }),
  file("modules-enabled/50-mod.conf", {
    kind: "link",
    included: true,
    outside: true,
    target: "/usr/share/nginx/modules-available/mod.conf",
  }),
  file("sites-enabled/gone", {
    kind: "link",
    missing: true,
    target: `${root}/sites-available/gone`,
  }),
  file("jd-auth/shop", { kind: "password", protected: true, managed: true }),
]

describe("the file tree", () => {
  test("paths are named under the root, and folders from it", () => {
    expect(relativePath(`${root}/conf.d/app.conf`, root)).toBe("conf.d/app.conf")
    expect(relativePath(`${root}/conf.d/app.conf`, `${root}/`)).toBe("conf.d/app.conf")
    expect(relativePath("/usr/share/x.conf", root)).toBe("/usr/share/x.conf")
    expect(relativePath("/etc/nginx-old/x", root)).toBe("/etc/nginx-old/x")
    expect(folderOf(`${root}/nginx.conf`, root)).toBe("")
    expect(folderOf(`${root}/deep/one/x.conf`, root)).toBe("deep/one")
  })

  test("the root's own files come first, then each folder by name, each sorted", () => {
    const groups = groupByFolder([...files].reverse(), root)
    expect(groups.map((g) => g.folder)).toEqual([
      "",
      "conf.d",
      "jd-auth",
      "modules-enabled",
      "sites-available",
      "sites-enabled",
    ])
    expect(groups.find((g) => g.folder === "sites-enabled").entries.map((e) => e.path)).toEqual([
      `${root}/sites-enabled/default`,
      `${root}/sites-enabled/default.bak`,
      `${root}/sites-enabled/gone`,
    ])
  })

  test("a link is read or not, but neither managed nor by hand; a password file is none", () => {
    expect(filterCounts(files)).toEqual({ all: 9, read: 6, unread: 2, managed: 1, hand: 4 })
    expect(visibleFiles(files, root, "hand", "")).toHaveLength(4)
    expect(visibleFiles(files, root, "all", "ENABLED/DEF").map((e) => e.path)).toEqual([
      `${root}/sites-enabled/default`,
      `${root}/sites-enabled/default.bak`,
    ])
    // The root's own name is not part of what a search matches.
    expect(visibleFiles(files, root, "all", "etc/nginx")).toHaveLength(0)
  })

  test("nginx reads a link and its file once, counted as the file", () => {
    // nginx.conf, conf.d/app.conf, sites-available/default (and its link),
    // default.bak and the module's file outside the directory.
    expect(readCount(files)).toBe(5)
    expect(unreadCount(files)).toBe(1)
    expect(authorship(files)).toEqual({ managed: 1, hand: 4 })
  })

  test("a file's state is a word, and unknown while the includes are", () => {
    expect(readState(files[1], true)).toEqual({ label: "read", tone: "running" })
    expect(readState(files[3], true)).toEqual({ label: "not read", tone: "stopped" })
    expect(readState(files[3], false)).toEqual({ label: "not known", tone: "unknown" })
    expect(readState(files[7], true)).toEqual({ label: "points at nothing", tone: "danger" })
  })

  test("the editor opens a link's file, and nothing it may not open", () => {
    expect(editorPath(files[4])).toBe(`${root}/sites-available/default`)
    expect(editorPath(files[1])).toBe(`${root}/conf.d/app.conf`)
    expect(editorPath(files[6])).toBeUndefined()
    expect(editorPath(files[7])).toBeUndefined()
    expect(editorPath(files[8])).toBeUndefined()
  })

  test("the row says where a link points, or why nginx reads the file", () => {
    const note = (entry) =>
      entryNote(
        entry,
        root,
        (n) => `${n} B`,
        () => "1d ago",
      )
    expect(note(files[0])).toBe("the main file · 1024 B · 1d ago")
    expect(note(files[1])).toBe("included by nginx.conf:14 · 1024 B · 1d ago")
    expect(note(files[2])).toBe("via sites-enabled/default · 1024 B · 1d ago")
    expect(note(files[3])).toBe("1024 B · 1d ago")
    expect(note(files[4])).toBe("→ sites-available/default")
    expect(note(files[6])).toBe("→ /usr/share/nginx/modules-available/mod.conf, outside /etc/nginx")
    expect(note(files[7])).toBe("→ sites-available/gone")
    expect(note(files[8])).toBe("the dashboard's password file, hidden")
    expect(note({ ...files[8], managed: false })).toBe("password file, hidden")
  })

  test("a backup nginx reads is marked, and a managed file says so", () => {
    expect(entryTags(files[5])).toEqual([{ label: "backup", tone: "warning" }])
    expect(entryTags(files[1])).toEqual([{ label: "managed", tone: "default" }])
    expect(entryTags(files[8])).toEqual([])
    expect(entryTags(files[4])).toEqual([])
  })
})

const effective = {
  checkedAt: "2026-09-27T10:00:00Z",
  files: [
    {
      path: `${root}/nginx.conf`,
      content: "events {}\nhttp {\n    include /etc/nginx/sites-enabled/*;\n    gzip on;\n}\n",
    },
    {
      path: `${root}/sites-enabled/app`,
      target: `${root}/sites-available/app`,
      content:
        "server {\n    listen 443 ssl;\n    server_name app.test;\n    location /api {\n        proxy_pass http://127.0.0.1:3000;\n    }\n}\n",
    },
  ],
  directives: [
    { name: "events", args: [], file: `${root}/nginx.conf`, line: 1, within: [], opens: true },
    { name: "http", args: [], file: `${root}/nginx.conf`, line: 2, within: [], opens: true },
    {
      name: "server",
      args: [],
      file: `${root}/sites-enabled/app`,
      line: 1,
      within: ["http"],
      opens: true,
    },
    {
      name: "listen",
      args: ["443", "ssl"],
      file: `${root}/sites-enabled/app`,
      line: 2,
      within: ["http", "server app.test"],
      opens: false,
    },
    {
      name: "server_name",
      args: ["app.test"],
      file: `${root}/sites-enabled/app`,
      line: 3,
      within: ["http", "server app.test"],
      opens: false,
    },
    {
      name: "location",
      args: ["/api"],
      file: `${root}/sites-enabled/app`,
      line: 4,
      within: ["http", "server app.test"],
      opens: true,
    },
    {
      name: "proxy_pass",
      args: ["http://127.0.0.1:3000"],
      file: `${root}/sites-enabled/app`,
      line: 5,
      within: ["http", "server app.test", "location /api"],
      opens: false,
    },
    {
      name: "gzip",
      args: ["on"],
      file: `${root}/nginx.conf`,
      line: 4,
      within: ["http"],
      opens: false,
    },
  ],
}

describe("the search over what nginx loads", () => {
  test("text is found in any case, marked, and placed in its blocks", () => {
    const { matches, total } = searchEffective(effective, "PROXY_pass", "text")
    expect(total).toBe(1)
    expect(matches[0]).toEqual({
      file: `${root}/sites-enabled/app`,
      open: `${root}/sites-available/app`,
      line: 5,
      text: "proxy_pass http://127.0.0.1:3000;",
      start: 0,
      end: 10,
      within: ["http", "server app.test", "location /api"],
    })
    expect(searchEffective(effective, "   ", "text")).toEqual({ matches: [], total: 0 })
  })

  test("a pattern is a regular expression, and one that is not says so", () => {
    const { matches } = searchEffective(effective, "listen\\s+\\d+", "regex")
    expect(matches.map((m) => [m.line, m.text.slice(m.start, m.end)])).toEqual([[2, "listen 443"]])
    const broken = searchEffective(effective, "listen (", "regex")
    expect(broken.matches).toEqual([])
    expect(broken.error).toMatch(/^Not a regular expression: /)
  })

  test("a directive is its name, then words its arguments contain", () => {
    const listen = searchEffective(effective, "listen 443", "directive")
    expect(listen.matches.map((m) => m.text)).toEqual(["listen 443 ssl;"])
    expect(searchEffective(effective, "listen 80", "directive").total).toBe(0)
    // A name alone is exact once it is typed out: server, not server_name.
    expect(searchEffective(effective, "server", "directive").matches.map((m) => m.text)).toEqual([
      "server {",
    ])
    // Typed part-way, it matches the names it begins.
    expect(searchEffective(effective, "server_n", "directive").matches.map((m) => m.text)).toEqual([
      "server_name app.test;",
    ])
    expect(searchEffective(effective, "location", "directive").matches[0]).toMatchObject({
      text: "location /api {",
      start: 0,
      end: 8,
      open: `${root}/sites-available/app`,
    })
  })

  test("a directive search without the tree says why", () => {
    const result = searchEffective(
      { ...effective, directives: null, treeError: "unexpected end of file" },
      "listen",
      "directive",
    )
    expect(result.error).toContain("unexpected end of file")
  })

  test("matches past the limit are counted, not drawn", () => {
    const big = {
      ...effective,
      files: [{ path: `${root}/big.conf`, content: "gzip on;\n".repeat(MAX_MATCHES + 50) }],
    }
    const { matches, total } = searchEffective(big, "gzip", "text")
    expect(matches).toHaveLength(MAX_MATCHES)
    expect(total).toBe(MAX_MATCHES + 50)
  })

  test("a directive is written as one line, quoting what needs it", () => {
    expect(
      directiveText({
        name: "add_header",
        args: ["Content-Security-Policy", "default-src 'self'"],
        opens: false,
      }),
    ).toBe(`add_header Content-Security-Policy "default-src 'self'";`)
    expect(directiveText({ name: "return", args: ["200", ""], opens: false })).toBe(
      'return 200 "";',
    )
  })

  test("a file's lines end where its last line feed does", () => {
    expect(fileLines("")).toEqual([])
    expect(fileLines("a\nb\n")).toEqual(["a", "b"])
    expect(fileLines("a\nb")).toEqual(["a", "b"])
    expect(fileLines("a\n\n")).toEqual(["a", ""])
  })
})

test("a diff's size leaves its file headers out", () => {
  const diff =
    "--- a/app\n+++ b/app\n@@ -1,3 +1,3 @@\n server {\n-    listen 80;\n+    listen 81;\n+    gzip on;\n }"
  expect(diffStat(diff)).toEqual({ added: 2, removed: 1 })
  expect(diffStat("")).toEqual({ added: 0, removed: 0 })
})

test("how long ago nginx was read, in its largest unit", () => {
  const at = Date.parse("2026-09-27T10:00:00Z")
  expect(readLabel(at, at + 3_000)).toBe("read just now")
  expect(readLabel(at, at + 14_000)).toBe("read 14s ago")
  expect(readLabel(at, at + 3 * 3_600_000 + 7_000)).toBe("read 3h ago")
})
