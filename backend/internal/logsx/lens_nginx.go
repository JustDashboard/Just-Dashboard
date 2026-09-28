package logsx

// An nginx container writes both of its logs to one stream: the image links
// access.log to stdout and error.log to stderr, and `docker logs` hands them
// over interleaved. Which of the two a line is shows in its first bytes — the
// error log's "2006/01/02 15:04:05 [" or `nginx -t`'s "nginx: [", and anything
// else is tried as a request — so the lens reads by shape rather than by
// stream, which also holds for an image configured to send both to one.
//
// The two lenses' events do not collide, so a line keeps this lens as its own
// rather than naming the one it was handed to.

func init() {
	register(&Lens{
		ID:     "nginx",
		Events: nginxUnion(httpAccessEvents, nginxErrorEvents),
		Attrs:  nginxUnion(httpAccessAttrs, nginxErrorAttrs),
		New:    func() Reader { return &nginxReader{} },
	})
}

type nginxReader struct {
	access httpAccessReader
}

func (r *nginxReader) Read(l *Line) {
	if nginxErrorShaped(l.Text) {
		readNginxError(l)
		return
	}
	r.access.Read(l)
}

// nginxUnion is a then whatever of b it lacks, in order, so the vocabulary the
// golden file records does not reorder itself between runs.
func nginxUnion(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		found := false
		for _, have := range a {
			if have == v {
				found = true
				break
			}
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}
