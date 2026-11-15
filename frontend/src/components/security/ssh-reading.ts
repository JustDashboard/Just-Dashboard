import type { SSHDConfig } from "@/lib/types"
import type { Tone } from "@/components/tone"

/**
 * The SSH server in one line, for sshd's node in the Security overview's
 * picture: the riskiest fact about how a login reaches a shell, and where sshd
 * listens.
 *
 * The overview said "Configuration" there, a label where everything beside it
 * is a reading, on the door that matters most. The SSH page already reads the two
 * directives that answer "is this server safe": whether a password alone gets
 * in, and whether root's does. Root signing in with a password is the red one;
 * passwords at all are amber; keys only is the expected answer and is plain.
 * A directive the server does not send is sshd's own default, as the page's
 * picture of the doors reads it.
 */
export function sshReading(
  config: SSHDConfig | undefined,
): { value: string; tone: Tone; hint: string } | undefined {
  if (!config) return undefined
  if (!config.available)
    return {
      value: "No SSH server",
      tone: "default",
      hint: config.error ?? "neither sshd nor its configuration was found",
    }
  const value = (key: string) => config.settings.find((setting) => setting.key === key)?.value
  const passwords = (value("passwordauthentication") ?? "yes") !== "no"
  const rootByPassword = passwords && (value("permitrootlogin") ?? "prohibit-password") === "yes"
  const ports = config.ports.length > 0 ? config.ports : ["22"]
  const hint = `${ports.length === 1 ? "port" : "ports"} ${ports.join(", ")}`
  if (rootByPassword) return { value: "Root can sign in", tone: "danger", hint }
  if (passwords) return { value: "Passwords allowed", tone: "warning", hint }
  return { value: "Keys only", tone: "default", hint }
}
