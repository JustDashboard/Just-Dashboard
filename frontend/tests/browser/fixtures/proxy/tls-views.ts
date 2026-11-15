import { certs } from "./certs"
import { now } from "./shared"

/**
 * The TLS page's two other views as a busy host answers them: every name it
 * serves or watches with its latest report, and a request to its nginx that
 * is redirected once before it is answered. Neither view had a spec until the
 * page was overhauled, so these are the data its specs and captures read.
 */

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const report = {
  reachable: true,
  expiring: false,
  expired: false,
  issues: 0,
  negotiated: "TLS 1.3",
}

export const fleet = {
  targets: [
    {
      host: "app.example.com",
      port: 443,
      sites: ["app.example.com"],
      watched: false,
      scan: {
        ...report,
        id: 11,
        grade: "B",
        summary: "A trusted certificate with headers to improve",
        daysLeft: 44,
        issues: 1,
        checkedAt: minutesAgo(3),
      },
    },
    {
      host: "shop.example.com",
      port: 443,
      sites: ["just-dashboard-shop"],
      watched: true,
      scan: {
        ...report,
        id: 12,
        grade: "A+",
        summary: "Everything a visitor gets is in order",
        daysLeft: 81,
        checkedAt: minutesAgo(3),
      },
    },
    {
      host: "legacy.example.com",
      port: 443,
      sites: ["legacy.example.com"],
      watched: false,
      scan: {
        ...report,
        id: 13,
        grade: "C",
        summary: "TLS 1.0 is still offered",
        daysLeft: 9,
        expiring: true,
        negotiated: "TLS 1.2",
        issues: 3,
        checkedAt: minutesAgo(4),
      },
    },
    {
      host: "mail.example.com",
      port: 993,
      sites: [],
      watched: true,
      scan: {
        ...report,
        id: 14,
        grade: "A",
        summary: "A trusted certificate",
        daysLeft: 60,
        checkedAt: minutesAgo(5),
      },
    },
    {
      host: "old.example.com",
      port: 443,
      sites: [],
      watched: true,
      scan: {
        ...report,
        id: 15,
        grade: "F",
        summary: "Nothing is listening on port 443",
        reachable: false,
        negotiated: undefined,
        issues: 1,
        checkedAt: minutesAgo(5),
      },
    },
    { host: "staging.example.com", port: 443, sites: ["staging.example.com"], watched: false },
  ],
}

const body =
  '<!doctype html>\n<html lang="en">\n<head><title>Acme</title></head>\n<body>Ship faster.</body>\n</html>\n'

/** http://app.example.com/ answered with a redirect to HTTPS, then the page. */
export const requestResult = {
  connectTo: "127.0.0.1",
  hops: [
    {
      url: "http://app.example.com/",
      method: "GET",
      site: "app.example.com",
      status: 301,
      proto: "HTTP/1.1",
      location: "https://app.example.com/",
      headers: [
        { name: "Server", value: "nginx" },
        { name: "Location", value: "https://app.example.com/" },
      ],
      body: "",
      bodyBytes: 0,
      timings: { connect: 0.4, firstByte: 1.2, total: 1.4 },
    },
    {
      url: "https://app.example.com/",
      method: "GET",
      site: "app.example.com",
      status: 200,
      proto: "HTTP/2.0",
      headers: [
        { name: "Server", value: "nginx" },
        { name: "Content-Type", value: "text/html; charset=utf-8" },
        { name: "Strict-Transport-Security", value: "max-age=31536000" },
      ],
      body,
      bodyBytes: body.length,
      timings: { connect: 0.5, tls: 3.1, firstByte: 12.4, total: 13.9 },
      tls: {
        version: "TLS 1.3",
        cipherSuite: "TLS_AES_128_GCM_SHA256",
        alpn: "h2",
        certificate: certs[0],
        origin: {
          state: "current",
          site: "app.example.com",
          match: "exact",
          file: certs[0],
          served: certs[0],
          detail: "nginx serves app.example.com from this file, and it is the one on disk.",
        },
      },
    },
  ],
}

export { now }
