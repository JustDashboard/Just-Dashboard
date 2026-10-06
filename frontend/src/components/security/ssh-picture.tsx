"use client"

import { useRef } from "react"
import {
  Globe,
  Key,
  LockClosed,
  LockOpen,
  SecureConnection,
  UserSettings,
} from "@/components/icons"
import { plural } from "@/lib/format"
import type { SSHDConfig } from "@/lib/types"
import { cn } from "@/lib/utils"
import { InitialsMark } from "@/components/account/user-avatar"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { AnimatedBeam } from "@/components/ui/animated-beam"

/** More accounts than this and the rest are a count beside the faces. */
const FACES = 4

/**
 * How a login gets in, drawn as the path it takes: from the internet, to sshd
 * on its port, and through whichever of the three doors sshd leaves open —
 * a key, a password, or root's own — with the accounts that hold a key as
 * their faces on the first.
 *
 * It replaced four tiles — the port, passwords, root login and the keyed
 * accounts — which were the same four facts as figures, read one at a time.
 * Drawn as doors they are read as one answer to "how would somebody get a
 * shell here?", which is the question the settings under it change. What the
 * picture draws is the draft, not the saved file: a pending change to
 * password authentication closes its door here before Test and apply is
 * pressed, so the reader sees what the change does while making it.
 *
 * The line is the state, as on every wiring picture: moving where logins
 * are arriving (the auth log's last day had any), amber into a door that
 * works but should not be relied on, red into root by password, and dashed
 * into a door sshd keeps shut.
 */
export function SSHPicture({
  config,
  value,
  traffic,
}: {
  config: SSHDConfig
  /** A directive's value as the page holds it — the draft over the saved one. */
  value: (key: string) => string | undefined
  /** The auth log's last day: logins and failed attempts, while it is known. */
  traffic?: { accepted?: number; failed?: number; attackers?: number }
}) {
  const container = useRef<HTMLDivElement>(null)
  const internet = useRef<HTMLDivElement>(null)
  const sshd = useRef<HTMLDivElement>(null)
  const keys = useRef<HTMLDivElement>(null)
  const passwords = useRef<HTMLDivElement>(null)
  const root = useRef<HTMLDivElement>(null)

  const keysOn = (value("pubkeyauthentication") ?? "yes") !== "no"
  const passwordsOn = (value("passwordauthentication") ?? "yes") !== "no"
  const rootLogin = value("permitrootlogin") ?? "prohibit-password"
  const ports = config.ports.length > 0 ? config.ports : ["22"]
  const keyCount = config.keyedAccounts.reduce((n, a) => n + a.keys, 0)
  const arriving = (traffic?.accepted ?? 0) + (traffic?.failed ?? 0) > 0

  return (
    <SettingPicture
      label="How a login reaches a shell"
      containerRef={container}
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={internet}
            toRef={sshd}
            still={!arriving}
            duration={2.4}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={sshd}
            toRef={keys}
            shape="s"
            still={!keysOn || config.keyedAccounts.length === 0}
            dashed={!keysOn}
            duration={2.4}
            delay={0.6}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={sshd}
            toRef={passwords}
            shape="s"
            still
            dashed={!passwordsOn}
            tone={passwordsOn ? "warning" : "default"}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={sshd}
            toRef={root}
            shape="s"
            still
            dashed={rootLogin === "no"}
            tone={rootLogin === "yes" ? "danger" : "default"}
          />
        </>
      }
      start={[
        <WireNode
          key="internet"
          nodeRef={internet}
          align="end"
          mark={
            <WireMark tone="logo" shape="square">
              <Globe aria-hidden />
            </WireMark>
          }
          eyebrow="Who knocks"
          title="The internet"
          hint={
            traffic?.failed !== undefined ? (
              <span className={cn("numeric", traffic.failed > 0 && "text-warning")}>
                {plural(traffic.failed, "failed attempt")} today
                {traffic.attackers ? ` · ${plural(traffic.attackers, "address", "addresses")}` : ""}
              </span>
            ) : (
              "every address that reaches the port"
            )
          }
        />,
      ]}
      middle={
        <WireNode
          nodeRef={sshd}
          align="center"
          mark={
            <WireMark tone="logo" shape="square">
              <SecureConnection aria-hidden />
            </WireMark>
          }
          eyebrow="Listener"
          title={
            <span className="numeric">
              sshd{" "}
              <span className="font-mono font-normal">
                {ports.map((port) => (
                  <span key={port}>
                    <span className="text-muted-foreground">:</span>
                    <span className="text-[var(--tag-pink)]">{port}</span>{" "}
                  </span>
                ))}
              </span>
            </span>
          }
          hint={
            ports.length === 1 && ports[0] === "22"
              ? "the port every scanner tries first"
              : "off the default port"
          }
        />
      }
      end={
        <ul className="flex min-w-0 flex-col gap-4">
          <li className="min-w-0">
            <WireNode
              nodeRef={keys}
              mark={
                <WireMark tone={keysOn ? "logo" : "neutral"} shape="square" size="md">
                  <Key aria-hidden />
                </WireMark>
              }
              title="Public keys"
              hint={
                !keysOn ? (
                  "refused"
                ) : config.keyedAccounts.length === 0 ? (
                  <span className="text-warning">no account holds one</span>
                ) : (
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="flex -space-x-1">
                      {config.keyedAccounts.slice(0, FACES).map((account) => (
                        <InitialsMark
                          key={account.user}
                          name={account.user}
                          className="ring-2 ring-background"
                        />
                      ))}
                    </span>
                    <span className="numeric truncate">
                      {config.keyedAccounts.length > FACES &&
                        `+${config.keyedAccounts.length - FACES} · `}
                      {plural(keyCount, "key")}
                    </span>
                  </span>
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={passwords}
              mark={
                <WireMark tone={passwordsOn ? "warning" : "neutral"} shape="square" size="md">
                  {passwordsOn ? <LockOpen aria-hidden /> : <LockClosed aria-hidden />}
                </WireMark>
              }
              title="Passwords"
              hint={
                passwordsOn ? (
                  <span className="text-warning">accepted — a guessed one is a shell</span>
                ) : (
                  "refused"
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={root}
              mark={
                <WireMark
                  tone={rootLogin === "yes" ? "danger" : rootLogin === "no" ? "neutral" : "logo"}
                  shape="square"
                  size="md"
                >
                  <UserSettings aria-hidden />
                </WireMark>
              }
              title="Root"
              hint={
                <span className={cn(rootLogin === "yes" && "text-destructive")}>
                  {ROOT_WORDS[rootLogin] ?? rootLogin}
                </span>
              }
            />
          </li>
        </ul>
      }
    />
  )
}

const ROOT_WORDS: Record<string, string> = {
  yes: "may sign in with a password",
  "prohibit-password": "keys only",
  "without-password": "keys only",
  "forced-commands-only": "forced commands only",
  no: "refused",
}
