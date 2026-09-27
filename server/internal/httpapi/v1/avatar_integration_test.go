package v1

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

func TestAvatarIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	id := ulidgen.New()
	email := "avatar-" + id + "@example.com"
	seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleOperator)
	session := loginAs(t, r, email)
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 256, 256))); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/me/avatar", bytes.NewReader(imageData.Bytes()))
	req.Header.Set("Content-Type", "image/png")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	addCSRF(req)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	actor := actorOf(t, rec)
	avatarURL, _ := actor["avatar_url"].(string)
	if avatarURL == "" {
		t.Fatal("avatar_url is missing")
	}

	got := getWithCookie(r, avatarURL, session)
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), imageData.Bytes()) {
		t.Fatalf("GET: %d", got.Code)
	}
	if got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("Content-Type: %s", got.Header().Get("Content-Type"))
	}

	deleted := bodyWithCookie(r, http.MethodDelete, "/api/v1/me/avatar", session, "", "")
	if deleted.Code != http.StatusOK || actorOf(t, deleted)["avatar_url"] != nil {
		t.Fatalf("DELETE: %d %s", deleted.Code, deleted.Body.String())
	}
	if got := getWithCookie(r, avatarURL, session); got.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE: %d", got.Code)
	}
}
