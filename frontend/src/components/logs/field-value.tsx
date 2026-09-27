"use client"

import { cn } from "@/lib/utils"
import { bytes } from "@/lib/format"
import { networkOf } from "@/lib/clients"
import { fieldOf, type LogField } from "@/lib/log-fields"
import { eventMeta } from "@/lib/log-lenses"
import { LEVEL_LABEL, type LogLevel } from "@/lib/log-filter"
import { CLASS_TEXT, latency, latencyTone, type StatusClass } from "@/lib/requests"
import { EVENT_WORD, LEVEL_WORD, laneStyle } from "@/components/logs/log-text"
import { Address, MethodWord, RequestPath, StatusCode } from "@/components/deploy/request-marks"
import { NETWORK_GLYPH } from "@/components/client-mark"
import {
  ProductGlyph,
  imageProduct,
  portProduct,
  processProduct,
  programProduct,
  unitProduct,
} from "@/components/product-logo"
import { jailProduct } from "@/components/security/marks"
import { packageProduct } from "@/components/packages/marks"

const PRODUCT: Record<NonNullable<LogField["product"]>, (value: string) => string | undefined> = {
  unit: unitProduct,
  process: (value) => processProduct(value) ?? programProduct(value),
  jail: jailProduct,
  package: packageProduct,
  image: imageProduct,
  port: (value) => (/^\d+$/.test(value) ? portProduct(Number(value)) : undefined),
}

/**
 * One value a lens read out of a line, drawn as what it is.
 *
 * Not a new vocabulary: an address is the request log's address, a status its
 * code in its family's colour, a method its word, a duration its latency with
 * its tone, a name its lane's hue — so a Postgres client and a request's
 * client are one drawing (§14). `compact` is a console column, where a glyph
 * per row would be a column of glyphs; the detail, the facet popover and the
 * Insights lists draw the product or the network beside the value.
 *
 * `plain` is the console's Colour switch turned off: a failure and a slow
 * answer keep their colour, because those are readings of state (§3).
 *
 * `glyph={false}` is a list with a slot of its own for the mark (a `BarList`
 * row's), which `FieldMark` fills so the names still start on one line.
 */
export function FieldValue({
  name,
  value,
  compact,
  plain,
  glyph = true,
  lens,
  className,
}: {
  name: string
  value: string
  compact?: boolean
  plain?: boolean
  glyph?: boolean
  /** The lens an `event` value is named by. */
  lens?: string
  className?: string
}) {
  const field = fieldOf(name)
  const frame = cn("min-w-0 truncate", className)

  if (name === "event") {
    const meta = eventMeta(lens, value)
    return (
      <span
        title={value}
        className={cn(frame, plain ? undefined : EVENT_WORD[meta?.tone ?? "default"])}
      >
        {meta?.label ?? value.replace(/_/g, " ")}
      </span>
    )
  }
  if (name === "level") {
    const level = (value || "unknown") as LogLevel
    return (
      <span className={cn(frame, plain ? undefined : LEVEL_WORD[level])}>
        {LEVEL_LABEL[level] ?? value}
      </span>
    )
  }
  if (name === "class") {
    return (
      <span className={cn(frame, "numeric font-mono", !plain && CLASS_TEXT[value as StatusClass])}>
        {value}
      </span>
    )
  }

  switch (field.kind) {
    case "address": {
      if (compact || !glyph) return <Address ip={value} plain={plain} className={className} />
      const network = networkOf(value)
      const Place = NETWORK_GLYPH[network.kind]
      return (
        <span title={network.label} className={cn("flex min-w-0 items-center gap-1.5", className)}>
          {network.product ? (
            <ProductGlyph id={network.product} />
          ) : (
            <Place aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <Address ip={value} plain={plain} />
        </span>
      )
    }
    case "status": {
      const status = Number(value)
      return Number.isInteger(status) ? (
        <StatusCode status={status} plain={plain} word={!compact} className={className} />
      ) : (
        <span className={frame}>{value}</span>
      )
    }
    case "method":
      return <MethodWord method={value} plain={plain} className={className} />
    case "path": {
      const at = value.indexOf("?")
      return (
        <RequestPath
          path={at < 0 ? value : value.slice(0, at)}
          query={at < 0 ? undefined : value.slice(at + 1)}
          plain={plain}
          className={className}
        />
      )
    }
    case "duration": {
      const ms = Number(value)
      if (!Number.isFinite(ms)) return <span className={frame}>{value}</span>
      return (
        <span
          title={`${value} ms`}
          className={cn(
            frame,
            "numeric",
            latencyTone(ms) === "warning" ? "font-medium text-warning" : "text-muted-foreground",
          )}
        >
          {latency(ms)}
        </span>
      )
    }
    case "bytes": {
      const n = Number(value)
      return (
        <span title={value} className={cn(frame, "numeric")}>
          {Number.isFinite(n) && value.trim() !== "" ? bytes(n) : value}
        </span>
      )
    }
    case "lane":
      return (
        <span
          title={value}
          className={cn(frame, "font-medium", plain && "text-muted-foreground")}
          style={plain ? undefined : laneStyle(value)}
        >
          {value}
        </span>
      )
    case "product": {
      const product = field.product ? PRODUCT[field.product](value) : undefined
      if (compact || !product || !glyph) {
        return (
          <span
            title={value}
            className={cn(frame, compact && "font-medium")}
            style={compact && !plain ? laneStyle(value) : undefined}
          >
            {value}
          </span>
        )
      }
      return (
        <span title={value} className={cn("flex min-w-0 items-center gap-1.5", className)}>
          <ProductGlyph id={product} />
          <span className="truncate">{value}</span>
        </span>
      )
    }
    case "query":
      return (
        <span title={value} className={cn(frame, "font-mono")}>
          {value}
        </span>
      )
    case "code":
      return (
        <span title={value} className={cn(frame, "numeric font-mono text-muted-foreground")}>
          {value}
        </span>
      )
    default:
      return (
        <span title={value} className={frame}>
          {value}
        </span>
      )
  }
}

/**
 * What a value is, as a mark before it: an address's network, a unit's or a
 * package's product. Nothing for a value that is no place and no product.
 */
export function FieldMark({ name, value }: { name: string; value: string }) {
  const field = fieldOf(name)
  if (field.kind === "address") {
    const network = networkOf(value)
    const Place = NETWORK_GLYPH[network.kind]
    return network.product ? (
      <ProductGlyph id={network.product} />
    ) : (
      <Place aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
    )
  }
  const product = field.kind === "product" && field.product && PRODUCT[field.product](value)
  return product ? <ProductGlyph id={product} /> : null
}
