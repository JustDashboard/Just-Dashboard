package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sizes nginx's ngx_hash_init needs: a name takes align(len+2, 8)+16
// bytes of a bucket, and nginx rounds the bucket up to its cache line.
func TestServerNameBucketSizes(t *testing.T) {
	for _, tc := range []struct {
		length, bucket int
		fits           bool
	}{
		{46, 64, true}, {47, 64, false}, {110, 128, true}, {111, 128, false},
		// A value off the cache line is rounded up before it is used.
		{47, 65, true}, {110, 100, true}, {111, 129, true},
	} {
		if got := bucketFits(tc.bucket, tc.length); got != tc.fits {
			t.Errorf("bucketFits(%d, %d) = %v, want %v", tc.bucket, tc.length, got, tc.fits)
		}
	}
	for _, tc := range []struct{ length, want int }{
		{0, 128}, {46, 128}, {47, 128}, {53, 128}, {110, 128}, {111, 256}, {238, 256}, {239, 512}, {253, 512},
	} {
		if got := bucketFor(tc.length); got != tc.want {
			t.Errorf("bucketFor(%d) = %d, want %d", tc.length, got, tc.want)
		}
		if !bucketFits(bucketFor(tc.length), tc.length) {
			t.Errorf("bucketFor(%d) = %d does not fit the name", tc.length, bucketFor(tc.length))
		}
	}
}

func TestReadHashRefusalReadsBothForms(t *testing.T) {
	one := &ValidationResult{Output: "nginx: [emerg] could not build server_names_hash, you should increase server_names_hash_bucket_size: 64\nnginx: configuration file /etc/nginx/nginx.conf test failed"}
	if r, ok := readHashRefusal(one); !ok || r.bucket != 64 || r.maxSize != 0 {
		t.Fatalf("bucket form read as %+v, %v", r, ok)
	}
	many := &ValidationResult{Output: "nginx: [emerg] could not build server_names_hash, you should increase either server_names_hash_max_size: 512 or server_names_hash_bucket_size: 128"}
	if r, ok := readHashRefusal(many); !ok || r.bucket != 128 || r.maxSize != 512 {
		t.Fatalf("max-size form read as %+v, %v", r, ok)
	}
	if _, ok := readHashRefusal(&ValidationResult{Valid: true, Output: one.Output}); ok {
		t.Fatal("a passing test read as a refusal")
	}
	if _, ok := readHashRefusal(&ValidationResult{Output: "nginx: [emerg] could not build variables_hash, you should increase variables_hash_bucket_size: 64"}); ok {
		t.Fatal("the variables hash read as the server-name hash")
	}
}

// fakeHashNginx is an nginx that refuses the way the real one does: when the
// longest server_name in the files it reads needs more of a bucket than the
// bucket it reads (rounded up to a 64-byte cache line), or its max size is
// smaller than root/need-max; and it refuses a directive set twice.
// root/other-error, when present, is an error it gives once the hash fits.
const fakeHashNginx = `#!/bin/sh
root=ROOT
all=$(cat "$root/nginx.conf" "$root"/conf.d/*.conf "$root"/sites-enabled/* 2>/dev/null)
for d in server_names_hash_bucket_size server_names_hash_max_size; do
	if [ "$(printf '%s\n' "$all" | grep -cE "^[[:space:]]*$d[[:space:]]")" -gt 1 ]; then
		echo "nginx: [emerg] \"$d\" directive is duplicate in $root/nginx.conf:9" >&2
		exit 1
	fi
done
bucket=$(printf '%s\n' "$all" | sed -n 's/^[[:space:]]*server_names_hash_bucket_size[[:space:]]*\([0-9]*\);.*/\1/p')
[ -n "$bucket" ] || bucket=64
max=$(printf '%s\n' "$all" | sed -n 's/^[[:space:]]*server_names_hash_max_size[[:space:]]*\([0-9]*\);.*/\1/p')
[ -n "$max" ] || max=512
longest=$(printf '%s\n' "$all" | sed -n 's/^[[:space:]]*server_name[[:space:]]*\([^;]*\);.*/\1/p' | tr ' ' '\n' | awk '{ if (length($0) > m) m = length($0) } END { print m + 0 }')
need=$(( (longest + 2 + 7) / 8 * 8 + 16 ))
usable=$(( (bucket + 63) / 64 * 64 ))
needm=$(cat "$root/need-max" 2>/dev/null || echo 0)
if [ "$usable" -lt "$need" ]; then
	echo "nginx: [emerg] could not build server_names_hash, you should increase server_names_hash_bucket_size: $usable" >&2
	echo "nginx: configuration file $root/nginx.conf test failed" >&2
	exit 1
fi
if [ "$max" -lt "$needm" ]; then
	echo "nginx: [emerg] could not build server_names_hash, you should increase either server_names_hash_max_size: $max or server_names_hash_bucket_size: $usable" >&2
	exit 1
fi
if [ -f "$root/other-error" ]; then
	cat "$root/other-error" >&2
	exit 1
fi
echo "nginx: the configuration file $root/nginx.conf syntax is ok" >&2
`

const longName = "riverside-garden-store-4c2e91.203-0-113-7.sslip.io" // 50 characters

// hashHost is a Debian-layout nginx directory whose nginx refuses long names
// the way nginx does, with a recorder attached.
func hashHost(t *testing.T, conf string) (*Service, *changeLog, string) {
	t.Helper()
	service, log, root := recordingService(t)
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if conf == "" {
		conf = "events {}\nhttp {\n    include $ROOT/conf.d/*.conf;\n    include $ROOT/sites-enabled/*;\n}\n"
	}
	writeNamesFile(t, filepath.Join(root, "nginx.conf"), strings.ReplaceAll(conf, "$ROOT", root))
	writeNamesFile(t, filepath.Join(root, "bin", "nginx"), strings.ReplaceAll(fakeHashNginx, "ROOT", root))
	if err := os.Chmod(filepath.Join(root, "bin", "nginx"), 0o755); err != nil {
		t.Fatal(err)
	}
	return service, log, root
}

// serveName writes a site serving name.
func serveName(t *testing.T, root, site, name string) {
	t.Helper()
	writeNamesFile(t, filepath.Join(root, "sites-enabled", site), "server {\n    listen 80;\n    server_name "+name+";\n}\n")
}

func writeNamesFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func namesFile(root string) string {
	return filepath.Join(root, "conf.d", serverNamesFile)
}

func TestFitWritesTheManagedFileWhenANameDoesNotFit(t *testing.T) {
	service, log, root := hashHost(t, "")
	serveName(t, root, "store", longName)
	res := service.testNginx(context.Background())
	if !res.Valid {
		t.Fatalf("test still refused: %s", res.Output)
	}
	raw, err := os.ReadFile(namesFile(root))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.HasPrefix(content, managedMarker) || !strings.Contains(content, "\nserver_names_hash_bucket_size 128;\n") ||
		strings.Contains(content, maxSizeDirective) {
		t.Fatalf("managed file:\n%s", content)
	}
	if !strings.Contains(res.Note, "server_names_hash_bucket_size 128") || !strings.Contains(res.Note, longName) {
		t.Fatalf("note = %q", res.Note)
	}
	if len(log.changes) != 1 || log.changes[0].Path != namesFile(root) || log.changes[0].BeforeExisted ||
		string(log.changes[0].After) != content {
		t.Fatalf("recorded %+v", log.changes)
	}

	// Never written twice: a test that passes changes nothing.
	if res := service.testNginx(context.Background()); !res.Valid || len(log.changes) != 1 {
		t.Fatalf("second test: %v, %d changes", res.Valid, len(log.changes))
	}

	// A longer name raises it, and a shorter one never lowers it.
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 50) + ".example.test" // 126 characters
	serveName(t, root, "long", long)
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("raise refused: %s", res.Output)
	}
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "\nserver_names_hash_bucket_size 256;\n") {
		t.Fatalf("not raised:\n%s", raw)
	}
	if len(log.changes) != 2 || !log.changes[1].BeforeExisted || string(log.changes[1].Before) != content {
		t.Fatalf("raise recorded as %+v", log.changes)
	}
	serveName(t, root, "short", "shop.example.test")
	if res := service.testNginx(context.Background()); !res.Valid || len(log.changes) != 2 {
		t.Fatalf("a short name changed the file: %v, %d changes", res.Valid, len(log.changes))
	}
}

func TestFitRaisesTheMaxSizeForManyNames(t *testing.T) {
	service, _, root := hashHost(t, "")
	serveName(t, root, "store", longName)
	writeNamesFile(t, filepath.Join(root, "need-max"), "1024")
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	raw, _ := os.ReadFile(namesFile(root))
	if !strings.Contains(string(raw), "\nserver_names_hash_bucket_size 128;\n") ||
		!strings.Contains(string(raw), "\nserver_names_hash_max_size 1024;\n") {
		t.Fatalf("managed file:\n%s", raw)
	}
}

// A value the operator set is theirs: nothing is written, and the refusal
// says where it is and what it needs to be.
func TestFitLeavesTheOperatorsDirectiveAlone(t *testing.T) {
	conf := "events {}\nhttp {\n    server_names_hash_bucket_size 64;\n    include $ROOT/conf.d/*.conf;\n    include $ROOT/sites-enabled/*;\n}\n"
	service, log, root := hashHost(t, conf)
	serveName(t, root, "store", longName)
	res := service.testNginx(context.Background())
	if res.Valid {
		t.Fatal("passed over a bucket too small")
	}
	headline := FailureHeadline(res)
	want := "server_names_hash_bucket_size is 64, too small for the server name " + longName + " (50 characters); set it to 128 in " +
		filepath.Join(root, "nginx.conf") + ":3"
	if headline != want {
		t.Fatalf("headline = %q, want %q", headline, want)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatalf("managed file written beside the operator's directive: %v", err)
	}
	if len(log.changes) != 0 {
		t.Fatalf("recorded %+v", log.changes)
	}
	// The operator's own value, large enough, is simply used.
	writeNamesFile(t, filepath.Join(root, "nginx.conf"), strings.ReplaceAll(strings.ReplaceAll(conf, "64;", "128;"), "$ROOT", root))
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused with the operator's 128: %s", res.Output)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatal("managed file written though nothing needed it")
	}
}

func TestFitIsPutBackWhenNginxStillRefuses(t *testing.T) {
	service, log, root := hashHost(t, "")
	serveName(t, root, "store", longName)
	writeNamesFile(t, filepath.Join(root, "other-error"), "nginx: [emerg] unknown directive \"proxy_passs\" in /etc/nginx/sites-enabled/salon:4\n")
	res := service.testNginx(context.Background())
	if res.Valid || !strings.Contains(FailureHeadline(res), "unknown directive") {
		t.Fatalf("got %v %q, want nginx's next error", res.Valid, FailureHeadline(res))
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatalf("raise kept after a refused test: %v", err)
	}
	// A file that was there goes back to what it held.
	before := renderServerNames(128, 0, "")
	writeNamesFile(t, namesFile(root), before)
	long := strings.Repeat("c", 120) + ".test"
	serveName(t, root, "long", long)
	if res := service.testNginx(context.Background()); res.Valid {
		t.Fatal("passed")
	}
	if raw, _ := os.ReadFile(namesFile(root)); string(raw) != before {
		t.Fatalf("managed file not restored:\n%s", raw)
	}
	if len(log.changes) != 0 {
		t.Fatalf("a refused raise was recorded: %+v", log.changes)
	}
}

// A dry run says what the save will set and leaves nothing behind; the save
// makes the raise.
func TestValidateAndWriteConfigFitTheNames(t *testing.T) {
	service, log, root := hashHost(t, "")
	site := filepath.Join(root, "sites-enabled", "store")
	content := "server {\n    listen 80;\n    server_name " + longName + ";\n}\n"
	writeNamesFile(t, site, "server {\n    listen 80;\n    server_name shop.example.test;\n}\n")
	res, err := service.Validate(context.Background(), KindNginx, site, content)
	if err != nil || !res.Valid {
		t.Fatalf("dry run: %v %+v", err, res)
	}
	if !strings.Contains(res.Note, "when the change is made") {
		t.Fatalf("note = %q", res.Note)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatal("a dry run left the raise behind")
	}
	if len(log.changes) != 0 {
		t.Fatalf("a dry run recorded %+v", log.changes)
	}
	res, err = service.WriteConfig(context.Background(), KindNginx, site, content)
	if err != nil || !res.Valid {
		t.Fatalf("save: %v %+v", err, res)
	}
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "server_names_hash_bucket_size 128;") {
		t.Fatalf("save did not raise:\n%s", raw)
	}
	if len(log.changes) != 2 || log.changes[0].Path != namesFile(root) || log.changes[1].Path != site {
		t.Fatalf("recorded %+v", log.changes)
	}
}

// A site save that nginx takes only with the raise keeps it, and one rolled
// back takes the raise with it.
func TestSiteSaveFitsTheNames(t *testing.T) {
	service, log, root := hashHost(t, "")
	spec := proxySpec()
	spec.Domains = []string{longName}
	res, err := service.ApplySite(context.Background(), spec, true, false, false)
	if err != nil || !res.Validation.Valid {
		t.Fatalf("save: %v %+v", err, res)
	}
	if _, err := os.Stat(namesFile(root)); err != nil {
		t.Fatalf("no raise: %v", err)
	}
	found := false
	for _, c := range log.changes {
		found = found || c.Path == namesFile(root)
	}
	if !found {
		t.Fatalf("raise not recorded: %+v", log.changes)
	}
}

// A configuration with no directory included in http gets marked lines at
// the top of nginx.conf's http block, and they are raised in place.
func TestFitWritesMarkedLinesWhereNoDirectoryIsIncluded(t *testing.T) {
	conf := "events {}\nhttp { # the sites\n    include $ROOT/sites-enabled/*;\n}\n"
	service, log, root := hashHost(t, conf)
	serveName(t, root, "store", longName)
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "nginx.conf"))
	want := "events {}\nhttp { # the sites\n    server_names_hash_bucket_size 128; " + namesLineMark + "\n    include " + root + "/sites-enabled/*;\n}\n"
	if string(raw) != want {
		t.Fatalf("nginx.conf:\n%s\nwant:\n%s", raw, want)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatal("conf.d file written where nginx does not include it")
	}
	serveName(t, root, "long", strings.Repeat("d", 120)+".test")
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("raise refused: %s", res.Output)
	}
	raw, _ = os.ReadFile(filepath.Join(root, "nginx.conf"))
	if strings.Count(string(raw), bucketDirective) != 1 || !strings.Contains(string(raw), bucketDirective+" 256; "+namesLineMark) {
		t.Fatalf("nginx.conf after the raise:\n%s", raw)
	}
	if len(log.changes) != 2 {
		t.Fatalf("recorded %d changes", len(log.changes))
	}
}

// An http block that opens and closes on one line, and includes only the
// sites, gets the marked lines inside it: at the brace, not after the line,
// which is past the block's end.
func TestFitWritesMarkedLinesInsideAOneLineHTTPBlock(t *testing.T) {
	conf := "events {}\nhttp { include $ROOT/sites-enabled/*; }\n"
	service, _, root := hashHost(t, conf)
	serveName(t, root, "store", longName)
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "nginx.conf"))
	want := "events {}\nhttp {\n    server_names_hash_bucket_size 128; " + namesLineMark + "\n    include " + root + "/sites-enabled/*; }\n"
	if string(raw) != want {
		t.Fatalf("nginx.conf:\n%s\nwant:\n%s", raw, want)
	}
	assertNamesInHTTP(t, string(raw), 1)
}

func TestSpliceServerNamesAtTheOpeningBrace(t *testing.T) {
	mark := func(d string, n int) string { return fmt.Sprintf("    %s %d; %s", d, n, namesLineMark) }
	for _, tc := range []struct{ name, conf, want string }{
		{
			"a block closed on its line, with a comment after it",
			"http { include sites-enabled/*; } # the sites\n",
			"http {\n" + mark(bucketDirective, 128) + "\n" + mark(maxSizeDirective, 1024) + "\n    include sites-enabled/*; } # the sites\n",
		},
		{
			"CRLF, no space before the brace's content and no final newline",
			"events {}\r\nhttp {include sites-enabled/*;}",
			"events {}\r\nhttp {\r\n" + mark(bucketDirective, 128) + "\r\n" + mark(maxSizeDirective, 1024) + "\r\n    include sites-enabled/*;}",
		},
		{
			"only a comment after the brace stays on the opener's line",
			"http { # the sites\n    include sites-enabled/*;\n}\n",
			"http { # the sites\n" + mark(bucketDirective, 128) + "\n" + mark(maxSizeDirective, 1024) + "\n    include sites-enabled/*;\n}\n",
		},
		{
			"the brace ends the file",
			"http {",
			"http {\n" + mark(bucketDirective, 128) + "\n" + mark(maxSizeDirective, 1024) + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := spliceServerNames(tc.conf, 128, 1024)
			if !ok || got != tc.want {
				t.Fatalf("got %q, %v\nwant %q", got, ok, tc.want)
			}
			if strings.Contains(tc.conf, "}") {
				assertNamesInHTTP(t, got, 2)
			}
			// A later fit starts from the file this one left, and puts
			// the same lines back where they are.
			if again, ok := spliceServerNames(got, 128, 1024); !ok || again != got {
				t.Fatalf("second splice: %q, %v", again, ok)
			}
		})
	}
}

// assertNamesInHTTP reads content as nginx does and checks that the marked
// directives, n of them, sit directly in its http block.
func assertNamesInHTTP(t *testing.T, content string, n int) {
	t.Helper()
	directives, err := ParseNginxFile("nginx.conf", content, nil)
	if err != nil {
		t.Fatalf("%v in:\n%s", err, content)
	}
	inside := 0
	for _, d := range directives {
		if d.Name == bucketDirective || d.Name == maxSizeDirective {
			t.Fatalf("%s at line %d is outside http:\n%s", d.Name, d.Line, content)
		}
		if d.Name != "http" {
			continue
		}
		for _, in := range d.Block {
			if in.Name == bucketDirective || in.Name == maxSizeDirective {
				inside++
			}
		}
	}
	if inside != n {
		t.Fatalf("%d marked directives in http, want %d:\n%s", inside, n, content)
	}
}

// A first deploy's challenge site that nginx refuses says nginx's reason,
// not "configuration failed validation".
func TestDeploymentChallengeFailureCarriesNginxsSentence(t *testing.T) {
	conf := "events {}\nhttp {\n    server_names_hash_bucket_size 64;\n    include $ROOT/conf.d/*.conf;\n    include $ROOT/sites-enabled/*;\n}\n"
	service, _, root := hashHost(t, conf)
	probe := func(context.Context, []string) error { return errors.New("not served yet") }
	_, err := service.prepareDeploymentWebroot(context.Background(), []string{longName}, filepath.Join(root, "web"),
		[]Listener{{Address: "127.0.0.1", Port: 80}}, probe)
	if err == nil {
		t.Fatal("accepted a challenge nginx refused")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "nginx could not serve the certificate challenge: nginx refused the challenge site: server_names_hash_bucket_size is 64, too small for the server name "+longName) ||
		!strings.HasSuffix(msg, "set it to 128 in "+filepath.Join(root, "nginx.conf")+":3") || strings.Contains(msg, "configuration failed validation") {
		t.Fatalf("error = %q", msg)
	}
	if !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("error %v is no longer ErrInvalidConf", err)
	}

	// Any other refusal is nginx's own first error.
	writeNamesFile(t, filepath.Join(root, "nginx.conf"), strings.ReplaceAll(strings.ReplaceAll(conf, "64;", "128;"), "$ROOT", root))
	writeNamesFile(t, filepath.Join(root, "other-error"), "nginx: [emerg] host not found in upstream \"backend\" in /etc/nginx/conf.d/app.conf:3\n")
	_, err = service.prepareDeploymentWebroot(context.Background(), []string{longName}, filepath.Join(root, "web"),
		[]Listener{{Address: "127.0.0.1", Port: 80}}, probe)
	if err == nil || !strings.Contains(err.Error(), `host not found in upstream "backend" in /etc/nginx/conf.d/app.conf:3`) {
		t.Fatalf("error = %v", err)
	}
	for _, dir := range []string{"conf.d", "sites-available", "sites-enabled"} {
		if entries, _ := os.ReadDir(filepath.Join(root, dir)); len(entries) != 0 {
			t.Fatalf("challenge left %v behind in %s", entries, dir)
		}
	}
}
