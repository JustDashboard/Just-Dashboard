import { describe, expect, test } from "bun:test"
import {
  isForgeMedia,
  markdownMedia,
  markdownText,
  onlyMedia,
  parseInline,
  parseMarkdown,
  safeHref,
} from "./markdown"

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

  test("an image is data, and one that would run something is its text", () => {
    expect(parseInline("![shot](https://x.test/a.png)")).toEqual([
      { t: "image", src: "https://x.test/a.png", alt: "shot" },
    ])
    expect(parseInline("![shot](javascript:alert(1))")).toEqual([{ t: "text", v: "shot" }])
  })

  test("a badge is its picture rather than brackets around one", () => {
    expect(parseInline("[![ci](https://x.test/b.svg)](https://x.test/run)")).toEqual([
      { t: "image", src: "https://x.test/b.svg", alt: "ci" },
    ])
  })

  test("an upload alone on its line is the file, and in a sentence a link", () => {
    const url = "https://github.com/user-attachments/assets/1a2b-3c"
    expect(parseInline(url)).toEqual([{ t: "image", src: url, alt: "" }])
    expect(parseInline(`see ${url} there`)[1]).toEqual({
      t: "link",
      href: url,
      c: [{ t: "text", v: url }],
    })
  })
})

describe("the HTML a description is written in", () => {
  test("a fold keeps its summary and reads what is in it", () => {
    const blocks = parseMarkdown(
      "<details><summary><code>/docker</code></summary>\n\n**Before**\n\n![](https://x.test/a.png)\n\n</details>\n\nAfter it",
    )
    expect(blocks[0].t).toBe("details")
    expect(blocks[0].open).toBe(false)
    expect(parseMarkdown("<details open>\nx\n</details>")[0].open).toBe(true)
    expect(blocks[0].summary).toEqual([{ t: "code", v: "/docker" }])
    expect(blocks[0].c.map((b) => b.t)).toEqual(["paragraph", "paragraph"])
    expect(blocks[1]).toEqual({ t: "paragraph", c: [{ t: "text", v: "After it" }] })
  })

  test("a fold inside a fold closes where it should", () => {
    const blocks = parseMarkdown(
      "<details><summary>a</summary>\n<details><summary>b</summary>\nin\n</details>\nout\n</details>",
    )
    expect(blocks).toHaveLength(1)
    expect(blocks[0].c.map((b) => b.t)).toEqual(["details", "paragraph"])
  })

  test("a tag with no opening one is not printed", () => {
    expect(parseMarkdown("text\n</details>")).toEqual([
      { t: "paragraph", c: [{ t: "text", v: "text" }] },
    ])
  })

  test("img and video are the media they name", () => {
    const [p] = parseMarkdown(
      '<img src="https://x.test/a.png" alt="shot" width="300"> <video src="https://x.test/b.mp4" controls></video>',
    )
    expect(p.c.filter((r) => r.t === "image")).toEqual([
      { t: "image", src: "https://x.test/a.png", alt: "shot" },
      { t: "image", src: "https://x.test/b.mp4", alt: "" },
    ])
  })

  test("what is inside code stays the words the author typed", () => {
    expect(parseMarkdown('```html\n<img src="https://x.test/a.png">\n```')).toEqual([
      { t: "code", lang: "html", v: '<img src="https://x.test/a.png">' },
    ])
    expect(parseInline("`<b>x</b>`")).toEqual([{ t: "code", v: "<b>x</b>" }])
    expect(parseMarkdown("`<details>` is a tag")[0].c[0]).toEqual({ t: "code", v: "<details>" })
  })
})

describe("media", () => {
  test("only GitHub's media hosts are drawn", () => {
    expect(isForgeMedia("https://raw.githubusercontent.com/o/r/abc/a.png")).toBe(true)
    expect(isForgeMedia("https://github.com/user-attachments/assets/abc")).toBe(true)
    expect(isForgeMedia("https://github.com/settings/profile")).toBe(false)
    expect(isForgeMedia("http://raw.githubusercontent.com/a.png")).toBe(false)
    expect(isForgeMedia("https://githubusercontent.com.evil.test/a.png")).toBe(false)
    expect(isForgeMedia("https://x.test/a.png")).toBe(false)
    expect(isForgeMedia("not a url")).toBe(false)
  })

  test("the lightbox steps through a body's pictures once each, folds and tables included", () => {
    const a = "https://raw.githubusercontent.com/o/r/s/a.png"
    const b = "https://raw.githubusercontent.com/o/r/s/b.png"
    const blocks = parseMarkdown(
      `![one](${a})\n\n|x|\n|-|\n|![two](${b})|\n\n<details><summary>s</summary>\n\n![again](${a})\n![far](https://x.test/c.png)\n</details>`,
    )
    expect(markdownMedia(blocks).map((m) => m.src)).toEqual([a, b])
  })

  test("a paragraph of pictures is told from a sentence with one in it", () => {
    expect(onlyMedia(parseInline("![a](https://x.test/a.png)\n![b](https://x.test/b.png)"))).toBe(
      true,
    )
    expect(onlyMedia(parseInline("see ![a](https://x.test/a.png)"))).toBe(false)
  })
})
