package main

import "net/http"

// openapi prints the dashboard's description of the deployment API as the
// server serves it, for a client generator: jd openapi > deploy-openapi.json.
// It is the one route of this API that describes the rest, so it is printed
// as it came rather than reshaped here.
func (a *app) openapi(args []string) error {
	fs := a.newFlags("openapi", "openapi [--yaml]")
	yaml := fs.Bool("yaml", false, "print the description as YAML instead of JSON")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usagef("usage: jd openapi [--yaml]")
	}
	c, err := a.api()
	if err != nil {
		return err
	}
	path := "/deploy/openapi.json"
	if *yaml {
		path = "/deploy/openapi.yaml"
	}
	raw, err := c.do(http.MethodGet, path, nil, nil)
	if err != nil {
		return err
	}
	a.printRaw(raw)
	return nil
}
