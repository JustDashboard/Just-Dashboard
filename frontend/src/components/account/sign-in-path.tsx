"use client"

import { useRef } from "react"
import { Fingerprint, LockClosed, ShieldCheck } from "@/components/icons"
import { parseAgent } from "@/lib/clients"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import type { DashboardUser, SessionInfo } from "@/lib/types"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyphs } from "@/components/product-logo"
import { AccountWireMark } from "@/components/account/marks"
import { displayNameOf } from "@/components/account/user-avatar"

/**
 * What a sign-in passes on its way to this server, drawn as the path a
 * request takes on Configuration: this account, its password, the second
 * factor, the server and the sessions it holds.
 *
 * The page is opened with one question — am I protected? — and the picture
 * answers it before the forms below are read. Enrolled, the wire runs
 * through the authenticator and pulses all the way in. Not enrolled, the
 * second factor is a dashed ring where the authenticator would stand, the
 * wires through it are dashed, and the line that actually carries a sign-in
 * runs from the password straight to the server, in amber: a password alone
 * is one stolen credential away from root on this machine.
 */
export function SignInPath({
  user,
  required,
  sessions,
}: {
  user: DashboardUser
  required: boolean
  sessions?: SessionInfo[]
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const you = useRef<HTMLDivElement>(null)
  const password = useRef<HTMLDivElement>(null)
  const factor = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const enrolled = user.totpEnabled
  const clients = [
    ...new Set(
      (sessions ?? []).map((s) => parseAgent(s.userAgent).product).filter((id) => id !== undefined),
    ),
  ]
  const count = sessions?.length

  return (
    <div className="relative min-w-0 py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className={cn("relative min-w-0", !enrolled && "lg:pt-12")}>
        <AnimatedBeam containerRef={container} fromRef={you} toRef={password} duration={2.4} />
        <AnimatedBeam
          containerRef={container}
          fromRef={password}
          toRef={factor}
          still={!enrolled}
          dashed={!enrolled}
          tone={!enrolled && !wide ? "warning" : "default"}
          duration={2.4}
          delay={0.45}
        />
        <AnimatedBeam
          containerRef={container}
          fromRef={factor}
          toRef={server}
          still={!enrolled}
          dashed={!enrolled}
          tone={!enrolled && !wide ? "warning" : "default"}
          duration={2.4}
          delay={0.9}
        />
        {/* A column has no room to bow a line round the ring, so on a phone
            the dashed wires through it carry the amber instead. */}
        {!enrolled && wide && (
          // The way a sign-in actually goes without a second factor, bowed
          // over the ring it skips.
          <AnimatedBeam
            containerRef={container}
            fromRef={password}
            toRef={server}
            curvature={110}
            tone="warning"
            duration={2.4}
            delay={0.45}
          />
        )}

        <ol
          aria-label="What a sign-in passes"
          className="grid min-w-0 gap-y-7 lg:grid-cols-4 lg:items-center lg:gap-x-6 lg:pb-16"
        >
          <li className="min-w-0">
            <WireNode
              nodeRef={you}
              align="center"
              mark={<AccountWireMark user={user} />}
              eyebrow="You"
              title={displayNameOf(user)}
              hint={<span className="font-mono">@{user.username}</span>}
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={password}
              align="center"
              mark={
                <WireMark tone="logo">
                  <LockClosed aria-hidden className="text-[var(--tag-blue)]" />
                </WireMark>
              }
              eyebrow="First factor"
              title="Password"
              hint="12 characters, three kinds"
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={factor}
              align="center"
              mark={
                enrolled ? (
                  <WireMark tone="success">
                    <Fingerprint aria-hidden />
                  </WireMark>
                ) : (
                  <WirePlaceholder fallback={Fingerprint} />
                )
              }
              eyebrow="Second factor"
              title={enrolled ? "Authenticator code" : "Not enrolled"}
              hint={
                enrolled ? (
                  <span className="inline-flex items-center gap-1 text-success">
                    <ShieldCheck aria-hidden className="size-3.5" />
                    asked at every sign-in
                  </span>
                ) : (
                  <span className="text-warning">
                    {required ? "required on this install" : "a password alone gets in"}
                  </span>
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={server}
              align="center"
              mark={<WireHost />}
              eyebrow="This server"
              title={
                count === undefined
                  ? "Signed in"
                  : `${count} ${count === 1 ? "session" : "sessions"}`
              }
              hint={
                clients.length > 0 ? (
                  <span className="inline-flex items-center gap-1.5">
                    <ProductGlyphs ids={clients} />
                  </span>
                ) : undefined
              }
            />
          </li>
        </ol>
      </div>
    </div>
  )
}
