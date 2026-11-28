package media

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// The step makes the flaresolverr tag and the stack's indexer proxy at the
// Compose name, tested before it is saved, gives the tag to every source,
// keeping the source's own tags, and changes nothing the second time.
func TestProwlarrFlareSolverrSavesTheProxyAndTagsEverySource(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.prowlarr.passes = func(resource string, _ map[string]any, settings map[string]any) bool {
		return resource != "indexerproxy" || settings["host"] == "http://flaresolverr:8191/"
	}
	stack.prowlarr.tags = []map[string]any{{"id": 2, "label": "movies"}}
	stack.prowlarr.nextID = 20
	stack.prowlarr.indexers = []map[string]any{
		{"id": 4, "name": "Theirs", "enable": true, "tags": []any{2}, "fields": []any{map[string]any{"name": "apiKey", "value": arrFakeMask}}},
		{"id": 5, "name": "Mine", "enable": false, "tags": []any{}, "fields": []any{}},
	}

	first := arrRun(t, stack.env, "prowlarr.flaresolverr")
	arrWant(t, stack.prowlarr.written(), []string{"POST tag", "POST indexerproxy", "PUT indexer/4", "PUT indexer/5"})
	if first.Outputs["tagId"] != "20" || first.Outputs["proxyId"] != "21" || !strings.Contains(first.Message, "and 2 media sources tagged for it") {
		t.Fatalf("first run: %+v", first)
	}
	proxy := stack.prowlarr.lists["indexerproxy"][0]
	settings := stack.prowlarr.settings("indexerproxy", 21, proxy)
	if proxy["name"] != "FlareSolverr (Just Dashboard)" || proxy["implementation"] != "FlareSolverr" ||
		settings["host"] != "http://flaresolverr:8191/" || settings["requestTimeout"] != float64(60) {
		t.Fatalf("saved proxy: %v %v", proxy, settings)
	}
	if tags, _ := proxy["tags"].([]any); len(tags) != 1 || tags[0] != float64(20) {
		t.Fatalf("proxy tags = %v", proxy["tags"])
	}
	theirs := stack.prowlarr.indexers[0]
	if tags := sourceTags(theirs["tags"]); !slices.Equal(tags, []int{2, 20}) {
		t.Fatalf("a source's own tag was not kept: %v", tags)
	}
	// What Prowlarr keeps secret goes back as the mask it read, which
	// Prowlarr takes as "keep the stored one".
	if fields, _ := theirs["fields"].([]any); len(fields) != 1 || fields[0].(map[string]any)["value"] != arrFakeMask {
		t.Fatalf("fields sent back = %v", theirs["fields"])
	}

	stack.forget()
	second := arrRun(t, stack.env, "prowlarr.flaresolverr")
	arrWant(t, stack.prowlarr.written(), nil)
	if strings.Contains(second.Message, "tagged for it") || !strings.Contains(second.Message, "already") {
		t.Fatalf("second run: %q", second.Message)
	}
}

// A tag the person made under the name is the same tag; a source Prowlarr
// will not save now is named and tried again, not a failure; a proxy
// Prowlarr cannot reach fails with Prowlarr's reason.
func TestProwlarrFlareSolverrUsesTheirTagAndNamesASourceItCouldNotTag(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.prowlarr.tags = []map[string]any{{"id": 7, "label": "FlareSolverr"}}
	stack.prowlarr.indexers = []map[string]any{{"id": 4, "name": "Down", "enable": true, "tags": []any{}, "fields": []any{}}}
	stack.prowlarr.refuse[4] = true

	result := arrRun(t, stack.env, "prowlarr.flaresolverr")
	arrWant(t, stack.prowlarr.written(), []string{"POST indexerproxy"})
	if result.Outputs["tagId"] != "7" || !strings.Contains(result.Message, `would not save "Down" with its tag yet`) {
		t.Fatalf("result: %+v", result)
	}

	stack.forget()
	stack.prowlarr.lists["indexerproxy"] = nil
	stack.prowlarr.passes = func(resource string, _ map[string]any, _ map[string]any) bool { return resource != "indexerproxy" }
	err := arrFail(t, stack.env, "prowlarr.flaresolverr")
	if !strings.Contains(err.Error(), "Unable to connect to proxy") || !strings.Contains(err.Error(), "FlareSolverr (Just Dashboard)") {
		t.Fatalf("err = %v", err)
	}
	arrWant(t, stack.prowlarr.written(), nil)
}

// A pass whose release runs no FlareSolverr (a rollback to one from before it
// came with the template) takes the stack's own proxy out of Prowlarr, and
// nothing else: a proxy the person made, the tag and the sources' tags stay.
// A second pass finds nothing to take.
func TestProwlarrFlareSolverrWithdrawRemovesOnlyTheStacksProxy(t *testing.T) {
	stack := newArrFakeStack(t)
	stack.prowlarr.tags = []map[string]any{{"id": 7, "label": "flaresolverr"}}
	stack.prowlarr.indexers = []map[string]any{{"id": 4, "name": "Theirs", "enable": true, "tags": []any{7}, "fields": []any{}}}
	stack.prowlarr.lists["indexerproxy"] = []map[string]any{
		{"id": 2, "name": "FlareSolverr (Just Dashboard)", "implementation": "FlareSolverr", "tags": []any{7}, "fields": []any{}},
		{"id": 3, "name": "My FlareSolverr", "implementation": "FlareSolverr", "tags": []any{7}, "fields": []any{}},
		{"id": 5, "name": "FlareSolverr (Just Dashboard) proxy", "implementation": "Http", "tags": []any{}, "fields": []any{}},
	}
	if stack.env.Has("flaresolverr") {
		t.Fatal("the fake stack runs FlareSolverr")
	}
	withdraw := arrStep(t, "prowlarr.flaresolverr").Withdraw
	if withdraw == nil {
		t.Fatal("the FlareSolverr step takes nothing back")
	}

	first, err := withdraw(context.Background(), stack.env)
	if err != nil {
		t.Fatal(err)
	}
	arrWant(t, stack.prowlarr.written(), []string{"DELETE indexerproxy/2"})
	if !strings.Contains(first.Message, "no longer sends sources through a FlareSolverr") {
		t.Fatalf("first: %q", first.Message)
	}
	left := []string{}
	for _, proxy := range stack.prowlarr.lists["indexerproxy"] {
		left = append(left, proxy["name"].(string))
	}
	if !slices.Equal(left, []string{"My FlareSolverr", "FlareSolverr (Just Dashboard) proxy"}) {
		t.Fatalf("proxies left: %v", left)
	}
	if tags, _ := stack.prowlarr.indexers[0]["tags"].([]any); len(stack.prowlarr.tags) != 1 || len(tags) != 1 || tags[0] != 7 {
		t.Fatalf("the tag was touched: %v %v", stack.prowlarr.tags, stack.prowlarr.indexers[0]["tags"])
	}

	stack.forget()
	second, err := withdraw(context.Background(), stack.env)
	if err != nil {
		t.Fatal(err)
	}
	arrWant(t, stack.prowlarr.written(), nil)
	if !strings.Contains(second.Message, "no FlareSolverr proxy of the stack's") {
		t.Fatalf("second: %q", second.Message)
	}
}
