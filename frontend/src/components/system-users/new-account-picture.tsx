"use client"

import { useRef } from "react"
import { Key, LockClosed, Shield, SettingsGear, Slash } from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"
import { groupGrant } from "@/components/system-users/access"
import { GroupWireMark } from "@/components/system-users/wire-marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import type { SudoLevel, SystemGroup } from "@/lib/types"

type Way = "key" | "github" | "password" | "none"

const WAY: Record<Way, { title: string; hint?: string }> = {
  key: { title: "An SSH key", hint: "the one pasted here" },
  github: { title: "GitHub's keys", hint: "what that account publishes" },
  password: { title: "A password", hint: "shown once, after it is made" },
  none: { title: "Nobody directly", hint: "until it is given a key or a password" },
}

/**
 * What the form is about to make, drawn as it is filled in: the way in, the
 * account, and how far it will reach. The form's own answers say this one
 * field at a time; the picture says the outcome, and it is where an account
 * that could become root with nothing asked, or one nobody can sign in to, is
 * seen before Create is pressed.
 */
export function NewAccountPicture({
  username,
  service,
  way,
  sudo,
  groups,
  hostGroups,
}: {
  username: string
  /** A service's account: nobody signs in as it, and it reaches nothing. */
  service: boolean
  way: Way
  sudo: SudoLevel
  groups: string[]
  hostGroups: SystemGroup[]
}) {
  const container = useRef<HTMLDivElement>(null)
  const inRef = useRef<HTMLDivElement>(null)
  const accountRef = useRef<HTMLDivElement>(null)
  const reachRef = useRef<HTMLDivElement>(null)

  const rootGroups = groups.filter((name) => {
    const group = hostGroups.find((g) => g.name === name)
    return group?.rootEquivalent || group?.sudoRules.some((rule) => rule.full)
  })
  // sudo asks for the account's own password, and one made without a password
  // has nothing to answer with.
  const stranded = sudo === "password" && way !== "password"
  const closed = service || way === "none"
  const reach = service
    ? { tone: "idle" as const, title: "Nothing", hint: "It runs a program and owns its files." }
    : sudo === "nopassword"
      ? { tone: "danger" as const, title: "Root, nothing asked", hint: "its own sudo rule" }
      : sudo === "password" && !stranded
        ? {
            tone: "warning" as const,
            title: "Root, asks for its password",
            hint: "its own sudo rule",
          }
        : rootGroups.length > 0
          ? {
              tone: "warning" as const,
              title: "Root by another name",
              hint: `through ${rootGroups.join(" and ")}`,
            }
          : stranded
            ? {
                tone: "idle" as const,
                title: "Root it cannot use",
                hint: "sudo will ask for a password it lacks",
              }
            : { tone: "idle" as const, title: "No root access", hint: undefined }
  const other = groups.filter((name) => !rootGroups.includes(name))

  return (
    <section aria-label="What it makes" data-slot="new-account-picture" className="min-w-0">
      <h2 className="border-b border-hairline pb-3 text-title font-semibold tracking-tight">
        What it makes
      </h2>
      <div ref={container} className="relative flex flex-col gap-9 pt-5">
        <AnimatedBeam
          containerRef={container}
          fromRef={inRef}
          toRef={accountRef}
          shape="arc"
          curvature={0}
          still
          dashed={closed}
        />
        <AnimatedBeam
          containerRef={container}
          fromRef={accountRef}
          toRef={reachRef}
          shape="arc"
          curvature={0}
          still
          dashed={reach.tone === "idle"}
          tone={reach.tone === "idle" ? "default" : reach.tone}
        />
        <WireNode
          nodeRef={inRef}
          mark={
            closed ? (
              <WirePlaceholder size="md">
                <Slash aria-hidden />
              </WirePlaceholder>
            ) : (
              <WireMark tone={way === "password" ? "neutral" : "logo"} size="md">
                {way === "github" ? (
                  <ProductGlyph id="github" />
                ) : way === "password" ? (
                  <LockClosed aria-hidden />
                ) : (
                  <Key aria-hidden />
                )}
              </WireMark>
            )
          }
          eyebrow="Signs in with"
          title={service ? "Nobody" : WAY[way].title}
          hint={service ? "a service's account has no login" : WAY[way].hint}
        />
        <WireNode
          nodeRef={accountRef}
          mark={
            service ? (
              <WireMark tone="logo" size="md">
                <SettingsGear aria-hidden />
              </WireMark>
            ) : (
              <span className="flex shrink-0 rounded-xl bg-background">
                <InitialsMark name={username || "new"} size="md" className="size-11 rounded-xl" />
              </span>
            )
          }
          eyebrow="The account"
          title={
            <span className="font-mono">
              {username || <span className="text-muted-foreground">not named yet</span>}
            </span>
          }
          hint={
            other.length > 0 ? (
              <span className="flex flex-wrap items-center gap-x-2">
                in
                {other.map((name) => (
                  <span key={name} className="font-mono" title={groupGrant(name)}>
                    {name}
                  </span>
                ))}
              </span>
            ) : undefined
          }
        />
        <WireNode
          nodeRef={reachRef}
          mark={
            reach.tone === "idle" ? (
              <WirePlaceholder size="md">
                <Shield aria-hidden />
              </WirePlaceholder>
            ) : rootGroups.length > 0 && sudo === "none" ? (
              <GroupWireMark name={rootGroups[0]} root size="md" />
            ) : (
              <WireMark tone={reach.tone} size="md">
                <Shield aria-hidden />
              </WireMark>
            )
          }
          eyebrow="Reaches"
          title={reach.title}
          hint={reach.hint}
        />
      </div>
    </section>
  )
}
