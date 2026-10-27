package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
)

// variable is one environment variable as the list states it: everything
// but the value, which no route gives a key.
type variable struct {
	Name        string   `json:"name"`
	Sensitivity string   `json:"sensitivity"`
	Scopes      []string `json:"scopes"`
	ValueDigest string   `json:"valueDigest"`
	Reference   *struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
	} `json:"reference"`
	Pending   bool   `json:"pending"`
	CreatedBy string `json:"createdBy"`
}

// revision is the environment's desired revision, which every write names so
// two writers cannot silently undo each other.
func (a *app) revision(t target) (int, error) {
	var configuration struct {
		Revision int `json:"revision"`
	}
	if _, err := a.client.getJSON(t.envPath("/configuration"), nil, &configuration); err != nil {
		return 0, err
	}
	return configuration.Revision, nil
}

// withRevision runs a write at the current revision and, when somebody else
// wrote in between, once more at the new one. A second conflict is reported:
// two writers racing are for a person to sort out.
func (a *app) withRevision(t target, revision *int, write func(int) error) error {
	if *revision == 0 {
		r, err := a.revision(t)
		if err != nil {
			return err
		}
		*revision = r
	}
	err := write(*revision)
	if !isCode(err, "revision_conflict") {
		return err
	}
	r, err := a.revision(t)
	if err != nil {
		return err
	}
	*revision = r
	return write(r)
}

func (a *app) varsList(args []string) error {
	fs := a.newFlags("vars ls", "vars ls APP [--env NAME]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "vars ls APP [--env NAME]"); err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	var list []variable
	raw, err := a.client.getJSON(t.envPath("/variables"), nil, &list)
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	a.printVariables(t, list)
	return nil
}

// printVariables is the table every variable command ends with. It has no
// value column because there is no value to put in it.
func (a *app) printVariables(t target, list []variable) {
	if len(list) == 0 {
		a.say("%s (%s) has no variables.", t.row.Name, t.envName)
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tSCOPES\tDIGEST\tSTATE")
	for _, v := range list {
		digest := shortDigest(v.ValueDigest)
		if v.Reference != nil {
			digest = "→ " + v.Reference.Kind + "." + v.Reference.Target
		}
		state := "applied"
		if v.Pending {
			state = "pending deploy"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", v.Name, v.Sensitivity, strings.Join(v.Scopes, ","), orDash(digest), state)
	}
	w.Flush()
}

// variableFlags are the type and scopes a write gives its variables.
type variableFlags struct {
	env    *string
	plain  *bool
	secret *bool
	scopes *string
}

func (a *app) addVariableFlags(fs *flag.FlagSet) variableFlags {
	return variableFlags{
		env:    fs.String("env", "", "environment name or id"),
		plain:  fs.Bool("plain", false, "store as a plain variable"),
		secret: fs.Bool("secret", false, "store as a secret (the default)"),
		scopes: fs.String("scope", "runtime", "where the value is given: runtime, build, release_task, comma-separated"),
	}
}

func (f variableFlags) resolve() (string, []string, error) {
	if *f.plain && *f.secret {
		return "", nil, usagef("--plain and --secret cannot both be given")
	}
	sensitivity := "secret"
	if *f.plain {
		sensitivity = "plain"
	}
	var scopes []string
	for _, s := range strings.Split(*f.scopes, ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) == 0 {
		return "", nil, usagef("--scope needs at least one of runtime, build, release_task")
	}
	return sensitivity, scopes, nil
}

// readValue is stdin's whole content less the one newline a shell or a pipe
// adds; a value that should end in a newline keeps any others.
func (a *app) readValue(name string) (string, error) {
	// Typed at a terminal the value is echoed, so jd says what it is waiting
	// for; a pipe needs no prompt.
	if f, ok := a.stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintf(a.stderr, "Value for %s (ends at Ctrl-D; a pipe keeps it off the screen):\n", name)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
	if err != nil {
		return "", err
	}
	value := string(raw)
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	return value, nil
}

func (a *app) varsSet(args []string) error {
	fs := a.newFlags("vars set", "vars set APP KEY=VALUE [KEY=- ...] [--plain] [--scope runtime,build]")
	flags := a.addVariableFlags(fs)
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return usagef("usage: jd vars set APP KEY=VALUE [KEY2=VALUE2 ...]; KEY=- reads that value from stdin")
	}
	sensitivity, scopes, err := flags.resolve()
	if err != nil {
		return err
	}
	type assignment struct{ name, value string }
	var (
		assignments []assignment
		fromStdin   bool
		inline      bool
	)
	for _, word := range positional[1:] {
		name, value, ok := strings.Cut(word, "=")
		if !ok || name == "" {
			return usagef("%q is not KEY=VALUE; to read a value from stdin write KEY=-", maskAssignment(word))
		}
		if value == "-" {
			if fromStdin {
				return usagef("only one variable can read its value from stdin")
			}
			fromStdin = true
			if value, err = a.readValue(name); err != nil {
				return err
			}
		} else {
			inline = true
		}
		assignments = append(assignments, assignment{name, value})
	}
	if inline {
		a.hint("Note: a value typed on the command line can end up in your shell history; KEY=- reads one from stdin instead.")
	}
	t, err := a.resolve(positional[0], *flags.env)
	if err != nil {
		return err
	}
	revision := 0
	for _, set := range assignments {
		var result struct {
			Variable        variable `json:"variable"`
			DesiredRevision int      `json:"desiredRevision"`
		}
		err := a.withRevision(t, &revision, func(r int) error {
			_, err := a.client.sendJSON(http.MethodPut, t.envPath("/variables/"+url.PathEscape(set.name)), nil,
				map[string]any{"revision": r, "value": set.value, "sensitivity": sensitivity, "scopes": scopes}, &result)
			return err
		})
		if err != nil {
			return fmt.Errorf("setting %s: %w", set.name, err)
		}
		revision = result.DesiredRevision
		digest := shortDigest(result.Variable.ValueDigest)
		if a.opts.json {
			fmt.Fprintf(a.stdout, `{"name":%q,"sensitivity":%q,"valueDigest":%q,"desiredRevision":%d}`+"\n",
				set.name, result.Variable.Sensitivity, result.Variable.ValueDigest, result.DesiredRevision)
			continue
		}
		fmt.Fprintf(a.stdout, "set %s (%s, %s) %s\n", set.name, sensitivity, strings.Join(scopes, ","), digest)
	}
	a.hint("The new values reach %s on its next deploy: jd deploy %s", t.row.Name, t.row.Name)
	return nil
}

// maskAssignment keeps a mistyped KEY=VALUE's value out of the error that
// repeats it.
func maskAssignment(word string) string {
	if name, _, ok := strings.Cut(word, "="); ok {
		return name + "=…"
	}
	if len(word) > 3 {
		return word[:3] + "…"
	}
	return word
}

func (a *app) varsImport(args []string) error {
	fs := a.newFlags("vars import", "vars import APP FILE.env [--dry-run] [--plain] [--scope runtime]")
	flags := a.addVariableFlags(fs)
	dryRun := fs.Bool("dry-run", false, "say what the import would change, and change nothing")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 2, "vars import APP FILE.env (FILE - reads stdin)"); err != nil {
		return err
	}
	sensitivity, scopes, err := flags.resolve()
	if err != nil {
		return err
	}
	dotenv, err := a.readFile(positional[1], 1<<20)
	if err != nil {
		return err
	}
	t, err := a.resolve(positional[0], *flags.env)
	if err != nil {
		return err
	}
	if *dryRun {
		var preview struct {
			Variables []struct {
				Name   string `json:"name"`
				Line   int    `json:"line"`
				Change string `json:"change"`
				Reason string `json:"reason"`
			} `json:"variables"`
		}
		raw, err := a.client.sendJSON(http.MethodPost, t.envPath("/variables/import"), query("dryRun", "1"),
			map[string]any{"dotenv": dotenv, "sensitivity": sensitivity, "scopes": scopes}, &preview)
		if err != nil {
			return err
		}
		if a.opts.json {
			a.printRaw(raw)
			return nil
		}
		w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tLINE\tCHANGE\tREASON")
		for _, v := range preview.Variables {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", v.Name, v.Line, v.Change, orDash(v.Reason))
		}
		w.Flush()
		a.say("Dry run: nothing was changed.")
		return nil
	}
	revision := 0
	var result struct {
		Variables       []variable `json:"variables"`
		DesiredRevision int        `json:"desiredRevision"`
	}
	var raw []byte
	err = a.withRevision(t, &revision, func(r int) error {
		var err error
		raw, err = a.client.sendJSON(http.MethodPost, t.envPath("/variables/import"), nil,
			map[string]any{"revision": r, "dotenv": dotenv, "sensitivity": sensitivity, "scopes": scopes}, &result)
		return err
	})
	if err != nil {
		return err
	}
	if a.opts.json {
		a.printRaw(raw)
		return nil
	}
	a.say("Imported into %s (%s); the environment now has these variables:", t.row.Name, t.envName)
	a.printVariables(t, result.Variables)
	return nil
}

func (a *app) varsRemove(args []string) error {
	fs := a.newFlags("vars rm", "vars rm APP KEY [KEY ...]")
	env := fs.String("env", "", "environment name or id")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return usagef("usage: jd vars rm APP KEY [KEY ...]")
	}
	t, err := a.resolve(positional[0], *env)
	if err != nil {
		return err
	}
	revision := 0
	for _, name := range positional[1:] {
		var result struct {
			DesiredRevision int `json:"desiredRevision"`
		}
		err := a.withRevision(t, &revision, func(r int) error {
			_, err := a.client.sendJSON(http.MethodDelete, t.envPath("/variables/"+url.PathEscape(name)), nil,
				map[string]any{"revision": r}, &result)
			return err
		})
		if err != nil {
			return fmt.Errorf("removing %s: %w", name, err)
		}
		revision = result.DesiredRevision
		fmt.Fprintf(a.stdout, "removed %s\n", name)
	}
	return nil
}

// readFile reads a file argument, or stdin for "-".
func (a *app) readFile(name string, limit int64) (string, error) {
	var (
		raw []byte
		err error
	)
	if name == "-" {
		raw, err = io.ReadAll(io.LimitReader(a.stdin, limit+1))
	} else {
		var f *os.File
		if f, err = os.Open(name); err != nil {
			return "", err
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, limit+1))
	}
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > limit {
		return "", fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	return string(raw), nil
}
