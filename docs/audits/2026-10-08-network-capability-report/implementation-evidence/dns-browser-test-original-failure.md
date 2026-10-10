# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: network-dns-evidence.spec.ts >> retained DNS evidence separates answering links, native trust and application scope
- Location: tests/browser/network-dns-evidence.spec.ts:176:5

# Error details

```
Error: expect(locator).toBeVisible() failed

Locator: getByLabel('DNS policy evidence report').getByText('Native reports encryption', { exact: true })
Expected: visible
Timeout: 5000ms
Error: element(s) not found

Call log:
  - Expect "toBeVisible" with timeout 5000ms
  - waiting for getByLabel('DNS policy evidence report').getByText('Native reports encryption', { exact: true })

```

```yaml
- link "Just Dashboard 0.7.0":
  - /url: /
  - img "Just"
  - text: Dashboard 0.7.0
- button "Search": Search… Ctrl K
- navigation "Sidebar":
  - button "Back to All pages": All pages
  - paragraph: Network
  - list:
    - listitem:
      - link "Overview":
        - /url: /network
    - listitem:
      - link "Drift":
        - /url: /network/drift
    - listitem:
      - link "Address planning":
        - /url: /network/ipam
    - listitem:
      - link "External checks":
        - /url: /network/external
    - listitem:
      - link "Interfaces":
        - /url: /network/interfaces
    - listitem:
      - link "Routing":
        - /url: /network/routing
    - listitem:
      - link "Firewall":
        - /url: /network/firewall
    - listitem:
      - link "Gateway":
        - /url: /network/gateway
    - listitem:
      - link "Protection":
        - /url: /network/protection
    - listitem:
      - link "VPN":
        - /url: /network/vpn
    - listitem:
      - link "DNS":
        - /url: /network/dns
    - listitem:
      - link "Traffic":
        - /url: /network/traffic
    - listitem:
      - link "Socket history":
        - /url: /network/flows
    - listitem:
      - link "Connections":
        - /url: /network/connections
    - listitem:
      - link "Connection path":
        - /url: /network/investigate
    - listitem:
      - link "Tools":
        - /url: /network/tools
    - listitem:
      - link "Packet captures":
        - /url: /network/captures
    - listitem:
      - link "Saved runs":
        - /url: /network/runs
- button "Account menu for operator": operator admin
- button "Toggle Sidebar"
- main:
  - heading "DNS" [level=1]
  - heading "Resolver chain" [level=2]
  - text: systemd-resolved stub
  - region "Programs on this server":
    - paragraph: This server
    - text: Programs ask 127.0.0.53 search tail4f2c.ts.net
  - region "Resolver":
    - paragraph: systemd-resolved
    - text: 127.0.0.53 214 cached answers
  - region "Upstream servers":
    - list:
      - listitem:
        - paragraph: global · in use
        - text: Cloudflare 1.1.1.1#cloudflare-dns.com Configured opportunistic DoT
      - listitem:
        - paragraph: global
        - text: Cloudflare 1.0.0.1#cloudflare-dns.com Configured opportunistic DoT
      - listitem:
        - paragraph: ens3
        - text: 198.51.100.2 Configured classic DNS
      - listitem:
        - paragraph: tailscale0 · ~ts.net · in use
        - text: MagicDNS 100.100.100.100 inside the tailnet
  - region "Answering on port 53":
    - paragraph: Answering on port 53
    - list:
      - listitem:
        - paragraph: 127.0.0.53:53 · udp tcp
        - text: systemd-resolved systemd-resolve · pid 812
      - listitem:
        - paragraph: 127.0.0.54:53 · udp
        - text: systemd-resolved systemd-resolve · pid 812
      - listitem:
        - paragraph: 10.0.4.9:53 · udp tcp
        - text: AdGuard Home container · AdGuardHome ad-blocker
  - heading "Resolver" [level=2]
  - paragraph: Answered from cache
  - text: 78 %
  - meter "Share of lookups answered from the cache"
  - paragraph: 4,214 hits · 1,188 misses
  - paragraph: Lookups
  - text: 9,628
  - paragraph: 2 in flight · 3 timeouts · 11 failures
  - paragraph: DNSSEC
  - text: 312 secure
  - paragraph: 0 bogus · 4,871 unsigned · setting allow-downgrade
  - paragraph: Encryption policy
  - text: When offered
  - paragraph: Configured DNS over TLS · 2 of 2 servers named
  - heading "Upstreams" [level=2]
  - text: the host's own settings
  - paragraph: Ask a public resolver
  - button "Cloudflare No filtering"
  - button "Cloudflare Blocks malware"
  - button "Quad9 Blocks malware"
  - button "Google No filtering"
  - button "AdGuard DNS Blocks ads and trackers"
  - button "Mullvad Blocks ads and trackers"
  - text: Verification name
  - textbox "Verification name":
    - /placeholder: nas.home.arpa
  - paragraph: "Optional: a name your private network resolves, such as nas.home.arpa. Leave empty to check public names after applying."
  - text: Servers
  - textbox "Servers":
    - /placeholder: "1.1.1.1#cloudflare-dns.com\n1.0.0.1#cloudflare-dns.com"
    - text: 1.1.1.1#cloudflare-dns.com 1.0.0.1#cloudflare-dns.com
  - paragraph: One per line, as 9.9.9.9 or 9.9.9.9#dns.quad9.net. Up to eight; empty uses the servers the network hands out.
  - text: Fallback servers
  - textbox "Fallback servers":
    - /placeholder: 192.168.1.53#resolver.home.arpa
  - paragraph: "Optional, up to eight. Used when no other server is known. Leave empty for the host's fallback defaults. Ports, %interface and #TLS names use the same format as Servers."
  - text: Cache mode
  - combobox "Cache mode": Resolved's default
  - paragraph: Positive answers only avoids caching a missing name; the default leaves the host's policy in charge.
  - text: DNSSEC
  - combobox "DNSSEC": Validate where the server supports it
  - text: Domains
  - textbox "Domains":
    - /placeholder: ~. lan.example.net
  - paragraph: Search domains, or ~domain to send a domain's names only to these servers; ~. is every name.
  - group "DNS over TLS":
    - text: DNS over TLS
    - button "DNS over TLS off": "Off"
    - text: Plain DNS on port 53, readable and changeable by anyone on the path
    - button "DNS over TLS opportunistic" [pressed]: Opportunistic
    - text: Encrypted when the server offers it, plain when it does not
    - button "DNS over TLS required": Required
    - text: Plain DNS is refused; every server needs its name, like 1.1.1.1#cloudflare-dns.com
  - button "Apply" [disabled]
  - heading "Ad-blocking" [level=2]
  - list:
    - listitem:
      - text: AdGuard Home runs as a container · adguard/adguardhome:v0.107.57 Answering on port 53
      - link "Open AdGuard Home's web page on port 3000":
        - /url: http://127.0.0.1:3000
        - text: Open :3000
  - heading "Host records" [level=2]
  - text: 2 records made here in /etc/hosts
  - button "Add record"
  - list:
    - listitem:
      - textbox "Address of record 1":
        - /placeholder: 192.0.2.10
        - text: 192.0.2.10
      - textbox "Names of record 1":
        - /placeholder: nas.lan nas
        - text: nas.lan nas
      - button "Remove record 1"
    - listitem:
      - textbox "Address of record 2":
        - /placeholder: 192.0.2.10
        - text: 10.0.4.20
      - textbox "Names of record 2":
        - /placeholder: nas.lan nas
        - text: grafana.lan
      - button "Remove record 2"
  - button "Save" [disabled]
  - heading "Everything else in the file" [level=2]
  - text: 6 entries · read only
  - table:
    - rowgroup:
      - row "Line Address Names":
        - columnheader "Line"
        - columnheader "Address"
        - columnheader "Names"
    - rowgroup:
      - row "1 127.0.0.1 localhost":
        - cell "1"
        - cell "127.0.0.1"
        - cell "localhost"
      - row "2 127.0.1.1 vps-edge-01":
        - cell "2"
        - cell "127.0.1.1"
        - cell "vps-edge-01"
      - row "3 ::1 ip6-localhost ip6-loopback":
        - cell "3"
        - cell "::1"
        - cell "ip6-localhost ip6-loopback"
      - row "4 fe00::0 ip6-localnet":
        - cell "4"
        - cell "fe00::0"
        - cell "ip6-localnet"
      - row "5 ff02::1 ip6-allnodes":
        - cell "5"
        - cell "ff02::1"
        - cell "ip6-allnodes"
      - row "6 ff02::2 ip6-allrouters":
        - cell "6"
        - cell "ff02::2"
        - cell "ip6-allrouters"
  - heading "Resolve a name" [level=2]
  - text: Name
  - group:
    - textbox "Name":
      - /placeholder: example.com
    - button "Resolve" [disabled]
  - paragraph: Uses the configured resolver chain. Native split-DNS is checked when supported; other paths are marked unknown.
  - text: Record
  - combobox "Record": A
  - switch "Compare named resolvers"
  - text: Compare named resolvers
  - heading "Policy investigation" [level=2]
  - paragraph: Ask the identified native resolver for a fresh DNS record under its split-DNS, TLS and DNSSEC policy. Private names stay within that policy; failure does not try another resolver. Reports retain this host scope and timestamps for seven days, up to 128 finished reports.
  - text: Investigation name
  - textbox "Investigation name":
    - /placeholder: secret.corp.example
  - text: Record type
  - combobox "Record type": A
  - text: Expected policy link (optional)
  - textbox "Expected policy link (optional)":
    - /placeholder: Native automatic selection
  - button "Investigate policy" [disabled]
  - paragraph: An expected link must already be a best-match native policy scope. A/AAAA selects the answer family; it does not force the resolver upstream family or source address.
  - button "secret.corp.example · AAAA · completed"
  - paragraph: secret.corp.example · AAAA
  - paragraph:
    - text: completed ·
    - time: 10/8/2026, 7:00:00 PM
    - text: →
    - time: 10/8/2026, 7:00:01 PM
  - link "Export evidence":
    - /url: /api/v1/network/dns/evidence/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/export
  - button "Delete saved report"
  - paragraph: systemd-resolved · systemd 257 · host_native_resolver · answer family inet6 · upstream family not_measured
  - term: DNS route
  - definition:
    - paragraph: Native answering links · native_reply
    - paragraph: The native reply reports answering link index 7; the exact upstream endpoint remains unmeasured.
  - term: Transport
  - definition:
    - paragraph: Native reports encryption · native_reply
    - paragraph: Native-reported fresh network encryption; upstream forwarding is unknown.
  - term: TLS trust
  - definition:
    - paragraph: Native strict TLS policy · native_reply_and_configuration
    - paragraph: The native strict TLS policy validates its declared identity; certificate inspection was not performed.
  - term: DNSSEC
  - definition:
    - paragraph: Native reports DNSSEC validation · native_reply
    - paragraph: Native resolver reports authenticated fresh DNS data under its trust policy.
  - term: Wire and NSS
  - definition:
    - paragraph: Not measured · scope
    - paragraph: Hosts, NSS and search semantics are not measured by this wire record query.
  - paragraph: Answers
  - paragraph: 2001:db8::7
  - paragraph: "Answering link indexes: 7"
  - paragraph: "Policy snapshot · longest suffix: 2 labels"
  - paragraph: vpn0 · index 7 · configured DoT yes · configured DNSSEC yes
  - paragraph: "Native DNS scope: active"
  - paragraph: ~corp.example → 10.0.0.53#dns.corp.example
  - paragraph: "Current server observation: 10.0.0.53#dns.corp.example; not a per-query endpoint."
  - list:
    - listitem: Application DoH, upstream forwarding and provider layers remain unmeasured.
- region "Notifications alt+T"
- alert
```

# Test source

```ts
  84  | ) {
  85  |   const calls: DNSInvestigationRequest[] = []
  86  |   const saved = record()
  87  |   if (options.alias && saved.result) {
  88  |     const result = saved.result
  89  |     saved.request = { ...saved.request, name: "alias.corp.example" }
  90  |     result.request = saved.request
  91  |     const unknown = { state: "unknown", basis: "unmeasured", summary: "No accepted native flags." }
  92  |     const common = {
  93  |       ownerIdentity: result.ownerIdentity!,
  94  |       policyMatch: result.policyMatch,
  95  |       policy: result.policy,
  96  |       policyAfter: result.policy,
  97  |       policyStable: true,
  98  |       snapshotBefore: "b".repeat(64),
  99  |       snapshotAfter: "b".repeat(64),
  100 |       answerInterfaces: result.answerInterfaces,
  101 |       records: result.records,
  102 |       route: result.route,
  103 |       transport: result.transport,
  104 |       trust: result.trust,
  105 |       dnssec: result.dnssec,
  106 |       nativeFlags: result.nativeFlags,
  107 |     }
  108 |     result.hops = [
  109 |       {
  110 |         ...common,
  111 |         name: "alias.corp.example",
  112 |         type: "AAAA",
  113 |         answers: [],
  114 |         records: [],
  115 |         answerInterfaces: [],
  116 |         nativeFlags: undefined,
  117 |         transport: unknown,
  118 |         trust: unknown,
  119 |         dnssec: unknown,
  120 |         error: "Call failed: CNAME resolving disabled on 'alias.corp.example'",
  121 |       },
  122 |       {
  123 |         ...common,
  124 |         name: "alias.corp.example",
  125 |         type: "CNAME",
  126 |         answers: ["secret.corp.example."],
  127 |         records: [{ interfaceIndex: 7, owner: "alias.corp.example", type: 5, ttl: 60 }],
  128 |         aliasTarget: "secret.corp.example",
  129 |       },
  130 |       {
  131 |         ...common,
  132 |         name: "secret.corp.example",
  133 |         type: "AAAA",
  134 |         answers: result.answers,
  135 |       },
  136 |     ]
  137 |   }
  138 |   if (options.interrupted) {
  139 |     saved.status = "interrupted"
  140 |     delete saved.result
  141 |   }
  142 |   await mockNetwork(page, [], {
  143 |     overrides,
  144 |     session: options.readonly
  145 |       ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
  146 |       : admin,
  147 |   })
  148 |   await page.route("**/api/v1/network/dns/evidence**", async (route) => {
  149 |     const request = route.request()
  150 |     const path = new URL(request.url()).pathname
  151 |     if (request.method() === "POST") {
  152 |       calls.push(request.postDataJSON())
  153 |       if (options.failedLaunch)
  154 |         return json(
  155 |           route,
  156 |           {
  157 |             error: {
  158 |               code: "unavailable",
  159 |               message: "Native owner unavailable; no alternate resolver was used.",
  160 |             },
  161 |           },
  162 |           503,
  163 |         )
  164 |       return json(route, saved, 201)
  165 |     }
  166 |     if (path.endsWith(`/${id}`)) return json(route, saved)
  167 |     return json(route, [{ ...saved, result: undefined }])
  168 |   })
  169 |   await page.goto("/network/dns")
  170 |   await expect(
  171 |     page.getByRole("heading", { name: "Policy investigation", exact: true }),
  172 |   ).toBeVisible()
  173 |   return calls
  174 | }
  175 |
  176 | test("retained DNS evidence separates answering links, native trust and application scope", async ({
  177 |   page,
  178 | }) => {
  179 |   await setup(page)
  180 |   await page
  181 |     .getByRole("button", { name: "secret.corp.example · AAAA · completed", exact: true })
  182 |     .click()
  183 |   const report = page.getByLabel("DNS policy evidence report")
> 184 |   await expect(report.getByText("Native reports encryption", { exact: true })).toBeVisible()
      |                                                                                ^ Error: expect(locator).toBeVisible() failed
  185 |   await expect(report.getByText("Native reports DNSSEC validation", { exact: true })).toBeVisible()
  186 |   await expect(report.getByText("Native strict TLS policy", { exact: true })).toBeVisible()
  187 |   await expect(report.getByText("2001:db8::7", { exact: true })).toBeVisible()
  188 |   await expect(report).toContainText("not a per-query endpoint")
  189 |   await expect(report).toContainText("Hosts, NSS and search semantics")
  190 |   await expect(report).toContainText("provider layers remain unmeasured")
  191 |   await expect(report.getByRole("link", { name: "Export evidence" })).toHaveAttribute(
  192 |     "href",
  193 |     `/api/v1/network/dns/evidence/${id}/export`,
  194 |   )
  195 | })
  196 |
  197 | for (const width of [390, 1280]) {
  198 |   test(`retained alias questions expose scope, answers and per-question trust at ${width}`, async ({
  199 |     page,
  200 |   }) => {
  201 |     await page.setViewportSize({ width, height: 1000 })
  202 |     await setup(page, { alias: true })
  203 |     await page
  204 |       .getByRole("button", { name: "alias.corp.example · AAAA · completed", exact: true })
  205 |       .click()
  206 |     const chain = page.getByLabel("Native DNS question chain")
  207 |     await expect(chain).toContainText("failed discovery questions have no transport measurement")
  208 |     const questions = chain.getByRole("listitem")
  209 |     await expect(questions).toHaveCount(3)
  210 |     await expect(questions.nth(0)).toContainText("alias.corp.example · AAAA")
  211 |     await expect(questions.nth(0)).toContainText("Accepted records: None")
  212 |     await expect(questions.nth(0)).toContainText("Unknown")
  213 |     await expect(questions.nth(1)).toContainText("CNAME target → secret.corp.example")
  214 |     await expect(questions.nth(1)).toContainText("vpn0 · ~corp.example · DNS active")
  215 |     await expect(questions.nth(1)).toContainText("Native strict TLS policy")
  216 |     await expect(questions.nth(2)).toContainText("Accepted records: 2001:db8::7")
  217 |     await expect(questions.nth(2)).toContainText("Native reports DNSSEC validation")
  218 |     expect(await chain.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
  219 |   })
  220 | }
  221 |
  222 | test("explicit launch sends only the question and expected native policy link", async ({
  223 |   page,
  224 | }) => {
  225 |   const calls = await setup(page)
  226 |   await page.getByLabel("Investigation name", { exact: true }).fill("secret.corp.example")
  227 |   await page.getByLabel("Record type", { exact: true }).click()
  228 |   await page.getByRole("option", { name: "AAAA", exact: true }).click()
  229 |   await page.getByLabel("Expected policy link (optional)", { exact: true }).fill("vpn0")
  230 |   await page.getByRole("button", { name: "Investigate policy", exact: true }).click()
  231 |   await expect
  232 |     .poll(() => calls)
  233 |     .toEqual([{ name: "secret.corp.example", type: "AAAA", expectedInterface: "vpn0" }])
  234 |   await expect(page.getByLabel("DNS policy evidence report")).toContainText("answer family inet6")
  235 | })
  236 |
  237 | test("failed launch preserves dated selected evidence without an automatic retry", async ({
  238 |   page,
  239 | }) => {
  240 |   const calls = await setup(page, { failedLaunch: true })
  241 |   await page
  242 |     .getByRole("button", { name: "secret.corp.example · AAAA · completed", exact: true })
  243 |     .click()
  244 |   await expect(page.getByLabel("DNS policy evidence report")).toContainText("2001:db8::7")
  245 |   await page.getByLabel("Investigation name", { exact: true }).fill("other.corp.example")
  246 |   await page.getByRole("button", { name: "Investigate policy", exact: true }).click()
  247 |   await expect(
  248 |     page.getByRole("alert").filter({ hasText: "Investigation unavailable" }),
  249 |   ).toContainText("No automatic retry")
  250 |   await expect(page.getByLabel("DNS policy evidence report")).toContainText("2001:db8::7")
  251 |   expect(calls).toHaveLength(1)
  252 | })
  253 |
  254 | test("interrupted saved questions have no fabricated trust and never rerun on read", async ({
  255 |   page,
  256 | }) => {
  257 |   const calls = await setup(page, { interrupted: true })
  258 |   await page
  259 |     .getByRole("button", { name: "secret.corp.example · AAAA · interrupted", exact: true })
  260 |     .click()
  261 |   const report = page.getByLabel("DNS policy evidence report")
  262 |   await expect(report).toContainText("An interrupted query is not rerun")
  263 |   await expect(report.getByText("Native reports encryption", { exact: true })).toHaveCount(0)
  264 |   expect(calls).toHaveLength(0)
  265 | })
  266 |
  267 | test("reader sees the diagnostic boundary without private-history requests", async ({ page }) => {
  268 |   const requests: string[] = []
  269 |   page.on("request", (request) => {
  270 |     if (request.url().includes("/dns/evidence")) requests.push(request.url())
  271 |   })
  272 |   await setup(page, { readonly: true })
  273 |   await expect(
  274 |     page.getByText(
  275 |       "Policy investigations and saved private answers require system administration access.",
  276 |     ),
  277 |   ).toBeVisible()
  278 |   await expect(page.getByRole("button", { name: "Investigate policy", exact: true })).toHaveCount(0)
  279 |   expect(requests).toHaveLength(0)
  280 | })
  281 |
```
