# A deployment domain's DNS record

A plan whose domain does not point at this server used to stop at the public-DNS precheck and send the
operator to their DNS provider's dashboard. The credentials the Certificates page saves for certbot's
DNS-01 plugins — Cloudflare, Route 53, DigitalOcean, Google Cloud DNS, Linode, OVH, Gandi and RFC 2136
(`proxysvc/dns01.go`) — write an address record the same way they write a challenge's TXT record. So the
Domains settings now say which record a name needs and whether a saved credential can write it, and one
press of **Create the record** writes it: an `A` and an `AAAA` to this server, or for a name served
through Cloudflare Tunnel the proxied `CNAME` to the tunnel. Preflight says the same before a release,
and its finding opens the sheet. Nothing is ever deleted.

Code: `dnsrecords/` (the provider layer: zone matching, the plan, each provider's API, the DNS UPDATE
and TSIG wire code), `proxysvc/dns_record_credentials.go` (which providers have something saved, what is
saved, and whose nameservers a zone names), `api/handlers_deploy_dns_record.go` (the two routes, the plan
they share with preflight, the record kept of what was written), `deploy/preflight_dns_record.go` (the
findings), `store/schema_dns_records.go`, and on the page `components/deploy/settings/dns-record-panel.tsx`
with its words in the unit-tested `dns-record-model.ts`.

## What a name needs

- **A name served at this server** wants an `A` to the first public IPv4 address and an `AAAA` to the
  first public IPv6 address the host's interfaces carry (`proxysvc.PublicAddresses`, the list preflight's
  comparison reads, sorted): private, loopback, link-local and CGNAT addresses, Tailscale's among them,
  never count. A family the host has no public address of gets no record. A host with no public address
  at all — every VPS behind provider NAT — gets nothing: the address the record should name is the one
  the provider maps in front of the instance, which cannot be read from here (`no_public_address`).
- **A name served through Cloudflare Tunnel** (`domains[i].tunnel`) wants a proxied `CNAME` to
  `<tunnel-id>.cfargotunnel.com` with Cloudflare's automatic TTL, carrying the comment
  `Just Dashboard: routed through Cloudflare Tunnel` (`cftunnel.RecordComment`), the mark the tunnel's own
  pass knows its records by, so either may later move it to another tunnel. With no tunnel connected
  there is nothing to name (`tunnel_not_connected`). Only a zone on Cloudflare can hold it: a
  `cfargotunnel.com` target answers only behind Cloudflare's proxy, so a tunnel's name whose zone a
  different provider holds is refused (`tunnel_zone`) rather than written where it would answer nothing.
  The tunnel's pass already writes this record as the release goes live when the Cloudflare credentials
  reach the zone (`cftunnel.EnsureCNAME`); the sheet writes it on demand, before a release, or after the
  pass refused it.

TTLs: 300 seconds unless the request asks for another (`?ttl=` on the dry run, `ttl` on the write), never
under the provider's floor (Cloudflare 60, DigitalOcean 30, OVH 60, Gandi 300; Route 53, Google and RFC
2136 take any), never over a day, and on Linode rounded up to its next step (30, 120, 300, 3600, …), so
what is shown is what is kept (`dnsrecords.TTLFor`).

## Which credential, which zone

Credentials are kept per provider, not per zone, so every provider with something saved is asked at once,
each bounded at 20 seconds, which of its zones holds the name (`dnsrecords.Resolve`). The answer is the
**longest** zone any of them reaches: a sub-zone delegated to another provider (`preview.example.com` at
Route 53 inside `example.com` at Cloudflare) is the one whose servers answer for the name. Two providers
holding the very same zone — a move between providers half done — are told apart by the zone's
nameservers (`proxysvc.ZoneHostedBy`, the lookup and patterns a wildcard's challenge uses); with no
answer there it is a refusal (`ambiguous`), since a record written into the zone nobody serves changes
nothing anyone sees. None reaching the name is `no_zone`, with each provider's own refusal or "reaches no
zone holding it"; nothing saved is `no_credentials`. Each refusal names `Proxy → Certificates → DNS
challenge providers (/proxy/certificates)`.

| Provider | API, as documented | Signs in with | Zones | Records |
| --- | --- | --- | --- | --- |
| Cloudflare | v4 `zones`, `zones/{id}/dns_records` | API token (Bearer), or e-mail + global key | every zone the token reaches, across accounts, a page at a time | one per value; proxied flag and comment kept; an update keeps an address record's proxied flag as the operator set it |
| AWS Route 53 | REST 2013-04-01 `hostedzone`, `…/rrset` | SigV4 (`us-east-1`, `route53`) from the saved access key pair | public hosted zones; private ones skipped | sets: `CREATE` (refused if one appeared since it was read), `UPSERT`; an alias or a routing policy's set is refused, as it is changed in the Route 53 console |
| DigitalOcean | v2 `domains`, `…/records` | personal access token | every domain, following `links.pages.next` | one per value, names relative, a CNAME's target with its dot |
| Google Cloud DNS | v1 `managedZones`, `rrsets` create / patch | the service account's JSON key: an RS256 JWT exchanged at its `token_uri` (JWT bearer grant), the token reused until a minute before it expires | the key's project's public zones | sets, created and patched whole |
| Linode | v4 `domains`, `…/records` | personal access token (v3 keys refused: Linode retired that API) | master domains; slave domains skipped | one per value, TTL rounded to Linode's steps |
| OVH | `/domain/zone`, `…/record`, `…/refresh` | the application key, secret and consumer key, every call signed `$1$` + SHA-1 over the secret, consumer key, method, URL, body and the API's own clock (`/auth/time`, read once) | every zone of the account | one per value, then a zone refresh, which is what makes OVH serve it |
| Gandi | LiveDNS v5 `domains`, `…/records/{name}/{type}` | personal access token (Bearer) or the older API key (`Apikey`), with the sharing id when saved | every domain | sets: `POST` creates (refused if it exists), `PUT` replaces; TTL floor 300 |
| RFC 2136 | DNS itself, over TCP to the saved server and port | the saved TSIG key (HMAC-MD5 … HMAC-SHA512, the plugin's default MD5) | the zone whose SOA the server answers authoritatively for the name or a parent | one signed `UPDATE` per set whose prerequisite holds the set as read — absent for a create, present for a change, so one that changed in between is refused (`YXRRSET`, `NXRRSET`) — and whose change replaces the set in the same message; every answer's TSIG is verified |

The RFC 2136 half is written against the RFCs (1035, 2136, 8945) in `dnsrecords/dnswire.go` — names,
records, the header, compression when reading, TSIG signing and verification — rather than adding a DNS
library to `go.mod`: writing one set and reading three takes a page of it. Route 53's signer is a copy of
`secretstore/sigv4.go`, checked against the same AWS example, so the leaf package does not reach the
store; a fix to one is a fix to both. Route 53 saved without a key (certbot using the machine's role)
cannot write from here, and the refusal says so.

## The plan

The zone's `A`, `AAAA` and `CNAME` sets at the name are read and compared with what the name should hold
(`dnsrecords.Diff`), one line per set:

- `create` — the name has none of that type.
- `update` — it has other values. On a provider that keeps one record per value an update patches the
  records in place and adds what is missing; on a set provider it replaces the set.
- `keep` — already right (values compared as a set, case and the root's dot aside; for the tunnel's
  CNAME, proxied too). A TTL that differs is not a reason to write.
- `conflict` — nothing is written while one stands, because it would take deleting a record: a CNAME
  where an address is wanted, an address where the tunnel's CNAME is, a set a one-record-per-value
  provider would have to shrink, a set this code cannot replace (Route 53's alias or routing policy). The
  sentence says what to remove where the zone is kept.
- `stale` — an `AAAA` (or `A`) of a family this server has no public address of, which sends those
  visitors elsewhere. It is said and left alone.

Every set this dashboard writes is kept in `deploy_dns_records` — one row per name and type (`UNIQUE
(hostname, record_type)`), the values and TTL as written, the provider and zone, who wrote it first and
who last, and when. A set whose values the zone still holds as written is **ours**, and so is a tunnel
CNAME carrying the tunnel's comment. The dry run marks each line with it, the sheet says "Record managed
here · created 3h ago by alice", and an `update` of a set that is not ours makes the plan `foreign`: the
write then changes it only when told `replace: true`, which the sheet sends after it has said "Replace the
record". Rows are never read to delete anything, and stay when a deployment is removed: they describe the
zone, which keeps the record.

## Routes

| Route | Gate | Audit |
| --- | --- | --- |
| `GET /deploy/{id}/domains/{domain}/dns-record` | `system.admin`, a session | — (a read) |
| `POST /deploy/{id}/domains/{domain}/dns-record` | `system.admin`, a session | `deploy.domain.record` |

`{domain}` is a name the deployment's saved configuration lists, in production or staging, else `404
domain_not_found`. The GET is the dry run (`dnsRecordPlan`): the sets wanted, the provider and zone (and
account), every provider as it was asked, saved credentials that could not be used at all, the lines,
`writable`, `ready`, `foreign`, the managed rows and a one-sentence `summary`; a plan that cannot be
written answers 200 with `refusal` and `refusalCode`, since the dry run's job is to say so. The POST
(optional body `{ttl?, replace?}`) plans again, so nothing between the dry run and the press is written
blind, then writes the pending lines in order and answers the plan as written. Its refusals:
`409 dns_record_unwritable` (no credentials, no zone, ambiguous), `409 dns_record_tunnel_zone`, `409
dns_record_conflict`, `409 dns_record_foreign` (without `replace`), `409 no_public_address`, `409
tunnel_not_connected`, and `502 dns_provider_error` in the provider's words (`Cloudflare refused to create
A app.example.com: Invalid TTL… (code 9021)`), naming what was written before it when a later set failed.
A plan already right answers 200 and is audited as `present`. The audit's detail carries the deployment,
environment, provider key, zone, each record written with its action, `replace`, and the result or the
error — never a credential.

Both routes stay a session's whatever the key: `deployKeyRoutes` gives each `keySession(whyDNSRecord)`
("a domain's DNS record is read and written in a zone outside this server, with the DNS credentials saved
under Certificates, in the dashboard"), so a deploy key and an ordinary key get `session_required`. They
sit with the configuration PUT the Domains settings save through, at `system.admin`.

## The credentials

What the Certificates page saved stays where it was saved, 0600 in certbot's tree
(`/etc/letsencrypt/jd-dns/<provider>.ini`). A route that needs it reads the file for that request
(`proxysvc.SavedDNSCredentials`, bounded at 64 KiB) and keeps nothing: no answer, log line, audit entry or
error carries a token, key or secret, and the providers' own refusals are read from their error documents,
never echoed from a request. Every call goes to the provider's documented endpoint — the one exception is
what an administrator saved, an RFC 2136 server's address or an OVH endpoint given as an `https://` URL
— so a request cannot point a saved credential anywhere new. A deployment run still never opens these
files: it names them to certbot (`deployment_certificate_dns.go`). The Cloudflare file was already read
this way by the tunnel (`proxysvc.SavedCloudflareCredentials`).

## Preflight

When a planned name's public lookup does not reach where it should — this server, or for a tunnel's name
Cloudflare's edge (a proxied record resolves to Cloudflare's addresses, so `BehindProxy` is "there") —
preflight asks the same plan (`HostPreflightObserver.WithDNSRecords`, bounded at 20 seconds) and adds,
beside the lookup's own `dns_unverified`:

- `dns_record_writable` (warning): *DNS record can be created from here* (or *corrected*), the records as
  lines, which saved credentials reach which zone, and the fix `{kind: "dns_record", field:
  "domains.<name>", value: <name>}`, which the run page draws as **Create the record** leading to
  `/deploy/<id>/settings/domains?record=<name>`; the Domains settings open the sheet on it and drop the
  address.
- `dns_record_missing` (warning): *DNS record is missing* (or *names another server*), the records to
  create by hand, why nothing saved can write them, and a link to `/proxy/certificates`.

A zone that already holds exactly the records (resolvers have not caught up) adds nothing; neither does a
host with no public address or a tunnel's name with no tunnel, which preflight already speaks of. Both
are warnings, like the lookup's: a release may go ahead and serve once the record exists. Nothing is
written by preflight.

## The Domains settings

Each saved domain's menu has **Open the DNS record**, an administrator's (a name not saved yet has
nothing on the server to ask). The sheet asks the dry run once as it opens — never polled, since every
read is a call to the provider's API — and draws where the record stands (*Missing*, *Names another
server*, *Points here*, *Points at the tunnel*, *Another record is in the way*, *Cannot be written from
here*), the dry run's sentence, each set as the line to copy with its TTL, its word (*To create*, *To
correct*, *To replace*, *Managed here*, *Already right*, *In the way*, *Left as it is*) and what the zone
holds now, the zone and provider, and who wrote what this dashboard manages. The one button is the dry
run's own verb — **Create the record**, **Correct the record**, or **Replace the record**, which sends
`replace` — and is absent when there is nothing to write or nothing can be written. A refusal is a notice
titled with what stands in the way (*No DNS credentials are saved*, *No saved credential reaches this
zone*, *The zone is not on Cloudflare*, …) carrying the server's sentence and, where saving a token is the
answer, **Open Certificates**, under the records to create by hand where the zone is kept.

## Verification

- `go test ./internal/dnsrecords` — zone matching (the longest zone, wildcards, names that merely end
  alike), the plan's every action and refusal, TTL floors and Linode's steps, `Resolve` across providers
  (a delegated sub-zone, a tie the nameservers settle and one they do not, each provider's refusal), and
  each provider against an `httptest` fake of its documented API that keeps the rules the real service
  does (a CNAME stands alone, a create of an existing set is refused, TTL floors): Cloudflare across two
  accounts a page at a time, Route 53 with every request's SigV4 verified by a verifier of the fake's own
  and a private zone of the same name passed over, DigitalOcean following its page links, Linode skipping
  a slave domain, Gandi with a token and with the older key, OVH with its signature checked against the
  fake's own clock two minutes ahead, Google signing in once with a JWT the fake verifies against the
  key's public half and a private zone passed over. Each fake also refuses in its provider's error shape,
  and the client's error carries those words. The RFC 2136 client is checked against an in-process
  authoritative server that verifies every request's TSIG, signs its answers, applies prerequisites and
  answers with compressed names; the copied SigV4 signer against AWS's documented example.
- `go test ./internal/api -run 'TestDeploymentDNSRecord|TestDNSRecordObserver'` — the dry run and the
  write through the routes against a provider kept in memory: the plan's words, the write, the row and
  the audit, a second write that has nothing to do, a record moved elsewhere replaced only with
  `replace`, a CNAME in the way, the provider's refusal, no credentials, none reaching the zone, no
  public address, a tunnel's name with no tunnel and in a zone not on Cloudflare, its CNAME written
  proxied with TTL 1 and the tunnel's comment, a deploy key and an ordinary key refused
  `session_required`, and limited and read-only roles refused; preflight's observer from the same plan.
- `go test ./internal/deploy -run 'TestPreflightNamesTheRecordADomainNeeds|TestDNSRecordIsAsked'` — when
  preflight asks and what each finding says.
- `JD_DEPLOY_LIVE=1 go test ./internal/dnsrecords -run TestLiveRFC2136WritesRecordsIntoBIND` (root, for
  Docker; build with `go test -c` and run under `sudo`) writes into the BIND the DNS-01 live tests use
  (`proxysvc/acmetest.StartBIND`, its update policy granted A, AAAA, CNAME and TXT): the zone found from a
  name two labels under it, an A and an AAAA created and read back by BIND's own `dig`, the A changed in
  place, a CNAME, a create of a set that appeared since it was read refused by the prerequisite, a name
  outside the zone refused, and a wrong secret refused by BIND's signature check with nothing written.
- `tests/browser/deploy-dns-record.spec.ts` — the sheet from a row's menu and from a finding's address,
  a create, a replace that says so, and a refusal leading to Certificates at phone width.

The public providers cannot be reached from the tests: no test writes to Cloudflare, Route 53,
DigitalOcean, Google Cloud DNS, Linode, OVH or Gandi themselves. Their clients are proven against fakes
that follow the providers' documentation, not against the services.

## Limits

- **One address per family.** A host with several public IPv4 addresses gets a record for the first;
  round-robin across them is not offered.
- **Nothing is deleted**, ever: a record in the way is the operator's to remove where the zone is kept,
  and a name that leaves a deployment keeps its record.
- **Propagation is the provider's.** The write answers when the provider accepted it; resolvers see it
  when the old record's TTL runs out, which preflight's lookup keeps saying until then.
- **Google Cloud DNS** reaches the zones of the key's own project only.
- **A host behind NAT** gets no record from here, since its public address cannot be read.
