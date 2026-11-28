package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// FlareSolverr opens a site behind Cloudflare's browser check in a headless
// browser and hands Prowlarr the page and its cookies. Prowlarr uses it as an
// indexer proxy: one saved under the stack's own name, applied to the
// sources that carry its tag, and only for a request whose answer is
// Cloudflare's check. A tagged source whose site answers directly is never
// sent through it, so the stack tags every source.

const (
	// flareSolverrTag is the tag Prowlarr matches a source to the proxy by.
	flareSolverrTag = "flaresolverr"
	// flareSolverrProxy is the proxy's name in Prowlarr, the stack's own.
	flareSolverrProxy = "FlareSolverr" + arrOwnedSuffix
	// flareSolverrTimeout is how long FlareSolverr may take over one page,
	// in seconds: Prowlarr's own default.
	flareSolverrTimeout = 60
)

// prowlarrTag is one of GET tag.
type prowlarrTag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// flareSolverrTagID is the id of Prowlarr's flaresolverr tag, whatever its
// case: one the person made is the same tag. With create it is made when
// missing; without, a missing one is found false.
func (c *arrClient) flareSolverrTagID(ctx context.Context, create bool) (int, bool, error) {
	var tags []prowlarrTag
	if err := c.call(ctx, http.MethodGet, "tag", nil, &tags); err != nil {
		return 0, false, err
	}
	for _, tag := range tags {
		if strings.EqualFold(tag.Label, flareSolverrTag) && tag.ID > 0 {
			return tag.ID, true, nil
		}
	}
	if !create {
		return 0, false, nil
	}
	var made prowlarrTag
	if err := c.call(ctx, http.MethodPost, "tag", map[string]string{"label": flareSolverrTag}, &made); err != nil {
		return 0, false, err
	}
	if made.ID <= 0 {
		return 0, false, fmt.Errorf("Prowlarr made the tag %q but gave it no id", flareSolverrTag)
	}
	return made.ID, true, nil
}

// flareSolverrProxyTag is the tag the stack's FlareSolverr proxy is applied
// by, when Prowlarr has both: what an added source is given. A stack without
// FlareSolverr (Essential, or a release from before it) has neither.
func (c *arrClient) flareSolverrProxyTag(ctx context.Context) (int, bool, error) {
	tag, found, err := c.flareSolverrTagID(ctx, false)
	if err != nil || !found {
		return 0, false, err
	}
	var proxies []struct {
		Name string `json:"name"`
		Tags []int  `json:"tags"`
	}
	if err := c.call(ctx, http.MethodGet, "indexerproxy", nil, &proxies); err != nil {
		return 0, false, err
	}
	for _, proxy := range proxies {
		if proxy.Name == flareSolverrProxy && slices.Contains(proxy.Tags, tag) {
			return tag, true, nil
		}
	}
	return 0, false, nil
}

// prowlarrFlareSolverr makes the stack's FlareSolverr one of Prowlarr's
// indexer proxies, at its Compose name, applied by the flaresolverr tag, and
// gives that tag to every source Prowlarr has. Prowlarr's own test of the
// proxy (it asks FlareSolverr for its version) runs before it is saved.
func prowlarrFlareSolverr(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	client, err := newArrClient(ctx, env, arrProwlarr)
	if err != nil {
		return stacks.Result{}, err
	}
	tag, _, err := client.flareSolverrTagID(ctx, true)
	if err != nil {
		return stacks.Result{}, err
	}
	changed, id, err := client.ensureProvider(ctx, arrProvider{
		resource:       "indexerproxy",
		what:           "indexer proxy",
		name:           flareSolverrProxy,
		implementation: "FlareSolverr",
		top:            map[string]any{"tags": []int{tag}},
		fields: map[string]any{
			"host":           "http://flaresolverr:" + strconv.Itoa(PortFlareSolver) + "/",
			"requestTimeout": flareSolverrTimeout,
		},
	})
	if err != nil {
		return stacks.Result{}, err
	}
	if changed {
		env.Logf("Prowlarr's indexer proxy %q was saved after its own test passed", flareSolverrProxy)
	}
	tagged, untagged, err := client.tagSources(ctx, env, tag)
	if err != nil {
		return stacks.Result{}, err
	}
	message := "Prowlarr opens the sources behind Cloudflare through FlareSolverr"
	if !changed && tagged == 0 && len(untagged) == 0 {
		message = "Prowlarr already opens the sources behind Cloudflare through FlareSolverr"
	}
	switch {
	case len(untagged) > 0:
		message += fmt.Sprintf("; Prowlarr would not save %s with its tag yet, which the next pass tries again", strings.Join(untagged, ", "))
	case tagged > 0:
		message += fmt.Sprintf(", and %s tagged for it", prowlarrCount(tagged))
	}
	return stacks.Result{Message: message, Outputs: map[string]string{
		"proxyId": strconv.Itoa(id), "tagId": strconv.Itoa(tag),
	}}, nil
}

// tagSources gives the tag to every source of Prowlarr's that lacks it,
// keeping the source's other tags and settings: a secret Prowlarr answers
// as "********" keeps its stored value when it is sent back so. A source
// Prowlarr will not save now (its site is down, say) is named, not an error:
// it keeps working as it did, and the next pass tries again.
func (c *arrClient) tagSources(ctx context.Context, env stacks.Env, tag int) (int, []string, error) {
	var indexers []map[string]any
	if err := c.call(ctx, http.MethodGet, "indexer", nil, &indexers); err != nil {
		return 0, nil, err
	}
	tagged := 0
	untagged := []string{}
	for _, indexer := range indexers {
		id, found := arrID(indexer)
		if !found {
			continue
		}
		tags := sourceTags(indexer["tags"])
		if slices.Contains(tags, tag) {
			continue
		}
		name, _ := indexer["name"].(string)
		indexer["tags"] = append(tags, tag)
		if err := c.call(ctx, http.MethodPut, "indexer/"+strconv.Itoa(id)+"?forceSave=true", indexer, nil); err != nil {
			if ctx.Err() != nil {
				return 0, nil, ctx.Err()
			}
			env.Logf("Prowlarr did not save the source %q with the %s tag: %v", name, flareSolverrTag, err)
			untagged = append(untagged, strconv.Quote(name))
			continue
		}
		tagged++
		env.Logf("Prowlarr's source %q was given the %s tag", name, flareSolverrTag)
	}
	return tagged, untagged, nil
}

// sourceTags reads a resource's tags, decoded as JSON numbers.
func sourceTags(raw any) []int {
	list, _ := raw.([]any)
	tags := make([]int, 0, len(list)+1)
	for _, value := range list {
		switch number := value.(type) {
		case float64:
			tags = append(tags, int(number))
		case json.Number:
			if parsed, err := strconv.Atoi(number.String()); err == nil {
				tags = append(tags, parsed)
			}
		}
	}
	return tags
}
