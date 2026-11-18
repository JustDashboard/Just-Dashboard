"use client"

import { Database } from "@/components/icons"
import { ProductCard } from "@/components/choice-card"
import { kindWord, type EngineKind } from "@/components/database/engine"
import { Tag } from "@/components/tag"

/**
 * One database engine, as every picker of one draws it: the engine's own logo,
 * its name, the kind of store it is, and what exactly would run.
 *
 * Three pickers drew this — the deployment's quick setup, New database and the
 * connection dialog — and they had already come apart: one with a generic
 * database glyph, two with none, three different second lines. §4's rule about
 * a component whose name already exists applies to a shape too, which is why
 * it is now a `ProductCard` with the kind between the name and the image.
 */
export function EngineCard({
  engine,
  label,
  kind,
  detail,
  selected,
  disabled,
  working,
  onClick,
}: {
  /** The engine or driver key the logo is looked up by. */
  engine: string
  label: string
  /** What kind of store it is, from the engine registry. */
  kind?: EngineKind
  /** A literal under the name: the image that would run. */
  detail?: string
  selected?: boolean
  disabled?: boolean
  /** The engine is being started — the quick setup's provisioning. */
  working?: boolean
  onClick?: () => void
}) {
  return (
    <ProductCard
      product={engine}
      label={label}
      detail={detail}
      mono
      fallback={Database}
      selected={selected}
      disabled={disabled}
      working={working}
      onClick={onClick}
    >
      {kind && <Tag>{kindWord(kind)}</Tag>}
    </ProductCard>
  )
}
