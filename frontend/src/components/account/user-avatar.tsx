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
  xs: "size-4 rounded-sm",
  sm: "size-7 rounded-md",
  md: "size-9 rounded-md",
  lg: "size-12 rounded-xl",
  xl: "size-18 rounded-xl",
} as const

/**
 * An account's picture, or a head-and-shoulders silhouette while it has none.
 *
 * The silhouette takes a hue by the username, as a commit's author and a run's
 * actor take one by theirs: a list of eight people in eight brand-blue faces
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

  if (src && broken !== src) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        data-slot="user-avatar"
        src={src}
        alt=""
        onError={() => setBroken(src)}
        className={cn("shrink-0 object-cover select-none", SIZES[size], "rounded-full", className)}
      />
    )
  }
  return (
    <Silhouette
      slot="user-avatar"
      hue={hueFor(user.username.toLowerCase(), LANES)}
      size={size}
      className={cn("rounded-full", className)}
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
  slot,
  className,
}: {
  name: string
  size?: keyof typeof SIZES
  /** `user-avatar` where the name is an account signed in here, drawn round. */
  slot?: "user-avatar"
  className?: string
}) {
  return (
    <Silhouette
      slot={slot}
      hue={hueFor(name.toLowerCase(), LANES)}
      size={size}
      className={className}
    />
  )
}

/**
 * The face of a person with no picture: a head and shoulders cut off by the
 * tile's edge, in a solid hue. It was two letters in a 20% wash of the same
 * hue, which read as a dim tint beside a real photograph and said nothing of
 * the name that every row carries beside it already.
 */
function Silhouette({
  slot = "initials-mark",
  hue,
  size,
  className,
}: {
  slot?: "user-avatar" | "initials-mark"
  hue: string
  size: keyof typeof SIZES
  className?: string
}) {
  return (
    <span
      data-slot={slot}
      aria-hidden
      className={cn(
        "flex shrink-0 items-end justify-center overflow-hidden select-none",
        SIZES[size],
        className,
      )}
      style={{ background: hue }}
    >
      <svg viewBox="0 0 32 32" fill="currentColor" className="size-full text-white/90">
        <circle cx="16" cy="12.5" r="6" />
        <path d="M3.5 32c0-7.2 5.6-11.5 12.5-11.5S28.5 24.8 28.5 32z" />
      </svg>
    </span>
  )
}

/** Who a reading counts, drawn as them: a few faces in a row, then how many more. */
export function AvatarRun({ users, max = 4 }: { users: DashboardUser[]; max?: number }) {
  if (users.length === 0) return null
  return (
    <span aria-hidden className="flex shrink-0 items-center gap-0.5">
      {users.slice(0, max).map((user) => (
        <UserAvatar key={user.id} user={user} scope="admin" size="xs" />
      ))}
      {users.length > max && (
        <span className="numeric ml-0.5 text-micro text-muted-foreground">
          +{users.length - max}
        </span>
      )}
    </span>
  )
}
