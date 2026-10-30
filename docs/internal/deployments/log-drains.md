# Log drains

A log drain copies what deployments say, while they say it, to a log service the operator already
runs. Two things are copied: each deployment container's **output** (every line, with its stream
and container) and the **requests** the ingress served it (method, path, query, status, size,
duration, client address, user agent, referer, and the trace the ingress sent the application). An
OpenTelemetry drain can also copy those requests as **traces**: one span per request, under the
trace the ingress sent the application, so the application's own spans join it. Nothing is read for a drain that the dashboard
does not already read for its own pages — the container output Docker keeps and the access log
[request observability](request-observability.md) describes — and nothing is kept for a drain
beyond a bounded queue in memory.

The page is **Deployments → Log drains** (`/deploy/drains`).

## Formats

| Kind | Sends | Path added to the address | Fits |
| --- | --- | --- | --- |
| `otlp` | OTLP/HTTP logs and traces, JSON encoding of the protos | `/v1/logs` for logs and `/v1/traces` for spans, unless the address already ends in one — the rule an OpenTelemetry exporter applies to `OTEL_EXPORTER_OTLP_ENDPOINT`, so Grafana Cloud's `…/otlp` works as given, and an address ending in `/v1/logs` sends its spans to the `/v1/traces` beside it | an OpenTelemetry Collector, Grafana Cloud, Loki's own `/otlp`, Tempo, Jaeger, Honeycomb, Axiom, Datadog, New Relic, SigNoz |
| `loki` | Loki's push API, JSON | `/loki/api/v1/push`, unless already there (so a reverse proxy's prefix is kept) | Loki, Grafana Cloud Logs |
| `http` | JSON lines, one object per record, `application/x-ndjson` | nothing | Vector's `http_server`, Fluent Bit, Logstash's `http` input, Axiom's ingest API |

Every request is gzip-compressed (`Content-Encoding: gzip`) and carries the drain's headers.

**OTLP.** One resource per project and environment: `service.name` (the project),
`service.namespace` `just-dashboard`, `deployment.environment.name` (the environment's slug),
`just_dashboard.project_id`, and `host.name` (this server). A log record's body is the output line,
or a request as a terminal shows it (`GET /checkout?step=2 502 31.5ms`); its attributes are
`just_dashboard.stream`, `log.iostream`, `container.id`, `container.name`, or for a request the HTTP
semantic-convention names — `http.request.method`, `url.path`, `url.query`, `server.address`,
`network.protocol.name` and `.version` (`http`, `1.1`), `http.response.status_code`,
`http.response.body.size`, `client.address`, `user_agent.original`, `http.request.header.referer` and
`http.server.request.duration` in seconds. A request's duration is kept to the microsecond, which is
what the ingress measures. A request's log record carries its span's `traceId` and `spanId`, so a log
service opens the trace from the line.

**Loki.** Streams are labelled `service_name`, `environment`, `stream` (`output` or `requests`),
`source` `just-dashboard`, `host`, `iostream` for output, and `level` where one is known — a handful
of values per deployment, so a stream's cardinality is the deployments', not their requests'. A
request's line is its JSON, which LogQL's `json` parser reads field by field. Each stream's entries
are sent in time order.

**JSON lines.** `timestamp` (RFC 3339 with nanoseconds), `service`, `project_id`, `environment`,
`stream`, `message`, `host`, `level` when known, and `iostream`, `container_id`, `container_name`
for output or a nested `request` object for a request. Loki's and JSON lines' request object carries
`traceId`, `spanId` and `clientClass` too.

### Traces

The `traces` stream (OTLP only; `logdrain/traces.go`) sends every request the ingress recorded as one
span to `/v1/traces`, with the drain's headers, compression, batching and retry:

- **Resource**: `service.name` (the project — the deployment is the service), `service.namespace`
  `just-dashboard`, `deployment.environment.name`, `service.version` (the release live in the
  environment when the request was read), `just_dashboard.project_id`, `host.name`. One resource per
  project, environment and release.
- **Span**: kind `SERVER`; named `<method> <route>`, where the route is the path with what identifies
  one record taken out — a number, a UUID, a hex or token id of 16 or more characters each read `{id}`
  (`RouteTemplate`: `/orders/1041` is `GET /orders/{id}`), because a span's name is what a trace
  service groups by and the ingress does not know the application's routes; from when the ingress
  began to answer (the record's instant less its duration) to when it finished, of no length when
  the format carries no duration; status `ERROR` with the code for a 5xx and unset otherwise.
- **Attributes**: `http.request.method`, `http.route`, `url.path`, `url.query`, `url.scheme`,
  `http.response.status_code`, `http.response.body.size`, `server.address`, `network.protocol.*`,
  `client.address`, `user_agent.original`, `http.server.request.duration`,
  `deployment.environment.name`, `just_dashboard.release` and `just_dashboard.client.class`
  (`accesslog.ClientClass`: `browser`, `bot`, `scanner` for a probe of a path no application serves,
  `client` for any other agent, `unknown`).
- **Ids**: the trace and span the ingress sent the application in its `traceparent`, recorded beside
  the request ([request observability](request-observability.md#trace-context)), with the client's
  span as the parent when the client sent one. A request from a route rendered before the ingress
  sent one has none recorded; it is given random ids here — shared by its log line and its span — so
  it is still a span, of a trace nothing else joins, until the next deploy renders the route.

A batch's log records and spans are two requests to two paths: the logs go first, and when the spans
then fail only they go back to the queue, so a destination is not sent the same lines twice.

### Severity

A record's level is what it says of itself and nothing else: a JSON line's `level`, `severity`,
`lvl`, `log.level` or `levelname` (pino's and bunyan's numbers included), a logfmt `level=`, or a
leading level word (`ERROR:`, `[WARN]`, after a timestamp too). stderr is not a level — the same
rule the Output view follows, because npm's notices and Prisma's banners are printed there. A
request's level is its status class: 5xx `error`, 4xx `warn`, else `info`. OTLP severity numbers are
5, 9, 13 and 17.

## Settings

`deploy_log_drains` holds each drain: name (unique), kind, endpoint, enabled, `streams_json`
(`output`, `requests`, `traces`, at least one; `traces` only on an `otlp` drain), `projects_json`
(empty sends every project, including ones created later), `trace_url` and `config_enc`, the sealed
headers. `trace_url` is where the drain's service opens a trace, with `{traceId}` where the id goes —
`https://grafana.example.com/explore?traceId={traceId}` — an http or https address the request
console draws as a link beside a request's trace id; it is kept only while the drain sends traces. Header values are never returned; the list
carries `headerNames`. An edit without `headers` keeps the sealed ones, and an empty list removes
them. A header may not be one the encoder sets or that would redirect the request
(`Content-Type`, `Content-Encoding`, `Content-Length`, `Host`, `Transfer-Encoding`, `Connection`),
and may not contain a line break; at most 16.

**Transport.** A drain with headers sends them over https, or plain http only to a loopback address
— the rule notification channels, SMTP sign-in and the Git hosts' APIs follow. A drain with no
header may use plain http anywhere, because a collector on the local network seldom has TLS or a
credential; the page says that its records then cross the network unencrypted. Redirects are never
followed. Endpoints may not carry user info or a fragment.

## Delivery

`logdrain.Manager` is started with the server and runs one shipper per enabled drain, and one
reader per thing a drain asks for:

- **Output.** Every ten seconds the manager lists the running containers Docker labels
  `io.just-dashboard.managed=true`, maps each to its project through
  `io.just-dashboard.environment-id`, and follows the output of those some drain wants
  (`docker logs --follow --timestamps`). A container is read from its creation if that is after the
  dashboard started — so a new release's first lines are not lost to the ten-second pass — and
  otherwise from when the dashboard started: a drain carries what happens while it exists, not a
  container's history. Docker's timestamp is each record's instant and where a follow resumes when
  Docker ends the stream.
- **Requests.** Each environment that has a domain — only those have a request record — and whose
  requests or traces a drain wants is read every five seconds through `accesslog.Store.After`
  (`deploy.RouteNameFor`), from now on; each request is offered as a `requests` record and as a
  `traces` one, each to the drains that want it, so a span is a request read once. The
  store reads each byte of the access log once whoever asks, though on Docker Caddy each read is an
  exec into the ingress, which is why the interval is not shorter.
- **Shipping.** A record is offered to every drain that wants its stream and project without
  blocking the reader. A shipper sends a batch a second after its first record or as soon as it holds
  1,000 records or 1 MiB, with a 15-second timeout. A failed batch goes back to the head of the queue
  and is retried with backoff from one second to a minute; past 20,000 queued records the oldest are
  dropped and counted. Stopping the server makes one bounded last attempt; editing a drain hands its
  queue to the shipper that replaces it; pausing or removing one drops it.
- **Status.** `deploy_log_drain_status` keeps what each drain shipped, dropped and batched, and the
  times and words of its last success and last failure; `queued` comes from the running shipper.
  The page reads a drain whose last failure is newer than its last success as failing, in amber, with
  the destination's answer and what is waiting.

**Test** sends one record ("Test record from Just Dashboard: the drain … delivers here.") through the
drain's own settings and answers with the destination's words, and a drain that sends traces is also
sent one span (`GET /just-dashboard/test`) to `/v1/traces`; neither is counted as shipped.

## API

- `GET /deploy/log-drains` — every drain with its status (any signed-in role).
- `POST /deploy/log-drains`, `PUT /deploy/log-drains/{drain}`, `PUT …/{drain}/enabled`,
  `POST …/{drain}/test`, `DELETE …/{drain}` — `system.admin` with a session, audited as
  `deploy.log_drain.create`, `.update`, `.pause`/`.resume`, `.test` and `.delete`; no header value is
  ever audited. A test the destination refused answers 502 `drain_unreachable` with its words.

## Verification

- `logdrain_test.go`: validation and sealing (no value read back or stored in the clear, an edit
  keeping headers, an empty list clearing them, the plain-http rule), severity from JSON, pino,
  logfmt and leading words, each format's shape, the endpoint paths, and the manager end to end with
  fake sources — two drains each getting only what they asked for, a paused one nothing, a new
  container read from its creation, a refused delivery retried, the status recorded — and the queue's
  bound. Run with `-race` too (CI's race lanes cover `api` and `deploy`).
- `traces_test.go`: the span encoding (resource per release, kind, name from the route, times from
  the duration, a 5xx's error status, every attribute, ids for a request with none), the route
  template, the `/v1/traces` path beside `/v1/logs`, traces refused on Loki and JSON lines and the
  link's rule, a span never queued for a logs-only kind, and the manager end to end: one recorded
  request delivered as a line and a span with the same ids and the drain's header, and a refused span
  retried without the line being sent twice.
- `logdrain_live_test.go` (`JD_DEPLOY_LIVE=1`, Docker): Loki 3.5 fed through its push API and its
  OTLP endpoint and read back with LogQL, the labels and the error stream included; an OpenTelemetry
  Collector 0.128 writing the resource, severities and HTTP attributes it received to a file, and the
  spans of its traces pipeline to another — the trace, span and parent ids, the name, kind, status,
  release and client class; Vector 0.47's `http_server` writing the JSON lines.
- `handlers_log_drains_test.go`: roles, no header value in any answer, a test record delivered with
  the drain's header, a refused test in the destination's words, the plain-http refusal, and every
  audit entry.
- `deploy-log-drains.spec.ts` and `log-drains.test.js`: the page's formats, a drain added with a
  sealed header, a failing drain's line, Send test and Pause, an edit keeping headers, the
  plain-http rules, the health and reach sentences, an OpenTelemetry drain sending traces with a link
  (and Loki offering none), and which drain a request's trace link comes from.
