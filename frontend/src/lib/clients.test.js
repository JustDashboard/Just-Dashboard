import { describe, expect, test } from "bun:test"
import { describeClient, keyProduct, networkOf, parseAgent } from "./clients"

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
  })

  test("everything else is the internet", () => {
    expect(networkOf("203.0.113.7")).toEqual({ kind: "internet", label: "internet" })
    expect(networkOf("2a01:4f8::1").kind).toBe("internet")
    expect(networkOf("not an address").kind).toBe("internet")
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
