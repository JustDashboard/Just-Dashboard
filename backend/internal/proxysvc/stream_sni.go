package proxysvc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// One port, several TLS services: nginx reads the host name a client asks
// for from its TLS hello (ssl_preread) and forwards the connection, still
// encrypted, to the backend for that name. Each backend keeps its own
// certificate and nginx never holds a key — the way a second HTTPS service is
// put on a host whose 443 is already taken by a stream router, or several
// TLS services share one public port.
//
// The routes are a map from $ssl_preread_server_name to an upstream block of
// the stream's own, and the stream's pool is the map's default: a name no
// route matches, a client that sends no name, and a client that does not
// speak TLS at all go there. An upstream block per route rather than an
// address in the map keeps a host name resolved at load, as every other
// stream server is; a bare name in a variable proxy_pass needs a resolver.

// StreamRoute sends clients asking for one TLS name to one backend.
type StreamRoute struct {
	// Name is a host name, or *.example.com for every name under it.
	Name string `json:"name"`
	// Upstream is host:port or unix:/path, as a stream's Upstream is.
	Upstream string `json:"upstream"`
}

// maxStreamRoutes bounds the routes to what a form row per route can show.
const maxStreamRoutes = 64

// ErrNoStreamPreread refuses routes by name when nginx was built without
// stream_ssl_preread_module, which is what reads the name.
var ErrNoStreamPreread = errors.New("this nginx was built without stream_ssl_preread_module, so a stream cannot route by TLS name")

// streamSNIVar is the variable the stream's map sets: the upstream block for
// the name the client asked for.
func streamSNIVar(name string) string {
	return "$" + NginxIdent(name) + "_sni"
}

// streamRouteUpstream is the upstream block of a stream's i-th route,
// counted from one. Its suffix never ends in _backend, so it cannot meet
// another stream's pool.
func streamRouteUpstream(name string, i int) string {
	return NginxIdent(name) + "_sni" + strconv.Itoa(i+1)
}

// validStreamRoutes checks the routes by name. Routing reads the client's
// TLS without ending it, so nginx cannot also be the TLS end, nor wrap the
// passed-through TLS in a second layer to the backend.
func validStreamRoutes(spec *StreamSpec) error {
	if len(spec.Routes) == 0 {
		spec.Routes = nil
		return nil
	}
	if spec.Protocol != "tcp" {
		return fmt.Errorf("routing by TLS name is for TCP only")
	}
	if spec.TLS || spec.UpstreamTLS {
		return fmt.Errorf("routing by TLS name passes the client's TLS through unopened — turn off serving TLS and encrypting to the backend")
	}
	if len(spec.Routes) > maxStreamRoutes {
		return fmt.Errorf("a stream takes at most %d routes", maxStreamRoutes)
	}
	seen := map[string]bool{}
	for i := range spec.Routes {
		r := &spec.Routes[i]
		r.Name = strings.ToLower(strings.TrimSpace(r.Name))
		r.Upstream = strings.TrimSpace(r.Upstream)
		// A name with no dot is refused as well as being unusual in SNI: a
		// map line for "default", "include" or "hostnames" is read by nginx
		// as that keyword.
		if !validRouteName(r.Name) {
			return fmt.Errorf("route %d: the name must be a host name like app.example.com, or *.example.com", i+1)
		}
		if seen[r.Name] {
			return fmt.Errorf("%s is routed twice", r.Name)
		}
		seen[r.Name] = true
		if err := validStreamUpstream(r.Upstream); err != nil {
			return fmt.Errorf("route %d: %w", i+1, err)
		}
	}
	return nil
}

func validRouteName(name string) bool {
	return domainRe.MatchString(name) && strings.Contains(strings.TrimPrefix(name, "*."), ".")
}

// renderStreamRoutes writes the map and the routes' upstream blocks, which
// sit at the top of the stream context beside the pool's.
func renderStreamRoutes(l *lines, spec *StreamSpec) {
	l.add("# The name the client asks for in its TLS hello picks the backend;")
	l.add("# any other name, no name, or no TLS at all goes to the default.")
	l.add("map $ssl_preread_server_name %s {", streamSNIVar(spec.Name))
	l.add("    hostnames;")
	for i, r := range spec.Routes {
		l.add("    %s %s;", r.Name, streamRouteUpstream(spec.Name, i))
	}
	l.add("    default %s;", streamUpstreamName(spec.Name))
	l.add("}")
	l.blank()
	for i, r := range spec.Routes {
		l.add("upstream %s {", streamRouteUpstream(spec.Name, i))
		l.add("    server %s;", r.Upstream)
		l.add("}")
		l.blank()
	}
}

// readSNI takes a proxy_pass through the stream's own map: each route's
// upstream block holding one plain server, and the default the stream's pool,
// read as any proxy_pass target is.
func (p *parsedStream) readSNI(maps map[string]Directive, upstreams map[string]Directive, used map[string]bool) {
	variable := streamSNIVar(p.spec.Name)
	block, ok := maps[variable]
	if !ok {
		p.cannot("proxy_pass with a variable")
		return
	}
	p.sniMap = true
	hostnames, target := false, ""
	for _, d := range block.Block {
		switch {
		case d.Name == "hostnames" && len(d.Args) == 0 && !hostnames && len(p.spec.Routes) == 0:
			hostnames = true
		case d.Name == "default" && len(d.Args) == 1 && target == "":
			target = d.Args[0]
		case len(d.Args) == 1 && validRouteName(d.Name) && d.Name == strings.ToLower(d.Name):
			p.readRoute(d.Name, d.Args[0], upstreams, used)
		default:
			p.cannot("map line " + strings.TrimSpace(d.Name+" "+strings.Join(d.Args, " ")))
		}
	}
	if !hostnames {
		p.cannot("a name map without hostnames")
	}
	if target == "" {
		p.cannot("a name map without a default")
		return
	}
	p.readProxyPass(target, upstreams, used)
}

// readRoute takes one map line naming an upstream block of this file that
// holds one server with no options — what a route renders as.
func (p *parsedStream) readRoute(name, target string, upstreams map[string]Directive, used map[string]bool) {
	block, ok := upstreams[target]
	if !ok || used[target] || len(block.Block) != 1 || block.Block[0].Name != "server" ||
		len(block.Block[0].Args) != 1 || validStreamUpstream(block.Block[0].Args[0]) != nil {
		p.cannot("a route to " + target)
		return
	}
	for _, r := range p.spec.Routes {
		if r.Name == name {
			p.cannot(name + " routed twice")
			return
		}
	}
	used[target] = true
	p.spec.Routes = append(p.spec.Routes, StreamRoute{Name: name, Upstream: block.Block[0].Args[0]})
}

// checkSNI names a router the form cannot write back: preread without the
// map it feeds, or routes alongside TLS the form refuses with them.
func (p *parsedStream) checkSNI() {
	if p.preread != p.sniMap {
		p.cannot("ssl_preread without a name map, or the other way round")
	}
	if p.sniMap && len(p.spec.Routes) == 0 {
		p.cannot("a name map with no routes")
	}
	if len(p.spec.Routes) > 0 && (p.spec.TLS || p.spec.UpstreamTLS || p.spec.Protocol != "tcp") {
		p.cannot("routes by name with TLS or UDP")
	}
	if len(p.spec.Routes) > maxStreamRoutes {
		p.cannot(fmt.Sprintf("%d routes", len(p.spec.Routes)))
	}
}
