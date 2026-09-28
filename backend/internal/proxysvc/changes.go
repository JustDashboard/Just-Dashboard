package proxysvc

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ChangeAction is what happened to a configuration file.
type ChangeAction string

const (
	ChangeWrite   ChangeAction = "write"
	ChangeDelete  ChangeAction = "delete"
	ChangeEnable  ChangeAction = "enable"
	ChangeDisable ChangeAction = "disable"
	ChangeRename  ChangeAction = "rename"
)

// Change is one configuration file this service changed, with what it held on
// either side of the change.
//
// Before and After are the file's content before and after. For a write,
// BeforeExisted says whether there was a file to replace; for a delete, After
// is empty. Enable and disable change the file's link in sites-enabled, not
// the file: Path is the site file, and Before and After both hold its
// content, which is what went live or stopped serving — except for a site
// file that resolves outside the proxy's directories, which is recorded
// without content because ReadConfig will not show it. Actor is who asked,
// from WithActor, and is empty when nobody did: a deployment cutover or a
// background loop.
type Change struct {
	Path          string
	Action        ChangeAction
	Actor         string
	Before        []byte
	BeforeExisted bool
	After         []byte
}

// ChangeRecorder keeps the changes. It is called after a change has been
// committed — tested and in place, whether or not a reload follows — with the
// service's lock held, so it sees changes in the order they happened and must
// not call back into the Service.
type ChangeRecorder interface {
	Record(ctx context.Context, c Change) error
}

// SetRecorder is called once while wiring, before the service is used.
func (s *Service) SetRecorder(r ChangeRecorder) { s.recorder = r }

type actorKey struct{}

// WithActor names who is asking, for the changes made on their behalf.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom is the name WithActor put on ctx, or empty.
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}

// recordChange runs after every committed change to a configuration file.
//
// The cached `nginx -T` is forgotten whatever else happens. The record is
// skipped for password files, which are credentials rather than
// configuration, and a recorder that fails is logged rather than failing the
// change: the file is already written, tested and possibly serving, and
// reporting the operator's save as failed would be the one untrue answer.
// The request's cancellation is detached for the same reason.
func (s *Service) recordChange(ctx context.Context, c Change) {
	s.forgetEffective()
	if s.recorder == nil || s.isPasswordFile(c.Path) {
		return
	}
	c.Actor = ActorFrom(ctx)
	if err := s.recorder.Record(context.WithoutCancel(ctx), c); err != nil {
		slog.Warn("proxy configuration change was not recorded",
			"path", c.Path, "action", c.Action, "error", err)
	}
}

// KindOf is the engine that reads a recorded file, so a revision can be
// restored through WriteConfig and tested by the server it belongs to. A
// file in nginx's directory is nginx's even when the Caddyfile sits beside
// it; anything else the service records is the Caddyfile's.
func (s *Service) KindOf(full string) Kind {
	if full == s.nginxDir || strings.HasPrefix(full, s.nginxDir+string(os.PathSeparator)) {
		return KindNginx
	}
	return KindCaddy
}
