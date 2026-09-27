"use client"

import { fieldPredicates } from "@/lib/log-filter"
import { dockerSource, fileSource } from "@/lib/log-sources"
import type { DeploymentRequests, RequestEntry } from "@/lib/types"
import { FactDot } from "@/components/metrics/host-identity"
import { ProductGlyph } from "@/components/product-logo"
import { LinesBlock, OutputLines } from "@/components/deploy/output-lines"
import type { Answering } from "@/components/deploy/logs-model"

/** What Caddy names a failure it answered for: a refused dial, a timeout, any other 5xx. */
const CADDY_FAILURES = ["upstream_refused", "upstream_timeout", "error"]

const iso = (ms: number) => new Date(ms).toISOString()

/**
 * What was written while one request was in flight, inside the request.
 *
 * The container's lines from the moment it arrived to a second after it was
 * answered — "approximate", and said so: the ingress records no request id
 * for the application to log, so these are the lines of that stretch of
 * time, which on a busy container are other requests' too. The container is
 * the one that was live then (`containerAt`), not whichever is live now; when
 * that one has been removed, the block says so rather than reading another.
 * An ingress that records no duration (nginx's combined format) leaves only
 * when the answer went out, so the stretch is the second either side of it,
 * and the block's words say that instead of "in flight".
 *
 * A failure also gets what the proxy said about it: Caddy's error line for
 * the same host within two seconds, read off the ingress container's output
 * through the caddy lens, or the site's nginx error file. "dial tcp
 * 172.18.0.5:3000: connect: connection refused" is the sentence a 502 row
 * could not say before; its absence says something too — the application
 * answered the error itself.
 */
export function RequestLines({
  entry,
  window,
  answering,
}: {
  entry: RequestEntry
  window: DeploymentRequests
  answering?: Answering
}) {
  const at = Date.parse(entry.time)
  if (!Number.isFinite(at)) return null
  const failed = entry.status >= 500
  const proxy = failed ? proxyQuery(entry, window, at) : undefined
  const timed = entry.durationMs !== undefined
  return (
    <>
      {proxy && (
        <OutputLines
          title="Proxy said"
          facts={
            <>
              <ProductGlyph id={proxy.product} className="size-3" />
              <span className="truncate">{proxy.from}</span>
            </>
          }
          query={proxy.query}
          empty="The proxy wrote nothing about this request, so the error was the application's own answer."
        />
      )}
      {answering &&
        ("gone" in answering ? (
          <LinesBlock title="Lines from this request">
            <p className="text-hint text-muted-foreground">
              Release #{answering.gone} was live then, and its containers have since been removed —
              what they wrote went with them.
            </p>
          </LinesBlock>
        ) : (
          <OutputLines
            title="Lines from this request"
            facts={
              <span
                className="flex min-w-0 items-center gap-1.5"
                title={
                  timed
                    ? "Every line the container wrote from the moment the request arrived to a second after it was answered. The proxy records no request id, so on a busy container some are other requests'."
                    : "Every line the container wrote in the second either side of the answer: this proxy records no duration, so when the request arrived is not known. Nothing ties a line to a request, so on a busy container some are other requests'."
                }
              >
                <span className="shrink-0">approximate</span>
                <FactDot />
                <span className="truncate font-mono">{answering.container.name}</span>
              </span>
            }
            query={{
              source: dockerSource(answering.container.containerId),
              since: iso(at - (entry.durationMs ?? 0) - 1000),
              until: iso(at + 1000),
              order: "asc",
              limit: 50,
            }}
            empty={
              timed
                ? "The container wrote nothing while this request was in flight."
                : "The container wrote nothing in the second either side of the answer."
            }
          />
        ))}
    </>
  )
}

/** Where the proxy in front of this deployment writes why it failed, asked about one request. */
function proxyQuery(entry: RequestEntry, window: DeploymentRequests, at: number) {
  const around = { since: iso(at - 2000), until: iso(at + 2000), limit: 5 }
  if (window.ingress) {
    return {
      product: "caddy",
      from: "Caddy ingress",
      query: {
        source: dockerSource(window.ingress),
        lens: "caddy",
        f: fieldPredicates({
          event: CADDY_FAILURES,
          ...(entry.host ? { host: [entry.host] } : {}),
        }),
        ...around,
      },
    }
  }
  if (window.errorLog) {
    return {
      product: "nginx",
      from: "the site's nginx error log",
      query: { source: fileSource(window.errorLog), lens: "nginx-error", ...around },
    }
  }
  return undefined
}
