import { ApiError, errorMessage } from "@/lib/api"
import type { StreamStatus } from "@/lib/types"
import {
  includedPlace,
  moduleMissing,
  moduleRemedy,
  protocolLabel,
  streamOutage,
} from "@/lib/streams"
import { DANGEROUS_PORTS, type ProxyFinding } from "@/components/proxy/findings/shared"

export type StreamFindingInput = { streams?: StreamStatus; streamsError?: Error }

/**
 * A stream directory that stops nginx reloading, streams nginx is not
 * reading, and a forward open to anyone.
 *
 * The include advice waits for the module: on a host where nginx has no
 * stream module, "add the stream block" is advice that stops nginx reloading
 * — for every site, not only the streams. An include inside http does the
 * same once a file is there: nginx reads the file, as http, and refuses it.
 * Both fixes the page can make — installing the module, connecting the
 * directory — are named as the page's, so the advice leads to a button.
 *
 * A directory that could not be read is a finding of its own. Without one,
 * no streams read as nothing to report, and the overview said streams were
 * within limits over four open forwards it could not see.
 */
export function streamFindings({ streams, streamsError }: StreamFindingInput): ProxyFinding[] {
  const out: ProxyFinding[] = []
  if (streamsError) {
    const directory =
      streamsError instanceof ApiError && streamsError.code === "stream_dir_unreadable"
    out.push({
      id: "streams.unreadable",
      level: "warning",
      title: directory ? "Could not read the stream directory" : "Could not read the streams",
      detail: errorMessage(streamsError),
      advice: streams
        ? "What this list says about streams is from the last read that worked."
        : "Until it can be read, no stream is checked for an open port or for a reload it would stop.",
      meta: "streams",
      href: "/proxy/streams",
    })
  }
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
      advice: `${moduleRemedy(streams.module)} ${
        streams.connection
          ? "Or disconnect the stream directory on the Streams page."
          : "Or take the stream block out of nginx.conf."
      }`,
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
      advice: streams.module.package
        ? `Install ${streams.module.package} from the Streams page, then connect ${streams.dir} there.`
        : `${moduleRemedy(streams.module)} Then connect ${streams.dir} on the Streams page.`,
      meta: "streams",
      href: "/proxy/streams",
    })
  } else if (count > 0 && !streams.included) {
    out.push({
      id: "streams.not-included",
      level: "warning",
      title: `${these} but nginx is not reading ${count === 1 ? "it" : "them"}`,
      detail: streams.includeError
        ? `Whether nginx.conf includes ${streams.dir} could not be read: ${streams.includeError}.`
        : `nginx.conf has no stream block including ${streams.dir}.`,
      advice: streams.includeError
        ? "Make nginx.conf readable to the dashboard, then connect the directory on the Streams page."
        : "Connect the directory on the Streams page, which shows the change to nginx.conf before it makes it.",
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
