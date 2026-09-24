import { describe, expect, test } from "bun:test"
import {
  agentProduct,
  crawlerOf,
  describeClient,
  hostOf,
  keyProduct,
  networkOf,
  parseAgent,
  productOfHost,
  refererProduct,
  wordsProduct,
} from "./clients"

const MAC_CHROME =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
const WIN_EDGE =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0"
const IPHONE_SAFARI =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1"
const UBUNTU_FIREFOX =
  "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"
const ANDROID_TABLET =
  "Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
const ANDROID_PHONE_OPERA =
  "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36 OPR/84.0.0"

describe("parseAgent", () => {
  test("names the browser a Chromium browser is, not the one it imitates", () => {
    expect(parseAgent(MAC_CHROME)).toEqual({
      client: "Chrome",
      product: "chrome",
      os: "macOS",
      osProduct: "apple",
      device: "desktop",
    })
    expect(parseAgent(WIN_EDGE)).toMatchObject({ client: "Edge", product: "edge", os: "Windows" })
    expect(parseAgent(ANDROID_PHONE_OPERA)).toMatchObject({
      client: "Opera",
      os: "Android",
      device: "phone",
    })
  })

  test("tells a phone from a tablet from a desktop", () => {
    expect(parseAgent(IPHONE_SAFARI)).toMatchObject({
      client: "Safari",
      os: "iOS",
      osProduct: "apple",
      device: "phone",
    })
    expect(parseAgent(ANDROID_TABLET)).toMatchObject({ os: "Android", device: "tablet" })
    expect(parseAgent(UBUNTU_FIREFOX)).toMatchObject({
      client: "Firefox",
      os: "Ubuntu",
      osProduct: "ubuntu",
      device: "desktop",
    })
  })

  test("a program is drawn as the runtime that sent it", () => {
    expect(parseAgent("curl/8.5.0")).toEqual({
      client: "curl",
      product: "curl",
      os: "",
      device: "program",
    })
    expect(parseAgent("Go-http-client/2.0")).toMatchObject({ client: "Go", product: "go" })
    expect(parseAgent("python-requests/2.32.3")).toMatchObject({ product: "python" })
    expect(parseAgent("Wget/1.21.4")).toMatchObject({ client: "Wget", product: undefined })
  })

  test("anything else keeps its first word and no mark", () => {
    expect(parseAgent("okhttp/4.12.0")).toMatchObject({
      client: "okhttp",
      product: undefined,
      device: "program",
    })
    expect(parseAgent("")).toMatchObject({ client: "Unknown client", product: undefined })
  })
})

test("describeClient says it as a person would", () => {
  expect(describeClient(MAC_CHROME)).toBe("Chrome on macOS")
  expect(describeClient("curl/8.5.0")).toBe("curl")
})

describe("networkOf", () => {
  test("the tailnet in both families", () => {
    expect(networkOf("100.101.7.12")).toEqual({
      kind: "tailscale",
      label: "Tailscale",
      product: "tailscale",
    })
    expect(networkOf("fd7a:115c:a1e0::1234").kind).toBe("tailscale")
    // The edges of 100.64.0.0/10.
    expect(networkOf("100.63.255.255").kind).toBe("internet")
    expect(networkOf("100.128.0.1").kind).toBe("internet")
  })

  test("private ranges, loopback and a mapped address", () => {
    expect(networkOf("192.168.1.20").kind).toBe("local")
    expect(networkOf("172.18.0.4").kind).toBe("local")
    expect(networkOf("172.32.0.4").kind).toBe("internet")
    expect(networkOf("10.0.0.2").kind).toBe("local")
    expect(networkOf("fe80::1").kind).toBe("local")
    expect(networkOf("fd12:3456::1").kind).toBe("local")
    expect(networkOf("127.0.0.1").kind).toBe("server")
    expect(networkOf("::1").kind).toBe("server")
    expect(networkOf("::ffff:192.168.0.9").kind).toBe("local")
    expect(networkOf("169.254.10.2").kind).toBe("local")
    expect(networkOf("0.0.0.0").kind).toBe("local")
  })

  test("everything else is the internet", () => {
    expect(networkOf("203.0.113.7")).toEqual({ kind: "internet", label: "internet" })
    expect(networkOf("2a01:4f8::1").kind).toBe("internet")
    expect(networkOf("not an address").kind).toBe("internet")
  })
})

describe("crawlerOf", () => {
  test("a crawler in a browser's clothes is the engine that sends it", () => {
    const smartphone =
      "Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
    expect(crawlerOf(smartphone)).toEqual({ name: "Googlebot", product: "google" })
    expect(crawlerOf("Mozilla/5.0 (compatible; bingbot/2.0)")).toEqual({
      name: "bingbot",
      product: "bing",
    })
    expect(crawlerOf("AhrefsBot/7.0")).toEqual({ name: "A crawler" })
    expect(crawlerOf(MAC_CHROME)).toBeUndefined()
  })
})

describe("keyProduct", () => {
  test("reads where a key lives from its name", () => {
    expect(keyProduct("github-actions")).toBe("github")
    expect(keyProduct("gh_deploy")).toBe("github")
    expect(keyProduct("Home Assistant")).toBe("home-assistant")
    expect(keyProduct("uptime-kuma-probe")).toBe("uptime-kuma")
    expect(keyProduct("grafana cloud")).toBe("grafana")
    expect(keyProduct("k8s-operator")).toBe("kubernetes")
  })

  test("a name with no product in it has no mark", () => {
    expect(keyProduct("backup-cron")).toBeUndefined()
    expect(keyProduct("laptop")).toBeUndefined()
    expect(keyProduct("")).toBeUndefined()
  })
})

describe("wordsProduct", () => {
  test("a pair of words is read before either alone", () => {
    const table = { homeassistant: "home-assistant", home: "homepage", n8n: "n8n" }
    expect(wordsProduct(["home", "assistant", "token"], table)).toBe("home-assistant")
    expect(wordsProduct(["my", "home", "page"], table)).toBe("homepage")
    expect(wordsProduct(["ci", "n8n"], table)).toBe("n8n")
    expect(wordsProduct([], table)).toBeUndefined()
  })
})

describe("agentProduct", () => {
  test("the request log's browser families are drawn as their browsers", () => {
    expect(agentProduct("Chrome")).toEqual({ product: "chrome", kind: "browser" })
    expect(agentProduct("Edge")).toEqual({ product: "edge", kind: "browser" })
    expect(agentProduct("Firefox")).toEqual({ product: "firefox", kind: "browser" })
    expect(agentProduct("Safari")).toEqual({ product: "safari", kind: "browser" })
  })

  test("a program's first word is the runtime that sent it", () => {
    expect(agentProduct("curl")).toEqual({ product: "curl", kind: "program" })
    expect(agentProduct("Go-http-client")).toEqual({ product: "go", kind: "program" })
    expect(agentProduct("python-requests")).toEqual({ product: "python", kind: "program" })
    expect(agentProduct("okhttp")).toEqual({ kind: "program" })
  })

  test("the named crawlers are their search engines, and the rest are bots", () => {
    expect(agentProduct("Googlebot")).toEqual({ product: "google", kind: "bot" })
    expect(agentProduct("bingbot")).toEqual({ product: "bing", kind: "bot" })
    expect(agentProduct("Other bots")).toEqual({ product: undefined, kind: "bot" })
  })

  test("a browser-shaped agent the backend could not name is unknown", () => {
    expect(agentProduct("Mozilla")).toEqual({ kind: "unknown" })
    expect(agentProduct("")).toEqual({ kind: "unknown" })
  })
})

describe("hostOf", () => {
  test("every shape a host is typed or pasted in", () => {
    expect(hostOf("github.com")).toBe("github.com")
    expect(hostOf("  GitHub.com ")).toBe("github.com")
    expect(hostOf("https://github.com/owner/repo.git")).toBe("github.com")
    expect(hostOf("https://x-access-token:abc@github.com/owner/repo")).toBe("github.com")
    expect(hostOf("git@gitlab.com:group/project.git")).toBe("gitlab.com")
    expect(hostOf("ssh://git@codeberg.org:22/owner/repo.git")).toBe("codeberg.org")
    expect(hostOf("ghcr.io/owner/app:1.2")).toBe("ghcr.io")
    expect(hostOf("registry.example.com:5000")).toBe("registry.example.com")
    expect(hostOf("https://example.com?next=/")).toBe("example.com")
    expect(hostOf("")).toBe("")
  })
})

test("productOfHost matches a host and every name under it, nearest first", () => {
  const table = { "github.com": "github", "api.github.com": "api" }
  expect(productOfHost("github.com", table)).toBe("github")
  expect(productOfHost("api.github.com", table)).toBe("api")
  expect(productOfHost("uploads.github.com", table)).toBe("github")
  expect(productOfHost("notgithub.com", table)).toBeUndefined()
  expect(productOfHost("", table)).toBeUndefined()
})

describe("refererProduct", () => {
  test("a site is drawn as itself, under any of its subdomains", () => {
    expect(refererProduct("github.com")).toBe("github")
    expect(refererProduct("gitlab.com")).toBe("gitlab")
    expect(refererProduct("cn.bing.com")).toBe("bing")
    expect(refererProduct("duckduckgo.com")).toBe("duckduckgo")
    expect(refererProduct("news.ycombinator.com")).toBe("ycombinator")
    expect(refererProduct("old.reddit.com")).toBe("reddit")
    expect(refererProduct("www.linkedin.com")).toBe("linkedin")
    expect(refererProduct("lnkd.in")).toBe("linkedin")
    expect(refererProduct("l.facebook.com")).toBe("facebook")
    expect(refererProduct("discord.com")).toBe("discord")
    expect(refererProduct("app.slack.com")).toBe("slack")
    expect(refererProduct("t.me")).toBe("telegram")
    expect(refererProduct("claude.ai")).toBe("claude")
  })

  test("X answers on three names", () => {
    expect(refererProduct("t.co")).toBe("x")
    expect(refererProduct("x.com")).toBe("x")
    expect(refererProduct("mobile.twitter.com")).toBe("x")
  })

  test("Google on every country's domain, and nothing that only contains the word", () => {
    expect(refererProduct("www.google.com")).toBe("google")
    expect(refererProduct("google.co.uk")).toBe("google")
    expect(refererProduct("www.google.com.br")).toBe("google")
    expect(refererProduct("google.de")).toBe("google")
    expect(refererProduct("google.github.io")).toBeUndefined()
    expect(refererProduct("notgoogle.com")).toBeUndefined()
  })

  test("a full referer reads the same as its host, and an unknown site has no mark", () => {
    expect(refererProduct("https://news.ycombinator.com/item?id=1")).toBe("ycombinator")
    expect(refererProduct("example.com")).toBeUndefined()
    expect(refererProduct("")).toBeUndefined()
  })
})
