"use client"

import Link from "next/link"
import {
  DesktopDevice,
  Key,
  PhoneDevice,
  ShieldCheck,
  ShieldOff,
  TabletDevice,
  Terminal,
} from "@/components/icons"
import { keyProduct, parseAgent } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import type { ApiToken, DashboardUser, SessionInfo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { activeNow } from "@/components/account/access"
import { UserAvatar } from "@/components/account/user-avatar"

type Size = "lg" | "md" | "sm"

const DEVICE_GLYPH = {
  desktop: DesktopDevice,
  phone: PhoneDevice,
  tablet: TabletDevice,
  program: Terminal,
} as const

/**
 * A session on a wire: the browser's own mark on the picture's tile, with the
 * system it runs on in the corner — the sessions list's `ClientMark` at the
 * size a picture draws — so the same Safari on an iPhone is one drawing in
 * the list and on the wire into it. A client nobody makes a mark for keeps
 * the glyph of the device it said it was.
 */
export function SessionWireMark({ userAgent, size = "md" }: { userAgent: string; size?: Size }) {
  const agent = parseAgent(userAgent)
  const id = agent.product ?? agent.osProduct
  const badge = agent.product && agent.osProduct !== agent.product ? agent.osProduct : undefined
  const Glyph = DEVICE_GLYPH[agent.device]
  return (
    <span className="relative flex">
      <WireMark tone="logo" size={size}>
        {hasProductLogo(id) ? <ProductGlyph id={id} /> : <Glyph aria-hidden />}
      </WireMark>
      {badge && (
        <span
          aria-hidden
          className={cn(
            "absolute -right-1 -bottom-1 flex items-center justify-center rounded-md border border-hairline bg-background",
            size === "sm" ? "size-4" : "size-5",
          )}
        >
          <ProductGlyph id={badge} className={size === "sm" ? "size-2.5" : "size-3"} />
        </span>
      )}
    </span>
  )
}

/** A key on a wire, drawn as what its name says holds it, else the key glyph. */
export function KeyWireMark({
  name,
  size = "md",
  spent,
}: {
  name: string
  size?: Size
  /** Revoked or expired: the mark is greyed, as the row is. */
  spent?: boolean
}) {
  const product = keyProduct(name)
  return (
    <WireMark tone="logo" size={size} className={cn(spent && "opacity-50 grayscale")}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Key aria-hidden />}
    </WireMark>
  )
}

/**
 * An account at the middle of a picture: its face inside the brand's ring —
 * `WireHost`'s ring, because this is where the reader stands — with what
 * guards it in the corner: a green shield while a code is asked for at every
 * sign-in, an amber one while a password alone opens it.
 */
export function AccountWireMark({
  user,
  scope = "self",
}: {
  user: Pick<DashboardUser, "id" | "username" | "displayName" | "avatarVersion" | "totpEnabled">
  scope?: "self" | "admin"
}) {
  const Guard = user.totpEnabled ? ShieldCheck : ShieldOff
  return (
    <span className="relative flex shrink-0 rounded-full border border-rule-brand bg-background p-[3px]">
      <UserAvatar key={user.avatarVersion} user={user} scope={scope} size="lg" />
      <span
        aria-hidden
        className="absolute -right-1 -bottom-1 flex size-6 items-center justify-center rounded-md border border-hairline bg-background"
      >
        <Guard className={cn("size-3.5", user.totpEnabled ? "text-success" : "text-warning")} />
      </span>
    </span>
  )
}

/**
 * The marks a column has no room for, as a count on a tile of the marks'
 * own shape — a link to the page that lists them all.
 */
export function WireMore({
  count,
  href,
  label,
  size = "md",
}: {
  count: number
  href: string
  label: string
  size?: Size
}) {
  return (
    <Link
      href={href}
      aria-label={label}
      className={cn(
        "numeric flex shrink-0 items-center justify-center rounded-xl border border-dashed border-border-strong bg-background text-hint text-muted-foreground focus-ring transition-colors hover:text-foreground",
        size === "lg" && "size-14",
        size === "md" && "size-11",
        size === "sm" && "size-9",
      )}
    >
      +{count}
    </Link>
  )
}

/**
 * A session's eyebrow on a wire: what it is doing, green while it is open —
 * the word over the mark that Configuration's nodes carry ("You", "Proxy"),
 * so every node in a picture reads eyebrow, name, detail.
 */
export function SessionState({ session, now }: { session: SessionInfo; now: number }) {
  const open = session.current || activeNow(session, now)
  return (
    <span className={cn(open && "text-success")}>
      {session.current
        ? "this device"
        : open
          ? "active now"
          : `seen ${relativeTime(session.lastSeenAt)}`}
    </span>
  )
}

/** When a key was last called and when it stops: the line under its name on a wire. */
export function keyHint(token: ApiToken) {
  return [
    token.lastUsedAt ? `used ${relativeTime(token.lastUsedAt)}` : "never used",
    token.expiresAt && `expires ${relativeTime(token.expiresAt)}`,
  ]
    .filter(Boolean)
    .join(" · ")
}
