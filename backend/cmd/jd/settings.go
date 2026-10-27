package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
)

// configurationView is the part of an environment's configuration the
// human form of jd config get shows; --json shows all of it.
type configurationView struct {
	Revision int `json:"revision"`
	Build    struct {
		Method        string `json:"method"`
		Recipe        string `json:"recipe"`
		Framework     string `json:"framework"`
		RootDirectory string `json:"rootDirectory"`
		Dockerfile    string `json:"dockerfile"`
	} `json:"build"`
	Runtime struct {
		Image        string   `json:"image"`
		Command      []string `json:"command"`
		InternalPort int      `json:"internalPort"`
		HostPort     int      `json:"hostPort"`
		BindAddress  string   `json:"bindAddress"`
		Strategy     string   `json:"strategy"`
		MemoryMB     int64    `json:"memoryMb"`
		CPUs         float64  `json:"cpus"`
	} `json:"runtime"`
	Domains []struct {
		Hostname string `json:"hostname"`
		HTTPS    bool   `json:"https"`
	} `json:"domains"`
	Checks []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Phase string `json:"phase"`
	} `json:"checks"`
	Variables []variable `json:"variables"`
	Source    *struct {
		Kind         string `json:"kind"`
		URL          string `json:"url"`
		Repository   string `json:"repository"`
		Ref          string `json:"ref"`
		Image        string `json:"image"`
		LocalPath    string `json:"localPath"`
		Subdirectory string `json:"subdirectory"`
		CredentialID int64  `json:"credentialId"`
	} `json:"source"`
	Pending *struct {
		Pending bool `json:"pending"`
	} `json:"pending"`
}

func (a *app) configGet(args []string) error {
	fs := a.newFlags("config get", "config get APP [--env NAME] [--json]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "config get APP [--env NAME] [--json]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var view configurationView
	raw, err := a.client.getJSON(t.envPath("/configuration"), nil, &view)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "deployment\t%s (%s)\n", t.row.Name, t.envName)
	fmt.Fprintf(w, "revision\t%d\n", view.Revision)
	if s := view.Source; s != nil {
		where := s.Image
		switch {
		case s.Repository != "":
			where = s.Repository
		case s.URL != "":
			where = s.URL
		case s.LocalPath != "":
			where = s.LocalPath
		}
		if s.Ref != "" {
			where += "@" + s.Ref
		}
		if s.Subdirectory != "" {
			where += " (" + s.Subdirectory + ")"
		}
		if s.CredentialID != 0 {
			where += fmt.Sprintf(", saved credential %d", s.CredentialID)
		}
		fmt.Fprintf(w, "source\t%s %s\n", s.Kind, where)
	}
	build := view.Build.Method
	for _, extra := range []string{view.Build.Recipe, view.Build.Framework, view.Build.Dockerfile} {
		if extra != "" {
			build += " · " + extra
		}
	}
	fmt.Fprintf(w, "build\t%s\n", orDash(build))
	runtime := []string{}
	if view.Runtime.Image != "" {
		runtime = append(runtime, view.Runtime.Image)
	}
	if view.Runtime.InternalPort != 0 {
		runtime = append(runtime, fmt.Sprintf("port %d", view.Runtime.InternalPort))
	}
	if view.Runtime.HostPort != 0 {
		runtime = append(runtime, fmt.Sprintf("published on %s:%d", orDash(view.Runtime.BindAddress), view.Runtime.HostPort))
	}
	if view.Runtime.Strategy != "" {
		runtime = append(runtime, strings.ReplaceAll(view.Runtime.Strategy, "_", " "))
	}
	if view.Runtime.MemoryMB != 0 {
		runtime = append(runtime, fmt.Sprintf("%d MiB", view.Runtime.MemoryMB))
	}
	if view.Runtime.CPUs != 0 {
		runtime = append(runtime, fmt.Sprintf("%g CPU", view.Runtime.CPUs))
	}
	if len(view.Runtime.Command) > 0 {
		runtime = append(runtime, "command "+strings.Join(view.Runtime.Command, " "))
	}
	fmt.Fprintf(w, "runtime\t%s\n", orDash(strings.Join(runtime, " · ")))
	domains := []string{}
	for _, d := range view.Domains {
		scheme := "http://"
		if d.HTTPS {
			scheme = "https://"
		}
		domains = append(domains, scheme+d.Hostname)
	}
	fmt.Fprintf(w, "domains\t%s\n", orDash(strings.Join(domains, ", ")))
	checks := []string{}
	for _, c := range view.Checks {
		checks = append(checks, fmt.Sprintf("%s (%s, %s)", c.Name, c.Kind, c.Phase))
	}
	fmt.Fprintf(w, "checks\t%s\n", orDash(strings.Join(checks, ", ")))
	names := []string{}
	for _, v := range view.Variables {
		names = append(names, v.Name)
	}
	fmt.Fprintf(w, "variables\t%s\n", orDash(strings.Join(names, ", ")))
	if view.Pending != nil && view.Pending.Pending {
		fmt.Fprintf(w, "pending\tchanges wait for the next deploy\n")
	}
	return w.Flush()
}

// configurationFields are what the configuration route takes. A document
// saved with jd config get --json carries the read-only fields too
// (variables, source, pending); they are dropped so the round trip works.
var configurationFields = []string{"revision", "build", "runtime", "dependencies", "checks", "domains"}

func (a *app) configSet(args []string) error {
	fs := a.newFlags("config set", "config set APP FILE.json [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 2, "config set APP FILE.json (FILE - reads stdin)"); err != nil {
		return err
	}
	document, err := a.readDocument(positional[1])
	if err != nil {
		return err
	}
	body := map[string]json.RawMessage{}
	for _, field := range configurationFields {
		if value, ok := document[field]; ok {
			body[field] = value
		}
	}
	for _, field := range []string{"build", "runtime"} {
		if _, ok := body[field]; !ok {
			return usagef("%s has no %q: jd config set replaces the whole configuration, as jd config get --json prints it", positional[1], field)
		}
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var saved struct {
		Revision int `json:"revision"`
	}
	put := func(revision int) error {
		body["revision"] = json.RawMessage(fmt.Sprint(revision))
		_, err := a.client.sendJSON(http.MethodPut, t.envPath("/configuration"), nil, body, &saved)
		return err
	}
	if err := a.writeAtRevision(t, body, put); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "saved the configuration of %s (%s); revision %d\n", t.row.Name, t.envName, saved.Revision)
	a.hint("It takes effect on the next deploy: jd deploy %s", t.row.Name)
	return nil
}

func (a *app) sourceSet(args []string) error {
	fs := a.newFlags("source set", "source set APP FILE.json [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 2, "source set APP FILE.json (FILE - reads stdin)"); err != nil {
		return err
	}
	document, err := a.readDocument(positional[1])
	if err != nil {
		return err
	}
	// The source as jd config get --json prints it is under "source"; the
	// route takes its fields at the top level beside the revision.
	if inner, ok := document["source"]; ok {
		if _, hasKind := document["kind"]; !hasKind {
			revision, hasRevision := document["revision"]
			document = map[string]json.RawMessage{}
			if err := json.Unmarshal(inner, &document); err != nil {
				return usagef("%s: source is not an object: %v", positional[1], err)
			}
			if hasRevision {
				document["revision"] = revision
			}
		}
	}
	if _, ok := document["kind"]; !ok {
		return usagef("%s has no source kind", positional[1])
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var saved struct {
		Revision int `json:"revision"`
	}
	put := func(revision int) error {
		document["revision"] = json.RawMessage(fmt.Sprint(revision))
		_, err := a.client.sendJSON(http.MethodPut, t.envPath("/source"), nil, document, &saved)
		return err
	}
	if err := a.writeAtRevision(t, document, put); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "saved the source of %s (%s); revision %d\n", t.row.Name, t.envName, saved.Revision)
	a.hint("It takes effect on the next deploy: jd deploy %s", t.row.Name)
	return nil
}

// writeAtRevision writes a document. A revision the document names is the
// one it was edited against, so a conflict there is reported rather than
// retried: retrying would replace somebody else's newer change. Without one
// the current revision is read, and a conflict is retried once.
func (a *app) writeAtRevision(t target, document map[string]json.RawMessage, put func(int) error) error {
	if raw, ok := document["revision"]; ok {
		var revision int
		if err := json.Unmarshal(raw, &revision); err != nil || revision <= 0 {
			return usagef("the document's revision is not a positive number")
		}
		err := put(revision)
		if isCode(err, "revision_conflict") {
			return fmt.Errorf("%w — the document was edited against revision %d; read it again with jd config get --json", err, revision)
		}
		return err
	}
	revision := 0
	return a.withRevision(t, &revision, put)
}

// readDocument reads a JSON object from a file, or stdin for "-".
func (a *app) readDocument(name string) (map[string]json.RawMessage, error) {
	text, err := a.readFile(name, 4<<20)
	if err != nil {
		return nil, err
	}
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		return nil, usagef("%s is not a JSON object: %v", name, err)
	}
	return document, nil
}
