package auth

import (
	"testing"
	"time"
)

func TestDeleteInactiveAPITokenKeepsUsableCredentials(t *testing.T) {
	svc, owner := newTestService(t, false)
	for _, tc := range []struct {
		name   string
		ttl    time.Duration
		revoke bool
		want   bool
	}{
		{name: "no expiry"},
		{name: "future expiry", ttl: time.Hour},
		{name: "revoked", revoke: true, want: true},
		{name: "expired", ttl: time.Hour, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, secret, err := svc.CreateAPIToken(t.Context(), owner, tc.name, RoleReadOnly, tc.ttl)
			if err != nil {
				t.Fatal(err)
			}
			if tc.revoke {
				if err := svc.RevokeAPIToken(t.Context(), token.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "expired" {
				if _, err := svc.st.DB.ExecContext(t.Context(), `UPDATE api_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), token.ID); err != nil {
					t.Fatal(err)
				}
			}
			deleted, err := svc.DeleteInactiveAPIToken(t.Context(), token.ID)
			if err != nil || deleted != tc.want {
				t.Fatalf("deleted = %v, err = %v, want %v", deleted, err, tc.want)
			}
			if !tc.want {
				if _, _, _, err := svc.ResolveAPIToken(t.Context(), secret); err != nil {
					t.Fatalf("usable key changed: %v", err)
				}
			}
			if tc.want {
				if _, _, _, err := svc.ResolveAPIToken(t.Context(), secret); err == nil {
					t.Fatal("removed credential authenticated")
				}
			}
		})
	}
	if deleted, err := svc.DeleteInactiveAPIToken(t.Context(), 99999); err != nil || deleted {
		t.Fatalf("missing key = %v, %v", deleted, err)
	}
}
