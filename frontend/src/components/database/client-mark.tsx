"use client"

import { Connection } from "@/components/icons"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"

/**
 * A database client drawn as the program it is (`clientProduct`): Grafana as
 * Grafana, `pg_dump` as the elephant, `shop-api` as the Node.js its
 * deployment runs. The dashboard's own connection is the J, because it is the
 * dashboard; a client whose name says nothing keeps a connection's glyph on
 * the same tile, so its name lines up with the rest.
 *
 * `tile` is the row's 32px square, the account sessions' size; `wire` is the
 * picture's mark, which a line can meet.
 */
export function DbClientMark({
  product,
  self,
  size = "tile",
  tone,
}: {
  product?: string
  self?: boolean
  size?: "tile" | "md" | "sm"
  /** A wire mark's state: what is wrong with the client, over its logo. */
  tone?: "warning" | "danger"
}) {
  if (size === "tile") {
    if (self) {
      return (
        <span
          aria-hidden
          className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-brand"
        >
          <LogoGlyph className="size-4" />
        </span>
      )
    }
    return <ProductLogo id={product} size="sm" fallback={Connection} />
  }
  const known = hasProductLogo(product) ? product : undefined
  return (
    <WireMark
      tone={tone ?? (self ? "brand" : known ? "logo" : "neutral")}
      size={size}
      shape="square"
    >
      {self ? (
        <LogoGlyph />
      ) : known ? (
        <ProductGlyph id={known} />
      ) : (
        <Connection aria-hidden className="text-muted-foreground" />
      )}
    </WireMark>
  )
}
