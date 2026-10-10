package netpath

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// streamEvidence joins a native nginx stream into the explanation: its
// configured forward, the sockets nginx holds for it, the backend legs it
// holds open now and what its log recorded over the last hour. None of it is
// this tuple's own traversal; the measured leg is streamTraversal's.
func streamEvidence(ctx context.Context, name string, p Providers) Evidence {
	item := evidence("stream", "Stream proxy", "native nginx stream "+name, "nginx stream", "/proxy/streams?stream="+name)
	path, err := p.Stream(ctx, name)
	if err != nil {
		item.failure(err)
		return item
	}
	item.Basis, item.State = Observed, path.State
	item.Summary = fmt.Sprintf("nginx forwards %s on %s to %s.", strings.ToUpper(path.Protocol), strings.Join(path.Listens, " and "), streamBackends(path))
	if path.StateReason != "" {
		item.Summary += " " + path.StateReason
	}
	item.Facts = append(item.Facts, Fact{"Access", path.Access})
	if path.Balance != "" {
		item.Facts = append(item.Facts, Fact{"Balancing", path.Balance})
	}
	if path.TLS != "" {
		item.Facts = append(item.Facts, Fact{"TLS ended by nginx towards", path.TLS})
	}
	if path.SocketsRead {
		item.Facts = append(item.Facts, Fact{"Client sessions now", strconv.Itoa(path.Clients)})
	}
	for _, server := range path.Servers {
		role := "backend"
		switch {
		case server.Down:
			role = "backend marked down"
		case server.Backup:
			role = "backup backend"
		}
		reading := []string{}
		if path.SocketsRead {
			reading = append(reading, fmt.Sprintf("%d connection%s from nginx now", server.Connections, plural(server.Connections)))
		}
		if path.Logging {
			reading = append(reading, fmt.Sprintf("%d session%s in the last hour, %d failed", server.Sessions, plural(server.Sessions), server.Failed))
		}
		item.Facts = append(item.Facts, Fact{role + " " + server.Address, strings.Join(reading, "; ")})
	}
	if path.Logging {
		item.Facts = append(item.Facts, Fact{"Last hour", fmt.Sprintf("%d sessions, %d failed (5xx), %d denied", path.LogSessions, path.LogFailed, path.LogDenied)})
	}
	item.Limitations = append(item.Limitations, "Configured forwards and access rules are the stream file's; the connections and sessions are nginx's, not this tuple's.")
	if path.Note != "" {
		item.Limitations = append(item.Limitations, path.Note)
	}
	switch {
	case !path.Logging:
		item.Limitations = append(item.Limitations, "The stream does not log its sessions, so which backend a connection reached is not recorded.")
	case path.LogError != "":
		item.Limitations = append(item.Limitations, "The stream's log could not be read: "+path.LogError)
	case !path.LogComplete:
		item.Limitations = append(item.Limitations, "The log read stopped short of the hour, so its counts are floors.")
	}
	return item
}

// streamBackends names where a stream forwards: every backend not marked down.
func streamBackends(path *proxysvc.StreamPath) string {
	var live []string
	for _, server := range path.Servers {
		if !server.Down {
			live = append(live, server.Address)
		}
	}
	if len(live) == 0 {
		return "no backend that is not marked down"
	}
	return strings.Join(live, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// streamTraversal reads the session the measured connection left in the
// stream's log: where nginx forwarded it and how it ended. Correlated by
// client address and time, since nginx logs no client port.
func streamTraversal(ctx context.Context, name, client string, sent time.Time, p Providers) Evidence {
	item := evidence("stream_traversal", "Stream backend leg", "the measured connection through "+name, "nginx stream", "/proxy/streams?stream="+name)
	session, err := p.StreamSession(ctx, name, client, sent)
	switch {
	case err != nil:
		item.Summary = "Which backend nginx forwarded the measured connection to is unknown: " + err.Error() + "."
		return item
	case session == nil:
		item.Summary = "nginx logged no session from " + client + " after the measured connection, so which backend it reached is unknown."
		return item
	}
	tried := strings.Split(session.Upstream, ",")
	last := strings.TrimSpace(tried[len(tried)-1])
	item.Basis = Measured
	item.Facts = []Fact{{"Client as nginx saw it", session.Client}, {"Status", strconv.Itoa(session.Status)}, {"Backend", last}, {"Session length", fmt.Sprintf("%.3f s", session.Seconds)}}
	if len(tried) > 1 {
		item.Facts = append(item.Facts, Fact{"Tried first", strings.Join(tried[:len(tried)-1], ", ")})
	}
	switch {
	case session.Status == 200 && last != "":
		item.State = "forwarded"
		item.Summary = fmt.Sprintf("nginx forwarded the measured connection to %s and logged it as completed.", last)
	case session.Status == 403:
		item.State = "denied"
		item.Summary = "nginx's access rules refused the measured connection; it reached no backend."
	case session.Status >= 500:
		item.State = "failed"
		item.Summary = "nginx accepted the measured connection but could not forward it to a backend."
		if last != "" {
			item.Summary = fmt.Sprintf("nginx accepted the measured connection but failed forwarding it, last to %s.", last)
		}
	default:
		item.State = "logged"
		item.Summary = fmt.Sprintf("nginx logged the measured connection with status %d.", session.Status)
	}
	item.Limitations = []string{"Matched by client address and time: another connection from the same address in the same second could be the one logged.", "A completed TCP leg does not prove the backend's protocol or application answered."}
	return item
}
