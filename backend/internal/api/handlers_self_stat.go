package api

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfstat"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/go-chi/chi/v5"
)

// mountSelfStatRoutes mounts the dashboard's reading of its own process,
// under /dashboard.
//
// The dashboard measured the host, its containers and its databases and had
// nothing to say about itself: no memory or goroutine count, no word on which
// background loop was failing, no profile. Why the backend sat at 130 MB, or
// what it was doing at 3% CPU with nobody signed in, had to be guessed from
// docker stats.
//
// All of it is an administrator's, behind the network allowlist and
// authentication like every route under /api/v1; nothing here is on /healthz
// or reachable without a credential. net/http/pprof is deliberately not
// imported: its init registers the profiles on http.DefaultServeMux, where
// any listener that ever served the default mux would hand them to whoever
// asked. The profiles are written from runtime/pprof by a handler of this
// router instead.
func (s *Server) mountSelfStatRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/self", s.handle(s.handleDashboardSelf))
		r.Route("/debug/pprof", func(r chi.Router) {
			// The tight budget of the irreversible routes, without their
			// mark: a profile undoes nothing, but a CPU profile holds a
			// request open while it samples and a heap profile walks the
			// heap, so they are not to be asked for in a loop.
			r.Use(s.destrLim.ByPrincipal)
			r.Method(http.MethodGet, "/", s.handle(s.handleSelfProfileList))
			r.Method(http.MethodGet, "/{profile}", s.handle(s.handleSelfProfile))
		})
	})
}

// dashboardSelf is what the dashboard's process is using and how its
// background work is going.
type dashboardSelf struct {
	Process selfstat.Process `json:"process"`
	// Store is the dashboard's own database on disk. A write-ahead log that
	// stays large is a reader holding a checkpoint back.
	Store selfStore `json:"store"`
	// Tasks is every background task started since the process began
	// (internal/safego), each with how its passes are going where its loop
	// reports them.
	Tasks []selfTask `json:"tasks"`
}

type selfStore struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	// WALBytes and SHMBytes are the write-ahead log and its index, zero
	// when SQLite has checkpointed and removed them.
	WALBytes int64 `json:"walBytes"`
	SHMBytes int64 `json:"shmBytes"`
}

type selfTask struct {
	safego.Task
	// Passes is absent for a task that is not a loop, and for a loop that
	// does not report its passes (safego.Passes).
	Passes *safego.PassState `json:"passes,omitempty"`
}

func (s *Server) handleDashboardSelf(w http.ResponseWriter, _ *http.Request) error {
	out := dashboardSelf{Process: selfstat.Read(), Tasks: []selfTask{}}
	if s.Cfg.DataDir != "" {
		out.Store.Path = filepath.Join(s.Cfg.DataDir, store.DatabaseFile)
		out.Store.SizeBytes = fileSize(out.Store.Path)
		out.Store.WALBytes = fileSize(out.Store.Path + "-wal")
		out.Store.SHMBytes = fileSize(out.Store.Path + "-shm")
	}
	passes := safego.PassStates()
	for _, task := range safego.Tasks() {
		row := selfTask{Task: task}
		if state, ok := passes[task.Name]; ok {
			row.Passes = &state
		}
		out.Tasks = append(out.Tasks, row)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// fileSize is a file's size, zero for one that is not there.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// selfProfile is one profile the dashboard hands out of itself.
type selfProfile struct {
	Name string `json:"name"`
	Says string `json:"description"`
	// Seconds is how long a sampled profile runs when none is asked for;
	// zero for a snapshot, which answers at once.
	Seconds int    `json:"seconds,omitempty"`
	Path    string `json:"path"`
}

const (
	selfProfilePath = "/api/v1/dashboard/debug/pprof/"
	// cpuProfileName is the name net/http/pprof gives the CPU profile, kept
	// so the address is the one people already type.
	cpuProfileName    = "profile"
	cpuProfileSeconds = 10
	cpuProfileLongest = 60
)

// selfProfiles are the profiles offered, in the order they are listed. Each
// snapshot is a runtime/pprof profile of that name. Block and mutex are left
// out: both are empty unless their sampling is turned on for the whole
// process, which costs every lock and channel operation something.
var selfProfiles = []selfProfile{
	{Name: cpuProfileName, Says: "where processor time goes, sampled for ?seconds= (1 to 60, 10 unless said)", Seconds: cpuProfileSeconds},
	{Name: "heap", Says: "what holds memory now, by where it was allocated"},
	{Name: "allocs", Says: "everything allocated since the process started, by where"},
	{Name: "goroutine", Says: "every goroutine and what it waits on; ?debug=2 is the full stacks as text"},
	{Name: "threadcreate", Says: "what made the runtime start operating-system threads"},
}

func (s *Server) handleSelfProfileList(w http.ResponseWriter, _ *http.Request) error {
	out := make([]selfProfile, len(selfProfiles))
	for i, profile := range selfProfiles {
		profile.Path = selfProfilePath + profile.Name
		out[i] = profile
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"profiles": out})
	return nil
}

// handleSelfProfile writes one profile: the pprof format `go tool pprof`
// reads, as a file, or text with ?debug=1 or 2.
//
// It is on the audit trail although it is a read. A profile is taken when
// something is wrong, costs the process something while it runs, and shows
// the shape of what the dashboard is doing; who asked for one is worth a
// line.
func (s *Server) handleSelfProfile(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "profile")
	offered := false
	for _, profile := range selfProfiles {
		offered = offered || profile.Name == name
	}
	if !offered {
		return httpx.Err(http.StatusNotFound, "not_found", "The dashboard has no profile of that name.").
			Because("GET "+selfProfilePath+" lists the ones it has.", "")
	}
	if name == cpuProfileName {
		return s.writeCPUProfile(w, r)
	}
	debug, err := httpx.QueryInt(r, "debug", 0, 0, 2)
	if err != nil {
		return err
	}
	profile := pprof.Lookup(name)
	if profile == nil {
		return httpx.Internal(fmt.Errorf("runtime/pprof has no %q profile", name))
	}
	httpx.AuditRead(s.Audit, r, "dashboard.profile", name)
	profileHeaders(w, name, debug > 0)
	// The headers are sent: a write that fails now is the client having
	// gone, and there is nothing left to answer it with.
	_ = profile.WriteTo(w, debug)
	return nil
}

// writeCPUProfile samples the process for the seconds asked and answers with
// the profile. The runtime takes one CPU profile at a time; a second request
// while one runs is told so rather than made to wait behind it.
func (s *Server) writeCPUProfile(w http.ResponseWriter, r *http.Request) error {
	seconds, err := httpx.QueryInt(r, "seconds", cpuProfileSeconds, 1, cpuProfileLongest)
	if err != nil {
		return err
	}
	// Sampled into memory and written once the sampling has stopped. The
	// runtime writes a profile from a goroutine of its own, and a response is
	// not something two goroutines may write: handed the response itself, that
	// goroutine read the headers this one was still setting (the race gate
	// caught it). Nothing is written before the sampling starts, so its
	// refusal can still be the answer.
	var sampled bytes.Buffer
	if err := pprof.StartCPUProfile(&sampled); err != nil {
		return httpx.Err(http.StatusConflict, "profile_running", "A CPU profile of the dashboard is already being taken.").
			Because("The Go runtime samples for one CPU profile at a time. Ask again when the other has finished; it runs for at most "+strconv.Itoa(cpuProfileLongest)+" seconds.", err.Error()).
			Retry()
	}
	httpx.AuditRead(s.Audit, r, "dashboard.profile", "cpu "+strconv.Itoa(seconds)+"s")
	select {
	case <-time.After(time.Duration(seconds) * time.Second):
	case <-r.Context().Done():
		// The person closed the request, or the server is shutting down:
		// the sampling ends with it rather than running on for nobody.
	}
	// Returns once the runtime's goroutine has written the whole profile.
	pprof.StopCPUProfile()
	profileHeaders(w, "cpu", false)
	// The headers are sent: a write that fails now is the client having gone.
	_, _ = w.Write(sampled.Bytes())
	return nil
}

func profileHeaders(w http.ResponseWriter, name string, text bool) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	if text {
		header.Set("Content-Type", "text/plain; charset=utf-8")
		return
	}
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Disposition", `attachment; filename="just-dashboard-`+name+"-"+time.Now().UTC().Format("20060102T150405Z")+`.pb.gz"`)
}
