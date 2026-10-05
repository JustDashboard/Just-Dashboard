import { describe, expect, test } from "bun:test"
import { markdownText, parseInline, parseMarkdown, safeHref } from "./markdown"

describe("reading a pull request's description", () => {
  test("headings, paragraphs and soft breaks", () => {
    const blocks = parseMarkdown("## What changed\n\nRemove the import\npage entirely.")
    expect(blocks[0]).toEqual({ t: "heading", level: 2, c: [{ t: "text", v: "What changed" }] })
    expect(blocks[1]).toEqual({
      t: "paragraph",
      c: [{ t: "text", v: "Remove the import" }, { t: "br" }, { t: "text", v: "page entirely." }],
    })
  })

  test("lists, task lists and a nested list stay one list", () => {
    const [list] = parseMarkdown("- [x] done\n- [ ] open\n  - nested\n\n- after a gap")
    expect(list.t).toBe("list")
    expect(list.items).toHaveLength(3)
    expect(list.items[0]).toMatchObject({ task: true, done: true })
    expect(list.items[1]).toMatchObject({ task: true, done: false })
    expect(list.items[1].c[1].t).toBe("list")
    const [ordered] = parseMarkdown("3. three\n4. four")
    expect(ordered).toMatchObject({ t: "list", ordered: true, start: 3 })
  })

  test("fenced code keeps its text exactly, and its language", () => {
    const [code] = parseMarkdown("```go\nfunc main() {\n  **not bold**\n}\n```")
    expect(code).toEqual({ t: "code", lang: "go", v: "func main() {\n  **not bold**\n}" })
  })

  test("quotes, rules and tables", () => {
    const blocks = parseMarkdown("> quoted\n\n---\n\n| a | b |\n|:--|--:|\n| 1 | 2 |")
    expect(blocks.map((b) => b.t)).toEqual(["quote", "rule", "table"])
    expect(blocks[2].align).toEqual(["left", "right"])
    expect(blocks[2].rows[0][1]).toEqual([{ t: "text", v: "2" }])
  })

  test("template HTML is dropped and nothing else becomes markup", () => {
    const blocks = parseMarkdown("<!-- fill this in -->\nHello<br>world <script>x</script>")
    expect(markdownText("<!-- fill this in -->\nHello<br>world")).toBe("Hello world")
    expect(JSON.stringify(blocks)).toContain("<script>x</script>")
  })
})

describe("inline runs", () => {
  test("emphasis, code and strike", () => {
    expect(parseInline("**bold** and *it* and `co*de` and ~~gone~~")).toEqual([
      { t: "strong", c: [{ t: "text", v: "bold" }] },
      { t: "text", v: " and " },
      { t: "em", c: [{ t: "text", v: "it" }] },
      { t: "text", v: " and " },
      { t: "code", v: "co*de" },
      { t: "text", v: " and " },
      { t: "del", c: [{ t: "text", v: "gone" }] },
    ])
  })

  test("snake_case is not emphasis", () => {
    expect(parseInline("adoption_enc and value_mode")).toEqual([
      { t: "text", v: "adoption_enc and value_mode" },
    ])
  })

  test("references, mentions and links", () => {
    expect(parseInline("Reverts #145 for @Wayy01, see https://example.com/x.")).toEqual([
      { t: "text", v: "Reverts " },
      { t: "ref", n: 145 },
      { t: "text", v: " for " },
      { t: "mention", v: "Wayy01" },
      { t: "text", v: ", see " },
      { t: "link", href: "https://example.com/x", c: [{ t: "text", v: "https://example.com/x" }] },
      { t: "text", v: "." },
    ])
  })

  test("a link that would run something is its text", () => {
    expect(parseInline("[click](javascript:alert(1))")).toEqual([{ t: "text", v: "click" }])
    expect(safeHref("JAVASCRIPT:alert(1)")).toBeUndefined()
    expect(safeHref("https://github.com")).toBe("https://github.com")
  })

  test("an image is a link to itself", () => {
    expect(parseInline("![shot](https://x.test/a.png)")).toEqual([
      { t: "link", href: "https://x.test/a.png", c: [{ t: "text", v: "shot" }] },
    ])
  })
})
