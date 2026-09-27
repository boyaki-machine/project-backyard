package v1

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

type avatarFake struct {
	*fakeQuerier
	data []byte
	url  string
}

func (q *avatarFake) PutActorAvatar(_ context.Context, p gen.PutActorAvatarParams) error {
	q.data = append([]byte(nil), p.ImageData...)
	return nil
}
func (q *avatarFake) GetActorAvatar(context.Context, string) (gen.GetActorAvatarRow, error) {
	if q.data == nil {
		return gen.GetActorAvatarRow{}, pgx.ErrNoRows
	}
	return gen.GetActorAvatarRow{ContentType: "image/png", ImageData: q.data}, nil
}
func (q *avatarFake) DeleteActorAvatar(context.Context, string) error { q.data = nil; return nil }
func (q *avatarFake) SetActorAvatarURL(_ context.Context, p gen.SetActorAvatarURLParams) error {
	q.url = p.AvatarUrl.String
	q.profileRow.AvatarUrl = pgtype.Text{String: q.url, Valid: p.AvatarUrl.Valid}
	return nil
}

func TestAvatarUploadDisplayAndDelete(t *testing.T) {
	q := &avatarFake{fakeQuerier: meFake(t)}
	h := &handler{q: q, tx: &fakeTxRunner{q: q}}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 256, 256))); err != nil {
		t.Fatal(err)
	}

	upload := func(data []byte, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/me/avatar", bytes.NewReader(data))
		req.Header.Set("Content-Type", contentType)
		req = req.WithContext(auth.NewPrincipalContext(req.Context(), selfPrincipal()))
		rec := httptest.NewRecorder()
		h.putMyAvatar(rec, req)
		return rec
	}
	if got := upload([]byte("not an image"), "image/png"); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PNG: %d", got.Code)
	}
	if q.data != nil {
		t.Fatal("invalid PNG was stored")
	}
	if got := upload(imageData.Bytes(), "image/jpeg"); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong MIME: %d", got.Code)
	}
	var tooSmall bytes.Buffer
	if err := png.Encode(&tooSmall, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	if got := upload(tooSmall.Bytes(), "image/png"); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong dimensions: %d", got.Code)
	}
	if got := upload(imageData.Bytes(), "image/png"); got.Code != http.StatusOK {
		t.Fatalf("valid PNG: %d %s", got.Code, got.Body.String())
	}
	if q.url == "" {
		t.Fatal("avatar URL was not saved")
	}
	if len(q.audits) != 1 || q.audits[0].Action != "user.update" || bytes.Contains(q.audits[0].Detail, imageData.Bytes()) {
		t.Fatal("upload audit must record the change without storing image bytes")
	}

	r := chi.NewRouter()
	r.Get("/actors/{id}/avatar", h.getActorAvatar)
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/actors/"+testActorID+"/avatar", nil))
		return rec
	}
	if got := get(); got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), imageData.Bytes()) {
		t.Fatalf("GET avatar: %d", got.Code)
	}
	rec := httptest.NewRecorder()
	h.deleteMyAvatar(rec, meReq(http.MethodDelete, "/api/v1/me/avatar", "", selfPrincipal()))
	if rec.Code != http.StatusOK || q.url != "" {
		t.Fatalf("DELETE avatar: %d, url=%q", rec.Code, q.url)
	}
	if got := get(); got.Code != http.StatusNotFound {
		t.Fatalf("GET after delete: %d", got.Code)
	}
	if len(q.audits) != 2 || q.audits[1].Action != "user.update" {
		t.Fatal("delete audit is missing")
	}
	again := httptest.NewRecorder()
	h.deleteMyAvatar(again, meReq(http.MethodDelete, "/api/v1/me/avatar", "", selfPrincipal()))
	if again.Code != http.StatusOK || len(q.audits) != 2 {
		t.Fatalf("repeated delete: status=%d audits=%d", again.Code, len(q.audits))
	}
}
