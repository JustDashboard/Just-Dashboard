package proxysvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// managedRoute is one route file the ingress holds, as it was read.
type managedRoute struct {
	Path    string
	Content string
}

// managedRoutes is the managed routes directory at one moment.
type managedRoutes struct {
	// Integrated is false for a Caddy with no routes directory: an existing
	// server that deployments have not been attached to, which nothing here
	// may modify.
	Integrated bool
	Routes     []managedRoute
	// AgedCopies are the certificate copies old enough to prune, listed
	// only when the read asked for them.
	AgedCopies []string
}

// readRoutesScript prints every managed route in one run. $1 is the routes
// directory, $2 the boundary, $3 the certificate copies' directory (empty
// when they are not wanted) and $4 the age in minutes a copy must have.
//
// Each file is preceded by a line of its own, "<boundary> route <path>",
// after a newline that is not part of the file before it, so a file's bytes
// come back exactly, with or without a final newline. The boundary is random
// per run, so no route's content can be taken for a line of the framing.
// Only constant shell source is used; the paths are arguments.
const readRoutesScript = `if [ ! -d "$1" ]; then printf absent; exit 0; fi
for f in "$1"/just-dashboard-*.caddy; do
  if [ -f "$f" ] && [ ! -L "$f" ]; then
    printf '\n%s route %s\n' "$2" "$f"
    cat "$f" || exit 1
  fi
done
printf '\n%s copies\n' "$2"
if [ -n "$3" ] && [ -d "$3" ]; then
  if [ "$4" -gt 0 ]; then
    find "$3" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.key' \) -mmin +"$4" || exit 1
  else
    find "$3" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.key' \) || exit 1
  fi
fi
printf '%s end\n' "$2"`

// readRoutes reads every managed route in one exec, and with copies the
// certificate copies old enough to prune in the same one.
//
// The upkeep pass used to ask whether the directory was there, list it, and
// then test and cat each route: two execs and two more per route, every
// thirty seconds, and a third exec to list the copies. The site inventory did
// the same on every read of the Sites page.
func (c *dockerCaddy) readRoutes(ctx context.Context, copies bool) (managedRoutes, error) {
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return managedRoutes{}, err
	}
	boundary := "jd-" + hex.EncodeToString(seed[:])
	copiesDir, minutes := "", "0"
	if copies {
		copiesDir = dockerCaddyCertsRoot
		if certificateCopyGrace > 0 {
			minutes = strconv.Itoa(int(certificateCopyGrace / time.Minute))
		}
	}
	out, err := c.command(ctx, "", "sh", "-c", readRoutesScript, "sh", dockerCaddyRoot+"/routes", boundary, copiesDir, minutes)
	if err != nil {
		return managedRoutes{}, err
	}
	return parseManagedRoutes(string(out), boundary)
}

// parseManagedRoutes reads what readRoutesScript printed. Anything that is
// not exactly its framing is ErrRouteRecovery: a pass that cannot tell what
// the routes are must not repair them.
func parseManagedRoutes(out, boundary string) (managedRoutes, error) {
	if out == "absent" {
		return managedRoutes{}, nil
	}
	body, complete := strings.CutSuffix(out, boundary+" end\n")
	if !complete {
		return managedRoutes{}, ErrRouteRecovery
	}
	mark := "\n" + boundary + " copies\n"
	at := strings.LastIndex(body, mark)
	if at < 0 {
		return managedRoutes{}, ErrRouteRecovery
	}
	result := managedRoutes{Integrated: true, AgedCopies: strings.Fields(body[at+len(mark):])}
	files := strings.Split(body[:at], "\n"+boundary+" route ")
	if files[0] != "" {
		return managedRoutes{}, ErrRouteRecovery
	}
	for _, file := range files[1:] {
		path, content, found := strings.Cut(file, "\n")
		if !found || path == "" {
			return managedRoutes{}, ErrRouteRecovery
		}
		result.Routes = append(result.Routes, managedRoute{Path: path, Content: content})
	}
	return result, nil
}
