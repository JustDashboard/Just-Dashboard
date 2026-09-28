package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
)

// A stack source is every container of one compose project read as one log,
// merged by Docker's own stamps. "What did the stack do at 03:12" is a question
// about the database, the worker and the app together; reading them in three
// panes and lining the times up by eye is the work this does instead.
//
// Each container keeps its own lens and its own record gate: the Postgres
// container's DETAIL lines follow its ERRORs, not whatever the app printed in
// between.

type stackMember struct {
	container dockerx.Container
	service   string
	// lens is the one the container's image is detected as.
	lens string
}

// stackMembers is every container of a compose project, stopped ones and
// replicas included: a stack's history is in the containers that exited too,
// and the container list is the only record that has them all.
func (s *Server) stackMembers(ctx context.Context, project string) ([]stackMember, error) {
	containers, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	members := []stackMember{}
	for _, c := range containers {
		if c.ComposeStack != project {
			continue
		}
		service := c.ComposeSvc
		if service == "" {
			service = c.Name
		}
		members = append(members, stackMember{
			container: c,
			service:   service,
			lens:      logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindDocker, Image: s.containerImage(ctx, c)}),
		})
	}
	if len(members) == 0 {
		return nil, httpx.Err(http.StatusNotFound, "not_found", "No container belongs to the compose project "+project+".")
	}
	return members, nil
}

func (m stackMember) tag(stackLens string) containerTag {
	id := m.container.ID
	if len(id) > 12 {
		id = id[:12]
	}
	return containerTag{service: m.service, container: id, stackLens: stackLens}
}

// stackLens is the lens a stack reads through as a whole: the one forced, or
// the one every container shares. When they differ it is empty, and each line
// that names an event says which lens named it.
func stackLens(members []stackMember, f *logsx.Filter) string {
	if f != nil && f.Lens != "" {
		if f.Lens == logsx.LensNone {
			return ""
		}
		return f.Lens
	}
	common := ""
	for i, m := range members {
		if i == 0 {
			common = m.lens
			continue
		}
		if m.lens != common {
			return ""
		}
	}
	return common
}

// containerSource lists one container, with the lens its image reads through.
func (s *Server) containerSource(ctx context.Context, c dockerx.Container) logsx.Source {
	detail := c.Image
	if c.ComposeStack != "" {
		detail = c.ComposeStack + " · " + c.Image
	}
	return logsx.Source{
		ID:     "docker:" + c.ID,
		Label:  c.Name,
		Kind:   logsx.KindDocker,
		Detail: detail,
		Status: c.State,
		Lens:   logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindDocker, Image: s.containerImage(ctx, c)}),
	}
}

// stackSources lists one source per compose project among the containers.
func (s *Server) stackSources(ctx context.Context, containers []dockerx.Container) []logsx.Source {
	type stack struct {
		services map[string]bool
		images   []string
		running  bool
		members  []stackMember
	}
	stacks := map[string]*stack{}
	for _, c := range containers {
		// A project name compose would not have written cannot be opened as
		// a source, so it is not offered as one.
		if c.ComposeStack == "" || !stackProject.MatchString(c.ComposeStack) {
			continue
		}
		st := stacks[c.ComposeStack]
		if st == nil {
			st = &stack{services: map[string]bool{}}
			stacks[c.ComposeStack] = st
		}
		service := c.ComposeSvc
		if service == "" {
			service = c.Name
		}
		st.services[service] = true
		image := s.containerImage(ctx, c)
		if !slices.Contains(st.images, image) {
			st.images = append(st.images, image)
		}
		st.running = st.running || c.State == "running"
		st.members = append(st.members, stackMember{
			container: c, service: service,
			lens: logsx.DetectLens(logsx.LensTarget{Kind: logsx.KindDocker, Image: image}),
		})
	}
	names := make([]string, 0, len(stacks))
	for name := range stacks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]logsx.Source, 0, len(names))
	for _, name := range names {
		st := stacks[name]
		detail := fmt.Sprintf("%d services", len(st.services))
		if len(st.services) == 1 {
			detail = "1 service"
		}
		status := "exited"
		if st.running {
			status = "running"
		}
		out = append(out, logsx.Source{
			ID: "stack:" + name, Label: name, Kind: logsx.KindStack,
			Detail: detail, Status: status, Images: st.images, Lens: stackLens(st.members, nil),
		})
	}
	return out
}

// containerImage is the reference a container's image was created from. The
// container list already trades a moved tag's `sha256:` id for the name in the
// container's own config, but passes an id printed bare — twelve or sixty-four
// hex digits, no prefix — straight through, and that says nothing about what
// runs in it either. Those are inspected here, and the answers kept by image
// id, so a list of forty containers inspects each such image once rather than
// on every poll.
func (s *Server) containerImage(ctx context.Context, c dockerx.Container) string {
	if !bareImageID(c.Image) {
		return c.Image
	}
	key := c.ImageID
	if key == "" {
		key = c.Image
	}
	if ref, ok := s.logImageRefs.Load(key); ok {
		return ref.(string)
	}
	d, err := s.modules.docker.Inspect(ctx, c.ID)
	if err != nil {
		return c.Image
	}
	ref := c.Image
	if d.Image != "" && !bareImageID(d.Image) {
		ref = d.Image
	}
	s.logImageRefs.Store(key, ref)
	return ref
}

// bareImageID recognises an image named by its id: sha256:…, or the 12 or 64
// hex digits the CLI and some listings print without the prefix.
func bareImageID(ref string) bool {
	if dockerx.IsImageID(ref) {
		return true
	}
	if len(ref) != 12 && len(ref) != 64 {
		return false
	}
	for i := 0; i < len(ref); i++ {
		if c := ref[i]; !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// searchStack reads every container's log in the window and merges them by
// stamp into one collector, so a stack's search is one answer — one limit,
// one histogram, one set of facets — rather than N answers that cannot be
// added up. Line numbers count the merged lines.
func (s *Server) searchStack(ctx context.Context, project string, opts logsx.SearchOptions) (*logsx.SearchResult, error) {
	members, err := s.stackMembers(ctx, project)
	if err != nil {
		return nil, err
	}
	c, err := logsx.NewCollector(opts)
	if err != nil {
		return nil, err
	}
	c.NextFile("stack:"+project, false, nil)
	whole := stackLens(members, c.Filter())
	c.SetLens(whole)
	logOpts := containerSearchOptions(opts)
	streams := make([]*logsx.Stream, len(members))
	ins := make([]<-chan dockerx.LogLine, len(members))
	for i, m := range members {
		streams[i] = c.NewStream(m.lens)
		ch, closer, err := s.modules.docker.Logs(ctx, m.container.ID, logOpts)
		if err != nil {
			// A container removed since the list was read has nothing to
			// add; the rest of the stack still answers.
			continue
		}
		defer closer.Close()
		ins[i] = ch
	}
	n := 0
	var line logsx.Line
	mergeByStamp(ctx, ins, func(i int, stamp *time.Time, text string, raw dockerx.LogLine) {
		n++
		if c.Skip(streams[i], text) {
			return
		}
		readDockerLine(&line, stamp, text, raw, streams[i], members[i].tag(whole))
		line.No = n
		c.FeedFrom(streams[i], &line)
	})
	if ctx.Err() != nil {
		c.Incomplete()
	}
	return c.Result(), nil
}

// stackRejoin is how often a stack's live tail looks again at which of its
// containers are running.
var stackRejoin = 2 * time.Second

// followStack is the stack's live tail, in two phases so the opening window is
// in time order and nothing is sent twice. First each container's last n
// lines, merged by stamp, of which the last n kept are sent; then each running
// container followed from its own newest stamp, dropping anything at or before
// it.
//
// The second phase outlives any one container. Docker ends a follow when its
// container stops, and a restart, a crash loop and a redeploy are all stops,
// while the rest of the stack runs on and keeps the socket open. So every
// stackRejoin the stack's containers are listed again, and one that runs and
// is not followed is: the same container after a restart, from its own newest
// line; a redeploy's replacement, from the newest line its service sent; one
// that was stopped when the socket opened. The socket ends — and the page says
// the stream ended — once nothing in the stack is running.
func (s *Server) followStack(ctx context.Context, project string, members []stackMember, f *logsx.Filter, n int, out chan<- logsx.Line) {
	whole := stackLens(members, f)
	streams := make([]*logsx.Stream, len(members))
	for i, m := range members {
		streams[i] = f.Stream(m.lens)
	}
	opened := time.Now()
	ins := make([]<-chan dockerx.LogLine, len(members))
	closers := []io.Closer{}
	for i, m := range members {
		ch, closer, err := s.modules.docker.Logs(ctx, m.container.ID, dockerx.LogOptions{
			Tail: fmt.Sprint(n), Timestamps: true,
		})
		if err != nil {
			continue
		}
		closers = append(closers, closer)
		ins[i] = ch
	}
	newest := make([]time.Time, len(members))
	ring := make([]logsx.Line, 0, n)
	var line logsx.Line
	mergeByStamp(ctx, ins, func(i int, stamp *time.Time, text string, raw dockerx.LogLine) {
		// The newest stamp rather than the last: a container's stdout and
		// stderr arrive through two pipes, and the last line read can be
		// older than one read before it. Following from it would send that
		// one again.
		if stamp != nil && stamp.After(newest[i]) {
			newest[i] = *stamp
		}
		if streams[i].Skip(text) {
			return
		}
		readDockerLine(&line, stamp, text, raw, streams[i], members[i].tag(whole))
		if keep, _ := streams[i].Keep(&line, true); !keep {
			return
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, line)
	})
	for _, c := range closers {
		c.Close()
	}

	go func() {
		var producers sync.WaitGroup
		defer close(out)
		defer producers.Wait()
		// The opening window goes first and the followers start after it,
		// which is what keeps the page in time order. Starting them later
		// loses nothing: each asks Docker for what came after its own newest
		// stamp.
		for _, line := range ring {
			select {
			case <-ctx.Done():
				return
			case out <- line:
			}
		}

		// A container keeps its stream across a restart, so a record the
		// lens was reading goes on being read; a new one starts its own.
		type follow struct {
			member stackMember
			stream *logsx.Stream
			newest time.Time
			// live is whether a follow of it is open; refused, that Docker
			// would not give its log — a logging driver with nothing to read,
			// which no restart changes.
			live, refused bool
		}
		type ended struct {
			id     string
			newest time.Time
		}
		follows := map[string]*follow{}
		services := map[string]time.Time{}
		for i, m := range members {
			follows[m.container.ID] = &follow{member: m, stream: streams[i], newest: newest[i]}
			if newest[i].After(services[m.service]) {
				services[m.service] = newest[i]
			}
		}
		done := make(chan ended)
		live := func() int {
			n := 0
			for _, fl := range follows {
				if fl.live {
					n++
				}
			}
			return n
		}
		join := func(current []stackMember) {
			for _, m := range current {
				fl := follows[m.container.ID]
				if fl == nil && m.container.State == "running" {
					fl = &follow{member: m, stream: f.Stream(m.lens), newest: services[m.service]}
					follows[m.container.ID] = fl
				}
				if fl == nil || fl.live || fl.refused || m.container.State != "running" {
					continue
				}
				after := fl.newest
				if after.IsZero() {
					// Nothing in the opening window: follow from when it was
					// read, so a line written in between is neither lost nor
					// sent twice.
					after = opened
				}
				ch, closer, err := s.modules.docker.Logs(ctx, m.container.ID, dockerx.LogOptions{
					Tail: "all", Since: after.Format(time.RFC3339Nano), Timestamps: true, Follow: true,
				})
				if err != nil {
					fl.refused = ctx.Err() == nil
					continue
				}
				fl.live = true
				producers.Add(1)
				go func(id string, st *logsx.Stream, tag containerTag) {
					defer producers.Done()
					last := pumpContainer(ctx, ch, st, tag, after, out)
					closer.Close()
					select {
					case <-ctx.Done():
					case done <- ended{id: id, newest: last}:
					}
				}(m.container.ID, fl.stream, fl.member.tag(whole))
			}
		}
		if join(members); live() == 0 {
			return
		}
		tick := time.NewTicker(stackRejoin)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-done:
				fl := follows[e.id]
				fl.live = false
				if e.newest.After(fl.newest) {
					fl.newest = e.newest
				}
				if fl.newest.After(services[fl.member.service]) {
					services[fl.member.service] = fl.newest
				}
			case <-tick.C:
				if current, err := s.stackMembers(ctx, project); err == nil {
					join(current)
				}
				if live() == 0 {
					return
				}
			}
		}
	}()
}

// mergeByStamp hands the lines of several time-ordered container logs to fn,
// oldest first. Each log is in order on its own, so the oldest line overall is
// always one of the heads: the merge holds one line per container rather than
// every log in memory. A line Docker left unstamped sorts with the line before
// it from the same container.
func mergeByStamp(ctx context.Context, ins []<-chan dockerx.LogLine, fn func(i int, stamp *time.Time, text string, raw dockerx.LogLine)) {
	type head struct {
		raw   dockerx.LogLine
		stamp *time.Time
		text  string
		at    time.Time
		ok    bool
	}
	heads := make([]head, len(ins))
	last := make([]time.Time, len(ins))
	next := func(i int) {
		heads[i].ok = false
		if ins[i] == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-ins[i]:
			if !ok {
				ins[i] = nil
				return
			}
			stamp, text := splitDockerStamp(raw.Text)
			if stamp != nil {
				last[i] = *stamp
			}
			heads[i] = head{raw: raw, stamp: stamp, text: text, at: last[i], ok: true}
		}
	}
	for i := range ins {
		next(i)
	}
	for ctx.Err() == nil {
		pick := -1
		for i := range heads {
			if heads[i].ok && (pick < 0 || heads[i].at.Before(heads[pick].at)) {
				pick = i
			}
		}
		if pick < 0 {
			return
		}
		h := heads[pick]
		fn(pick, h.stamp, h.text, h.raw)
		next(pick)
	}
}
