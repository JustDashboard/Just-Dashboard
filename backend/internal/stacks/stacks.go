// Package stacks configures a multi-service deployment once its release is
// live: the cross-service setup a person would otherwise do by hand in each
// application (accounts, API keys, folders, the links between them).
//
// A stack is an ordinary Compose template deployment. Everything the
// deployment engine already owns — building, starting, networks, volumes,
// domains, logs, backups, rollback — stays there. This package adds one thing
// on top: a set of reviewed, compiled-in configuration steps that run against
// the live release's containers over its private network, record what they
// did, and can be run again at any time without creating anything twice.
//
// A step is Go code in this repository, never data from a manifest: there is
// no way for a template, an input or an upstream answer to make the dashboard
// run a command it did not ship.
package stacks

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Stage groups steps for the progress the page shows. The deployment run
// owns the stages before these (validating, preparing storage, creating the
// network and secrets, starting services); a stack's own begin once its
// release is live.
type Stage string

const (
	StageReadiness  Stage = "readiness"
	StageInitialize Stage = "initialize"
	StageLink       Stage = "link"
	StageVerify     Stage = "verify"
)

// StepStatus is where one step stands.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	// StepAction: the step did what it could and needs the person for the
	// rest — a media source only they may add, an application someone else
	// already set up. It is not a failure, and the steps after it still run
	// unless they depend on it.
	StepAction  StepStatus = "action_required"
	StepSkipped StepStatus = "skipped"
)

// Step is one idempotent piece of configuration. Run must converge: read
// what is there, change only what differs from what the stack wants, and
// read it back. Running it twice in a row makes no second change.
type Step struct {
	// ID is stable across versions: the stack's record keys on it.
	// "<service>.<what>", such as "radarr.rootfolder".
	ID string
	// Service is the application the step configures, which the page groups
	// it under.
	Service string
	// Title is the step as the page names it: "Create the Movies and Shows
	// libraries".
	Title string
	Stage Stage
	// After are the steps that must be done first. A step after one that
	// failed or is skipped is skipped with it; one after a step that needs
	// an action still runs only if it is not listed here.
	After []string
	// Needs are the services the preset must run for this step to apply. A
	// step whose services are not all present is skipped, and its After
	// entries that are skipped do not block it.
	Needs []string
	// Timeout bounds one attempt. Attempts is how many are made before the
	// step is failed; between them the runner waits with backoff.
	Timeout  time.Duration
	Attempts int
	Run      func(ctx context.Context, env Env) (Result, error)
	// Withdraw, when set, takes back what Run made in another app once the
	// step's own Service is not part of a pass but every service it Needs
	// is: a rollback to a release from before the service came with the
	// template leaves that app pointing at a container that is gone. It runs
	// in Run's place, with Run's After, Timeout and Attempts, and the step
	// stays absent all the same: it is not drawn and holds no step after it.
	// What it did, or why it could not, is kept in the step's record only.
	Withdraw func(ctx context.Context, env Env) (Result, error)
}

// Result is what a step reports. Message is a sentence for the page
// ("Movies at /data/media/movies, Shows at /data/media/tv"); Outputs are
// non-secret facts later steps read (a quality profile's id, a library id).
// Neither may hold a credential.
type Result struct {
	Message string
	Outputs map[string]string
}

// Action is something only the person can do.
type Action struct {
	// ID is stable, so the page can say the same action is still open.
	ID          string `json:"id"`
	Service     string `json:"service"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Blocking: until it is done the stack is not ready for what it is for
	// (a stack that cannot acquire anything without a source).
	Blocking bool `json:"blocking"`
}

// ActionError is returned by a step that needs the person. The step is
// recorded as action_required with the action, not as failed.
type ActionError struct{ Action Action }

func (e *ActionError) Error() string { return e.Action.Title }

// NeedsAction is how a step says it needs the person.
func NeedsAction(action Action) error { return &ActionError{Action: action} }

// permanentError is a failure another attempt would not change: an
// application version this stack does not support, a refusal that is not a
// timing problem.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks an error as not worth retrying.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var permanent *permanentError
	return errors.As(err, &permanent)
}

// ExecResult is a command's outcome inside a service's container.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Env is everything a step may touch. The runner implements it over the live
// release's containers; tests implement it over httptest servers or over
// containers they started themselves.
type Env interface {
	// Preset is the preset this stack was deployed as ("essential",
	// "complete"); Has reports whether it runs a service.
	Preset() string
	Has(service string) bool
	// Option is one of the stack's options as the deployment's variables hold
	// it now ("quality" → "1080p"), or its default.
	Option(name string) string

	// BaseURL is "http://<address>:<port>" for a service's container port on
	// the stack's private network, which this process reaches directly. It
	// is looked up on every call: a container's address changes whenever it
	// is recreated.
	BaseURL(ctx context.Context, service string, port int) (string, error)
	// HTTP is the client to reach it with: bounded timeouts, no proxy from
	// the environment, no redirects followed off the stack's own addresses.
	HTTP() *http.Client

	// Variable is one of the deployment's own variables, by name — the API
	// keys the template generated and handed to the services as environment
	// (RADARR_API_KEY). The value is a secret: never log it or return it.
	Variable(ctx context.Context, name string) (string, error)
	// Credential is a secret the stack keeps for itself, sealed, by the
	// manifest's credential name: a password it generates on first use (an
	// administrator account) and returns unchanged ever after.
	Credential(ctx context.Context, name string) (string, error)
	// SetCredential stores a secret an application handed back (an API key
	// it generated), replacing any earlier value. LookupCredential reads one
	// without generating it.
	SetCredential(ctx context.Context, name, value string) error
	LookupCredential(ctx context.Context, name string) (string, bool, error)
	// DeleteCredential removes one the stack no longer needs: an
	// application's own password once it answers to the stack's one login.
	DeleteCredential(ctx context.Context, name string) error

	// PublicURL is the address the live release serves a service at
	// ("https://media-requests.example.com"), read from its plan's domains:
	// an HTTPS one when it has one, and the primary domain for the primary
	// service. False when the release serves the service at no domain.
	PublicURL(service string) (string, bool)
	// NetworkSubnets are the subnets of the networks a service's container is
	// on, from which the proxy in front of it reaches it.
	NetworkSubnets(ctx context.Context, service string) ([]string, error)

	// Exec runs argv in a service's container as user ("" for the image's
	// own). No shell is involved unless argv names one.
	Exec(ctx context.Context, service, user string, argv []string) (ExecResult, error)
	// Logs is the last lines a service's container printed.
	Logs(ctx context.Context, service string, tail int) (string, error)
	// ReadFile and WriteFile reach a file inside a service's container.
	ReadFile(ctx context.Context, service, path string) ([]byte, error)
	WriteFile(ctx context.Context, service, path string, content []byte, mode int64, uid, gid int) error

	// Output is a non-secret fact an earlier step reported, or "".
	Output(step, key string) string
	// Logf adds a line to the step's record (StepRecord.Log), masked as its
	// message and error are. Still never pass it a secret on purpose.
	Logf(format string, args ...any)
}
