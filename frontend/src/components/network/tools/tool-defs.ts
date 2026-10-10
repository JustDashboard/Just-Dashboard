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
  targetLabel?: string
  targetPlaceholder?: string
  needsPort?: boolean
  /** An empty port is allowed and means the tool runs without that part. */
  portOptional?: boolean
  portLabel?: string
  portDefault?: string
  recordOptions?: string[]
  optionLabel?: string
  optionOptions?: { value: string; label: string }[]
  optionDefault?: string
  optionPlaceholder?: string
  optionRequired?: boolean
  /** Reaches outward: proves what the server can reach, never what can reach it. */
  outward?: boolean
  /** Takes an optional address (and TCP port) to watch for an answer afterwards. */
  verify?: boolean
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
        hint: "Resolve a name with this host's resolver and show which source answered: the hosts file or each configured nameserver",
        needsTarget: true,
        targetPlaceholder: "example.com",
        recordOptions: DNS_RECORDS,
      },
      {
        key: "dnsauth",
        label: "DNS authority",
        hint: "Which nameservers own the name, whether the parent's delegation and glue match, and whether every authority agrees",
        needsTarget: true,
        targetPlaceholder: "example.com",
      },
      {
        key: "ping",
        label: "Ping",
        hint: "Loss, round trips and jitter for four ICMP echoes; silence is reported as inconclusive, not down",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
      {
        key: "traceroute",
        label: "Traceroute",
        hint: "A hop table of what is between them, comparable with earlier saved runs",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
      {
        key: "route",
        label: "Route lookup",
        hint: "Ask the kernel which route, interface and source it would use; add a port to join the policy, NAT and firewall layers",
        needsTarget: true,
        targetPlaceholder: "203.0.113.9 or 2001:db8::1",
        needsPort: true,
        portOptional: true,
        portLabel: "Port (optional)",
        portDefault: "",
        optionLabel: "Protocol",
        optionOptions: [
          { value: "tcp", label: "TCP" },
          { value: "udp", label: "UDP" },
        ],
        optionDefault: "tcp",
      },
      {
        key: "mtu",
        label: "Path MTU",
        hint: "Find the path MTU with tracepath, or say why it stays unknown",
        needsTarget: true,
        targetPlaceholder: "example.com or 2001:db8::1",
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
        hint: "Open a TCP connection there, then read it against local listener ownership and firewall evidence",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
        outward: true,
      },
      {
        key: "scan",
        label: "Port scan",
        hint: "Try the exact catalogue of common TCP ports on one pinned address and list every result",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
        outward: true,
      },
      {
        key: "banner",
        label: "Banner grab",
        hint: "Read the service's greeting and identify it by protocol grammar, with a stated confidence",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "22",
        outward: true,
      },
      {
        key: "ssh",
        label: "SSH keys",
        hint: "The host keys offered, compared with fingerprints you saved as trusted",
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
        hint: "Status, redirects, timing stages and, separately, whether the certificate would be trusted",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "httpsec",
        label: "Header grade",
        hint: "The browser security headers that apply to what this response is — a page, an API or an asset",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
        optionLabel: "Response kind",
        optionOptions: [
          { value: "auto", label: "Detect" },
          { value: "page", label: "Page" },
          { value: "api", label: "API" },
        ],
        optionDefault: "auto",
      },
      {
        key: "tls",
        label: "TLS cert",
        hint: "The presented chain certificate by certificate, its trust verdict and fingerprints for later comparison",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "tlssurvey",
        label: "TLS versions",
        hint: "Which protocol versions complete a handshake, with refusals told apart from network failures",
        needsTarget: true,
        targetPlaceholder: "example.com",
        needsPort: true,
        portDefault: "443",
      },
      {
        key: "siteaudit",
        label: "Site audit",
        hint: "HTTP, certificate and headers in one run, each finding with the owner who fixes it",
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
        hint: "Each stage of the mail path — exchangers, SPF, DMARC, SMTP — without claiming delivery",
        needsTarget: true,
        targetPlaceholder: "example.com",
      },
      {
        key: "starttls",
        label: "STARTTLS",
        hint: "Upgrade a mail or FTP session stage by stage, then read its certificate",
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
        hint: "Ask each blocklist, keeping refused or failed queries apart from not listed",
        needsTarget: true,
        targetPlaceholder: "203.0.113.9",
      },
      {
        key: "asn",
        label: "Ownership",
        hint: "Who announces the address — AS, prefix, registry — with the source and the limits of its country",
        needsTarget: true,
        targetPlaceholder: "8.8.8.8",
      },
      {
        key: "whois",
        label: "Whois",
        hint: "Normalised registration fields, each present, redacted or absent, and lookup failures named",
        needsTarget: true,
        targetPlaceholder: "example.com or 203.0.113.9",
      },
    ],
  },
  {
    title: "This host",
    hint: "Interfaces, listeners and LAN devices around this server",
    tools: [
      {
        key: "capabilities",
        label: "Host support",
        hint: "Read the host's networking tools and probe what the kernel and this process actually allow",
        needsTarget: false,
      },
      {
        key: "capture",
        label: "Packet snapshot",
        hint: "Collect up to 50 summaries for 15 seconds, or hand off to a retained PCAP capture; decoded fields may include sensitive data",
        needsTarget: true,
        targetLabel: "Interface",
        targetPlaceholder: "eno1",
        optionLabel: "Protocol",
        optionOptions: [
          { value: "all", label: "All" },
          { value: "tcp", label: "TCP" },
          { value: "udp", label: "UDP" },
          { value: "icmp", label: "ICMP" },
          { value: "icmp6", label: "ICMPv6" },
        ],
        optionDefault: "all",
      },
      {
        key: "listeners",
        label: "Listeners",
        hint: "What this host is bound to, with each socket linked to its Ports ownership and exposure",
        needsTarget: false,
      },
      {
        key: "egress",
        label: "Egress",
        hint: "How each family reaches the internet — route, source and its scope — without claiming the public address",
        needsTarget: false,
      },
      {
        key: "neigh",
        label: "Neighbours",
        hint: "The kernel's neighbour cache with each state explained; a passive read, not a scan",
        needsTarget: false,
      },
      {
        key: "wol",
        label: "Wake-on-LAN",
        hint: "Send a magic packet on this server's LAN, optionally measuring whether the device answers afterwards",
        needsTarget: true,
        targetLabel: "MAC address",
        targetPlaceholder: "00:11:22:33:44:55",
        optionLabel: "LAN interface",
        optionPlaceholder: "eno1",
        optionRequired: true,
        verify: true,
      },
    ],
  },
]
