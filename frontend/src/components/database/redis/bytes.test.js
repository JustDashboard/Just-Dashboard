import { describe, expect, test } from "bun:test"
import {
  boxSafe,
  bytesId,
  bytesLabel,
  byteLength,
  fromBytes,
  globEscape,
  hasGlob,
  hexLines,
  isBinary,
  joinBytes,
  keyFromAddress,
  keyQuery,
  keyToAddress,
  lineSafe,
  looksLikeJson,
  parseHex,
  prefixQuery,
  sameBytes,
  textShape,
  toBytes,
  toHex,
} from "./bytes"

const b64 = (...bytes) => ({ base64: btoa(String.fromCharCode(...bytes)) })

describe("a name is its bytes", () => {
  test("text and bytes that spell the same thing are still told apart by form", () => {
    expect(bytesId("a")).toBe("t:a")
    expect(bytesId(b64(0xff))).toBe("b:/w==")
    expect(sameBytes("a", "a")).toBe(true)
    expect(sameBytes("a", "b")).toBe(false)
    expect(sameBytes(b64(0xff), b64(0xff))).toBe(true)
  })

  test("an absent name equals only another absent one", () => {
    expect(sameBytes(undefined, undefined)).toBe(true)
    expect(sameBytes(undefined, "")).toBe(false)
    expect(sameBytes("", undefined)).toBe(false)
  })

  test("the empty name is a name", () => {
    expect(sameBytes("", "")).toBe(true)
    expect(bytesId("")).toBe("t:")
  })
})

describe("bytes go out as they came in", () => {
  test("text round-trips as text, multi-byte characters included", () => {
    expect(fromBytes(toBytes("Chișinău ✓"))).toBe("Chișinău ✓")
    expect(byteLength("ș")).toBe(2)
  })

  test("bytes that are not UTF-8 stay bytes and are never mangled", () => {
    const raw = b64(0xff, 0xfe, 0x00, 0x62)
    expect(isBinary(raw)).toBe(true)
    expect([...toBytes(raw)]).toEqual([0xff, 0xfe, 0x00, 0x62])
    expect(fromBytes(toBytes(raw))).toEqual(raw)
  })

  test("a megabyte of bytes survives the trip", () => {
    const big = new Uint8Array(1_100_000).fill(0xfe)
    const back = toBytes(fromBytes(big))
    expect(back.length).toBe(big.length)
    expect(back[1_099_999]).toBe(0xfe)
  })

  test("windows of one value join in order; one binary window makes the whole binary", () => {
    expect(joinBytes(["ab", "cd"])).toBe("abcd")
    const joined = joinBytes(["ab", b64(0xff)])
    expect(isBinary(joined)).toBe(true)
    expect([...toBytes(joined)]).toEqual([0x61, 0x62, 0xff])
  })
})

describe("what is drawn for a name", () => {
  test("text is drawn as it is, with what cannot be seen written out", () => {
    expect(bytesLabel("user:1")).toBe("user:1")
    expect(bytesLabel("a\u0000b\nc")).toBe("a\\x00b\\x0ac")
    expect(bytesLabel("ș")).toBe("ș")
  })

  test("bytes are written as redis-cli writes them", () => {
    expect(bytesLabel(b64(0x62, 0x69, 0x6e, 0xff, 0x6b))).toBe("bin\\xffk")
    // A backslash in bytes is escaped, or `\x41` would read as an escape.
    expect(bytesLabel(b64(0x5c, 0xff))).toBe("\\x5c\\xff")
  })
})

describe("hex", () => {
  test("bytes to hex and back", () => {
    expect(toHex(new Uint8Array([0, 15, 255]))).toBe("000fff")
    expect([...parseHex("00 0f\nFF")]).toEqual([0, 15, 255])
    expect(parseHex("")).toEqual(new Uint8Array())
  })

  test("half a pair, or a letter past f, is not hex", () => {
    expect(parseHex("0")).toBeNull()
    expect(parseHex("0g")).toBeNull()
    expect(parseHex("xx")).toBeNull()
  })

  test("a dump is sixteen bytes a line and stops at the value's end", () => {
    const bytes = new Uint8Array(20).map((_, i) => 0x41 + i)
    const lines = hexLines(bytes)
    expect(lines.length).toBe(2)
    expect(lines[0].offset).toBe("00000000")
    expect(lines[0].hex).toBe("41 42 43 44 45 46 47 48  49 4a 4b 4c 4d 4e 4f 50")
    expect(lines[0].text).toBe("ABCDEFGHIJKLMNOP")
    expect(lines[1].offset).toBe("00000010")
    expect(lines[1].text).toBe("QRST")
    // A window past the end draws nothing past the end.
    expect(hexLines(bytes, 16, 4096).length).toBe(1)
    expect(hexLines(new Uint8Array(), 0, 160)).toEqual([])
  })
})

describe("naming a key to the server", () => {
  test("text travels as key, bytes as keyB64", () => {
    expect(keyQuery("user:1")).toEqual({ key: "user:1" })
    expect(keyQuery(b64(0xff))).toEqual({ keyB64: "/w==" })
  })

  test("the empty name is sent as an empty keyB64, since key= would read as no key", () => {
    expect(keyQuery("")).toEqual({ keyB64: "" })
  })

  test("the top of the tree sends no prefix", () => {
    expect(prefixQuery("")).toEqual({})
    expect(prefixQuery("user:")).toEqual({ prefix: "user:" })
    expect(prefixQuery(b64(0xff, 0x3a))).toEqual({ prefixB64: "/zo=" })
  })
})

describe("a key in the address", () => {
  test("every kind of name survives the round trip", () => {
    for (const name of ["user:1", "", b64(0xff, 0x00)]) {
      const address = keyToAddress(name)
      expect(keyFromAddress(address.key ?? "", address.keyB64 ?? "")).toEqual(name)
    }
  })

  test("no key is no key", () => {
    expect(keyToAddress(undefined)).toEqual({ key: null, keyB64: null })
    expect(keyFromAddress("", "")).toBeUndefined()
  })

  test("a keyB64 that is not base64 names nothing", () => {
    expect(keyFromAddress("", "not base64!")).toBeUndefined()
    expect(keyFromAddress("", "abc")).toBeUndefined()
  })
})

describe("patterns", () => {
  test("a pattern with a glob character is a glob; anything else is one key's name", () => {
    expect(hasGlob("user:*")).toBe(true)
    expect(hasGlob("user:?")).toBe(true)
    expect(hasGlob("user:[ab]")).toBe(true)
    expect(hasGlob("user:1")).toBe(false)
  })

  test("a name is escaped into a pattern that matches only itself", () => {
    expect(globEscape("a*b?c[d]\\")).toBe("a\\*b\\?c\\[d\\]\\\\")
    expect(globEscape("user:1")).toBe("user:1")
  })
})

describe("text that is a JSON document", () => {
  test("objects and arrays are; scalars and near-misses are not", () => {
    expect(looksLikeJson('{"a":1}')).toBe(true)
    expect(looksLikeJson(" [1, 2] ")).toBe(true)
    expect(looksLikeJson("42")).toBe(false)
    expect(looksLikeJson('"text"')).toBe(false)
    expect(looksLikeJson("{not json}")).toBe(false)
    expect(looksLikeJson("")).toBe(false)
  })
})

describe("text a text box hands back as it got it", () => {
  test("letters, tabs and line breaks are plain", () => {
    expect(textShape("")).toEqual({ plain: true, crlf: false })
    expect(textShape("one\ttwo\nthree é 日本")).toEqual({ plain: true, crlf: false })
  })

  test("a control character is not: the box shows nothing for it", () => {
    expect(textShape("\u0000\u0001\u0002").plain).toBe(false)
    expect(textShape("colour \u001b[31m").plain).toBe(false)
    expect(textShape("del \u007f").plain).toBe(false)
  })

  test("lines that all end in CR LF are plain, and say so for the save", () => {
    expect(textShape("a\r\nb\r\n")).toEqual({ plain: true, crlf: true })
  })

  test("a lone carriage return, or two kinds of line break, cannot be put back", () => {
    expect(textShape("a\rb").plain).toBe(false)
    expect(textShape("a\r\nb\nc").plain).toBe(false)
  })

  test("a box takes plain text without carriage returns; a field also wants one line", () => {
    expect(boxSafe("two\nlines")).toBe(true)
    expect(lineSafe("two\nlines")).toBe(false)
    expect(lineSafe("one line")).toBe(true)
    expect(boxSafe("a\r\nb")).toBe(false)
    expect(boxSafe("nul\u0000")).toBe(false)
    expect(boxSafe(b64(0xff))).toBe(false)
    expect(boxSafe(undefined)).toBe(false)
  })
})
