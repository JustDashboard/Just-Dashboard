// Command jd drives a Just Dashboard's deployments from a terminal or a CI job.
//
// It speaks the dashboard's own HTTP API with an API key, normally a deploy
// key made under Account → API keys, and nothing else: there is no second
// protocol and no state on the server that the dashboard's pages cannot see.
// What a key may do is decided by the server on every request, so this
// program never assumes a refusal away; it explains it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// version is stamped by the build (-ldflags "-X main.version=…"); a binary
// built by hand says so instead of claiming a release it is not.
var version = "dev"

// Exit codes, as the usage text promises them to scripts.
const (
	exitOK        = 0
	exitRefused   = 1
	exitUsage     = 2
	exitNoSession = 3
)

const usageText = `jd — deploy to a Just Dashboard from the command line

Usage: jd [flags] COMMAND [ARGUMENTS]

Getting started
  login URL --key KEY           Check a key against the dashboard and remember both (--key - reads stdin)
  logout                        Forget the stored URL and key
  version                       Print jd's version

Deployments
  deployments [ls]              List deployments with their health and last run
  deploy APP [--wait] [--ref R] Queue a deploy; with --wait, follow it and exit with its result
  runs APP [--limit N]          List an application's runs
  logs APP [--run N] [--follow] Print a run's transcript (default: the active run, else the last)
  releases APP                  List retained releases
  rollback APP [--to ID] [--wait]  Go back to a release (default: the one before the live one)
  rollback APP --instant [--wait]  Switch traffic back to the release kept warm after the last deploy
  keep APP                      Keep the new release now and retire the warm one
  promote APP [--wait]          Promote staging's live release to production (--release ID for another,
                                --from-preview N for PR N's preview into staging)
  maintenance APP on|off        Switch the maintenance page at once (--bypass CIDR,... for who is let past)
  status APP                    Environments, live release, rollback watch, maintenance and rehearsal
  create --name N --image IMG [--port P]  Create a deployment from an image, through a draft

Variables (write-only: values are never shown, only their digests)
  vars ls APP                   Names, type, scopes and value digests
  vars set APP KEY=VALUE ...    Set variables; KEY=- reads that value from stdin
  vars import APP FILE.env      Set every variable in a .env file (FILE - reads stdin; --dry-run)
  vars rm APP KEY ...           Remove variables

Settings
  config get APP                Show an environment's configuration (--json for the document)
  config set APP FILE.json      Replace the configuration with a document (FILE - reads stdin)
  source set APP FILE.json      Replace the environment's source

Drafts (how a deployment is created)
  drafts ls | create | show ID | save ID FILE | detect ID | preflight ID | commit ID [--ack CODE] | rm ID
  A key narrowed to some deployments makes drafts of its own and reaches what it commits.

APP is a deployment's name or id. Commands that act on an environment take
--env NAME|ID; without it they use the deployment's production environment.

Flags (before or after the command)
  --json                  Print the server's JSON (JSON lines for logs) instead of tables
  --quiet                 Leave out hints
  --timeout D             How long --wait and --follow keep waiting (default 30m)
  --ca-file PEM           Trust this certificate authority too (JD_CA_FILE)
  --insecure-skip-verify  Do not check the dashboard's certificate (not recommended)
  --insecure-http         Allow sending the key over plain http to a non-loopback address

Environment
  JD_URL, JD_KEY          Override the stored URL and key, for CI; nothing is written
  JD_CONFIG               Use this file instead of the user config directory's jd/config.json

Exit codes
  0  done; for --wait and --follow, the run succeeded
  1  the dashboard refused or failed the request, or the run did not succeed
  2  usage error
  3  not logged in, or the key is invalid, revoked or expired
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// usageError is a mistake on the command line, answered with exit 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{fmt.Sprintf(format, args...)}
}

// errRunFailed means the command already said what happened, and only the
// exit status is left to give.
var errRunFailed = errors.New("run did not succeed")

// notLoggedIn is answered with exit 3, like a key the server refused.
type notLoggedIn struct{ msg string }

func (e *notLoggedIn) Error() string { return e.msg }

// app is one invocation: where it writes, what it reads, and the options it
// was given.
type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	env            func(string) string
	opts           options
	client         *client
	// usage prints the running command's usage, for -h.
	usage func()
}

// options are the flags every command takes, so they can be written before
// or after the command name.
type options struct {
	json         bool
	quiet        bool
	timeout      time.Duration
	caFile       string
	skipVerify   bool
	insecureHTTP bool
}

func (o *options) register(fs *flag.FlagSet) {
	fs.BoolVar(&o.json, "json", false, "print the server's JSON")
	fs.BoolVar(&o.quiet, "quiet", false, "leave out hints")
	fs.DurationVar(&o.timeout, "timeout", 30*time.Minute, "how long --wait and --follow keep waiting")
	fs.StringVar(&o.caFile, "ca-file", "", "trust this certificate authority too")
	fs.BoolVar(&o.skipVerify, "insecure-skip-verify", false, "do not check the dashboard's certificate")
	fs.BoolVar(&o.insecureHTTP, "insecure-http", false, "allow plain http to a non-loopback address")
}

// globalValueFlags are the shared flags that take a value, which the scan
// of the words before the command has to step over.
var globalValueFlags = map[string]bool{"timeout": true, "ca-file": true}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env func(string) string) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr, env: env}
	// Flags written before the command belong to every command, so they are
	// handed on in front of the command's own words.
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "-" {
		name := strings.TrimLeft(args[i], "-")
		switch name {
		case "h", "help":
			fmt.Fprint(stdout, usageText)
			return exitOK
		case "version":
			fmt.Fprintf(stdout, "jd %s\n", version)
			return exitOK
		}
		if !strings.Contains(name, "=") && globalValueFlags[name] && i+1 < len(args) {
			i++
		}
		i++
	}
	if i >= len(args) {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	pre, command, rest := args[:i], args[i], args[i+1:]
	words := append(append([]string{}, pre...), rest...)
	return a.finish(a.dispatch(command, pre, rest, words))
}

func (a *app) dispatch(command string, pre, rest, words []string) error {
	switch command {
	case "help":
		fmt.Fprint(a.stdout, usageText)
		return nil
	case "version":
		fmt.Fprintf(a.stdout, "jd %s\n", version)
		return nil
	case "login":
		return a.login(words)
	case "logout":
		return a.logout(words)
	case "deployments", "deployment", "ls":
		return a.deployments(words)
	case "deploy":
		return a.deploy(words)
	case "runs":
		return a.runs(words)
	case "logs":
		return a.logs(words)
	case "releases":
		return a.releases(words)
	case "rollback":
		return a.rollback(words)
	case "promote":
		return a.promote(words)
	case "keep":
		return a.keep(words)
	case "maintenance":
		return a.maintenance(words)
	case "status":
		return a.status(words)
	case "create":
		return a.create(words)
	case "vars", "variables":
		return a.nested("vars", pre, rest, map[string]func([]string) error{
			"ls": a.varsList, "list": a.varsList, "set": a.varsSet, "import": a.varsImport,
			"rm": a.varsRemove, "remove": a.varsRemove,
		})
	case "config":
		return a.nested("config", pre, rest, map[string]func([]string) error{
			"get": a.configGet, "show": a.configGet, "set": a.configSet,
		})
	case "source":
		return a.nested("source", pre, rest, map[string]func([]string) error{"set": a.sourceSet})
	case "drafts", "draft":
		return a.nested("drafts", pre, rest, map[string]func([]string) error{
			"ls": a.draftsList, "list": a.draftsList, "create": a.draftsCreate, "show": a.draftsShow,
			"save": a.draftsSave, "detect": a.draftsDetect, "preflight": a.draftsPreflight,
			"commit": a.draftsCommit, "rm": a.draftsRemove, "discard": a.draftsRemove,
		})
	default:
		return usagef("unknown command %q; jd help lists them", command)
	}
}

// nested runs a command with its own verbs: jd vars ls, jd drafts commit.
func (a *app) nested(name string, pre, rest []string, verbs map[string]func([]string) error) error {
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		if len(rest) > 0 && (rest[0] == "-h" || rest[0] == "--help") {
			fmt.Fprint(a.stdout, usageText)
			return nil
		}
		return usagef("jd %s needs a command: %s", name, verbList(verbs))
	}
	verb, ok := verbs[rest[0]]
	if !ok {
		return usagef("jd %s has no command %q; it has %s", name, rest[0], verbList(verbs))
	}
	return verb(append(append([]string{}, pre...), rest[1:]...))
}

func verbList(verbs map[string]func([]string) error) string {
	order := []string{"ls", "create", "show", "get", "set", "save", "import", "detect", "preflight", "commit", "rm"}
	var out []string
	for _, v := range order {
		if _, ok := verbs[v]; ok {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// finish turns what a command returned into what the terminal sees and the
// status a script reads.
func (a *app) finish(err error) int {
	var (
		usage  *usageError
		api    *apiError
		absent *notLoggedIn
	)
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errRunFailed):
		return exitRefused
	case errors.As(err, &usage):
		fmt.Fprintf(a.stderr, "jd: %s\n", usage.msg)
		return exitUsage
	case errors.As(err, &absent):
		fmt.Fprintf(a.stderr, "jd: %s\n", absent.msg)
		return exitNoSession
	case errors.As(err, &api):
		fmt.Fprintf(a.stderr, "jd: %s\n", err.Error())
		if api.Status == 401 {
			return exitNoSession
		}
		return exitRefused
	default:
		fmt.Fprintf(a.stderr, "jd: %v\n", err)
		return exitRefused
	}
}

// hint is advice a person reads and a script does not need.
func (a *app) hint(format string, args ...any) {
	if a.opts.quiet || a.opts.json {
		return
	}
	fmt.Fprintf(a.stderr, format+"\n", args...)
}

// say is a status line. It goes to stderr so that stdout stays what the
// command produced — a transcript, a table — for whoever pipes it.
func (a *app) say(format string, args ...any) {
	if a.opts.json {
		return
	}
	fmt.Fprintf(a.stderr, format+"\n", args...)
}
