package procs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// hostRoot builds a host tree under a temporary directory: path → content
// for regular files, and links as link → target.
func hostRoot(t *testing.T, files map[string]string, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range links {
		full := filepath.Join(root, link)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, full); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReadUnitFilesReadsTheFragmentAndItsDropIns(t *testing.T) {
	root := hostRoot(t, map[string]string{
		"etc/systemd/system/app.service":                 "[Service]\nExecStart=/usr/bin/app\n",
		"etc/systemd/system/app.service.d/override.conf": "[Service]\nNice=5\n",
		"opt/app/linked.service":                         "[Service]\nExecStart=/opt/app/run\n",
	}, map[string]string{
		"etc/systemd/system/masked.service": "/dev/null",
		// `systemctl link` leaves an absolute link: it is the host's /opt,
		// not this process's.
		"etc/systemd/system/linked.service": "/opt/app/linked.service",
		// Links that would leave the host's root stay inside it.
		"etc/systemd/system/escape.service": "../../../../../../../../etc/os-release",
		"etc/systemd/system/environ.conf":   "/proc/self/environ",
	})
	if err := os.MkdirAll(filepath.Join(root, "etc/systemd/system/dir.service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "etc/systemd/system/fifo.conf"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := readUnitFiles(root, "/etc/systemd/system/app.service", []string{
		"/etc/systemd/system/app.service.d/override.conf",
		"/etc/systemd/system/missing.conf",
		"/etc/systemd/system/fifo.conf",
		"/etc/systemd/system/escape.service",
		"/etc/systemd/system/environ.conf",
		"relative.conf",
		"/etc/systemd/system/../system/app.service",
	})
	if len(files) != 8 {
		t.Fatalf("read %d files, want 8: %+v", len(files), files)
	}
	fragment := files[0]
	if fragment.Path != "/etc/systemd/system/app.service" || fragment.DropIn || fragment.Error != "" ||
		fragment.Content != "[Service]\nExecStart=/usr/bin/app\n" || fragment.Size != int64(len(fragment.Content)) || fragment.Truncated {
		t.Fatalf("fragment = %+v", fragment)
	}
	if d := files[1]; !d.DropIn || d.Content != "[Service]\nNice=5\n" || d.Error != "" {
		t.Fatalf("drop-in = %+v", d)
	}
	for i, want := range map[int]string{
		2: "no such file on the host",
		3: "not a regular file",
		4: "no such file on the host",
		5: "",
		6: "not an absolute path",
		7: "not an absolute path",
	} {
		got := files[i]
		if want == "" {
			// The link to /proc/self is resolved inside the host's root,
			// so it never reaches this process's environment.
			if got.Error == "" || got.Content != "" {
				t.Errorf("%s = %+v, want it refused", got.Path, got)
			}
			continue
		}
		if got.Error != want || got.Content != "" {
			t.Errorf("%s = %+v, want error %q", got.Path, got, want)
		}
	}

	masked := readUnitFiles(root, "/etc/systemd/system/masked.service", nil)
	if len(masked) != 1 || masked[0].Error != "masked: linked to /dev/null" || masked[0].Content != "" {
		t.Fatalf("masked = %+v", masked)
	}
	linked := readUnitFiles(root, "/etc/systemd/system/linked.service", nil)
	if len(linked) != 1 || linked[0].Content != "[Service]\nExecStart=/opt/app/run\n" {
		t.Fatalf("an absolute link was not read inside the host's root: %+v", linked)
	}
	dir := readUnitFiles(root, "/etc/systemd/system/dir.service", nil)
	if len(dir) != 1 || dir[0].Error != "not a regular file" {
		t.Fatalf("directory = %+v", dir)
	}
	if none := readUnitFiles(root, "", nil); none == nil || len(none) != 0 {
		t.Fatalf("a unit with no file = %#v, want an empty list", none)
	}
}

func TestReadUnitFilesCapsALargeFile(t *testing.T) {
	// The cut falls inside the three bytes of "€".
	big := strings.Repeat("a", unitFileLimit-1) + "€" + strings.Repeat("b", 100)
	root := hostRoot(t, map[string]string{"etc/systemd/system/big.service": big}, nil)
	files := readUnitFiles(root, "/etc/systemd/system/big.service", nil)
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	f := files[0]
	if !f.Truncated || f.Size != int64(len(big)) || len(f.Content) != unitFileLimit-1 || strings.ContainsRune(f.Content, '�') {
		t.Fatalf("truncated %v, size %d, content %d bytes", f.Truncated, f.Size, len(f.Content))
	}
}

// Before Linux 5.6 there is no openat2, and os.Root keeps the read inside the
// root by refusing a link that would leave it.
func TestTheFallbackTreeStaysInsideTheRoot(t *testing.T) {
	root := hostRoot(t, map[string]string{
		"etc/systemd/system/app.service": "[Service]\n",
	}, map[string]string{
		"etc/systemd/system/masked.service": "/dev/null",
		"etc/systemd/system/escape.service": "../../../../../../../../etc/os-release",
	})
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	tree := beneathTree{r}
	defer tree.Close()
	read := func(path string) UnitFile {
		f := UnitFile{Path: path}
		readUnitFile(tree, &f)
		return f
	}
	if f := read("/etc/systemd/system/app.service"); f.Content != "[Service]\n" || f.Error != "" {
		t.Errorf("regular file = %+v", f)
	}
	if f := read("/etc/systemd/system/masked.service"); f.Error != "masked: linked to /dev/null" {
		t.Errorf("masked = %+v", f)
	}
	if f := read("/etc/systemd/system/escape.service"); f.Error == "" || f.Content != "" {
		t.Errorf("a link out of the root was followed: %+v", f)
	}
}
