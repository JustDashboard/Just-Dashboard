import type { StreamStatus } from "@/lib/types"
import {
  includedPlace,
  moduleMissing,
  moduleRemedy,
  protocolLabel,
  streamOutage,
} from "@/lib/streams"
import { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"

export type StreamFindingInput = { streams?: StreamStatus }

/**
 * A stream directory that stops nginx reloading, streams nginx is not
 * reading, and a forward open to anyone.
 *
 * The include advice waits for the module: on a host where nginx has no
 * stream module, "add the stream block" is advice that stops nginx reloading
 * — for every site, not only the streams. An include inside http does the
 * same once a file is there: nginx reads the file, as http, and refuses it.
 */
export function streamFindings({ streams }: StreamFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []
  if (!streams) return out
  const count = streams.streams.length
  const these = `${count} stream${count === 1 ? " is" : "s are"} written`
  const missing = moduleMissing(streams.module)
  const outage = streamOutage(streams)

  if (outage === "module") {
    out.push({
      id: "streams.module-missing",
      level: "critical",
      title: "nginx.conf has a stream block this nginx cannot read",
      detail:
        "nginx has no stream module, so its configuration test fails on the stream block and every reload is refused.",
      advice: `${moduleRemedy(streams.module)} Or take the stream block out of nginx.conf.`,
      meta: "streams",
      href: "/proxy/streams",
    })
  } else if (outage === "misplaced") {
    const place = includedPlace(streams.includedIn ?? "")
    out.push({
      id: "streams.misplaced",
      level: "critical",
      title: `nginx refuses every reload: ${count === 1 ? "a stream file is" : `${count} stream files are`} included ${place}`,
      detail: `${streams.dir} is included ${place}, where a stream is not allowed, so nginx's configuration test fails and every reload is refused — for every site on this host, not only the streams.`,
      advice: missing
        ? `Take out the include that puts ${streams.dir} there, which ends the refusals. ${moduleRemedy(streams.module)} Then include it from a top-level stream block.`
        : "Move the include into a top-level stream block beside the http block, or delete the stream files.",
      meta: "streams",
      href: "/proxy/streams",
    })
  } else if (missing && count > 0) {
    out.push({
      id: "streams.module-missing",
      level: "warning",
      title: `${these} but this nginx has no stream module`,
      detail: "Adding the stream block to nginx.conf now would fail nginx's configuration test.",
      advice: `${moduleRemedy(streams.module)} Then include ${streams.dir} from a top-level stream block.`,
      meta: "streams",
      href: "/proxy/streams",
    })
  } else if (count > 0 && !streams.included) {
    out.push({
      id: "streams.not-included",
      level: "warning",
      title: `${these} but nginx is not reading ${count === 1 ? "it" : "them"}`,
      detail: `nginx.conf has no stream block including ${streams.dir}.`,
      advice:
        "Add the include the Streams page prints, at the top level of nginx.conf beside the http block.",
      meta: "streams",
      href: "/proxy/streams",
    })
  }
  for (const stream of streams.streams) {
    if (!stream.open || stream.error) continue
    const service = DANGEROUS_PORTS[stream.listen]
    out.push({
      id: `stream.open.${stream.name}`,
      level: service ? "warning" : "notice",
      title: `Stream ${stream.name} forwards port ${stream.listen} to anyone`,
      detail: `${protocolLabel(stream.protocol)} ${stream.listen} → ${stream.upstream} with no allow list${service ? `, and ${stream.listen} is ${service}` : ""}.`,
      advice:
        "A stream has no authentication of its own. Restrict the source unless the service behind it authenticates for itself.",
      meta: "stream",
      href: "/proxy/streams",
    })
  }

  return out
}
