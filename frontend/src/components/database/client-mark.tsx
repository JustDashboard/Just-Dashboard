"use client"

import { Connection } from "@/components/icons"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { ProjectFavicon, ProjectMarkOf } from "@/components/deploy/project-mark"

/**
 * A database client drawn as the program it is (`clientProduct`): Grafana as
 * Grafana, `pg_dump` as the elephant, `shop-api` as the Node.js its
 * deployment runs. The dashboard's own connection is the J, because it is the
 * dashboard; a client whose name says nothing keeps a connection's glyph on
 * the same tile, so its name lines up with the rest. A client that is one of
 * this server's deployments is drawn as the project, its website's icon first.
 *
 * `tile` is the row's 32px square, the account sessions' size; `wire` is the
 * picture's mark, which a line can meet.
 */
export function DbClientMark({
  product,
  projectId,
  self,
  size = "tile",
  tone,
}: {
  product?: string
  /** The deployment the client is, drawn by its website's icon where it has one. */
  projectId?: number
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
    return (
      <ProjectMarkOf
        projectId={projectId}
        size="sm"
        fallback={<ProductLogo id={product} size="sm" fallback={Connection} />}
      />
    )
  }
  const known = hasProductLogo(product) ? product : undefined
  return (
    <WireMark
      tone={tone ?? (self ? "brand" : known || projectId ? "logo" : "neutral")}
      size={size}
      shape="square"
    >
      {self ? (
        <LogoGlyph />
      ) : (
        <ProjectFavicon
          projectId={projectId}
          fallback={
            known ? (
              <ProductGlyph id={known} />
            ) : (
              <Connection aria-hidden className="text-muted-foreground" />
            )
          }
        />
      )}
    </WireMark>
  )
}
