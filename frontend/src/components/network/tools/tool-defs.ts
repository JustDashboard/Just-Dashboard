/**
 * The registry for the network tools page.
 *
 * Every card renders from one of these definitions and owns its input state,
 * so adding a tool is adding a definition plus a backend case — never shared
 * state surgery.
 */
export type ToolDef = {
  key: string
  label: string
  hint: string
  /** False for the host-local tools, which answer about this machine. */
  needsTarget: boolean
  targetPlaceholder?: string
  needsPort?: boolean
  portDefault?: string
  recordOptions?: string[]
  optionLabel?: string
  optionOptions?: { value: string; label: string }[]
  optionDefault?: string
  /** Reaches outward: proves what the server can reach, never what can reach it. */
  outward?: boolean
}

export type ToolGroup = {
  title: string
  hint: string
  tools: ToolDef[]
}

const DNS_RECORDS = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR"]

export const TOOL_GROUPS: ToolGroup[] = [
  {
    title: "Reachability",
    hint: "Can this server get there at all, and what is in the way",
    tools: [
      {
        key: "dns",
        label: "DNS",
        hint: "Resolve a name using this host's own resolver",
        needsTarget: true,
        targetPlaceholder: "example.com",
        recordOptions: DNS_RECORDS,
      },
      {
        key: "dnsauth",
        label: "DNS authority",
        hint: "Which nameservers own the name, and where they live",
        needsTarget: true,
        targetPlaceholder: "example.com",
      },
      {
        key: "ping",
        label: "Ping",
        hint: "Can this server reach that host at all",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
      {
        key: "traceroute",
        label: "Traceroute",
        hint: "What is between them",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
    ],
  },
  {
    title: "Ports & services",
    hint: "What answers out there, and what it volunteers about itself",
    tools: [
      {
        key: "port",
        label: "Port check",
        hint: "Can this server open a TCP connection there",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
        outward: true,
      },
      {
        key: "scan",
        label: "Port scan",
        hint: "Which of the common service ports answer on that host",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
        outward: true,
      },
      {
        key: "banner",
        label: "Banner grab",
        hint: "What the service says before you say anything",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "22",
        outward: true,
      },
      {
        key: "ssh",
        label: "SSH keys",
        hint: "The host keys offered, with fingerprints",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
        needsPort: true,
        portDefault: "22",
      },
    ],
  },
  {
    title: "Web & TLS",
    hint: "What the site serves, what it proves, and whether it hardened the answer",
    tools: [
      {
        key: "http",
        label: "HTTP",
        hint: "What that host serves — status, redirects and headers",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "httpsec",
        label: "Header grade",
        hint: "HSTS, CSP and the headers that keep a browser honest",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "tls",
        label: "TLS cert",
        hint: "The certificate a TLS port presents, and whether it is trusted",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "tlssurvey",
        label: "TLS versions",
        hint: "Which protocol versions the server still speaks",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "siteaudit",
        label: "Site audit",
        hint: "HTTP, certificate and header grade in one run",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
    ],
  },
  {
    title: "Mail & reputation",
    hint: "Whether mail gets there, and what the internet thinks of the address",
    tools: [
      {
        key: "mx",
        label: "Mail path",
        hint: "Exchangers, SPF, DMARC and a live SMTP knock",
        needsTarget: true,
        targetPlaceholder: "example.com",
      },
      {
        key: "starttls",
        label: "STARTTLS",
        hint: "Upgrade a mail or FTP session, then read its certificate",
        needsTarget: true,
        targetPlaceholder: "mail.example.com",
        needsPort: true,
        portDefault: "25",
        optionLabel: "Protocol",
        optionOptions: [
          { value: "smtp", label: "SMTP" },
          { value: "imap", label: "IMAP" },
          { value: "pop3", label: "POP3" },
          { value: "ftp", label: "FTP" },
        ],
        optionDefault: "smtp",
      },
      {
        key: "dnsbl",
        label: "Blocklists",
        hint: "Is this address known for spam",
        needsTarget: true,
        targetPlaceholder: "203.0.113.9",
      },
      {
        key: "asn",
        label: "Ownership",
        hint: "Who owns the address — AS, prefix, country, registry",
        needsTarget: true,
        targetPlaceholder: "8.8.8.8",
      },
      {
        key: "whois",
        label: "Whois",
        hint: "Registration details for a domain or address",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
    ],
  },
  {
    title: "This host",
    hint: "No target — these answer about the machine under the dashboard",
    tools: [
      {
        key: "listeners",
        label: "Listeners",
        hint: "What this host is bound to, and on which addresses",
        needsTarget: false,
      },
      {
        key: "egress",
        label: "Egress",
        hint: "How this host reaches the internet — source and route",
        needsTarget: false,
      },
      {
        key: "neigh",
        label: "Neighbours",
        hint: "The LAN neighbours this host knows, and their state",
        needsTarget: false,
      },
    ],
  },
]
