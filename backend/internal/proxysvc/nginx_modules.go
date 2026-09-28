package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The stream module is the one part of nginx the Streams page cannot work
// without, and the one most often missing. Debian and Ubuntu build it as a
// dynamic module shipped in its own package, libnginx-mod-stream, which a
// plain `apt install nginx` does not pull in — so on those hosts a top-level
// `stream {}` block is "unknown directive", nginx fails its config test, and
// every reload is refused until someone removes it. Telling an operator to
// paste that block without first asking nginx whether it can read it is how a
// page about forwarding one port takes every site on the host down.

// Stream module states, as StreamModule reports them.
const (
	// ModuleStatic is compiled into the binary.
	ModuleStatic = "static"
	// ModuleLoaded is a dynamic module the configuration loads.
	ModuleLoaded = "loaded"
	// ModuleNotLoaded is installed as a dynamic module that nothing loads.
	ModuleNotLoaded = "not-loaded"
	// ModuleNotInstalled is built as a dynamic module whose file is absent.
	ModuleNotInstalled = "not-installed"
	// ModuleAbsent is left out of this build of nginx altogether.
	ModuleAbsent = "absent"
	// ModuleUnknown is what is reported when nginx could not be asked.
	ModuleUnknown = "unknown"
)

// StreamModule says whether this nginx can read a stream block at all.
type StreamModule struct {
	State string `json:"state"`
	// Usable is true for a module nginx has now; for ModuleUnknown it is
	// false, which the page words as "could not tell", never as "missing".
	Usable bool `json:"usable"`
	// Path is the shared object a dynamic build loads.
	Path string `json:"path,omitempty"`
	// Package is what provides the module on this host's package manager,
	// where there is one to name.
	Package string `json:"package,omitempty"`
	// Detail is nginx's own words when it could not be asked.
	Detail string `json:"detail,omitempty"`
	// SSL is whether the build has stream_ssl_module, which a stream's TLS
	// on either end needs. It is part of the stream module — built into its
	// shared object on a dynamic build — so it is read from nginx -V alone.
	SSL bool `json:"ssl"`
	// Preread is whether the build has stream_ssl_preread_module, which
	// reads the TLS name a stream routes by. Like SSL it is read from
	// nginx -V alone.
	Preread bool `json:"preread"`
	// RealIP is whether the build has stream_realip_module, which takes a
	// client's address from the PROXY header a load balancer sends. Read
	// from nginx -V alone, as SSL is.
	RealIP bool `json:"realip"`
}

// nginxBuild is what `nginx -V` says about how the binary was compiled.
type nginxBuild struct {
	// modules maps a module named by --with-<name> to "static" or "dynamic".
	modules map[string]string
	// order is the modules as configure listed them.
	order       []string
	prefix      string
	modulesPath string
	// confPath, errorLog and pidPath are where the build reads its
	// configuration and writes its log and pid when nothing says otherwise.
	confPath string
	errorLog string
	pidPath  string
	version  string
	openssl  string
}

// parseNginxBuild reads the configure arguments `nginx -V` prints. The
// arguments are split the way the shell that ran configure split them, so a
// quoted --with-cc-opt full of spaces is one argument, and each is matched
// whole: --with-stream_ssl_module is a part of the stream module, not the
// module, and Ubuntu builds it statically while leaving the module itself
// dynamic.
func parseNginxBuild(out string) nginxBuild {
	build := nginxBuild{modules: map[string]string{}, prefix: "/usr/local/nginx"}
	var args string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "configure arguments:"); ok {
			args = rest
		} else if rest, ok := strings.CutPrefix(line, "nginx version:"); ok {
			build.version = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "built with "); ok && strings.Contains(rest, "SSL") {
			build.openssl = strings.TrimSpace(rest)
		}
	}
	for _, arg := range splitShellWords(args) {
		switch {
		case strings.HasPrefix(arg, "--prefix="):
			build.prefix = strings.TrimPrefix(arg, "--prefix=")
		case strings.HasPrefix(arg, "--modules-path="):
			build.modulesPath = strings.TrimPrefix(arg, "--modules-path=")
		case strings.HasPrefix(arg, "--conf-path="):
			build.confPath = strings.TrimPrefix(arg, "--conf-path=")
		case strings.HasPrefix(arg, "--error-log-path="):
			build.errorLog = strings.TrimPrefix(arg, "--error-log-path=")
		case strings.HasPrefix(arg, "--pid-path="):
			build.pidPath = strings.TrimPrefix(arg, "--pid-path=")
		case strings.HasPrefix(arg, "--with-"):
			name, kind, dynamic := strings.Cut(strings.TrimPrefix(arg, "--with-"), "=")
			switch {
			case !dynamic:
				build.modules[name] = "static"
			case kind == "dynamic":
				build.modules[name] = "dynamic"
			default:
				continue
			}
			build.order = append(build.order, name)
		}
	}
	// configure's own defaults, which are relative to the prefix.
	for _, p := range []struct {
		value    *string
		fallback string
	}{
		{&build.modulesPath, "modules"}, {&build.confPath, "conf/nginx.conf"},
		{&build.errorLog, "logs/error.log"}, {&build.pidPath, "logs/nginx.pid"},
	} {
		if *p.value == "" {
			*p.value = p.fallback
		}
		if !path.IsAbs(*p.value) && *p.value != "stderr" {
			*p.value = path.Join(build.prefix, *p.value)
		}
	}
	return build
}

// splitShellWords splits on unquoted whitespace and drops the quotes, which
// is all configure's own echo of its arguments needs.
func splitShellWords(s string) []string {
	var words []string
	var cur strings.Builder
	var quote byte
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// streamModuleFile is the shared object a dynamic stream module is built as.
const streamModuleFile = "ngx_stream_module.so"

// loadedModules are the shared objects a `nginx -T` dump loads, by file
// name. load_module is legal only in the main context, and nginx prints only
// the files it read, so any load_module in any printed file is one nginx
// acted on.
func loadedModules(dump string) map[string]bool {
	loaded := map[string]bool{}
	for _, file := range ParseEffective(dump) {
		directives, err := ParseNginxFile(file.Path, file.Content, nil)
		if err != nil {
			continue
		}
		for _, d := range directives {
			if d.Name == "load_module" && len(d.Args) == 1 {
				loaded[path.Base(d.Args[0])] = true
			}
		}
	}
	return loaded
}

// StreamModule asks this host's nginx whether it has the stream module.
//
// A static build answers from `nginx -V` alone. A dynamic one is loaded only
// by a load_module line, which `nginx -T` shows — and whose absence it proves
// outright when a stream block is already there and nginx calls it an unknown
// directive. The module file is looked for on the host, where the package
// manager put it, to tell "not loaded" from "not installed".
//
// The dump is read without the service lock, which a certificate order can
// hold for minutes. It looks only for load_module lines, which no candidate
// the lock protects carries; a candidate that fails its test while the dump
// runs makes this one answer ModuleUnknown, and the next poll asks again.
func (s *Service) StreamModule(ctx context.Context) StreamModule {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !hostexec.Available("nginx") {
		return StreamModule{State: ModuleUnknown, Detail: "nginx was not found on this host"}
	}
	out, err := hostexec.Command(ctx, "nginx", "-V").CombinedOutput()
	if err != nil {
		return StreamModule{State: ModuleUnknown, Detail: strings.TrimSpace("nginx -V: " + firstLine(string(out)) + " " + err.Error())}
	}
	build := parseNginxBuild(string(out))
	ssl := build.modules["stream_ssl_module"] != ""
	preread := build.modules["stream_ssl_preread_module"] != ""
	realip := build.modules["stream_realip_module"] != ""
	switch build.modules["stream"] {
	case "static":
		return StreamModule{State: ModuleStatic, Usable: true, SSL: ssl, Preread: preread, RealIP: realip}
	case "":
		return StreamModule{State: ModuleAbsent}
	}

	module := StreamModule{Path: path.Join(build.modulesPath, streamModuleFile), SSL: ssl, Preread: preread, RealIP: realip}
	load := readLoadedModules(ctx)
	module.State = load.state(ctx, "stream", module.Path)
	module.Usable = module.State == ModuleLoaded
	if module.State == ModuleUnknown {
		// The configuration fails for another reason, so what it loads
		// cannot be read — or the file itself could not be looked for.
		module.Detail = load.detail
		if module.Detail == "" {
			module.Detail = "could not check for " + module.Path + " on the host"
		}
	}
	return module
}

// failureLine is the line of nginx's output that says why it failed: the
// first [emerg], since warnings can come before it, or else the first line.
func failureLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "[emerg]") {
			return strings.TrimSpace(line)
		}
	}
	return firstLine(output)
}

// ModulePackage names the package that provides a dynamic module, as
// configure names the module, for a package manager as updates.Service.Manager
// names it. Debian and Ubuntu ship each as libnginx-mod-<name>, Fedora, RHEL
// and Alpine as nginx-mod-<name>; the XSLT module is the one whose package,
// like its file, is called xslt-filter. Arch, openSUSE and nginx.org's own
// packages build the modules in, so only the distributions that split them out
// have an answer. The name is only a candidate: the caller asks the package
// manager whether it has it.
func ModulePackage(manager, module string) string {
	base := strings.TrimSuffix(module, "_module")
	if module == "http_xslt_module" {
		base = "http_xslt_filter"
	}
	base = strings.ReplaceAll(base, "_", "-")
	switch manager {
	case "apt":
		return "libnginx-mod-" + base
	case "dnf", "yum", "apk":
		return "nginx-mod-" + base
	}
	return ""
}

// ErrNoNginx is a module report asked of a host without nginx.
var ErrNoNginx = errors.New("nginx was not found on this host")

// NginxModule is one module this nginx was built with.
type NginxModule struct {
	// Name is the module as configure names it: http_v2_module, stream.
	Name string `json:"name"`
	// State is static, loaded, not-loaded, not-installed, or unknown when the
	// configuration could not be read to tell loaded from not.
	State string `json:"state"`
	// Path is the shared object a dynamic module loads from.
	Path string `json:"path,omitempty"`
	// Package provides a module that is not installed, named only where the
	// host's package manager has it.
	Package string `json:"package,omitempty"`
}

// NginxModules is what this nginx was built with, and what of it the
// configuration loads: the answer to "can this host do HTTP/3, stream,
// auth_request" before a page offers any of them.
type NginxModules struct {
	// Version is nginx's own "nginx/1.26.3 (Ubuntu)".
	Version string `json:"version"`
	// OpenSSL is the library it was built with, as nginx -V says it.
	OpenSSL     string        `json:"openssl,omitempty"`
	ModulesPath string        `json:"modulesPath"`
	Modules     []NginxModule `json:"modules"`
	// Detail is why a dynamic module's state could not be read.
	Detail string `json:"detail,omitempty"`
}

// isModuleName tells a module from the other --with- switches configure
// takes: threads, compat, pcre-jit, debug, file-aio.
func isModuleName(name string) bool {
	return strings.HasSuffix(name, "_module") || name == "stream" || name == "mail"
}

// moduleFile is the shared object a dynamic module is built as: the module's
// name behind ngx_, except XSLT, whose module is its filter.
func moduleFile(module string) string {
	switch module {
	case "stream", "mail":
		return "ngx_" + module + "_module.so"
	case "http_xslt_module":
		return "ngx_http_xslt_filter_module.so"
	}
	return "ngx_" + module + ".so"
}

// moduleReportTTL is how long a module report is kept. What a build contains
// changes only with a new binary, and what it loads with a package install,
// whose page asks again with fresh set.
const moduleReportTTL = time.Minute

type moduleReport struct {
	at     time.Time
	report *NginxModules
}

// moduleReports are the reports kept per service. Keyed by the service rather
// than held in it, since the report is this file's concern alone.
var moduleReports = struct {
	sync.Mutex
	by map[*Service]moduleReport
}{by: map[*Service]moduleReport{}}

// NginxModules reports every module `nginx -V` lists — each --with- module,
// not the ones compiled in by default — with whether this configuration has
// it: compiled in, loaded, installed and not loaded, or not installed. The
// report is kept for a minute unless fresh is set.
//
// Like StreamModule it reads `nginx -T` without the service lock, and a dump
// that fails for a reason other than a dynamic module's unknown directive
// leaves the installed ones unknown — never missing, and never loaded.
func (s *Service) NginxModules(ctx context.Context, fresh bool) (*NginxModules, error) {
	moduleReports.Lock()
	kept, ok := moduleReports.by[s]
	moduleReports.Unlock()
	if ok && !fresh && time.Since(kept.at) < moduleReportTTL {
		return kept.report, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !hostexec.Available("nginx") {
		return nil, ErrNoNginx
	}
	out, err := hostexec.Command(ctx, "nginx", "-V").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nginx -V: %s %v", firstLine(string(out)), err)
	}
	build := parseNginxBuild(string(out))
	report := &NginxModules{
		Version: build.version, OpenSSL: build.openssl, ModulesPath: build.modulesPath,
		Modules: []NginxModule{},
	}
	var load *loadState
	for _, name := range build.order {
		if !isModuleName(name) {
			continue
		}
		module := NginxModule{Name: name, State: ModuleStatic}
		if build.modules[name] == "dynamic" {
			if load == nil {
				load = readLoadedModules(ctx)
				report.Detail = load.detail
			}
			module.Path = path.Join(build.modulesPath, moduleFile(name))
			module.State = load.state(ctx, name, module.Path)
		}
		report.Modules = append(report.Modules, module)
	}
	moduleReports.Lock()
	moduleReports.by[s] = moduleReport{at: time.Now(), report: report}
	moduleReports.Unlock()
	return report, nil
}

// loadState is what a `nginx -T` dump says the configuration loads.
type loadState struct {
	// read is whether the dump succeeded, so loaded is the whole answer.
	read   bool
	loaded map[string]bool
	// unknown are the directives nginx called unknown when the dump failed.
	unknown []string
	// detail is why the dump failed, when it was not an unknown directive.
	detail string
}

// readLoadedModules reads what the configuration loads. A dump that fails on
// a dynamic module's own block — "stream", "mail" — proves that module is not
// loaded; detail is nginx's reason whenever the dump failed.
func readLoadedModules(ctx context.Context) *loadState {
	var stdout, stderr bytes.Buffer
	dump := hostexec.Command(ctx, "nginx", "-T")
	dump.Stdout, dump.Stderr = &stdout, &stderr
	if err := dump.Run(); err == nil {
		return &loadState{read: true, loaded: loadedModules(stdout.String())}
	}
	load := &loadState{loaded: map[string]bool{}}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if _, rest, ok := strings.Cut(line, `unknown directive "`); ok {
			if name, _, ok := strings.Cut(rest, `"`); ok {
				load.unknown = append(load.unknown, name)
			}
		}
	}
	load.detail = failureLine(stderr.String())
	return load
}

// state places one dynamic module: loaded by a load_module line, or else by
// whether its file is on the host. A file that is there while the
// configuration could not be read is unknown rather than "not loaded".
func (l *loadState) state(ctx context.Context, name, file string) string {
	if l.loaded[path.Base(file)] {
		return ModuleLoaded
	}
	err := hostexec.CommandOnHost(ctx, "test", "-e", file).Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return ModuleNotInstalled
	case err != nil:
		return ModuleUnknown
	case l.read:
		return ModuleNotLoaded
	}
	for _, directive := range l.unknown {
		if directive == name {
			return ModuleNotLoaded
		}
	}
	return ModuleUnknown
}
