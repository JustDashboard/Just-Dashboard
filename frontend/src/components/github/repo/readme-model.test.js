import { describe, expect, test } from "bun:test"
import { absoluteReadme, languageShares } from "./readme-model"

describe("absoluteReadme", () => {
  const at = (source, path) => absoluteReadme(source, "northwind/api", "main", path)
  test("a picture beside the README is GitHub's raw file, a link its page", () => {
    expect(at("![Shot](docs/shot.png)")).toBe(
      "![Shot](https://github.com/northwind/api/raw/main/docs/shot.png)",
    )
    expect(at("See [setup](./docs/setup.md#run).")).toBe(
      "See [setup](https://github.com/northwind/api/blob/main/docs/setup.md#run).",
    )
  })
  test("resolved against the README's own folder, a leading slash from the root", () => {
    expect(at("[up](../LICENSE) [root](/Makefile)", "docs/README.md")).toBe(
      "[up](https://github.com/northwind/api/blob/main/LICENSE) [root](https://github.com/northwind/api/blob/main/Makefile)",
    )
    expect(at("![x](img/a.png)", "docs/README.md")).toBe(
      "![x](https://github.com/northwind/api/raw/main/docs/img/a.png)",
    )
  })
  test("absolute addresses, fragments and climbing out of the repository are left alone", () => {
    const kept = [
      "[site](https://example.net)",
      "[mail](mailto:a@b.c)",
      "[top](#install)",
      "[x](javascript:alert(1))",
      "[far](../../etc/passwd)",
      "[net](//evil.example)",
    ]
    for (const source of kept) expect(at(source)).toBe(source)
  })
  test("a title survives and a branch with a slash is escaped", () => {
    expect(absoluteReadme('[a](b.md "B")', "o/r", "release/1.0")).toBe(
      '[a](https://github.com/o/r/blob/release%2F1.0/b.md "B")',
    )
  })
})

test("languageShares turns bytes into shares, largest first, crumbs dropped", () => {
  expect(
    languageShares({
      languages: [
        { name: "Shell", bytes: 100 },
        { name: "Go", bytes: 899 },
        { name: "Makefile", bytes: 1 },
      ],
    }),
  ).toEqual([
    { name: "Go", share: 0.899 },
    { name: "Shell", share: 0.1 },
    { name: "Makefile", share: 0.001 },
  ])
  expect(languageShares({ languages: [] })).toEqual([])
  expect(languageShares(undefined)).toEqual([])
})
