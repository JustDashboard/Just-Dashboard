package assistant

import (
	"net/netip"
	"slices"
	"strings"
)

// What a command may be made of to be read as a read (ClassifyCommand).
//
// The rule is the one at the top of command.go: a misreading costs an Allow,
// never a write nobody saw. So this is a list of what a reader may be given, not
// of what it may not. A few programs can do nothing but read whatever they are
// given, and their arguments are not looked at (whatever). Every other one is a
// reader only as written here: the flags listed for it, the subcommands that only
// read, as many operands as cannot become an output file. A flag this table has
// not heard of — a new one, an abbreviation of a long one, a letter in a run of
// short ones — makes the line a change, and so does an argument that is not what
// was typed, because a variable's value or a matched file name can be a flag.
//
// The table errs to the side of asking in two more ways. A flag's value is taken
// from the word after it only where the program certainly takes it from there
// (orAfter): were that wrong, a real flag would be passed over as a value, so
// everywhere else a value written apart is read as an operand, which is checked
// like any other. And what points a reader at configuration of the line's own
// choosing is left out, since configuration is where these programs are told what
// to run: git -C and -c, docker --config and compose -f, nginx -c, npm --prefix.

// arity is where a flag's value is written, if it takes one.
type arity uint8

const (
	// noValue: -a, --all.
	noValue arity = iota + 1
	// onIt: -n5, --lines=5, or alone. The word after is never its value.
	onIt
	// orAfter: -u name, -uname, --user name, --user=name.
	orAfter
)

// flags are the flags a reader may be given.
type flags struct {
	// words: each flag is one whole word after a single dash (find's -name,
	// openssl's -noout), never a run of letters.
	words bool
	short map[rune]arity
	long  map[string]arity
	// numbers: -5 is a count.
	numbers bool
}

// flagsOf reads flags written as getopt would take them: "-a" and "--all" take
// no value, "-n?" and "--lines?" one written onto them, "-u:" and "--user=" one
// written onto them or after, and "-#" is a count (-5).
func flagsOf(spec string) *flags {
	f := &flags{short: map[rune]arity{}, long: map[string]arity{}}
	for _, token := range strings.Fields(spec) {
		how := noValue
		switch token[len(token)-1] {
		case '?':
			how, token = onIt, token[:len(token)-1]
		case ':', '=':
			how, token = orAfter, token[:len(token)-1]
		}
		switch {
		case token == "-#":
			f.numbers = true
		case strings.HasPrefix(token, "--"):
			f.long[token] = how
		default:
			f.short[[]rune(token)[1]] = how
		}
	}
	return f
}

// wordsOf reads flags that are each one word: "-noout" takes no value, "-in:"
// the word after it.
func wordsOf(spec string) *flags {
	f := &flags{words: true, long: map[string]arity{}}
	for _, token := range strings.Fields(spec) {
		if name, valued := strings.CutSuffix(token, ":"); valued {
			f.long[name] = orAfter
		} else {
			f.long[token] = noValue
		}
	}
	return f
}

// given is a command's arguments as its flags read them.
type given struct {
	// seen are the flags met, as listed: "-l", "--list".
	seen     map[string]bool
	operands []string
}

func (g given) any(names ...string) bool {
	return slices.ContainsFunc(names, func(name string) bool { return g.seen[name] })
}

// read reads every argument: false when one of them is a flag not listed, or a
// listed one written a way it is not taken.
func (f *flags) read(args []string) (given, bool) {
	g, _, ok := f.scan(args, false)
	return g, ok
}

// lead reads the flags in front of the first operand, and says how many words
// they took.
func (f *flags) lead(args []string) (given, int, bool) {
	return f.scan(args, true)
}

func (f *flags) scan(args []string, lead bool) (given, int, bool) {
	g := given{seen: map[string]bool{}}
	if f == nil {
		f = &flags{}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-" || !strings.HasPrefix(a, "-"):
			if lead {
				return g, i, true
			}
			g.operands = append(g.operands, a)
		case f.words:
			how, listed := f.long[a]
			if !listed {
				return g, i, false
			}
			g.seen[a] = true
			if how == orAfter {
				if i++; i >= len(args) {
					return g, i, false
				}
			}
		case a == "--":
			if lead {
				return g, i + 1, true
			}
			g.operands = append(g.operands, args[i+1:]...)
			return g, len(args), true
		case strings.HasPrefix(a, "--"):
			name, _, valued := strings.Cut(a, "=")
			how, listed := f.long[name]
			if !listed || (valued && how == noValue) {
				return g, i, false
			}
			g.seen[name] = true
			if how == orAfter && !valued {
				if i++; i >= len(args) {
					return g, i, false
				}
			}
		case f.numbers && digits(a[1:]):
			g.seen["-#"] = true
		default:
			// A run of short flags; one that takes a value takes the rest of it.
			letters := []rune(a[1:])
			for j, letter := range letters {
				how, listed := f.short[letter]
				if !listed {
					return g, i, false
				}
				g.seen["-"+string(letter)] = true
				if how == noValue {
					continue
				}
				if how == orAfter && j == len(letters)-1 {
					if i++; i >= len(args) {
						return g, i, false
					}
				}
				break
			}
		}
	}
	return g, len(args), true
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// reader is a program that only reads, as far as reads says.
type reader struct {
	// whatever: nothing it can be given makes it write or run something, so its
	// arguments are not read, and may be a variable's value or a pattern.
	whatever bool
	// reads reports whether it only reads, given these arguments as typed.
	reads func(args []string) bool
}

// only is a reader given nothing but these flags, and any operands.
func only(f *flags) func([]string) bool {
	return func(args []string) bool {
		_, ok := f.read(args)
		return ok
	}
}

// atMost is a reader whose operand past the nth is a file it writes.
func atMost(n int, f *flags) func([]string) bool {
	return func(args []string) bool {
		g, ok := f.read(args)
		return ok && len(g.operands) <= n
	}
}

// verbs is a program whose first operand says what it does, with its flags
// anywhere on the line: a read when that word is one of these, and, alone, when
// bare. A verb's own check is given the operands after it.
func verbs(f *flags, bare bool, does map[string]func(rest []string) bool) func([]string) bool {
	return func(args []string) bool {
		g, ok := f.read(args)
		switch {
		case !ok:
			return false
		case len(g.operands) == 0:
			return bare
		}
		check, known := does[g.operands[0]]
		return known && (check == nil || check(g.operands[1:]))
	}
}

// staged is a program that reads its own flags up to a subcommand, and leaves
// the rest of the line to that (docker, git, openssl).
func staged(f *flags, bare bool, subcommands map[string]func([]string) bool) func([]string) bool {
	return func(args []string) bool {
		_, n, ok := f.lead(args)
		switch {
		case !ok:
			return false
		case n == len(args):
			return bare
		}
		sub, known := subcommands[args[n]]
		return known && sub(args[n+1:])
	}
}

// reading lists verbs that read whatever follows them.
func reading(names string) map[string]func([]string) bool {
	out := map[string]func([]string) bool{}
	for _, name := range strings.Fields(names) {
		out[name] = nil
	}
	return out
}

// oneOf is a verb followed by at most one word, of these.
func oneOf(words ...string) func([]string) bool {
	return func(rest []string) bool {
		return len(rest) == 0 || len(rest) == 1 && slices.Contains(words, rest[0])
	}
}

// What only reads whatever it is given. sed, awk, less, env and xargs are not
// here because they run what they are told; tee and curl because they write;
// and ping, dig, host, nslookup and traceroute because what they are given is
// sent to a machine of the line's choosing, which is how what was read leaves:
// a name to look up is a message to whoever answers for it.
const whateverPrograms = "ls ll cat head tail grep egrep fgrep wc cut tr column stat du df free uptime uname whoami id groups " +
	"who w last echo pwd which type whereis ps pgrep pstree netstat lsblk findmnt lscpu lsmem lspci lsusb vmstat " +
	"iostat mpstat nproc readlink realpath basename dirname md5sum sha1sum sha256sum jq diff cmp zcat zgrep od " +
	"strings nl tac rev test [ true false seq sleep"

// readers are the programs a read may run, by name.
var readers = buildReaders()

func buildReaders() map[string]reader {
	out := map[string]reader{}
	for _, name := range strings.Fields(whateverPrograms) {
		out[name] = reader{whatever: true}
	}
	add := func(reads func([]string) bool, names ...string) {
		for _, name := range names {
			out[name] = reader{reads: reads}
		}
	}

	// find writes with -fprint, -fprintf and -fls, runs with -exec and -ok, and
	// removes with -delete; what is listed is its tests and the prints to its
	// own output. A test's value is the word after it, whatever it looks like.
	find := "-H -L -P -depth -d -daystart -follow -ignore_readdir_race -noignore_readdir_race -mount -xdev -noleaf " +
		"-nowarn -warn -empty -executable -readable -writable -false -true -nogroup -nouser -print -print0 -ls -prune " +
		"-quit -not -a -and -o -or -maxdepth: -mindepth: -amin: -anewer: -atime: -cmin: -cnewer: -ctime: -fstype: " +
		"-gid: -group: -ilname: -iname: -inum: -ipath: -iregex: -iwholename: -links: -lname: -mmin: -mtime: -name: " +
		"-newer: -path: -perm: -regex: -regextype: -samefile: -size: -type: -uid: -used: -user: -wholename: -xtype: " +
		"-printf:"
	for _, x := range "aBcm" {
		for _, y := range "aBcmt" {
			find += " -newer" + string(x) + string(y) + ":"
		}
	}
	add(only(wordsOf(find)), "find")

	// sort writes with -o and --output, runs --compress-program, and writes its
	// spill to -T.
	add(only(flagsOf("-b -d -f -g -i -M -h -n -R -r -V -c -C -m -s -u -z -k? -t? -S? --ignore-leading-blanks "+
		"--dictionary-order --ignore-case --general-numeric-sort --ignore-nonprinting --month-sort --human-numeric-sort "+
		"--numeric-sort --random-sort --reverse --version-sort --merge --stable --unique --zero-terminated --debug "+
		"--check? --key? --field-separator? --buffer-size? --parallel? --sort? --batch-size? --files0-from? --help --version")), "sort")
	// tree writes with -o, and with -R a 00Tree.html into every directory. Its
	// -L, -P and -I take the word after them, and go on reading the run of
	// letters they are in, so they are listed as taking nothing.
	add(only(flagsOf("-a -d -l -f -x -L -P -I -q -N -Q -p -u -g -s -h -D -F -v -t -c -U -r -i -A -S -n -C -J -X "+
		"--prune --noreport --dirsfirst --filesfirst --si --inodes --device --du --gitignore --matchdirs --ignore-case "+
		"--charset? --filelimit? --timefmt? --sort? --help --version")), "tree")
	// uniq in out writes out, and xxd in out too, with -r or without.
	add(atMost(1, flagsOf("-c -d -D -u -i -z -f: -s: -w: --count --repeated --unique --ignore-case --zero-terminated "+
		"--all-repeated? --group? --skip-fields= --skip-chars= --check-chars= --help --version")), "uniq")
	add(atMost(1, wordsOf("-a -autoskip -b -bits -C -capitalize -d -e -E -EBCDIC -i -include -p -ps -postscript -plain "+
		"-u -uppercase -c: -cols: -g: -groupsize: -l: -len: -s: -seek: -o: -offset:")), "xxd")
	// date sets the clock with -s, and with an operand that is not a +format.
	date := flagsOf("-u -R -d: -r: -I? --utc --universal --rfc-email --rfc-2822 --debug --date= --reference= " +
		"--iso-8601? --rfc-3339? --help --version")
	add(func(args []string) bool {
		g, ok := date.read(args)
		return ok && !slices.ContainsFunc(g.operands, func(o string) bool { return !strings.HasPrefix(o, "+") })
	}, "date")
	// hostname with an operand, -F or -b sets the name.
	add(atMost(0, flagsOf("-f -s -i -I -d -A -a -y -V -h --fqdn --long --short --ip-address --all-ip-addresses --domain "+
		"--all-fqdns --alias --yp --nis --help --version")), "hostname")
	// dmesg clears the ring buffer with -c and -C and sets the console with -n,
	// -E and -D.
	add(atMost(0, flagsOf("-T -H -k -u -x -r -t -w -W -e -d -J -P -S -V -h -L? -l? -f? -s? --ctime --human --kernel "+
		"--userspace --decode --raw --notime --follow --follow-new --reltime --show-delta --json --nopager --syslog "+
		"--color? --level? --facility? --buffer-size? --time-format? --since? --until? --help --version")), "dmesg")
	// file -C compiles a magic file beside the one -m names.
	add(only(flagsOf("-b -i -L -h -z -Z -s -k -r -p -N -n -0 -E -l -v -e? -F? -f? -P? --brief --mime --mime-type "+
		"--mime-encoding --dereference --no-dereference --uncompress --uncompress-noreport --special-files --keep-going "+
		"--raw --preserve-date --no-pad --no-buffer --print0 --extension --apple --list --exclude? --exclude-quiet? "+
		"--separator? --files-from? --parameter? --help --version")), "file")
	// blkid keeps a cache: -c names the file, -g cleans it.
	add(only(flagsOf("-p -l -k -i -D -d -V -h -o? -s? -t? -L? -U? -u? -n? -O? -S? --probe --list-one --list-filesystems "+
		"--info --no-part-details --no-encoding --output? --match-tag? --match-token? --label? --uuid? --usages? "+
		"--match-types? --offset? --size? --help --version")), "blkid")
	// printf -v sets a variable of the shell, PATH among them.
	add(func(args []string) bool {
		return len(args) == 0 || args[0] == "--" || !strings.HasPrefix(args[0], "-")
	}, "printf")
	// rg runs --pre on every file it reads, --hostname-bin for a link, and a
	// decompressor with -z.
	add(only(flagsOf("-i -S -s -w -x -v -c -l -n -N -H -I -o -q -u -a -F -P -U -0 -p -L -b -. -A? -B? -C? -e? -f? -g? "+
		"-t? -T? -m? -r? -M? -j? -d? -E? --ignore-case --smart-case --case-sensitive --word-regexp --line-regexp "+
		"--invert-match --count --count-matches --files-with-matches --files-without-match --line-number --no-line-number "+
		"--with-filename --no-filename --only-matching --quiet --fixed-strings --pcre2 --multiline --multiline-dotall "+
		"--text --null --null-data --pretty --hidden --no-ignore --no-ignore-vcs --no-ignore-parent --no-ignore-global "+
		"--no-ignore-dot --no-ignore-exclude --no-ignore-files --follow --byte-offset --column --no-column --vimgrep "+
		"--json --heading --no-heading --trim --no-messages --stats --files --type-list --one-file-system --no-config "+
		"--passthru --crlf --unrestricted --line-buffered --block-buffered --no-unicode --include-zero --binary "+
		"--no-binary --mmap --no-mmap --max-columns-preview --glob-case-insensitive --no-require-git --stop-on-nonmatch "+
		"--sort? --sortr? --glob? --iglob? --type? --type-not? --max-count? --max-depth? --max-filesize? --max-columns? "+
		"--after-context? --before-context? --context? --regexp? --file? --replace? --color? --colors? --threads? "+
		"--encoding? --engine? --context-separator? --field-match-separator? --field-context-separator? "+
		"--path-separator? --ignore-file? --help --version")), "rg")
	// ss -K closes the sockets it lists, and -D writes them to a file. Its
	// filter names sockets by state, port and address, and an address that is a
	// name is looked up.
	ss := flagsOf("-h -V -n -r -a -l -o -e -m -p -T -i -s -E -Z -z -b -4 -6 -0 -t -u -d -w -x -S -M -H -O -Q -f? -A? " +
		"--numeric --resolve --all --listening --options --extended --memory --processes --threads --info --summary " +
		"--events --context --contexts --bpf --ipv4 --ipv6 --packet --tcp --mptcp --sctp --udp --dccp --raw --unix " +
		"--vsock --xdp --tipc --tos --cgroup --kernel --no-header --oneline --no-queues --inet-sockopt --family? " +
		"--query? --socket? --help --version")
	add(func(args []string) bool {
		g, ok := ss.read(args)
		return ok && !slices.ContainsFunc(g.operands, func(o string) bool { return !ssFilterWord(o) })
	}, "ss")
	// lsof -i @host looks the host up, and getent hosts asks the resolver for
	// whatever name it is given.
	add(func(args []string) bool {
		return !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "@") })
	}, "lsof")
	getent := flagsOf("-i -V --no-idn --help --version")
	add(func(args []string) bool {
		g, ok := getent.read(args)
		if !ok || len(g.operands) < 2 {
			return ok
		}
		return !slices.Contains([]string{"hosts", "ahosts", "ahostsv4", "ahostsv6", "networks"}, g.operands[0])
	}, "getent")
	// journalctl removes with --vacuum-*, --rotate and --flush, and writes a
	// file with --cursor-file, --setup-keys and --update-catalog. -n and -b take
	// their value only when the word after is one, so nothing here takes it from
	// there.
	add(only(flagsOf("-# -a -e -f -k -l -m -q -r -x -N -h -b? -n? -u? -p? -o? -t? -g? -S? -U? -c? -D? -F? --all "+
		"--pager-end --follow --dmesg --full --no-full --merge --quiet --reverse --catalog --fields --no-pager "+
		"--no-tail --no-hostname --utc --list-boots --disk-usage --header --verify --show-cursor --user --system "+
		"--this-boot --truncate-newline --list-catalog --dump-catalog --boot? --lines? --unit? --user-unit? --priority? --output? --output-fields? "+
		"--identifier? --exclude-identifier? --grep? --case-sensitive? --since? --until? --cursor? --after-cursor? "+
		"--directory? --file? --field? --facility? --namespace? --help --version")), "journalctl")

	// systemctl -H and -M run it on another machine, over ssh.
	add(verbs(flagsOf("-a -l -q -r -n? -o? -t? -p? -P? --all --full --quiet --recursive --no-pager --no-legend --plain "+
		"--failed --value --user --system --global --reverse --after --before --with-dependencies --show-types "+
		"--no-ask-password --legend? --lines? --output? --type? --state? --property? --timestamp? --help --version"),
		true, reading("status show cat list-units list-unit-files list-timers list-sockets list-dependencies is-active "+
			"is-enabled is-failed is-system-running get-default")), "systemctl")
	add(verbs(flagsOf("-a -l -n? -o? -p? -P? --all --full --no-pager --no-legend --value --no-ask-password "+
		"--lines? --output? --property? --help --version"),
		true, reading("list-sessions session-status show-session list-users user-status show-user list-seats "+
			"seat-status show-seat")), "loginctl")
	add(verbs(flagsOf("-q --quiet --no-pager --no-legend --user --system --global --order --require --fuzz? "+
		"--iterations? --base-time? --from-pattern? --to-pattern? --man? --recursive-errors? --threshold? --json? "+
		"--help --version"),
		true, reading("blame critical-chain time verify security cat-config calendar timespan")), "systemd-analyze")

	add(dockerReads(), "docker")
	add(gitReads(), "git")

	// apt and apt-cache take any setting with -o and a file of them with -c,
	// where the cache is written is one.
	apt := flagsOf("-a -q -v -n -f -i --all-versions --installed --upgradable --upgradeable --manual-installed " +
		"--verbose --quiet --names-only --full --important --recurse --no-recommends --no-suggests --no-pre-depends " +
		"--no-depends --no-conflicts --no-breaks --no-replaces --no-enhances --implicit --help --version")
	add(verbs(apt, false, reading("list show search policy depends rdepends changelog")), "apt")
	add(verbs(apt, false, reading("show search policy depends rdepends showpkg madison pkgnames")), "apt-cache")
	add(dpkgReads, "dpkg", "dpkg-query")
	add(verbs(flagsOf("--all --verbose --abs-time --color? --unicode? --help --version"), true,
		reading("list info find version services changes")), "snap")

	// crontab -l, with -u name; anything else installs, edits or removes one.
	crontab := flagsOf("-l -u:")
	add(func(args []string) bool {
		g, ok := crontab.read(args)
		return ok && g.seen["-l"] && len(g.operands) == 0
	}, "crontab")
	add(verbs(flagsOf("--help --version"), true, map[string]func([]string) bool{
		"status":  oneOf("verbose", "numbered"),
		"show":    oneOf("raw", "builtins", "before-rules", "user-rules", "after-rules", "logging-rules", "listening", "added"),
		"version": oneOf(),
	}), "ufw")
	// iptables lists with -L and -S; every other letter of its own changes a
	// rule, a chain or a counter, and --modprobe names a program. -w took no
	// value once, so the letters after it are read as flags.
	iptables := flagsOf("-L -S -n -v -x -h -V -w -t? --list --list-rules --numeric --verbose --exact --line-numbers " +
		"--table? --wait? --help --version")
	add(func(args []string) bool {
		g, ok := iptables.read(args)
		return ok && g.any("-L", "-S", "--list", "--list-rules")
	}, "iptables")
	// nft reads its whole line as one script, so "list ruleset ; flush ruleset"
	// is two commands of its own: after list, only names.
	nft := flagsOf("-a -n -s -j -t -y -S -u -p -T --handle --numeric --stateless --json --terse --numeric-priority " +
		"--service --guid --numeric-protocol --numeric-time")
	add(func(args []string) bool {
		g, ok := nft.read(args)
		if !ok || len(g.operands) < 2 || g.operands[0] != "list" {
			return false
		}
		return !slices.ContainsFunc(g.operands[1:], func(o string) bool {
			return strings.ContainsFunc(o, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-')
			})
		})
	}, "nft")
	add(verbs(flagsOf("-v -V -h --verbose --help --version"), false, reading("status get ping version")), "fail2ban-client")

	// openssl writes with -out, -keyout, -sess_out, -keylogfile, -msgfile and
	// -writerand, and loads code with -engine and -provider, in every command of
	// its: each is listed whole. s_client is not one of them: it writes three of
	// those, and connects to whatever it is told to. A certificate or a list
	// named by an address rather than a path is fetched from it.
	openssl := staged(nil, false, map[string]func([]string) bool{
		"x509": only(wordsOf("-noout -text -dates -startdate -enddate -subject -issuer -serial -fingerprint -sha1 -sha256 " +
			"-sha384 -sha512 -md5 -email -ocsp_uri -ocspid -purpose -pubkey -modulus -hash -subject_hash -issuer_hash " +
			"-subject_hash_old -issuer_hash_old -alias -nocert -in: -inform: -nameopt: -certopt: -dateopt: -checkend: " +
			"-checkhost: -checkemail: -checkip: -ext:")),
		"verify": only(wordsOf("-verbose -x509_strict -show_chain -no-CAfile -no-CApath -partial_chain -check_ss_sig " +
			"-policy_check -CAfile: -CApath: -untrusted: -trusted: -purpose: -verify_hostname: -verify_email: " +
			"-verify_ip: -verify_depth: -attime:")),
		"crl": only(wordsOf("-noout -text -nextupdate -lastupdate -issuer -hash -hash_old -fingerprint -crlnumber " +
			"-verify -in: -inform: -nameopt: -CAfile: -CApath:")),
		"ciphers": atMost(1, wordsOf("-v -V -s -tls1 -tls1_1 -tls1_2 -tls1_3 -stdname -psk -srp -ciphersuites: -convert:")),
		"version": atMost(0, wordsOf("-a -v -b -o -f -p -d -e -m -r -c")),
	})
	add(func(args []string) bool {
		return !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "://") }) && openssl(args)
	}, "openssl")
	// certbot writes its log and its working files where it is told to.
	add(verbs(flagsOf("-d: --domain= --domains= --cert-name= --help"), false, reading("certificates")), "certbot")
	add(verbs(flagsOf("-m -s -v -V -h --mini-list --silent --no-color --sort? --help --version"), true,
		reading("list ls status show describe info jlist prettylist")), "pm2")
	// npm takes a registry, a cache and every other setting as a flag, and
	// from the .npmrc of the directory it is run in; view and outdated ask the
	// registry that names.
	add(verbs(flagsOf("-l -p -a -g --json --long --parseable --all --global --link --prod --production --dev --silent "+
		"--offline --depth? --omit? --include? --help"), false, map[string]func([]string) bool{
		"ls": nil, "list": nil,
		"config": func(rest []string) bool {
			return len(rest) > 0 && slices.Contains([]string{"get", "list", "ls"}, rest[0])
		},
	}), "npm")

	// ip runs a file of its commands with -batch and inside a namespace with -n;
	// an object's show, list and get read.
	ip := wordsOf("-4 -6 -0 -s -stats -statistics -d -details -o -oneline -j -json -p -pretty -c -color -br -brief " +
		"-h -human -human-readable -iec -r -resolve -a -all -t -timestamp -ts -tshort -V -Version")
	add(func(args []string) bool {
		g, ok := ip.read(args)
		switch {
		case !ok:
			return false
		case len(g.operands) == 0:
			return true
		}
		does, known := ipReads[g.operands[0]]
		second := ""
		if len(g.operands) > 1 {
			second = g.operands[1]
		}
		return known && slices.Contains(does, second)
	}, "ip")

	// nginx -t tests the configuration it is installed with; -c and -g give it
	// another, which can load a module, and without -t, -T, -v or -V it starts.
	add(func(args []string) bool {
		tests := false
		for _, a := range args {
			switch a {
			case "-t", "-T", "-v", "-V", "-h", "-?":
				tests = true
			case "-q":
			default:
				return false
			}
		}
		return tests
	}, "nginx")
	add(func(args []string) bool {
		for _, a := range args {
			if !slices.Contains([]string{"-t", "-S", "-M", "-v", "-V", "-l", "-L", "configtest"}, a) {
				return false
			}
		}
		return len(args) > 0
	}, "apache2ctl", "apachectl")
	return out
}

// ssFilterWords are the words of an ss filter that name no machine.
var ssFilterWords = setOf("state", "exclude", "all", "connected", "synchronized", "bucket", "big", "established",
	"syn-sent", "syn-recv", "fin-wait-1", "fin-wait-2", "time-wait", "closed", "close-wait", "last-ack", "listening",
	"closing", "sport", "dport", "src", "dst", "=", "==", "eq", "!=", "ne", "neq", "gt", "lt", "ge", "le", "and", "or",
	"not", "autobound")

// ssFilterWord is a word of an ss filter that is looked up nowhere: one of its
// own, a port by number, or an address written out.
func ssFilterWord(word string) bool {
	if ssFilterWords[word] || digits(strings.TrimPrefix(word, ":")) {
		return true
	}
	if _, err := netip.ParseAddr(word); err == nil {
		return true
	}
	if _, err := netip.ParsePrefix(word); err == nil {
		return true
	}
	_, err := netip.ParseAddrPort(word)
	return err == nil
}

// ipReads are what an ip object reads with, as the word after it; "" is the
// object alone.
var ipReads = map[string][]string{
	"r": {"", "show", "list", "get"}, "route": {"", "show", "list", "get"},
	"a": {"", "show", "list"}, "addr": {"", "show", "list"}, "address": {"", "show", "list"},
	"l": {"", "show", "list"}, "link": {"", "show", "list"},
	"rule": {"", "show", "list"}, "n": {"", "show", "list"}, "neigh": {"", "show", "list"},
	"maddr": {"", "show"},
}

// dockerReads is docker's listing and inspecting. What is left out of every
// command is what points it elsewhere: -H and --context at another daemon,
// --config at a directory whose plugins it runs, and compose's -f, --env-file
// and --project-directory at a project of the line's choosing, which can include
// one from a registry. compose config writes with -o.
func dockerReads() func([]string) bool {
	none := only(flagsOf("--help"))
	format := only(flagsOf("-f? --format? --help"))
	ps := only(flagsOf("-a -q -s -l -n? -f? --all --quiet --size --latest --no-trunc --last? --filter? --format? --help"))
	images := only(flagsOf("-a -q -f? --all --quiet --no-trunc --digests --tree --filter? --format? --help"))
	inspect := only(flagsOf("-s -f? --size --format? --type? --help"))
	logs := only(flagsOf("-f -t -n? --follow --timestamps --details --tail? --since? --until? --help"))
	stats := only(flagsOf("-a --all --no-stream --no-trunc --format? --help"))
	history := only(flagsOf("-H -q --no-trunc --quiet --human? --format? --help"))
	list := only(flagsOf("-q -f? --quiet --no-trunc --filter? --format? --help"))
	under := func(subcommands map[string]func([]string) bool) func([]string) bool {
		return staged(nil, false, subcommands)
	}
	return staged(flagsOf("-v -D --version --debug --help"), true, map[string]func([]string) bool{
		"ps": ps, "images": images, "inspect": inspect, "logs": logs, "stats": stats, "top": none, "port": none,
		"diff": none, "version": format, "info": format, "history": history,
		"image":   under(map[string]func([]string) bool{"ls": images, "list": images, "inspect": format, "history": history}),
		"network": under(map[string]func([]string) bool{"ls": list, "list": list, "inspect": only(flagsOf("-v -f? --verbose --format? --help"))}),
		"volume":  under(map[string]func([]string) bool{"ls": list, "list": list, "inspect": format}),
		"container": under(map[string]func([]string) bool{"ls": ps, "list": ps, "inspect": inspect, "logs": logs, "top": none,
			"port": none, "stats": stats, "diff": none}),
		"system": under(map[string]func([]string) bool{"df": only(flagsOf("-v --verbose --format? --help")), "info": format}),
		"compose": staged(flagsOf("-p: --project-name= --ansi? --progress? --profile? --help"), false, map[string]func([]string) bool{
			"ps":   only(flagsOf("-a -q --all --quiet --services --no-trunc --orphans? --status? --format? --filter? --help")),
			"ls":   only(flagsOf("-a -q --all --quiet --format? --filter? --help")),
			"logs": only(flagsOf("-f -t -n? --follow --timestamps --no-color --no-log-prefix --tail? --since? --until? --index? --help")),
			"config": only(flagsOf("-q --quiet --services --volumes --images --profiles --no-interpolate --no-normalize " +
				"--no-path-resolution --no-consistency --format? --hash? --help")),
			"top":     none,
			"images":  only(flagsOf("-q --quiet --format? --help")),
			"version": only(flagsOf("-f? --format? --short --help")),
			"port":    only(flagsOf("--protocol? --index? --help")),
		}),
	})
}

// gitReads is git's looking at the repository of the directory it is run in.
// Its configuration is where it is told what to run — a pager, an editor, a
// file-system monitor, an external diff, the ssh it reaches a remote through —
// so nothing that sets or moves it is listed: -c, --config-env, -C, --git-dir,
// --work-tree, --exec-path. Nor is what writes (--output), runs a filter of the
// configuration's (--ext-diff, --textconv, --show-signature), or reaches a
// remote, whose address can itself be a command (ls-remote, remote show).
func gitReads() func([]string) bool {
	// What log, show and diff take: every value is written onto its flag or
	// read as an operand, which a revision or a path is anyway.
	history := flagsOf("-# -p -u -s -c -m -g -w -b -i -E -F -P -R -a -W -z -q -n? -S? -G? -L? -U? -M? -C? -B? -X? " +
		"--oneline --graph --all --patch --no-patch --shortstat --numstat --name-only --name-status --summary --raw " +
		"--abbrev-commit --no-abbrev-commit --merges --no-merges --first-parent --reverse --follow --source --cached " +
		"--staged --no-color --no-decorate --check --ignore-all-space --ignore-space-change --ignore-space-at-eol " +
		"--ignore-blank-lines --minimal --patience --histogram --full-diff --date-order --topo-order " +
		"--author-date-order --left-right --cherry-pick --cherry-mark --cherry --boundary --no-ext-diff --no-textconv " +
		"--regexp-ignore-case --extended-regexp --fixed-strings --perl-regexp --all-match --invert-grep --exit-code " +
		"--quiet --no-index --no-notes --simplify-by-decoration --full-history --no-renames --function-context --text " +
		"--no-prefix --walk-reflogs --reflog --no-mailmap --use-mailmap --children --parents --merge-base --not " +
		"--no-walk? --stat? --color? --decorate? --pretty? --format? --date? --since? --after? --until? --before? " +
		"--author? --committer? --grep? --max-count? --skip? --diff-filter? --abbrev? --find-renames? --find-copies? " +
		"--relative? --unified? --inter-hunk-context? --word-diff? --word-diff-regex? --color-words? --color-moved? " +
		"--diff-algorithm? --stat-width? --stat-count? --dirstat? --branches? --tags? --remotes? --glob? --exclude? " +
		"--min-parents? --max-parents? --ancestry-path? --diff-merges? --encoding? --notes? --submodule? " +
		"--ignore-submodules? --src-prefix? --dst-prefix? --line-prefix? --ws-error-highlight? --decorate-refs? " +
		"--decorate-refs-exclude? --help")
	// git branch name and git tag name make one: operands are patterns only
	// when a flag that lists says so.
	lists := []string{"-l", "--list", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at"}
	listing := func(f *flags) func([]string) bool {
		return func(args []string) bool {
			g, ok := f.read(args)
			return ok && (len(g.operands) == 0 || g.any(lists...))
		}
	}
	remote := flagsOf("-v -n --verbose --push --all")
	return staged(flagsOf("-P --no-pager --no-optional-locks --literal-pathspecs --version --help"), true, map[string]func([]string) bool{
		"log": only(history), "show": only(history), "diff": only(history),
		"status": only(flagsOf("-s -b -v -z -u? --short --branch --long --verbose --show-stash --ahead-behind " +
			"--no-ahead-behind --renames --no-renames --no-column --porcelain? --untracked-files? --ignored? " +
			"--ignore-submodules? --column? --find-renames? --help")),
		"branch": listing(flagsOf("-a -r -v -l -i --all --remotes --verbose --list --show-current --ignore-case --no-color " +
			"--no-column --no-abbrev --contains? --no-contains? --merged? --no-merged? --points-at? --sort? --format? " +
			"--color? --column? --abbrev? --help")),
		"tag": listing(flagsOf("-l -i -n? --list --ignore-case --no-column --contains? --no-contains? --merged? --no-merged? " +
			"--points-at? --sort? --format? --color? --column? --help")),
		// git remote, -v and get-url read the configuration; show asks the remote
		// unless -n.
		"remote": func(args []string) bool {
			g, ok := remote.read(args)
			switch {
			case !ok:
				return false
			case len(g.operands) == 0:
				return !g.any("-n", "--push", "--all")
			case g.operands[0] == "get-url":
				return !g.any("-v", "--verbose", "-n")
			case g.operands[0] == "show":
				return g.seen["-n"] && !g.any("--push", "--all")
			}
			return false
		},
		"rev-parse": only(flagsOf("-q --quiet --verify --symbolic --symbolic-full-name --show-toplevel --show-prefix " +
			"--show-cdup --show-superproject-working-tree --git-dir --absolute-git-dir --git-common-dir " +
			"--is-inside-work-tree --is-inside-git-dir --is-bare-repository --is-shallow-repository --all --git-path " +
			"--local-env-vars --default --revs-only --no-revs --flags --no-flags --not --sq --short? --abbrev-ref? " +
			"--branches? --tags? --remotes? --glob? --exclude? --show-object-format? --path-format? --since? --until? " +
			"--after? --before? --help")),
		"describe": only(flagsOf("--tags --all --always --long --exact-match --contains --first-parent --debug --dirty? " +
			"--broken? --abbrev? --match? --exclude? --candidates? --help")),
		"ls-files": only(flagsOf("-c -d -m -o -i -s -u -k -t -v -f -z -x? -X? --cached --deleted --modified --others " +
			"--ignored --stage --unmerged --killed --exclude-standard --full-name --error-unmatch --debug --eol " +
			"--deduplicate --sparse --recurse-submodules --directory --no-empty-directory --exclude? --exclude-from? " +
			"--exclude-per-directory? --abbrev? --format? --with-tree? --help")),
		"blame": only(flagsOf("-l -t -p -b -s -e -w -f -n -c -L? -S? -M? -C? --reverse --porcelain --line-porcelain " +
			"--incremental --root --show-email --show-name --show-number --color-lines --color-by-age --first-parent " +
			"--minimal --show-stats --progress --no-progress --date? --abbrev? --since? --ignore-rev? " +
			"--ignore-revs-file? --encoding? --help")),
		"shortlog": only(flagsOf("-# -n -s -e -c -w? --numbered --summary --email --committer --all --no-merges --merges " +
			"--group? --format? --since? --after? --until? --before? --author? --grep? --max-count? --help")),
		// git reflog expire, delete and drop forget where a branch has been.
		"reflog": func(args []string) bool {
			g, ok := history.read(args)
			return ok && (len(g.operands) == 0 || slices.Contains([]string{"show", "list", "exists"}, g.operands[0]))
		},
	})
}

// dpkgReads is dpkg and dpkg-query asked a question: one of these actions, and
// no other flag but the format it answers in.
func dpkgReads(args []string) bool {
	actions := []string{"-l", "-L", "-s", "-S", "-p", "-W", "--list", "--listfiles", "--status", "--search", "--print-avail",
		"--show", "--print-architecture", "--get-selections", "--audit", "-C", "--version"}
	read := false
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.SplitN(a, "=", 2)[0]
		if slices.Contains(actions, name) {
			read = true
			continue
		}
		if name == "-f" || name == "--showformat" {
			continue
		}
		return false
	}
	return read
}
