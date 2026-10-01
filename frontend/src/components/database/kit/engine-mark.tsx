import { Database } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import type { Engine } from "@/components/database/engine"

/**
 * A database drawn as its engine (§14): the product's own logo on the
 * recessed tile, following the flavour — a MariaDB server is the seal, not
 * the MySQL dolphin its driver is named after.
 *
 * `sm` is a row's mark and the strip's, `md` a card's, and `lg` the identity
 * tile a database's home opens on. An engine with no artwork bundled keeps
 * the database glyph on the same tile, so titles still line up and nothing is
 * a guessed logo.
 */
export function EngineMark({
  engine,
  size = "md",
  className,
}: {
  engine: Engine
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  return (
    <ProductLogo
      id={engine.logo}
      size={size === "sm" ? "sm" : "md"}
      fallback={Database}
      className={cn(size === "lg" && "size-12 rounded-xl [&_img]:size-7", className)}
    />
  )
}

/**
 * The same mark bare, at a line's height: before a name in the rail's panel
 * head, in a palette row, inside a sentence of facts.
 */
export function EngineGlyph({ engine, className }: { engine: Engine; className?: string }) {
  if (engine.logo) return <ProductGlyph id={engine.logo} className={className} />
  return (
    <Database
      aria-hidden="true"
      className={cn("size-3.5 shrink-0 text-muted-foreground", className)}
    />
  )
}
