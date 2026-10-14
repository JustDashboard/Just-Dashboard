package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func TestUploadCleanupAndMetadataStayWithPinnedParent(t *testing.T) {
	for _, change := range []string{"outside-symlink", "inside-symlink", "replacement-directory"} {
		for _, guard := range []string{"legacy", "revision"} {
			for _, transfer := range []string{"complete", "interrupted"} {
				t.Run(change+"/"+guard+"/"+transfer, func(t *testing.T) {
					root, outside := t.TempDir(), t.TempDir()
					parent := filepath.Join(root, "images")
					moved := filepath.Join(root, "moved-images")
					if err := os.Mkdir(parent, 0o700); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(parent, "image.png")
					opened := "original image bytes"
					if err := os.WriteFile(path, []byte(opened), 0o600); err != nil {
						t.Fatal(err)
					}
					revision := contentguard.Revision(opened)
					var expected *string
					if guard == "revision" {
						expected = &revision
					}
					var shadow string
					var shadowInfo os.FileInfo
					var body io.Reader = strings.NewReader("new image bytes")
					if transfer == "interrupted" {
						body = &failingReader{n: 1}
					}
					reader := &beforeRead{r: body, before: func() {
						temps, err := filepath.Glob(filepath.Join(parent, ".jd-upload-*"))
						if err != nil || len(temps) != 1 {
							t.Fatalf("expected one stage before native retarget: %v %v", temps, err)
						}
						if err := os.Rename(parent, moved); err != nil {
							t.Fatal(err)
						}
						replacement := outside
						switch change {
						case "inside-symlink":
							replacement = filepath.Join(root, "replacement")
							if err := os.Mkdir(replacement, 0o700); err != nil {
								t.Fatal(err)
							}
						case "replacement-directory":
							replacement = parent
							if err := os.Mkdir(parent, 0o700); err != nil {
								t.Fatal(err)
							}
						}
						if change != "replacement-directory" {
							if err := os.Symlink(replacement, parent); err != nil {
								t.Fatal(err)
							}
						}
						// Matching bytes also exercise the parent identity guard:
						// a replacement directory has the same resolved pathname.
						if err := os.WriteFile(filepath.Join(replacement, "image.png"), []byte(opened), 0o644); err != nil {
							t.Fatal(err)
						}
						shadow = filepath.Join(replacement, filepath.Base(temps[0]))
						if err := os.WriteFile(shadow, []byte("unrelated native file"), 0o644); err != nil {
							t.Fatal(err)
						}
						shadowInfo, err = os.Stat(shadow)
						if err != nil {
							t.Fatal(err)
						}
					}}
					_, err := New([]string{root}).UploadChecked(path, reader, true, expected)
					if transfer == "interrupted" {
						if err == nil || !strings.Contains(err.Error(), "connection reset") {
							t.Fatalf("interrupted upload returned %v", err)
						}
					} else if change == "outside-symlink" {
						if !errors.Is(err, ErrOutsideRoot) {
							t.Fatalf("outward retarget returned %v", err)
						}
					} else if !errors.Is(err, contentguard.ErrConflict) {
						t.Fatalf("native parent replacement returned %v", err)
					}
					info, err := os.Stat(shadow)
					if err != nil || !os.SameFile(shadowInfo, info) || info.Mode().Perm() != 0o644 {
						t.Fatalf("upload changed or removed an unrelated shadow file: %v %v", info, err)
					}
					if content, err := os.ReadFile(shadow); err != nil || string(content) != "unrelated native file" {
						t.Fatalf("upload changed shadow bytes: %q %v", content, err)
					}
					for _, image := range []string{filepath.Join(moved, "image.png"), path} {
						if content, err := os.ReadFile(image); err != nil || string(content) != opened {
							t.Fatalf("upload replaced a native image: %q %v", content, err)
						}
					}
					if temps, err := filepath.Glob(filepath.Join(moved, ".jd-upload-*")); err != nil || len(temps) != 0 {
						t.Fatalf("stage leaked in the renamed original parent: %v %v", temps, err)
					}
				})
			}
		}
	}
}
