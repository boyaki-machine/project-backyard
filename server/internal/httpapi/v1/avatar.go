package v1

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

const avatarMaxBytes = 1 << 20

func (h *handler) getActorAvatar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.q.GetActorAvatar(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.Header().Set("Content-Type", row.ContentType)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(row.ImageData)
}

func (h *handler) putMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "PUT /me/avatar")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if r.Header.Get("Content-Type") != "image/png" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithMessage("PNG画像を選んでください"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, avatarMaxBytes+1))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.BadRequest))
		return
	}
	if len(data) == 0 || len(data) > avatarMaxBytes {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithMessage("画像は1 MiB以下にしてください"))
		return
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 256 || config.Height != 256 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithMessage("256×256ピクセルのPNG画像を選んでください"))
		return
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithMessage("PNG画像を読み取れませんでした"))
		return
	}
	// URL の版を変え、既に開いている画面にも新しい画像を読み込ませる。
	url := fmt.Sprintf("/api/v1/actors/%s/avatar?v=%d", p.ActorID, time.Now().UnixNano())
	var view sessionView
	rec := audit.FromRequest(r)
	err = h.tx.RunInTx(r.Context(), func(q gen.Querier) error {
		cur, err := q.GetActorProfile(r.Context(), p.ActorID)
		if err != nil {
			return err
		}
		if err := q.PutActorAvatar(r.Context(), gen.PutActorAvatarParams{ActorID: p.ActorID, ContentType: "image/png", ImageData: data}); err != nil {
			return err
		}
		if err := q.SetActorAvatarURL(r.Context(), gen.SetActorAvatarURLParams{ActorID: p.ActorID, AvatarUrl: pgtype.Text{String: url, Valid: true}}); err != nil {
			return err
		}
		if err := rec.Record(r.Context(), q, audit.Entry{Action: audit.UserUpdate, Result: audit.Success, TargetType: "app_user", TargetID: p.ActorID, Detail: map[string]any{"avatar": map[string]any{"before": cur.AvatarUrl.Valid, "after": true}}}); err != nil {
			return err
		}
		view, err = h.buildMeView(r.Context(), q, p)
		return err
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

func (h *handler) deleteMyAvatar(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "DELETE /me/avatar")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var view sessionView
	rec := audit.FromRequest(r)
	err := h.tx.RunInTx(r.Context(), func(q gen.Querier) error {
		cur, err := q.GetActorProfile(r.Context(), p.ActorID)
		if err != nil {
			return err
		}
		if err := q.DeleteActorAvatar(r.Context(), p.ActorID); err != nil {
			return err
		}
		if err := q.SetActorAvatarURL(r.Context(), gen.SetActorAvatarURLParams{ActorID: p.ActorID}); err != nil {
			return err
		}
		if cur.AvatarUrl.Valid {
			if err := rec.Record(r.Context(), q, audit.Entry{Action: audit.UserUpdate, Result: audit.Success, TargetType: "app_user", TargetID: p.ActorID, Detail: map[string]any{"avatar": map[string]any{"before": true, "after": false}}}); err != nil {
				return err
			}
		}
		view, err = h.buildMeView(r.Context(), q, p)
		return err
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}
