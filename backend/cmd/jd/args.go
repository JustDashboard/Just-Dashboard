package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// newFlags is a command's flag set with the shared flags already on it.
func (a *app) newFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a.opts.register(fs)
	// The flag package prints its usage on every mistake; jd prints it only
	// when asked, and says what the mistake was otherwise.
	fs.Usage = func() {}
	a.usage = func() {
		fmt.Fprintf(a.stdout, "Usage: jd %s\n\nFlags:\n", usage)
		fs.SetOutput(a.stdout)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
	return fs
}

// parse reads a command's words. The standard flag package stops at the
// first word that is not a flag, but people write `jd deploy web --wait` as
// often as `jd deploy --wait web`, so flags are moved in front of the
// positional words first. A flag that takes a value keeps the word after it.
func (a *app) parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		word := args[i]
		if word == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		// A lone "-" is the standard spelling of stdin, never a flag.
		if !strings.HasPrefix(word, "-") || word == "-" {
			positional = append(positional, word)
			continue
		}
		flags = append(flags, word)
		name := strings.TrimLeft(word, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			if a.usage != nil {
				a.usage()
			}
			return nil, err
		}
		return nil, usagef("%s: %v", fs.Name(), err)
	}
	return positional, nil
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// stringList is a flag that may be given more than once: --ack a --ack b.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

// exactly checks the count of positional words a command takes.
func exactly(positional []string, n int, usage string) error {
	if len(positional) != n {
		return usagef("usage: jd %s", usage)
	}
	return nil
}
