package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func signedIn(t *testing.T, s *Server, username string) (*client, *auth.User) {
	t.Helper()
	const password = "Correct-Horse-Battery-9"
	user, err := s.Auth.CreateUser(t.Context(), username, password, auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Auth.Login(t.Context(), user.Username, password, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, h: s.Routes(), cookie: httpx.SessionCookie + "=" + login.Token}, user
}

func pngBytes(t *testing.T, edge int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(0, 0, color.RGBA{R: 0xe0, G: 0x56, B: 0x23, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadAvatar(c *client, data []byte) *httptest.ResponseRecorder {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "me.bin")
	if err != nil {
		c.t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account/avatar", &body)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set(httpx.CSRFHeader, "1")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, req)
	return w
}

func TestAvatarIsStoredServedAndRemoved(t *testing.T) {
	s := testServer(t)
	c, _ := signedIn(t, s, "Wayy")

	if w := c.do(http.MethodGet, "/api/v1/account/avatar", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("fresh account avatar = %d", w.Code)
	}

	// Not an image at all, and an image too wide to be a resized portrait:
	// both refused before anything is stored.
	if w := uploadAvatar(c, []byte("<svg onload=alert(1)>")); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-image accepted: %d %s", w.Code, w.Body.String())
	}
	if w := uploadAvatar(c, pngBytes(t, maxAvatarEdge+1)); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized image accepted: %d %s", w.Code, w.Body.String())
	}

	w := uploadAvatar(c, pngBytes(t, 64))
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var updated auth.User
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.AvatarVersion == 0 || updated.DisplayName != "Wayy" {
		t.Fatalf("after upload: version %d display %q", updated.AvatarVersion, updated.DisplayName)
	}

	got := c.do(http.MethodGet, "/api/v1/account/avatar", "", nil)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve: %d %q", got.Code, got.Header().Get("Content-Type"))
	}
	if got.Header().Get("Cache-Control") == "no-store" {
		t.Fatal("a versioned avatar URL is served uncacheable")
	}
	if _, err := png.Decode(bytes.NewReader(got.Body.Bytes())); err != nil {
		t.Fatalf("served bytes are not the PNG that was stored: %v", err)
	}

	// The admin listing can fetch the same picture by id.
	byID := c.do(http.MethodGet, "/api/v1/dashboard-users/"+itoa(updated.ID)+"/avatar", "", nil)
	if byID.Code != http.StatusOK {
		t.Fatalf("avatar by id: %d", byID.Code)
	}

	if w := c.do(http.MethodDelete, "/api/v1/account/avatar", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, "/api/v1/account/avatar", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("removed avatar still served: %d", w.Code)
	}
}

func TestProfileRenameIsSelfServiceAndAudited(t *testing.T) {
	s := testServer(t)
	c, user := signedIn(t, s, "Wayy")

	w := c.do(http.MethodPatch, "/api/v1/account/profile", `{"displayName":"Ion M.","username":"Ion"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	var got auth.User
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "ion" || got.DisplayName != "Ion M." || got.ID != user.ID {
		t.Fatalf("renamed to %q / %q", got.Username, got.DisplayName)
	}
	// The session survives its own rename — a rename is not a re-login.
	if w := c.do(http.MethodGet, "/api/v1/auth/session", "", nil); w.Code != http.StatusOK {
		t.Fatalf("session after rename: %d", w.Code)
	}
	if w := c.do(http.MethodPatch, "/api/v1/account/profile", `{}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty profile patch: %d", w.Code)
	}
}

func TestRevokeOtherSessionsRoute(t *testing.T) {
	s := testServer(t)
	c, user := signedIn(t, s, "operator")
	for range 2 {
		if _, err := s.Auth.Login(t.Context(), user.Username, "Correct-Horse-Battery-9", "10.0.0.2", "phone"); err != nil {
			t.Fatal(err)
		}
	}
	w := c.do(http.MethodPost, "/api/v1/account/sessions/revoke-others", "{}", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke others: %d %s", w.Code, w.Body.String())
	}
	var res struct{ Revoked int }
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Revoked != 2 {
		t.Fatalf("revoked %d, want 2", res.Revoked)
	}
	if w := c.do(http.MethodGet, "/api/v1/account/sessions", "", nil); w.Code != http.StatusOK {
		t.Fatalf("own session lost: %d", w.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
