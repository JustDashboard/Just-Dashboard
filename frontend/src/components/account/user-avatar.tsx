"use client"

import { useState } from "react"
import { downloadUrl } from "@/lib/api"
import { LANES, hueFor } from "@/lib/hue"
import type { DashboardUser } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The name an account shows as. The field arrived in 0.6.8; a payload from a
 * mocked or older API without it falls back to the sign-in name, which is what
 * the product showed before there was anything else to show.
 */
export function displayNameOf(user: Pick<DashboardUser, "username" | "displayName">) {
  return user.displayName || user.username
}

/** The one or two letters that stand in for a picture: "Ion Moisei" → IM. */
export function initialsOf(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean)
  const letters = words.length >= 2 ? words[0][0] + words[1][0] : (words[0] ?? "?").slice(0, 2)
  return letters.toUpperCase()
}

/**
 * Where an account's picture is served from. The signed-in account reads its
 * own; the users table, which only a system.admin sees, reads anyone's by id.
 * The version rides on the URL so a fresh upload is a fresh fetch and an
 * unchanged one is served from the browser's cache.
 */
export function avatarSrc(
  user: Pick<DashboardUser, "id" | "avatarVersion">,
  scope: "self" | "admin",
) {
  if (!user.avatarVersion) return undefined
  const path = scope === "self" ? "/account/avatar" : `/dashboard-users/${user.id}/avatar`
  return downloadUrl(path, { v: user.avatarVersion })
}

const SIZES = {
  xs: "size-4 rounded-sm text-micro",
  sm: "size-7 rounded-md text-hint",
  md: "size-9 rounded-md text-xs",
  lg: "size-12 rounded-xl text-base",
  xl: "size-18 rounded-xl text-2xl",
} as const

/**
 * An account's picture, or its initials while it has none.
 *
 * Square with the control radius rather than a circle: there is no pill in
 * this product (§4), and a filled circle holding two letters is one.
 *
 * The initials take a hue by the username, as a commit's author and a run's
 * actor take one by theirs: a list of eight people in eight brand-blue squares
 * was a texture the eye read past, and the same person now keeps the same colour
 * in the rail, the users list and their own profile. The hues are `LANES`,
 * without red and amber, because these sit beside readings that use those to
 * say something failed.
 */
export function UserAvatar({
  user,
  scope = "self",
  size = "sm",
  className,
}: {
  user: Pick<DashboardUser, "id" | "username" | "displayName" | "avatarVersion">
  scope?: "self" | "admin"
  size?: keyof typeof SIZES
  className?: string
}) {
  const src = avatarSrc(user, scope)
  const [broken, setBroken] = useState<string | undefined>(undefined)
  const name = displayNameOf(user)

  if (src && broken !== src) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={src}
        alt=""
        onError={() => setBroken(src)}
        className={cn("shrink-0 object-cover select-none", SIZES[size], className)}
      />
    )
  }
  return (
    <Initials
      name={name}
      hue={hueFor(user.username.toLowerCase(), LANES)}
      size={size}
      className={className}
    />
  )
}

/**
 * A person drawn from a name alone: a run's actor, a commit's author, a
 * variable's author, a game's player — places that hold a username rather than
 * an account. It is `UserAvatar`'s face without a picture, hued by the same
 * name, so the person keeps the colour they have in the rail and the users
 * list. The name is hued without its case, because a forge writes "Mira"
 * where the dashboard account is "mira", and they are one person.
 */
export function InitialsMark({
  name,
  size = "sm",
  className,
}: {
  name: string
  size?: keyof typeof SIZES
  className?: string
}) {
  return (
    <Initials
      name={name}
      hue={hueFor(name.toLowerCase(), LANES)}
      size={size}
      className={className}
    />
  )
}

function Initials({
  name,
  hue,
  size,
  className,
}: {
  name: string
  hue: string
  size: keyof typeof SIZES
  className?: string
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center font-semibold select-none",
        SIZES[size],
        className,
      )}
      style={{ color: hue, background: `color-mix(in oklab, ${hue} 20%, transparent)` }}
    >
      {size === "xs" ? initialsOf(name)[0] : initialsOf(name)}
    </span>
  )
}
