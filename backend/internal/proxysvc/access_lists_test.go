package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateAccessListWritesEveryAddressOneWay(t *testing.T) {
	spec, err := ValidateAccessList(AccessListSpec{
		Allow:    []string{" 10.0.0.0/8", "2001:DB8::/32", "192.168.1.7"},
		Deny:     []string{"10.0.0.9", "::ffff:10.0.0.10"},
		AuthFile: "staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := AccessListSpec{
		Allow:    []string{"10.0.0.0/8", "2001:db8::/32", "192.168.1.7"},
		Deny:     []string{"10.0.0.9", "::ffff:10.0.0.10"},
		AuthFile: "staging", Realm: "Restricted", Satisfy: "all",
	}
	if !reflect.DeepEqual(spec, want) {
		t.Errorf("got %+v\nwant %+v", spec, want)
	}

	// No password, no prompt: a realm left over from a removed password
	// file is not written.
	spec, err = ValidateAccessList(AccessListSpec{Deny: []string{"203.0.113.7"}, Realm: "Staff"})
	if err != nil || spec.Realm != "" || spec.Satisfy != "all" {
		t.Errorf("got %+v, %v", spec, err)
	}
}

func TestValidateAccessListRefusesWhatWouldNotMeanWhatItSays(t *testing.T) {
	many := make([]string, maxAccessEntries+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.%d.%d.1", i/256, i%256)
	}
	cases := []struct {
		name string
		spec AccessListSpec
		want string
	}{
		{"nothing", AccessListSpec{}, "at least one address or a password file"},
		{"not an address", AccessListSpec{Allow: []string{"10.0.0.300"}}, `"10.0.0.300" is not an IP address`},
		{"a hostname", AccessListSpec{Allow: []string{"office.example.com"}}, "is not an IP address"},
		{"bits past the range", AccessListSpec{Allow: []string{"10.0.0.5/8"}}, "10.0.0.5/8 has bits set past its /8 — the range is 10.0.0.0/8"},
		{"a zone", AccessListSpec{Deny: []string{"fe80::1%eth0"}}, "is not an IP address"},
		{"allow all", AccessListSpec{Allow: []string{"all"}}, "rather than all"},
		{"deny all", AccessListSpec{Deny: []string{"all"}}, "rather than all"},
		{"twice", AccessListSpec{Allow: []string{"10.0.0.1", " 10.0.0.1"}}, "10.0.0.1 is on the allow list twice"},
		{"twice, spelled two ways", AccessListSpec{Deny: []string{"10.0.0.1", "10.0.0.1/32"}}, "10.0.0.1/32 is on the deny list twice"},
		{"too many", AccessListSpec{Allow: many}, "the most a list takes is 512"},
		{"a path for a password file", AccessListSpec{AuthFile: "../etc/shadow"}, "is not the name of a password file"},
		{"a quote in the prompt", AccessListSpec{AuthFile: "staging", Realm: `say "hi"`}, "may not contain quotes"},
		{"a variable in the prompt", AccessListSpec{AuthFile: "staging", Realm: "$host"}, "may not contain"},
		{"a newline in the prompt", AccessListSpec{AuthFile: "staging", Realm: "a\nb"}, "control characters"},
		{"off as the prompt", AccessListSpec{AuthFile: "staging", Realm: "Off"}, "turns the password off"},
		{"either without an allow list", AccessListSpec{AuthFile: "staging", Deny: []string{"10.0.0.1"}, Satisfy: "any"}, "needs both an allow list and a password file"},
		{"either without a password", AccessListSpec{Allow: []string{"10.0.0.1"}, Satisfy: "any"}, "needs both"},
		{"another satisfy", AccessListSpec{Allow: []string{"10.0.0.1"}, Satisfy: "some"}, "satisfy must be all or any"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateAccessList(tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want it to say %q", err, tc.want)
			}
		})
	}
}

func TestRenderAccessList(t *testing.T) {
	spec, err := ValidateAccessList(AccessListSpec{
		Allow: []string{"10.0.0.0/8"}, Deny: []string{"10.0.0.9"}, AuthFile: "staging", Satisfy: "any",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := renderAccessList("/etc/nginx/jd-access/office.conf", spec, "/etc/nginx/jd-auth/staging")
	want := `# Just Dashboard owned: an access list, edited on the Sites page.
# Sites take it in with: include /etc/nginx/jd-access/office.conf;
# Every site that includes it changes with it.

# all: an allowed address and a password; any: either one.
satisfy any;
auth_basic "Restricted";
auth_basic_user_file /etc/nginx/jd-auth/staging;

# nginx stops at the first rule an address matches: the denials
# first, then the allowed, then the fence.
deny 10.0.0.9;
allow 10.0.0.0/8;
deny all;
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// Only a denial: everybody else is let in, so there is no fence, and
	// a password alone needs no satisfy.
	deny := renderAccessList("/x.conf", AccessListSpec{Deny: []string{"203.0.113.7"}, Satisfy: "all"}, "")
	if strings.Contains(deny, "deny all") || strings.Contains(deny, "satisfy") || !strings.Contains(deny, "deny 203.0.113.7;") {
		t.Errorf("a deny-only list:\n%s", deny)
	}
	password := renderAccessList("/x.conf", AccessListSpec{AuthFile: "staging", Realm: "Team", Satisfy: "all"}, "/a/staging")
	if strings.Contains(password, "satisfy") || !strings.Contains(password, "auth_basic \"Team\";\nauth_basic_user_file /a/staging;\n") {
		t.Errorf("a password-only list:\n%s", password)
	}
}

func TestAccessListReadsBackWhatItWrites(t *testing.T) {
	svc, root := debianTree(t)
	writeFile(t, filepath.Join(root, "jd-auth", "staging"), "admin:$2y$10$abcdefghijklmnopqrstuuM0R0hzjYfTyGG0cZ1H5Xca8GZ9hB6Ge\n")
	for _, spec := range []AccessListSpec{
		{Allow: []string{"10.0.0.0/8", "2001:db8::/32"}, Deny: []string{"10.0.0.9"}, AuthFile: "staging", Realm: "Staff only", Satisfy: "any"},
		{Allow: []string{"10.0.0.0/8"}, AuthFile: "staging", Satisfy: "all"},
		{Deny: []string{"203.0.113.0/24"}},
		{AuthFile: "staging"},
	} {
		spec, err := ValidateAccessList(spec)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "jd-access", "office.conf")
		content := renderAccessList(path, spec, filepath.Join(root, "jd-auth", spec.AuthFile))
		if spec.AuthFile == "" {
			content = renderAccessList(path, spec, "")
		}
		list := svc.readAccessList("office", path, content)
		if list.HandWritten != "" || list.AuthFileMissing {
			t.Errorf("%+v read back as hand-written: %q", spec, list.HandWritten)
		}
		if !reflect.DeepEqual(list.AccessListSpec, spec) {
			t.Errorf("read back %+v\nwrote     %+v", list.AccessListSpec, spec)
		}
		if list.Include != "include "+path+";" {
			t.Errorf("include = %q", list.Include)
		}
	}
}

// A file under jd-access the form would rewrite into something that does
// another thing is said to be hand-written, with why.
func TestAccessListSaysWhenAFileIsHandWritten(t *testing.T) {
	svc, root := debianTree(t)
	path := filepath.Join(root, "jd-access", "office.conf")
	cases := []struct {
		name, content, want string
	}{
		{"an allow before a deny", "allow 10.0.0.0/8;\ndeny 10.0.0.9;\ndeny all;\n", "in an order or form the page would change"},
		{"an allow list with no fence", "allow 10.0.0.0/8;\n", "in an order or form the page would change"},
		{"a directive the form does not write", "allow 10.0.0.0/8;\ndeny all;\nadd_header X-Office yes;\n", "it has add_header, which the form does not write"},
		{"a block", "limit_except GET { deny all; }\n", "it has limit_except"},
		{"a password file elsewhere", "auth_basic \"x\";\nauth_basic_user_file /etc/nginx/.htpasswd;\n", "takes its passwords from /etc/nginx/.htpasswd"},
		{"allow all", "allow all;\n", "the form cannot hold it as it is"},
		{"a file nginx cannot read", "allow 10.0.0.1\n", "nginx could not read it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if list := svc.readAccessList("office", path, tc.content); !strings.Contains(list.HandWritten, tc.want) {
				t.Errorf("handWritten = %q, want it to say %q", list.HandWritten, tc.want)
			}
		})
	}
	// Rules the form's order already has read back as the form's, whatever
	// the comments and spacing.
	if list := svc.readAccessList("office", path, "deny 10.0.0.9;   allow 10.0.0.0/8; # office\ndeny all;\n"); list.HandWritten != "" {
		t.Errorf("a list in the form's order read as hand-written: %q", list.HandWritten)
	}
	// A password file that has gone is said to be gone.
	if list := svc.readAccessList("office", path, "auth_basic \"x\";\nauth_basic_user_file "+filepath.Join(root, "jd-auth", "gone")+";\n"); !list.AuthFileMissing || list.AuthFile != "gone" {
		t.Errorf("a missing password file read as %+v", list)
	}
}

// A site uses a list when it includes it: at any depth in its own file,
// through a snippet it includes, through a glob, by a relative path from
// nginx.conf's directory — enabled or not.
func TestAccessListsSayWhichSitesIncludeThem(t *testing.T) {
	svc, root := debianTree(t)
	access := func(name string) string { return filepath.Join(root, "jd-access", name+".conf") }
	for _, name := range []string{"office", "vpn", "unused"} {
		writeFile(t, access(name), "allow 10.0.0.0/8;\ndeny all;\n")
	}
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	writeFile(t, available("app"), "server {\n    server_name app.test;\n    include "+access("office")+";\n}\n")
	writeFile(t, available("docs"), "server {\n    server_name docs.test;\n    location /admin {\n        include snippets/office-guard.conf;\n    }\n}\n")
	writeFile(t, filepath.Join(root, "snippets", "office-guard.conf"), "include jd-access/office.conf;\n")
	writeFile(t, available("all"), "server {\n    server_name all.test;\n    include "+filepath.Join(root, "jd-access")+"/*.conf;\n}\n")
	writeFile(t, available("off"), "server {\n    server_name off.test;\n    include jd-access/vpn.conf;\n}\n")
	// Includes itself through a glob: read once, not for ever.
	writeFile(t, available("loop"), "server {\n    server_name loop.test;\n    include sites-available/*;\n}\n")
	writeFile(t, filepath.Join(root, "conf.d", "legacy.conf"), "server {\n    server_name legacy.test;\n    include ./jd-access/../jd-access/vpn.conf;\n}\n")
	for _, name := range []string{"app", "docs", "all", "loop"} {
		symlink(t, available(name), filepath.Join(root, "sites-enabled", name))
	}

	lists, err := svc.ListAccessLists()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string][]string{}
	for _, list := range lists {
		used[list.Name] = []string{}
		for _, use := range list.UsedBy {
			used[list.Name] = append(used[list.Name], fmt.Sprintf("%s:%v", use.Site, use.Enabled))
		}
	}
	want := map[string][]string{
		"office": {"all:true", "app:true", "docs:true", "loop:true"},
		"vpn":    {"all:true", "legacy.conf:true", "loop:true", "off:false"},
		"unused": {"all:true", "loop:true"},
	}
	if !reflect.DeepEqual(used, want) {
		t.Errorf("used by %v\nwant %v", used, want)
	}
}

func TestSaveAccessListWritesTestsAndReloads(t *testing.T) {
	svc, root := debianTree(t)
	calls := filepath.Join(root, "calls")
	nginxShim(t, fmt.Sprintf(`echo "$*" >> '%s'; exit 0`, calls))
	if _, err := svc.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {\n    include jd-access/office.conf;\n}\n")
	symlink(t, filepath.Join(root, "sites-available", "app"), filepath.Join(root, "sites-enabled", "app"))

	res, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{
		Allow: []string{"10.0.0.0/8"}, AuthFile: "staging", Satisfy: "any",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reload == nil || res.Reload.Err != nil || !res.Validation.Valid {
		t.Fatalf("result = %+v", res)
	}
	if len(res.List.UsedBy) != 1 || res.List.UsedBy[0].Site != "app" || res.List.Satisfy != "any" || res.List.Realm != "Restricted" {
		t.Errorf("list = %+v", res.List)
	}
	b, err := os.ReadFile(filepath.Join(root, "jd-access", "office.conf"))
	if err != nil || !strings.Contains(string(b), "satisfy any;\nauth_basic \"Restricted\";\nauth_basic_user_file "+filepath.Join(root, "jd-auth", "staging")+";") {
		t.Errorf("written:\n%s (%v)", b, err)
	}
	if got, _ := os.ReadFile(calls); string(got) != "-t\n-t\n-s reload\n" {
		t.Errorf("nginx ran %q", got)
	}

	// A new list never replaces one of the same name.
	if _, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{Deny: []string{"10.0.0.1"}}, false); !errors.Is(err, ErrAccessListExists) {
		t.Errorf("saving a second office: %v", err)
	}
	// A password file that is not there would refuse every login.
	if _, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{AuthFile: "gone"}, true); err == nil || !strings.Contains(err.Error(), "no password file called gone") {
		t.Errorf("saving with a missing password file: %v", err)
	}
	if _, err := svc.SaveAccessList(context.Background(), "Office", AccessListSpec{Deny: []string{"10.0.0.1"}}, false); err == nil {
		t.Error("an upper-case name was taken")
	}
	if _, err := svc.SaveAccessList(context.Background(), "../x", AccessListSpec{Deny: []string{"10.0.0.1"}}, false); err == nil {
		t.Error("a path was taken for a name")
	}
}

// nginx refusing the new list puts the old one back byte for byte, and a
// new list it refuses is not left behind.
func TestSaveAccessListPutsBackWhatNginxRefused(t *testing.T) {
	svc, root := debianTree(t)
	list := filepath.Join(root, "jd-access", "office.conf")
	app := filepath.Join(root, "sites-available", "app")
	// A site with a login prompt of its own includes the list: a list with
	// a password is a duplicate nginx refuses in the list's file.
	nginxShim(t, fmt.Sprintf(`if grep -qs auth_basic '%s'/*.conf; then echo 'nginx: [emerg] "auth_basic" directive is duplicate in %s:6' >&2; echo 'nginx: configuration file test failed' >&2; exit 1; fi; exit 0`, filepath.Dir(list), list))
	if _, err := svc.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, app, "server {\n    auth_basic \"App\";\n    include jd-access/office.conf;\n}\n")
	symlink(t, app, filepath.Join(root, "sites-enabled", "app"))
	if _, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{Allow: []string{"10.0.0.0/8"}}, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(list)

	_, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{Allow: []string{"10.0.0.0/8"}, AuthFile: "staging"}, true)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	if want := `nginx refuses office where a site includes it: "auth_basic" directive is duplicate in ` + list + ":6"; refused.Reason() != want {
		t.Errorf("reason = %q\nwant %q", refused.Reason(), want)
	}
	if after, _ := os.ReadFile(list); string(after) != string(before) {
		t.Errorf("the refused list was left in place:\n%s", after)
	}

	_, err = svc.SaveAccessList(context.Background(), "team", AccessListSpec{AuthFile: "staging"}, false)
	if !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "jd-access", "team.conf")); !os.IsNotExist(err) {
		t.Errorf("a refused new list was left behind: %v", err)
	}
}

// A configuration nginx already refuses is said to be refused without the
// change, and an error in a site's own file names the change as the cause.
func TestSaveAccessListSaysWhoseErrorItIs(t *testing.T) {
	svc, root := debianTree(t)
	broken := filepath.Join(root, "sites-available", "broken")
	nginxShim(t, fmt.Sprintf(`echo 'nginx: [emerg] unknown directive "foo" in %s:2' >&2; exit 1`, broken))
	_, err := svc.SaveAccessList(context.Background(), "office", AccessListSpec{Deny: []string{"10.0.0.1"}}, false)
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.HasPrefix(refused.Reason(), "nginx already refuses the configuration without this change to office: ") {
		t.Fatalf("got %v", err)
	}

	list := filepath.Join(root, "jd-access", "office.conf")
	nginxShim(t, fmt.Sprintf(`if [ -e '%s' ]; then echo 'nginx: [emerg] "satisfy" directive is not allowed here in %s:9' >&2; exit 1; fi; exit 0`, list, broken))
	_, err = svc.SaveAccessList(context.Background(), "office", AccessListSpec{Deny: []string{"10.0.0.1"}}, false)
	if !errors.As(err, &refused) || !strings.HasPrefix(refused.Reason(), "nginx refuses the configuration with this change to office: ") {
		t.Fatalf("got %v", err)
	}
}

func TestDeleteAccessList(t *testing.T) {
	svc, root := debianTree(t)
	list := filepath.Join(root, "jd-access", "office.conf")
	missing := filepath.Join(root, "missing")
	// nginx refuses the configuration once "missing" exists and the list
	// does not — something the sites do not show still includes it.
	nginxShim(t, fmt.Sprintf(`if [ -e '%s' ] && [ ! -e '%s' ]; then echo 'nginx: [emerg] open() "%s" failed (2: No such file or directory) in %s:12' >&2; exit 1; fi; exit 0`,
		missing, list, list, filepath.Join(root, "nginx.conf")))
	ctx := context.Background()
	if _, err := svc.SaveAccessList(ctx, "office", AccessListSpec{Allow: []string{"10.0.0.0/8"}}, false); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(list)
	for _, name := range []string{"app", "old"} {
		writeFile(t, filepath.Join(root, "sites-available", name), "server {\n    include jd-access/office.conf;\n}\n")
	}
	symlink(t, filepath.Join(root, "sites-available", "app"), filepath.Join(root, "sites-enabled", "app"))

	err := svc.DeleteAccessList(ctx, "office")
	var inUse *AccessListInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("deleting a list two sites include: %v", err)
	}
	if want := "app and old (disabled) include office — take the include out of those sites before deleting the list"; err.Error() != want {
		t.Errorf("error = %q\nwant %q", err, want)
	}
	if _, err := os.Stat(list); err != nil {
		t.Fatalf("the list went anyway: %v", err)
	}

	os.Remove(filepath.Join(root, "sites-enabled", "app"))
	os.Remove(filepath.Join(root, "sites-available", "app"))
	os.Remove(filepath.Join(root, "sites-available", "old"))
	writeFile(t, missing, "")
	var refused *RefusedError
	if err := svc.DeleteAccessList(ctx, "office"); !errors.As(err, &refused) || !strings.HasPrefix(refused.Reason(), "nginx refuses the configuration without office: open()") {
		t.Fatalf("deleting a list nginx still reads: %v", err)
	}
	if after, _ := os.ReadFile(list); string(after) != string(content) {
		t.Fatalf("the list was not put back:\n%s", after)
	}

	os.Remove(missing)
	if err := svc.DeleteAccessList(ctx, "office"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(list); !os.IsNotExist(err) {
		t.Errorf("the list is still there: %v", err)
	}
	if backup, _ := os.ReadFile(list + ".bak"); string(backup) != string(content) {
		t.Errorf("no copy kept:\n%s", backup)
	}
	if lists, _ := svc.ListAccessLists(); len(lists) != 0 {
		t.Errorf("the copy is listed as a list: %+v", lists)
	}
	if err := svc.DeleteAccessList(ctx, "office"); !errors.Is(err, ErrNoAccessList) {
		t.Errorf("deleting it again: %v", err)
	}
}
