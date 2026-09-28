package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

const (
	// portHistoryHours is the window the history answers for when none is
	// asked, and the longest it answers for: what the recorder keeps.
	portHistoryHours    = 24
	portHistoryMaxHours = int(proxysvc.PortHistoryRetention / time.Hour)
	// portHistoryLimit caps the events in one answer. A host whose programs
	// open fixed ports all day can make thousands in a month, more than a
	// page can usefully draw; the answer says when it was cut.
	portHistoryLimit    = 1000
	portHistoryMaxLimit = 5000
)

// mountPortHistoryRoutes is the ports history, beside the list it dates. It
// is read by every signed-in account, as the list is: the same sockets and
// owners, at other times.
func (s *Server) mountPortHistoryRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/history", s.handle(s.handlePortHistory))
}

// handlePortHistory answers what opened and closed in the last ?hours= (1 to
// 720, 24 by default), newest first, at most ?limit= events (1 to 5000, 1000
// by default). Each socket is placed and graded as GET /ports places and
// grades one listening now, against the host's network and firewall as they
// are now.
func (s *Server) handlePortHistory(w http.ResponseWriter, r *http.Request) error {
	hours, err := boundedQueryInt(r, "hours", portHistoryHours, portHistoryMaxHours)
	if err != nil {
		return err
	}
	limit, err := boundedQueryInt(r, "limit", portHistoryLimit, portHistoryMaxLimit)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, portListTimeout)
	defer cancel()
	firewall := make(chan *netsec.FirewallStatus, 1)
	go func() {
		status, err := s.modules.netsec.Status(ctx)
		if err != nil {
			status = nil
		}
		firewall <- status
	}()
	history, err := s.modules.proxyExtras.portHistory.History(ctx, time.Now().Add(-time.Duration(hours)*time.Hour), limit)
	if err != nil {
		<-firewall
		return httpx.Internal(err)
	}
	sockets := make([]proxysvc.Listener, len(history.Events))
	for i := range history.Events {
		sockets[i] = history.Events[i].Listener
	}
	placeListeners(sockets, netsec.ReadHostNetwork(ctx), <-firewall)
	for i := range history.Events {
		history.Events[i].Listener = sockets[i]
	}
	httpx.JSON(w, http.StatusOK, history)
	return nil
}

// boundedQueryInt reads a whole-number query parameter between 1 and most,
// refusing anything else rather than quietly answering for another value.
func boundedQueryInt(r *http.Request, name string, fallback, most int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > most {
		return 0, httpx.BadRequest("%s must be a whole number from 1 to %d", name, most)
	}
	return n, nil
}

// seenListener is a socket as GET /ports lists it, with when the ports
// history first saw it listening.
type seenListener struct {
	proxysvc.Listener
	// FirstSeen is absent for a socket that was already listening when the
	// history began, and for one opened since the last sample.
	FirstSeen *time.Time `json:"firstSeen,omitempty"`
}

// withFirstSeen dates each socket from the ports history. The list is what
// the page is for, so a history that cannot be read leaves the sockets
// undated rather than failing it.
func (s *Server) withFirstSeen(r *http.Request, listeners []proxysvc.Listener) []seenListener {
	out := make([]seenListener, len(listeners))
	for i, l := range listeners {
		out[i].Listener = l
	}
	seen, err := s.modules.proxyExtras.portHistory.FirstSeen(r.Context(), listeners)
	if err != nil {
		s.Log.Warn("the ports history could not date the listening sockets", "error", err)
		return out
	}
	for i := range out {
		out[i].FirstSeen = seen[i]
	}
	return out
}
