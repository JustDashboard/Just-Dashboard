import type { StreamAccess, StreamSpec } from "@/lib/types"

/**
 * The private ranges a restricted preset starts from. A stream has no
 * authentication of its own, so a database or a shell forwarded from a preset
 * reaches only the local network until someone widens it on purpose.
 */
export const PRIVATE_NETWORKS = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]

export type StreamPreset = {
  id: string
  label: string
  /** The port the service answers on behind the stream. */
  port: number
  /**
   * The port the stream listens on, where it cannot be the service's own:
   * 22 is the host's own sshd.
   */
  listen?: number
  protocol: StreamSpec["protocol"]
  udpMode?: StreamSpec["udpMode"]
  /** Whether it starts limited to private networks: true for anything with no login worth trusting. */
  restricted: boolean
  /** One line on why the preset is set up the way it is. */
  hint: string
}

export const STREAM_PRESETS: StreamPreset[] = [
  {
    id: "postgresql",
    label: "PostgreSQL",
    port: 5432,
    protocol: "tcp",
    restricted: true,
    hint: "A database port; private networks only until you widen it.",
  },
  {
    id: "mysql",
    label: "MySQL / MariaDB",
    port: 3306,
    protocol: "tcp",
    restricted: true,
    hint: "A database port; private networks only until you widen it.",
  },
  {
    id: "redis",
    label: "Redis",
    port: 6379,
    protocol: "tcp",
    restricted: true,
    hint: "Redis often runs with no password; private networks only.",
  },
  {
    id: "mongodb",
    label: "MongoDB",
    port: 27017,
    protocol: "tcp",
    restricted: true,
    hint: "A database port; private networks only until you widen it.",
  },
  {
    id: "ssh",
    label: "SSH",
    port: 22,
    listen: 2222,
    protocol: "tcp",
    restricted: true,
    hint: "Listens on 2222, since this host's own sshd holds 22.",
  },
  {
    id: "mqtt",
    label: "MQTT",
    port: 1883,
    protocol: "tcp",
    restricted: true,
    hint: "Plain MQTT sends credentials in the clear; private networks only.",
  },
  {
    id: "minecraft-java",
    label: "Minecraft Java",
    port: 25565,
    protocol: "tcp",
    restricted: false,
    hint: "Open to players; the server's own whitelist is the gate.",
  },
  {
    id: "minecraft-bedrock",
    label: "Minecraft Bedrock",
    port: 19132,
    protocol: "udp",
    udpMode: "session",
    restricted: false,
    hint: "UDP with one long-lived session per player.",
  },
  {
    id: "wireguard",
    label: "WireGuard",
    port: 51820,
    protocol: "udp",
    udpMode: "session",
    restricted: false,
    hint: "UDP with one long-lived session per peer; WireGuard's keys are the gate.",
  },
  {
    id: "dns",
    label: "DNS",
    port: 53,
    protocol: "both",
    udpMode: "request",
    restricted: true,
    hint: "One reply per UDP query; private networks only, since an open resolver is abused.",
  },
  {
    id: "syslog",
    label: "Syslog",
    port: 514,
    protocol: "udp",
    udpMode: "session",
    restricted: true,
    hint: "Senders get no reply; private networks only, since anyone could write the log.",
  },
]

/**
 * The form's fields after a preset is picked: port, protocol, UDP mode, and
 * — only where nothing was typed yet — a name and the access rules, so picking
 * a preset late never throws away what was written. The upstream is left to
 * the reader: 127.0.0.1 on the same port would be nginx's own listener, and
 * every connection would loop back into it.
 */
export function applyPreset(
  preset: StreamPreset,
  spec: StreamSpec,
  access: StreamAccess,
): { spec: StreamSpec; access: StreamAccess } {
  return {
    spec: {
      ...spec,
      name: spec.name || preset.id,
      listen: preset.listen ?? preset.port,
      protocol: preset.protocol,
      udpMode: preset.protocol === "tcp" ? undefined : preset.udpMode,
    },
    access:
      access.rules.length > 0 || !preset.restricted
        ? access
        : {
            rules: PRIVATE_NETWORKS.map((source) => ({ action: "allow" as const, source })),
            defaultAllow: false,
          },
  }
}
