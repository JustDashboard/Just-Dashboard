import type { DiagnosticResult } from "../../../src/lib/network-diagnostics"

/**
 * One representative structured answer per server diagnostic, in the shape
 * `backend/internal/netsec` returns, with the readings each tool's card must
 * show. The Go tests prove the backend builds these readings; these prove the
 * page shows them.
 */
export type ToolEvidence = {
  key: string
  target: string
  result: DiagnosticResult
  visible: string[]
}

const base = (
  tool: string,
  target: string,
): Pick<DiagnosticResult, "tool" | "target" | "duration" | "output"> => ({
  tool,
  target,
  duration: "40ms",
  output: `${tool} raw output`,
})

export const TOOL_EVIDENCE: ToolEvidence[] = [
  {
    key: "dns",
    target: "app.example.test",
    visible: [
      "Answers by source",
      "Where each answer came from",
      "The hosts file overrides DNS for this name",
      "Name service order",
      "nameserver 127.0.0.53",
    ],
    result: {
      ...base("dns", "app.example.test"),
      ok: true,
      verdict: "findings",
      summary: "1 A record(s) from this host's resolver.",
      records: ["192.0.2.7"],
      facts: [
        {
          label: "Resolver",
          value: "This dashboard's process resolver (Go, host network namespace)",
          basis: "configured",
        },
        { label: "Name service order", value: "files dns", basis: "configured" },
        { label: "Nameservers", value: "127.0.0.53", basis: "configured" },
      ],
      tables: [
        {
          id: "sources",
          title: "Answers by source",
          columns: ["Source", "Transport", "Result", "Answers", "TTL (s)", "Time"],
          rows: [
            ["hosts file (/etc/hosts)", "file", "entry", "192.0.2.7", "", ""],
            ["nameserver 127.0.0.53", "UDP", "NOERROR", "192.0.2.10", "300", "2ms"],
          ],
        },
        {
          id: "attribution",
          title: "Where each answer came from",
          columns: ["Answer", "Source"],
          rows: [["192.0.2.7", "hosts file (NSS files)"]],
        },
      ],
      findings: [
        {
          id: "hosts-override",
          level: "warning",
          title: "The hosts file overrides DNS for this name",
          detail: "Services on this host resolve app.example.test from /etc/hosts.",
          owner: "This host's hosts file",
        },
      ],
      links: [
        { label: "Inspect the local stub's upstream servers and policy", href: "/network/dns" },
      ],
    },
  },
  {
    key: "dnsauth",
    target: "example.test",
    visible: [
      "Delegation from the parent zone",
      "Authoritative servers",
      "Authoritative servers disagree on the SOA serial",
      "present",
    ],
    result: {
      ...base("dnsauth", "example.test"),
      ok: true,
      verdict: "findings",
      summary: "2 of 2 authoritative addresses answered; 1 consistency problem(s).",
      facts: [{ label: "Zone", value: "example.test", basis: "observed" }],
      tables: [
        {
          id: "delegation",
          title: "Delegation from the parent zone",
          columns: ["Nameserver", "Glue at parent", "Listed by the zone", "Glue check"],
          rows: [["ns1.example.test", "192.0.2.1", "yes", "present"]],
        },
        {
          id: "authorities",
          title: "Authoritative servers",
          columns: ["Nameserver", "Address", "Authoritative", "SOA serial", "NS set", "Time"],
          rows: [
            ["ns1.example.test", "192.0.2.1", "yes", "2026100901", "matches", "3ms"],
            ["ns2.dns.test", "198.51.100.2", "yes", "2026100800", "matches", "4ms"],
          ],
        },
      ],
      findings: [
        {
          id: "serial-mismatch",
          level: "warning",
          title: "Authoritative servers disagree on the SOA serial",
          detail: "A recent change may still be propagating.",
          owner: "DNS provider",
        },
      ],
    },
  },
  {
    key: "mtu",
    target: "192.0.2.9",
    visible: [
      "inconclusive",
      "unknown (at most 1500 observed before the destination)",
      "First-hop MTU on this host",
    ],
    result: {
      ...base("mtu", "192.0.2.9"),
      ok: true,
      verdict: "unknown",
      summary:
        "The destination was not reached. Up to hop 3 the path carried 1500-byte packets; beyond it the path MTU is unknown.",
      facts: [
        { label: "First-hop MTU on this host", value: "1500", basis: "configured" },
        {
          label: "Path MTU",
          value: "unknown (at most 1500 observed before the destination)",
          basis: "unknown",
        },
      ],
    },
  },
  {
    key: "port",
    target: "192.0.2.10",
    visible: [
      "Local process",
      "The firewall model predicted a block, yet TCP connected",
      "Path layers",
    ],
    result: {
      ...base("port", "192.0.2.10:5432"),
      ok: true,
      verdict: "findings",
      summary: "TCP connected from this host in 2ms.",
      facts: [
        { label: "Connected address", value: "192.0.2.10:5432", basis: "observed" },
        { label: "Local process", value: "postgres", basis: "observed" },
        { label: "Firewall prediction (tcp/5432)", value: "deny", basis: "inferred" },
      ],
      tables: [
        {
          id: "path",
          title: "Path layers",
          columns: ["Layer", "Basis", "State", "Summary", "Owner"],
          rows: [
            [
              "Destination owner",
              "observed",
              "observed",
              "A matching local listener was observed.",
              "Listener owner",
            ],
          ],
          rowLinks: ["/ports"],
        },
      ],
      findings: [
        {
          id: "firewall-disagrees",
          level: "notice",
          title: "The firewall model predicted a block, yet TCP connected",
          detail: "The modeled rule set is incomplete for this flow.",
          owner: "Host firewall",
          href: "/network/firewall",
        },
      ],
    },
  },
  {
    key: "scan",
    target: "192.0.2.5",
    visible: [
      "Ports tried",
      "exactly 30 TCP ports from the service catalogue",
      "UDP services and every other TCP port",
      "Port 3306 (MySQL / MariaDB) answers",
    ],
    result: {
      ...base("scan", "192.0.2.5"),
      ok: true,
      verdict: "findings",
      summary: "2 open, 1 closed, 27 without a reply of 30 TCP ports on 192.0.2.5.",
      facts: [
        { label: "Scanned address", value: "192.0.2.5", basis: "observed" },
        {
          label: "Coverage",
          value: "exactly 30 TCP ports from the service catalogue: 22, 80, 443, 3306",
          basis: "configured",
        },
        {
          label: "Not covered",
          value: "UDP services and every other TCP port",
          basis: "configured",
        },
      ],
      tables: [
        {
          id: "ports",
          title: "Ports tried",
          columns: ["Port", "Service", "State", "Time", "Note"],
          rows: [
            ["22", "SSH", "open", "1ms", ""],
            ["80", "HTTP", "closed", "1ms", ""],
            ["3306", "MySQL / MariaDB", "open", "1ms", "Databases should not be reachable"],
          ],
        },
      ],
      findings: [
        {
          id: "open-3306",
          level: "warning",
          title: "Port 3306 (MySQL / MariaDB) answers",
          detail: "Databases should not be reachable.",
          owner: "Service on 192.0.2.5",
        },
      ],
    },
  },
  {
    key: "banner",
    target: "192.0.2.4",
    visible: [
      "Identification confidence",
      "high — RFC 4253 identification string",
      "self-reported",
      "OpenSSH 9.6p1",
    ],
    result: {
      ...base("banner", "192.0.2.4:22"),
      ok: true,
      verdict: "ok",
      summary: "The service greeted with a banner identified as SSH 2.0 (high confidence).",
      facts: [
        { label: "Protocol", value: "SSH 2.0", basis: "inferred" },
        {
          label: "Identification confidence",
          value: "high — RFC 4253 identification string",
          basis: "inferred",
        },
        { label: "Software (self-reported)", value: "OpenSSH 9.6p1", basis: "self_reported" },
      ],
      limitations: ["A banner is whatever the service chooses to send."],
    },
  },
  {
    key: "http",
    target: "example.test",
    visible: [
      "Checks",
      "TLS handshake",
      "Transport trust",
      "The server answered, but a browser would reject its certificate",
      "Requests",
    ],
    result: {
      ...base("http", "https://example.test:443/"),
      ok: true,
      verdict: "findings",
      summary: "200 OK from https://example.test:443/; transport not trusted.",
      stages: [
        { id: "dns", label: "DNS lookup", status: "passed", duration: "2ms" },
        {
          id: "connect",
          label: "TCP connect",
          status: "passed",
          detail: "192.0.2.80:443",
          duration: "3ms",
        },
        { id: "tls", label: "TLS handshake", status: "passed", duration: "9ms" },
        {
          id: "response",
          label: "Request to first byte",
          status: "passed",
          detail: "200 OK",
          duration: "20ms",
        },
      ],
      facts: [
        { label: "HTTP result", value: "200 OK", basis: "observed" },
        {
          label: "Transport trust",
          value: "not trusted for example.test: x509: certificate signed by unknown authority",
          basis: "observed",
        },
      ],
      tables: [
        {
          id: "requests",
          title: "Requests",
          columns: [
            "Hop",
            "Request",
            "Status",
            "Address",
            "DNS",
            "Connect",
            "TLS",
            "First byte",
            "Total",
          ],
          rows: [
            [
              "1",
              "https://example.test:443/",
              "200 OK",
              "192.0.2.80:443",
              "2ms",
              "3ms",
              "9ms",
              "20ms",
              "21ms",
            ],
          ],
        },
      ],
      findings: [
        {
          id: "untrusted",
          level: "warning",
          title: "The server answered, but a browser would reject its certificate",
          detail: "x509: certificate signed by unknown authority.",
          owner: "TLS terminator",
        },
      ],
    },
  },
  {
    key: "httpsec",
    target: "api.example.test",
    visible: [
      "Security headers for this response",
      "not applicable",
      "Response kind",
      "api (detected from Content-Type application/json)",
    ],
    result: {
      ...base("httpsec", "https://api.example.test:443/"),
      ok: true,
      verdict: "ok",
      summary: "2 of 2 headers that apply to this response are set.",
      facts: [
        {
          label: "Response kind",
          value: "api (detected from Content-Type application/json)",
          basis: "inferred",
        },
      ],
      tables: [
        {
          id: "headers",
          title: "Security headers for this response",
          columns: ["Header", "Applies", "Result", "Value"],
          rows: [
            ["HSTS", "required", "set", "max-age=31536000"],
            ["Content-Security-Policy", "optional", "missing", ""],
            ["Framing protection", "not applicable", "missing", ""],
            ["X-Content-Type-Options", "recommended", "set", "nosniff"],
          ],
        },
      ],
    },
  },
  {
    key: "tls",
    target: "example.test",
    visible: [
      "Presented certificate chain",
      "intermediate 1",
      "Trusted for example.test (chains to ISRG Root X1).",
    ],
    result: {
      ...base("tls", "example.test:443"),
      ok: true,
      verdict: "ok",
      summary: "Trusted for example.test (chains to ISRG Root X1); expires in 61 days.",
      facts: [
        {
          label: "Trust",
          value: "Trusted for example.test (chains to ISRG Root X1).",
          basis: "observed",
        },
      ],
      tables: [
        {
          id: "chain",
          title: "Presented certificate chain",
          columns: [
            "Position",
            "Subject",
            "Issuer",
            "Valid from",
            "Valid until",
            "Key",
            "Signature",
            "SHA-256",
          ],
          rows: [
            [
              "leaf",
              "example.test",
              "R11",
              "2026-09-09",
              "2026-12-08",
              "ECDSA P-256",
              "SHA256-RSA",
              "a".repeat(64),
            ],
            [
              "intermediate 1",
              "R11",
              "ISRG Root X1",
              "2024-03-13",
              "2027-03-12",
              "RSA 2048",
              "SHA256-RSA",
              "b".repeat(64),
            ],
          ],
        },
      ],
      metrics: [
        { key: "days_left", label: "Days until the leaf expires", value: 61, unit: "days" },
      ],
    },
  },
  {
    key: "tlssurvey",
    target: "example.test",
    visible: [
      "Protocol versions",
      "rejected by the server",
      "Proxy TLS report for sites this dashboard serves",
    ],
    result: {
      ...base("tlssurvey", "example.test:443"),
      ok: true,
      verdict: "ok",
      summary: "Offers TLS 1.2, TLS 1.3; does not complete TLS 1.0, TLS 1.1.",
      records: ["TLS 1.2", "TLS 1.3"],
      tables: [
        {
          id: "versions",
          title: "Protocol versions",
          columns: ["Version", "Result", "Cipher suite", "Detail"],
          rows: [
            ["TLS 1.0", "rejected by the server", "", "protocol_version alert"],
            ["TLS 1.1", "rejected by the server", "", "protocol_version alert"],
            ["TLS 1.2", "offered", "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256", ""],
            ["TLS 1.3", "offered", "TLS_AES_128_GCM_SHA256", ""],
          ],
        },
      ],
      links: [{ label: "Proxy TLS report for sites this dashboard serves", href: "/proxy/tls" }],
    },
  },
  {
    key: "siteaudit",
    target: "blog.example.test",
    visible: [
      "Served by",
      "proxy site blog on this host",
      "Open proxy site blog",
      "HSTS is missing",
    ],
    result: {
      ...base("siteaudit", "blog.example.test"),
      ok: true,
      verdict: "findings",
      summary: "The site answers; 1 finding(s) to act on.",
      stages: [
        { id: "http", label: "HTTP", status: "passed", detail: "200 OK" },
        { id: "tls", label: "Certificate", status: "passed", detail: "Trusted" },
        { id: "headers", label: "Headers", status: "warning", detail: "4 of 5 headers set" },
      ],
      facts: [{ label: "Served by", value: "proxy site blog on this host", basis: "configured" }],
      findings: [
        {
          id: "headers-header-strict-transport-security",
          level: "warning",
          title: "HSTS is missing",
          detail: "Required for a page response.",
          owner: "Proxy site blog (or the application behind it)",
          action: "Add the header to proxy site blog, or have the application send it.",
          href: "/proxy/sites/blog",
        },
      ],
      links: [{ label: "Open proxy site blog", href: "/proxy/sites/blog" }],
    },
  },
  {
    key: "mx",
    target: "example.test",
    visible: ["SMTP connect (port 25)", "Not checked", "Many providers block outbound port 25"],
    result: {
      ...base("mx", "example.test"),
      ok: true,
      verdict: "unknown",
      summary: "DNS records look complete; a stage could not be observed from this host.",
      stages: [
        { id: "mx", label: "MX records", status: "passed", detail: "10 mx1.example.test" },
        {
          id: "addresses",
          label: "Exchanger addresses",
          status: "passed",
          detail: "1 of 1 exchangers resolve",
        },
        { id: "spf", label: "SPF", status: "passed", detail: "v=spf1 mx -all" },
        { id: "dmarc", label: "DMARC", status: "passed", detail: "v=DMARC1; p=reject" },
        {
          id: "smtp_connect",
          label: "SMTP connect (port 25)",
          status: "unknown",
          detail:
            "mx1.example.test: No reply before the deadline. Many providers block outbound port 25 from servers.",
        },
        { id: "smtp_greeting", label: "SMTP greeting", status: "skipped", detail: "no connection" },
      ],
      facts: [
        {
          label: "Not checked",
          value:
            "DKIM (needs a selector), reverse DNS of the exchangers, blocklists, TLS on port 25 and actual delivery",
          basis: "configured",
        },
      ],
    },
  },
  {
    key: "starttls",
    target: "mail.example.test",
    visible: ["EHLO capabilities", "Upgrade command", "Certificate trust", "skipped"],
    result: {
      ...base("starttls", "mail.example.test:25"),
      ok: false,
      verdict: "failed",
      summary:
        'Stopped at upgrade command: the server refused the upgrade ("454 TLS not available")',
      stages: [
        { id: "connect", label: "TCP connect", status: "passed" },
        { id: "greeting", label: "Greeting", status: "passed", detail: "220 mail ESMTP" },
        {
          id: "capabilities",
          label: "EHLO capabilities",
          status: "warning",
          detail: "STARTTLS is not advertised; trying the upgrade anyway",
        },
        {
          id: "upgrade",
          label: "Upgrade command",
          status: "failed",
          detail: "the server refused the upgrade",
        },
        { id: "handshake", label: "TLS handshake", status: "skipped", detail: "not reached" },
        { id: "certificate", label: "Certificate trust", status: "skipped", detail: "not reached" },
      ],
    },
  },
  {
    key: "dnsbl",
    target: "192.0.2.9",
    visible: [
      "Blocklists",
      "query refused",
      "Spamhaus refuses queries arriving through public or open resolvers",
      "Checked at",
    ],
    result: {
      ...base("dnsbl", "192.0.2.9"),
      ok: true,
      verdict: "unknown",
      summary: "Not listed on 3 list(s); 1 could not be checked.",
      facts: [{ label: "Checked at", value: "2026-10-09T12:00:00Z", basis: "observed" }],
      tables: [
        {
          id: "lists",
          title: "Blocklists",
          columns: ["List", "Result", "Return code", "Detail", "Checked at", "Time"],
          rows: [
            [
              "zen.spamhaus.org",
              "query refused",
              "127.255.255.254",
              "Spamhaus refuses queries arriving through public or open resolvers",
              "2026-10-09T12:00:00Z",
              "8ms",
            ],
            ["bl.spamcop.net", "not listed", "", "", "2026-10-09T12:00:00Z", "9ms"],
          ],
        },
      ],
    },
  },
  {
    key: "asn",
    target: "8.8.8.8",
    visible: [
      "Team Cymru IP-to-ASN service",
      "US (registry record, not a location)",
      "Announcements",
    ],
    result: {
      ...base("asn", "8.8.8.8"),
      ok: true,
      verdict: "ok",
      summary: "AS15169 (GOOGLE, US) announces 8.8.8.0/24; registered in US.",
      facts: [
        {
          label: "Source",
          value:
            "Team Cymru IP-to-ASN service (whois.cymru.com), built from BGP announcements and regional registry allocation files",
          basis: "registry",
        },
        {
          label: "Registration country",
          value: "US (registry record, not a location)",
          basis: "registry",
        },
      ],
      tables: [
        {
          id: "origins",
          title: "Announcements",
          columns: ["AS", "AS name", "BGP prefix", "Registration country", "Registry", "Allocated"],
          rows: [["AS15169", "GOOGLE, US", "8.8.8.0/24", "US", "arin", "2023-12-28"]],
        },
      ],
    },
  },
  {
    key: "whois",
    target: "example.test",
    visible: [
      "Registration",
      "redacted",
      "absent",
      "2 fields present, 1 redacted, 1 absent from this answer",
    ],
    result: {
      ...base("whois", "example.test"),
      ok: true,
      verdict: "ok",
      summary: "Registration found: 2 fields present, 1 redacted by the registry.",
      facts: [
        {
          label: "Lookup outcome",
          value: "2 fields present, 1 redacted, 1 absent from this answer",
          basis: "registry",
        },
      ],
      tables: [
        {
          id: "fields",
          title: "Registration",
          columns: ["Field", "Value", "State"],
          rows: [
            ["Domain", "EXAMPLE.TEST", "present"],
            ["Registrar", "Example Registrar, Inc.", "present"],
            ["Registrant organisation", "REDACTED FOR PRIVACY", "redacted"],
            ["Abuse contact", "", "absent"],
          ],
        },
      ],
    },
  },
  {
    key: "egress",
    target: "",
    visible: [
      "Egress by family",
      "shared address space (carrier-grade NAT)",
      "Measure the public address from an external vantage",
    ],
    result: {
      ...base("egress", "this host"),
      ok: true,
      verdict: "findings",
      summary: "IPv4 and IPv6 both have independent routes to the internet.",
      tables: [
        {
          id: "egress",
          title: "Egress by family",
          columns: [
            "Family",
            "Route",
            "Next hop",
            "Interface",
            "Source",
            "Source scope",
            "Default routes",
          ],
          rows: [
            [
              "IPv4",
              "unicast",
              "100.64.0.1",
              "eth0",
              "100.64.12.7",
              "shared address space (carrier-grade NAT)",
              "1",
            ],
            ["IPv6", "unicast", "fe80::1", "eth0", "2001:db8::7", "global", "1"],
          ],
        },
      ],
      findings: [
        {
          id: "ipv4-nat",
          level: "notice",
          title: "IPv4 leaves from a shared address space (carrier-grade NAT) address",
          detail: "An upstream NAT almost certainly translates it.",
          owner: "Upstream router or provider",
        },
      ],
      links: [
        { label: "Measure the public address from an external vantage", href: "/network/external" },
      ],
    },
  },
  {
    key: "neigh",
    target: "",
    visible: [
      "Neighbour cache",
      "By interface",
      "nothing answered resolution for this address",
      "Docker bridge",
    ],
    result: {
      ...base("neigh", "this host"),
      ok: true,
      verdict: "ok",
      summary: "2 cached neighbours on 2 interfaces: 1 confirmed, 0 stale, 1 failed.",
      tables: [
        {
          id: "neighbours",
          title: "Neighbour cache",
          columns: ["Address", "Family", "Interface", "MAC", "State", "Meaning"],
          rows: [
            [
              "192.168.1.1",
              "IPv4",
              "eth0",
              "aa:bb:cc:dd:ee:01",
              "REACHABLE",
              "confirmed reachable recently",
            ],
            [
              "172.17.0.9",
              "IPv4",
              "docker0",
              "none",
              "FAILED",
              "nothing answered resolution for this address",
            ],
          ],
        },
        {
          id: "interfaces",
          title: "By interface",
          columns: [
            "Interface",
            "Role",
            "Local addresses",
            "Entries",
            "Confirmed",
            "Stale",
            "Failed",
          ],
          rows: [
            ["docker0", "Docker bridge", "172.17.0.1/16", "1", "0", "0", "1"],
            ["eth0", "", "192.168.1.10/24", "1", "1", "0", "0"],
          ],
        },
      ],
    },
  },
  {
    key: "capabilities",
    target: "",
    visible: ["Capability probes", "restricted", "Raw packet sockets is restricted"],
    result: {
      ...base("capabilities", "this host"),
      ok: true,
      verdict: "findings",
      summary:
        "18 of 20 tools present; capability probes: 8 supported, 1 restricted, 1 unsupported, 0 unknown.",
      tables: [
        {
          id: "probes",
          title: "Capability probes",
          columns: ["Capability", "Status", "Evidence", "Enables"],
          rows: [
            [
              "Raw packet sockets",
              "restricted",
              "opening an AF_PACKET socket was refused: the process lacks CAP_NET_RAW",
              "Wake-on-LAN frames from the dashboard process",
            ],
            ["CAKE queue discipline", "supported", "available, not loaded yet", "CAKE shaping"],
          ],
        },
      ],
      findings: [
        {
          id: "probe-packet_socket",
          level: "notice",
          title: "Raw packet sockets is restricted",
          detail: "opening an AF_PACKET socket was refused.",
          owner: "Host kernel and container privileges",
        },
      ],
    },
  },
]
