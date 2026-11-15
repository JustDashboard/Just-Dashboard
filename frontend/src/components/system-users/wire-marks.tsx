"use client"

import { SettingsGear, Shield, ShieldCheck, UserMinus, Users } from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { WireMark } from "@/components/deploy/wire"
import { hasProductLogo, ProductGlyph } from "@/components/product-logo"
import { accountProduct, groupProduct } from "@/components/system-users/marks"
import type { SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"

type Size = "lg" | "md" | "sm"

const FACE = {
  lg: "size-14 rounded-xl",
  md: "size-11 rounded-xl",
  sm: "size-9 rounded-lg",
} as const

/**
 * An account as a mark in a wiring picture, at the picture's own sizes: a
 * person as their face in their hue, a package's account as the product that
 * runs under it, root behind a shield. It stands on a ring of the page's
 * ground, as `WireMark` does, because the line to it is drawn underneath.
 */
export function AccountWireMark({
  user,
  size = "md",
}: {
  user: Pick<SystemUser, "username" | "system" | "uid">
  size?: Size
}) {
  if (!user.system) {
    return (
      <span className="flex shrink-0 rounded-xl bg-background">
        <InitialsMark name={user.username} size="md" className={FACE[size]} />
      </span>
    )
  }
  const product = accountProduct(user)
  const Glyph = user.uid === 0 ? Shield : user.uid === 65534 ? UserMinus : SettingsGear
  return (
    <WireMark tone="logo" size={size}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />}
    </WireMark>
  )
}

/** A group as a mark in a wiring picture: docker and lxd as themselves, the rest a glyph. */
export function GroupWireMark({
  name,
  root,
  size = "md",
  className,
}: {
  name: string
  /** It hands root over, so it stands behind a shield rather than as people. */
  root?: boolean
  size?: Size
  className?: string
}) {
  const product = groupProduct(name)
  const Glyph = root ? ShieldCheck : Users
  return (
    <WireMark tone="logo" size={size} className={cn(className)}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />}
    </WireMark>
  )
}
