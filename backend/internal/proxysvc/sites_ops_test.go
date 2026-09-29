package proxysvc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBulkDeleteKeepsRestrictedBackupsAndRefusesAnUnreadableBackup(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "backups"
		if blocked {
			name = "refusal"
		}
		t.Run(name, func(t *testing.T) {
			service, root := siteNginx(t, cleanTest, 0, "", 0)
			for _, site := range []string{"first", "second"} {
				full := filepath.Join(root, "sites-available", site)
				if err := os.WriteFile(full, []byte("server {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			secondBackup := filepath.Join(root, "sites-available", "second.bak")
			if blocked {
				if err := os.Mkdir(secondBackup, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(secondBackup, []byte("older\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := service.BulkSites(t.Context(), BulkDelete, []string{"first", "second"}, false)
			if blocked {
				if err == nil {
					t.Fatal("bulk delete accepted an unreadable backup")
				}
				for _, site := range []string{"first", "second"} {
					if _, statErr := os.Stat(filepath.Join(root, "sites-available", site)); statErr != nil {
						t.Fatalf("%s was removed despite the refusal: %v", site, statErr)
					}
				}
				if _, statErr := os.Stat(filepath.Join(root, "sites-available", "first.bak")); !os.IsNotExist(statErr) {
					t.Fatalf("a refused bulk delete left a backup: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, site := range []string{"first", "second"} {
				full := filepath.Join(root, "sites-available", site)
				if _, statErr := os.Stat(full); !os.IsNotExist(statErr) {
					t.Fatalf("%s was not deleted: %v", site, statErr)
				}
				info, statErr := os.Stat(full + ".bak")
				if statErr != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("%s backup mode: %v, %v", site, info, statErr)
				}
			}
		})
	}
}
