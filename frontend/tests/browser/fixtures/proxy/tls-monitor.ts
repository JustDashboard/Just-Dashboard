import { json, now, type ProxyRoutes } from "./shared"

/**
 * The TLS report's deep scan of app.example.com: TLS 1.2 still taking a CBC
 * and an RSA key exchange suite, no post-quantum group, HTTP/2 switched on in
 * the site form and not offered, HTTP/3 advertised with nothing answering
 * QUIC, and a certificate for anyone who names no site.
 */

const suite = (
  id: number,
  openssl: string,
  kex: string,
  auth: string,
  cipher: string,
  rating: "strong" | "weak" | "insecure",
  reasons: string[] = [],
) => ({
  id,
  name: openssl,
  openssl,
  kex,
  auth,
  cipher,
  bits: cipher.includes("256") || cipher.startsWith("ChaCha") ? 256 : 128,
  forwardSecrecy: kex !== "RSA",
  aead: !cipher.endsWith("CBC"),
  rating,
  reasons,
})

const refused = (name: string) => ({
  name,
  status: "refused",
  complete: true,
  detail: "The server answered an offer of 104 suites with: protocol version not supported.",
  suites: [],
})

const group = (id: number, name: string, status: string, postQuantum = false) => ({
  id,
  name,
  postQuantum,
  status,
  ...(status === "refused" ? { detail: "The server answered: handshake failure." } : {}),
})

export const deepScan = {
  domain: "app.example.com",
  port: 443,
  checkedAt: now,
  reachable: true,
  address: "203.0.113.10:443",
  where: "here",
  connections: 41,
  versions: [
    {
      name: "TLS 1.3",
      status: "accepted",
      complete: true,
      order: "client",
      suites: [
        suite(0x1301, "TLS_AES_128_GCM_SHA256", "any", "any", "AES-128-GCM", "strong"),
        suite(0x1302, "TLS_AES_256_GCM_SHA384", "any", "any", "AES-256-GCM", "strong"),
        suite(0x1303, "TLS_CHACHA20_POLY1305_SHA256", "any", "any", "ChaCha20-Poly1305", "strong"),
      ],
    },
    {
      name: "TLS 1.2",
      status: "accepted",
      complete: true,
      order: "server",
      suites: [
        suite(0xc02b, "ECDHE-ECDSA-AES128-GCM-SHA256", "ECDHE", "ECDSA", "AES-128-GCM", "strong"),
        suite(
          0xcca9,
          "ECDHE-ECDSA-CHACHA20-POLY1305",
          "ECDHE",
          "ECDSA",
          "ChaCha20-Poly1305",
          "strong",
        ),
        suite(0xc009, "ECDHE-ECDSA-AES128-SHA", "ECDHE", "ECDSA", "AES-128-CBC", "weak", [
          "CBC mode",
        ]),
        suite(0x002f, "AES128-SHA", "RSA", "RSA", "AES-128-CBC", "weak", [
          "no forward secrecy",
          "CBC mode",
        ]),
      ],
    },
    refused("TLS 1.1"),
    refused("TLS 1.0"),
    refused("SSL 3.0"),
  ],
  groups: [
    group(0x11ec, "X25519MLKEM768", "refused", true),
    group(0x11eb, "SecP256r1MLKEM768", "refused", true),
    group(0x11ed, "SecP384r1MLKEM1024", "refused", true),
    group(0x1d, "X25519", "accepted"),
    group(0x17, "P-256", "accepted"),
    group(0x18, "P-384", "accepted"),
    group(0x19, "P-521", "refused"),
    group(0x1e, "X448", "refused"),
    group(0x100, "ffdhe2048", "refused"),
    group(0x101, "ffdhe3072", "refused"),
    group(0x102, "ffdhe4096", "refused"),
  ],
  browserGroup: "X25519",
  browserGroupVersion: "TLS 1.3",
  alpn: {
    offered: ["h2", "http/1.1"],
    negotiated: "http/1.1",
    site: { name: "app.example.com", http2: true },
  },
  http3: {
    answered: true,
    altSvc: 'h3=":443"; ma=86400',
    advertised: true,
    port: 443,
    quic: {
      port: 443,
      answered: false,
      detail:
        "Two packets went unanswered for 3 seconds: UDP is filtered on the way, or nothing speaks QUIC there.",
    },
  },
  resumption: [
    {
      version: "TLS 1.3",
      status: "resumed",
      detail: "The second connection resumed the first one's session.",
    },
    {
      version: "TLS 1.2",
      status: "no-ticket",
      detail:
        "The server sent no session ticket. It may still resume by session ID, which this scan cannot offer.",
    },
  ],
  sni: [
    {
      kind: "none",
      status: "certificate",
      subject: "app.example.com",
      issuer: "R11",
      names: ["app.example.com"],
      fingerprint: "AB:CD",
      sameAsNamed: true,
    },
    {
      kind: "unknown",
      sent: "jd-0a1b2c3d4e5f.invalid",
      status: "certificate",
      subject: "app.example.com",
      issuer: "R11",
      names: ["app.example.com"],
      fingerprint: "AB:CD",
      sameAsNamed: true,
    },
  ],
  findings: [
    {
      id: "tls.alpn.h2-off",
      level: "warning",
      title: "HTTP/2 is on in the site form and not offered",
      detail:
        "app.example.com has HTTP/2 switched on, and offered h2 and http/1.1 the server chose http/1.1.",
      advice:
        "nginx reads `http2 on;` from the server block the name selects, from 1.25.1. A change saved without a reload is not served yet; otherwise check which site answers this name.",
    },
    {
      id: "tls.h3.no-quic",
      level: "warning",
      title: "HTTP/3 is advertised and QUIC does not answer",
      detail:
        "Alt-Svc offers h3 on UDP 443, and nothing answered a QUIC packet there. Two packets went unanswered for 3 seconds.",
      advice:
        "Browsers try it and fall back to TCP, a delay on the first visit. Open UDP 443 in the firewall and at the provider.",
    },
    {
      id: "tls.cipher.weak",
      level: "notice",
      title: "2 weak cipher suites are accepted",
      detail: "ECDHE-ECDSA-AES128-SHA (CBC mode); AES128-SHA (no forward secrecy, CBC mode).",
      advice: "Mozilla's intermediate profile drops them.",
    },
    {
      id: "tls.kex.no-pq",
      level: "notice",
      title: "No post-quantum key exchange",
      detail: "X25519MLKEM768, which current browsers offer first, was refused.",
      advice: "X25519MLKEM768 arrived in OpenSSL 3.5.",
    },
    {
      id: "tls.sni.default-certificate",
      level: "notice",
      title: "A client that names no site, or one this server does not have, gets a certificate",
      detail: "It gets the certificate the scanned name gets, for app.example.com.",
      advice:
        "A default server for the port that refuses those handshakes closes that: server { listen 443 ssl default_server; ssl_reject_handshake on; }",
    },
  ],
}

export const routes: ProxyRoutes = {
  "/certificates/scan/deep": (route) => json(route, deepScan),
}

export const showcase: ProxyRoutes = {}
