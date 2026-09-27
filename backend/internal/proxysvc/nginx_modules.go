package proxysvc

import (
	"bytes"
	"context"
	"path"
	"strings"
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
}

// nginxBuild is what `nginx -V` says about how the binary was compiled.
type nginxBuild struct {
	// modules maps a module named by --with-<name> to "static" or "dynamic".
	modules     map[string]string
	prefix      string
	modulesPath string
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
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "configure arguments:"); ok {
			args = rest
		}
	}
	for _, arg := range splitShellWords(args) {
		switch {
		case strings.HasPrefix(arg, "--prefix="):
			build.prefix = strings.TrimPrefix(arg, "--prefix=")
		case strings.HasPrefix(arg, "--modules-path="):
			build.modulesPath = strings.TrimPrefix(arg, "--modules-path=")
		case strings.HasPrefix(arg, "--with-"):
			name, kind, dynamic := strings.Cut(strings.TrimPrefix(arg, "--with-"), "=")
			switch {
			case !dynamic:
				build.modules[name] = "static"
			case kind == "dynamic":
				build.modules[name] = "dynamic"
			}
		}
	}
	if build.modulesPath == "" {
		build.modulesPath = path.Join(build.prefix, "modules")
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

// loadsStreamModule reports whether a `nginx -T` dump loads the stream
// module. load_module is legal only in the main context, and nginx prints
// only the files it read, so any load_module naming the module in any printed
// file is one nginx acted on.
func loadsStreamModule(dump string) bool {
	for _, file := range ParseEffective(dump) {
		directives, err := ParseNginxFile(file.Path, file.Content, nil)
		if err != nil {
			continue
		}
		for _, d := range directives {
			if d.Name == "load_module" && len(d.Args) == 1 && path.Base(d.Args[0]) == streamModuleFile {
				return true
			}
		}
	}
	return false
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
	switch build.modules["stream"] {
	case "static":
		return StreamModule{State: ModuleStatic, Usable: true}
	case "":
		return StreamModule{State: ModuleAbsent}
	}

	module := StreamModule{Path: path.Join(build.modulesPath, streamModuleFile)}
	var stdout, stderr bytes.Buffer
	dump := hostexec.Command(ctx, "nginx", "-T")
	dump.Stdout, dump.Stderr = &stdout, &stderr
	dumpErr := dump.Run()
	switch {
	case dumpErr == nil && loadsStreamModule(stdout.String()):
		module.State, module.Usable = ModuleLoaded, true
		return module
	case dumpErr != nil && !strings.Contains(stderr.String(), `unknown directive "stream"`):
		// The configuration fails for another reason, so what it loads
		// cannot be read.
		module.State = ModuleUnknown
		module.Detail = failureLine(stderr.String())
		if module.Detail == "" {
			module.Detail = "nginx -T: " + dumpErr.Error()
		}
		return module
	}
	if hostexec.CommandOnHost(ctx, "test", "-e", module.Path).Run() == nil {
		module.State = ModuleNotLoaded
	} else {
		module.State = ModuleNotInstalled
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

// StreamModulePackage names the package that provides the stream module for
// a package manager, as updates.Service.Manager names it. Arch, openSUSE and
// nginx.org's own packages build the module in, so only the distributions
// that split it out have an answer.
func StreamModulePackage(manager string) string {
	switch manager {
	case "apt":
		return "libnginx-mod-stream"
	case "dnf", "yum", "apk":
		return "nginx-mod-stream"
	}
	return ""
}
