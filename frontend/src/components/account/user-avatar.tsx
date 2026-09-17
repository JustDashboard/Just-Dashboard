"use client"

import { useState } from "react"
import { downloadUrl } from "@/lib/api"
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
  const letters =
    words.length >= 2 ? words[0][0] + words[1][0] : (words[0] ?? "?").slice(0, 2)
  return letters.toUpperCase()
}

/**
 * Where an account's picture is served from. The signed-in account reads its
 * own; the users table, which only a system.admin sees, reads anyone's by id.
 * The version rides on the URL so a fresh upload is a fresh fetch and an
 * unchanged one is served from the browser's cache.
 */
export function avatarSrc(user: Pick<DashboardUser, "id" | "avatarVersion">, scope: "self" | "admin") {
  if (!user.avatarVersion) return undefined
  const path = scope === "self" ? "/account/avatar" : `/dashboard-users/${user.id}/avatar`
  return downloadUrl(path, { v: user.avatarVersion })
}

const SIZES = {
  sm: "size-7 rounded-md text-hint",
  md: "size-9 rounded-md text-xs",
  lg: "size-18 rounded-lg text-2xl",
} as const

/**
 * An account's picture, or its initials on the brand plot while it has none.
 *
 * Square with the control radius rather than a circle: there is no pill in
 * this product (§4), and a filled circle holding two letters is one.
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
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center bg-plot-brand font-semibold text-brand select-none",
        SIZES[size],
        className,
      )}
    >
      {initialsOf(name)}
    </span>
  )
}
