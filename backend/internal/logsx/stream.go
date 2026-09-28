package logsx

// Stream is one ordered source read through its lens: one file (each archive
// separately), one container, one journalctl run. It owns the two things a
// line cannot be judged without — the reader's state, and whether the record
// a continuation line belongs to was kept.
//
// The second is the reason this type exists. A Postgres ERROR is followed by
// its DETAIL and its STATEMENT; a search for "deadlock" matches the ERROR and
// not the DETAIL that names the processes, and a level filter drops the
// STATEMENT because it has no level of its own. Judged line by line, the
// record arrives in pieces, and the piece that explains it is the one lost. So
// a continuation line takes its head's verdict, whatever its own text says.
//
// Every place that reads lines — the live tail, the prefill, the history
// search, the export, and the container and journal pumps — runs them through
// a Stream, in the same four steps: Skip before parsing, ParseLine, Read,
// Keep. Without a lens nothing is a continuation and those steps are exactly
// the filter they replaced.
type Stream struct {
	f    *Filter
	lens string
	r    Reader
	// off is a forced "none": no lens at all, and none routed to either.
	off bool
	// pick names a lens for one line when it is not the stream's own; the
	// journal is the case, where a unit's output and the manager's lines about
	// that unit arrive in one run.
	pick   func(*Line) string
	routed map[string]Reader

	// head is whether a head line has been read, and level is its level,
	// which its continuation lines are drawn in.
	head  bool
	level string
	// seen is whether a line has been judged yet. A continuation line before
	// any is an orphan — the window began part-way through a record — and it
	// stands on its own rather than following a head nobody saw.
	seen bool
	// open is the verdict on the record now running.
	open bool
}

// Stream starts reading one source. The filter's lens wins when it names one
// (or names "none"); otherwise auto — the lens detected for this particular
// source, which in a stack differs container by container. An id that is not
// registered reads as no lens: detection may name a lens this build does not
// have, and that is not an error the operator made.
func (f *Filter) Stream(auto string) *Stream {
	s := &Stream{f: f}
	id := auto
	if f != nil && f.Lens != "" {
		id = f.Lens
	}
	if id == LensNone {
		s.off = true
		return s
	}
	if lens, _ := LensByID(id); lens != nil {
		s.lens, s.r = lens.ID, lens.New()
	}
	return s
}

// Lens is the id this stream reads through, "" for none.
func (s *Stream) Lens() string { return s.lens }

// Route hands some lines to another lens. pick answers "" to keep a line on
// the stream's own lens. A line read by another lens that names an event is
// marked with that lens, so the viewer can find the event's label; a lens
// that is not registered reads nothing.
func (s *Stream) Route(pick func(*Line) string) { s.pick = pick }

// Skip is the cheap test before parsing: a line whose text the filter rejects
// needs neither a parse nor a lens — unless a kept record is open, when it may
// be one of that record's continuation lines and only the lens can say.
//
// It never skips while a lens reads the stream. A lens carries values from
// line to line — the transaction's Start-Date onto its Upgrade line, the
// address a connection came from onto the FATAL that ends it — and a line it
// never saw is a value the next line never gets: the same line would then
// read differently with a query than without one, and lose the stamp that
// keeps it out of a window it is not in.
func (s *Stream) Skip(raw string) bool {
	if s.open || s.r != nil || (s.pick != nil && !s.off) || s.f.MatchText(raw) {
		return false
	}
	// A line passed over is a line decided: whatever continues it follows it
	// out, which is also what judging it would have concluded.
	s.seen = true
	return true
}

// Read runs the lens over a parsed line and draws a continuation line in its
// head's level, so a stack trace under an error reads as part of the error.
func (s *Stream) Read(l *Line) {
	r, id := s.r, s.lens
	if s.pick != nil && !s.off {
		if other := s.pick(l); other != "" && other != s.lens {
			r, id = s.reader(other), other
		}
	}
	if r != nil {
		r.Read(l)
		if l.Event != "" && l.Lens == "" && id != s.lens {
			l.Lens = id
		}
	}
	if l.Cont && s.head {
		l.Level = s.level
		return
	}
	if !l.Cont {
		s.head, s.level = true, l.Level
	}
}

func (s *Stream) reader(id string) Reader {
	if r, ok := s.routed[id]; ok {
		return r
	}
	var r Reader
	if lens, _ := LensByID(id); lens != nil {
		r = lens.New()
	}
	if s.routed == nil {
		s.routed = map[string]Reader{}
	}
	s.routed[id] = r
	return r
}

// Keep judges a line after Read. A head — any line that is not a continuation
// — is kept when it is within the window and the filter matches it. A
// continuation line takes its head's verdict and is never a match of its own:
// own is false, so it is not counted, charted or highlighted.
func (s *Stream) Keep(l *Line, within bool) (keep, own bool) {
	if l.Cont && s.seen {
		return s.open, false
	}
	s.seen = true
	s.open = within && s.f.match(l)
	return s.open, s.open
}
