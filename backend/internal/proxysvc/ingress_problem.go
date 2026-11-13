package proxysvc

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The codes of an IngressProblem.
const (
	// IngressUnreachable is Docker not saying which containers run: the
	// daemon stopped, its socket not there or not readable.
	IngressUnreachable = "ingress_unreachable"
	// IngressNotRunning is the container JD_INGRESS_CONTAINER names not
	// running, or not there at all.
	IngressNotRunning = "ingress_not_running"
	// IngressUnreadable is an ingress that was found and then did not let
	// its routes be read.
	IngressUnreadable = "ingress_unreadable"
	// IngressAmbiguous is more than one public Caddy, between which the
	// dashboard does not choose.
	IngressAmbiguous = "ingress_ambiguous"
	// IngressUnusable is a Caddy that deployments cannot share as it runs:
	// no HTTPS port, nothing persisted, not serving /etc/caddy/Caddyfile.
	IngressUnusable = "ingress_unusable"
)

// IngressProblem is the Docker ingress lookup not ending in an answer: Docker
// could not be asked, or what it said does not name one Caddy the deployments
// can share. It is neither "there is an ingress" nor "there is none", and it
// used to reach a page as "internal error".
//
// Message is the sentence for a person, Advice what they can do about it, and
// Unwrap what Docker or the lookup said. The lookup returns nothing else, so a
// caller can tell it from a failure of its own with errors.As.
type IngressProblem struct {
	Code    string
	Message string
	Advice  string
	cause   error
}

func (p *IngressProblem) Error() string {
	if p.cause != nil {
		return p.Message + ": " + p.cause.Error()
	}
	return p.Message
}

func (p *IngressProblem) Unwrap() error { return p.cause }

// Raw is what Docker or the lookup said, for a page's details. It is empty
// when the sentence is all there is to say.
func (p *IngressProblem) Raw() string {
	if p.cause == nil {
		return ""
	}
	return p.cause.Error()
}

// Retryable says the same request may work a moment later with nothing
// changed by hand: Docker or the container answering again. The others stand
// until the operator changes the containers or names one.
func (p *IngressProblem) Retryable() bool {
	return p.Code == IngressUnreachable || p.Code == IngressNotRunning || p.Code == IngressUnreadable
}

// IngressNotice is an IngressProblem as GET /proxy/status carries it, under
// the names an API error uses for the same things so a page draws both alike.
type IngressNotice struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Reason    string `json:"reason,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

// ingressNotice is the notice for what the lookup returned, nil for nil.
func ingressNotice(err error) *IngressNotice {
	if err == nil {
		return nil
	}
	problem := asIngressProblem(err)
	return &IngressNotice{Code: problem.Code, Message: problem.Message, Reason: problem.Advice, Raw: problem.Raw(), Retryable: problem.Retryable()}
}

// asIngressProblem is err as the problem it is. The lookup only returns
// problems; anything else is Docker not having answered.
func asIngressProblem(err error) *IngressProblem {
	var problem *IngressProblem
	if errors.As(err, &problem) {
		return problem
	}
	return ingressUnreachable(err)
}

const ingressSitesUnaffected = "Sites served by nginx and by the host's Caddyfile are read from their files and are not affected."

func ingressUnreachable(cause error) *IngressProblem {
	return &IngressProblem{
		Code:    IngressUnreachable,
		Message: "Docker did not say which containers are running, so the Caddy ingress that deployments share could not be looked up",
		Advice:  "The Docker daemon may be stopped, or the dashboard may not be able to read its socket. " + ingressSitesUnaffected + " Deployment routes are not listed until Docker answers.",
		cause:   commandFailure(cause),
	}
}

func ingressUnreadable(name string, cause error) *IngressProblem {
	return &IngressProblem{
		Code:    IngressUnreadable,
		Message: fmt.Sprintf("The Caddy ingress %s is running but its routes could not be read", name),
		Advice:  "The container may be restarting, or its admin endpoint on 127.0.0.1:2019 may be off. Nothing was changed; reading again once the container has settled is the remedy.",
		cause:   commandFailure(cause),
	}
}

func ingressWithoutHTTPS(name string) *IngressProblem {
	return &IngressProblem{
		Code:    IngressUnusable,
		Message: fmt.Sprintf("Caddy container %s owns HTTP but does not publish HTTPS on port 443", name),
		Advice:  "Deployments share a Caddy that publishes 80 and 443 on every address. Publish 443 on that container, or name the container deployments should use in JD_INGRESS_CONTAINER and restart the dashboard. " + ingressSitesUnaffected,
	}
}

func ingressAmbiguous() *IngressProblem {
	return &IngressProblem{
		Code:    IngressAmbiguous,
		Message: "More than one public Caddy container is running, and the dashboard does not choose between them",
		Advice:  "Name the one deployments should use in JD_INGRESS_CONTAINER and restart the dashboard. " + ingressSitesUnaffected,
	}
}

func ingressNotPersisted(name string) *IngressProblem {
	return &IngressProblem{
		Code:    IngressUnusable,
		Message: fmt.Sprintf("Caddy container %s needs a persisted Caddyfile, /config and /data before deployments can share it", name),
		Advice:  "Bind-mount its /etc/caddy/Caddyfile and give it writable /config and /data, so routes and certificates outlive the container. " + ingressSitesUnaffected,
	}
}

func ingressNotRunning(name string, cause error) *IngressProblem {
	return &IngressProblem{
		Code:    IngressNotRunning,
		Message: fmt.Sprintf("The ingress container %s is not running", name),
		Advice:  "JD_INGRESS_CONTAINER names it, and nothing else is used in its place. Start that container, or correct the name and restart the dashboard.",
		cause:   commandFailure(cause),
	}
}

func ingressNotCaddy(name string) *IngressProblem {
	return &IngressProblem{
		Code:    IngressUnusable,
		Message: fmt.Sprintf("The ingress container %s is not a Caddy serving /etc/caddy/Caddyfile", name),
		Advice:  "JD_INGRESS_CONTAINER must name a container whose command is `caddy run --config /etc/caddy/Caddyfile`. Correct the name and restart the dashboard.",
	}
}

// commandFailure is a command's error with what it printed. The docker CLI
// says why on stderr and exits 1, and "exit status 1" alone names nothing.
func commandFailure(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if said := strings.TrimSpace(string(exit.Stderr)); said != "" {
			return fmt.Errorf("%s (%w)", said, err)
		}
	}
	return err
}
