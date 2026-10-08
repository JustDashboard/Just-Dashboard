"use client"

import Link from "next/link"
import { Box, Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { ContainerDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { rowReveal } from "@/components/icon-action"
import { ProductLogo, containerProduct } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { useNow } from "@/components/deploy/vocabulary"
import {
  restartWords,
  sinceWords,
  splitImage,
  upWords,
  type Verdict,
} from "@/components/docker/container"

/**
 * The container as the thing it runs, with its state in the tile's corner,
 * the way the runtime map and the event feed draw a container.
 */
export function ContainerMark({
  detail,
  verdict,
  size = "lg",
}: {
  detail: Pick<ContainerDetail, "image" | "labels">
  verdict: Verdict
  size?: "lg" | "md"
}) {
  return (
    <span className="relative flex shrink-0">
      <ProductLogo
        id={containerProduct(detail)}
        fallback={Box}
        className={size === "lg" ? "size-12 rounded-xl [&_img]:size-7" : undefined}
      />
      <span
        aria-hidden
        className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background"
      >
        <StatusDot tone={verdict.tone} live={verdict.live} />
      </span>
    </span>
  )
}

/**
 * The line a container's page opens on, the one every destination in the
 * product opens on (`HostIdentity`): the product it runs as its mark, its name,
 * and after it the facts a reader opens a container to check — which image,
 * which project, how long it has been up (ticking), whether it comes back,
 * and the handle to quote. The verdict stands at the line's right end.
 *
 * It replaces a strip of four grey label-and-value pairs that said the name a
 * second time and nothing that moved.
 */
export function ContainerIdentity({
  detail,
  verdict,
}: {
  detail: ContainerDetail
  verdict: Verdict
}) {
  const running = detail.state === "running"
  const now = useNow(1000, running)
  const up =
    running || detail.state === "paused"
      ? upWords(detail.startedAt, now)
      : sinceWords(detail.state, detail.status)
  const stack = detail.composeStack
  const [repository, tag] = splitImage(detail.image)

  return (
    <HostIdentity
      // The views strip under it draws the rule, so the line draws none of its own.
      className="animate-rise border-b-0 pb-0"
      logo={<ContainerMark detail={detail} verdict={verdict} />}
      title={detail.name}
      facts={
        <>
          <span className="inline-flex min-w-0 items-baseline font-mono" title={detail.image}>
            <span className="truncate">{repository}</span>
            {tag && <span className="shrink-0 text-foreground">:{tag}</span>}
          </span>
          {stack && (
            <>
              <FactDot />
              <Link
                href={`/docker/stacks/${encodeURIComponent(stack)}`}
                className="inline-flex min-w-0 items-center gap-1.5 rounded-sm focus-ring hover:text-foreground hover:underline"
              >
                <span
                  aria-hidden
                  className="h-2.5 w-0.5 shrink-0 rounded-full"
                  style={{ background: hueFor(stack, LANES) }}
                />
                <span className="truncate">
                  {stack}
                  {detail.composeService && (
                    <span className="text-muted-foreground/70"> · {detail.composeService}</span>
                  )}
                </span>
              </Link>
            </>
          )}
          {up && (
            <>
              <FactDot />
              <span className={cn("numeric", running && "text-foreground")}>{up}</span>
            </>
          )}
          <FactDot />
          <span>
            {restartWords(detail.restartPolicy)}
            {detail.restartCount > 0 && (
              <span className={cn(detail.restartCount >= 5 && "text-warning")}>
                {" "}
                · {plural(detail.restartCount, "restart")}
              </span>
            )}
          </span>
          <FactDot />
          <button
            type="button"
            onClick={() => void copyText(detail.id, "Container id copied")}
            className="group inline-flex items-center gap-1 rounded-sm font-mono focus-ring hover:text-foreground"
            aria-label={`Copy container id ${detail.id.slice(0, 12)}`}
          >
            {detail.id.slice(0, 12)}
            <Copy aria-hidden className={cn("size-3", rowReveal())} />
          </button>
        </>
      }
      aside={
        <span className="flex flex-col items-end gap-0.5 max-sm:items-start">
          <Status
            tone={verdict.tone}
            live={verdict.live}
            label={verdict.word}
            className="text-body"
          />
          {verdict.detail && (
            <span className="text-hint text-muted-foreground">{verdict.detail}</span>
          )}
        </span>
      }
    />
  )
}
