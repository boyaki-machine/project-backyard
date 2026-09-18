// PB 全体のバックアップと復元（ApiDesign.md 11.11〜11.13）。pb-147。
//
//	GET  /api/v1/admin/backup.tar.gz   11.11
//	POST /api/v1/admin/restore         11.12
//
// **必要権限は system.settings**（11章と同じ）。
//
// **オーナーの資格情報を保存しない**（DbDesign.md 3.4）。取り込みのあいだだけ接続に
// 使い、応答にも構造化ログにも監査ログにも残さない。
package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/backup"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/maintenance"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// Backups は PB 全体の書き出しと取り込みを行う口。
//
// **インターフェースで受けるのは、ハンドラのテストが実 DB を立てずに
// 差し替えられるようにするため**（DatabaseStats と同じ理由）。
type Backups interface {
	// Dump は書庫を w へ流す。
	Dump(ctx context.Context, w io.Writer) error
	// CaptureSession は操作者のセッション行を控える。無ければ nil を返す。
	CaptureSession(ctx context.Context, tokenID string) (*backup.KeptRow, error)
	// Restore は書庫を取り込む。
	Restore(ctx context.Context, ownerURL string, src io.Reader, keep *backup.KeptRow) (backup.Result, error)
	// OwnerURL は、受け取ったロール名とパスワードから接続文字列を組み立てる。
	//
	// **組み立てるのは PB 自身である。** 画面が渡すのはロール名とパスワードだけで、
	// 接続先は PB がいま繋いでいるところを使う。
	OwnerURL(user, password string) string
}

// downloadBackup は GET /api/v1/admin/backup.tar.gz を処理する（11.11）。
func (h *handler) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if h.backups == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /admin/backup.tar.gz に書き出しの口が渡されていない")))
		return
	}
	rec := audit.FromRequest(r)

	// **この要求のあいだだけ応答の期限を外す**（11.11）。サーバ全体の WriteTimeout は
	// 60 秒で、行が増えれば書き出しはこれを超える。**超えると接続が途中で切れ、
	// 壊れた書庫が落ちる**——ブラウザからは途中まで落ちたファイルと区別が付かない。
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	// **見出しは書き始める前に出す。** 途中で落ちても、ここまでに送った分は
	// 取り消せない——だからこそ、失敗を隠さずログと監査に残す。
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+backup.FileName(time.Now())+`"`)
	w.WriteHeader(http.StatusOK)

	if err := h.backups.Dump(r.Context(), w); err != nil {
		// **応答は始まっている**ので、エラー形式へ切り替えられない。
		// **監査に failure を残す**のが、利用者から見て壊れていることの唯一の跡になる。
		h.recordBackup(r, rec, audit.Failure, map[string]any{"cause": err.Error()})
		slog.ErrorContext(r.Context(), "バックアップの書き出しに失敗した",
			slog.String("cause", err.Error()))
		return
	}
	h.recordBackup(r, rec, audit.Success, nil)
}

func (h *handler) recordBackup(r *http.Request, rec *audit.Recorder, result audit.Result, detail map[string]any) {
	if h.tx == nil {
		return
	}
	_ = h.tx.RunInTx(context.WithoutCancel(r.Context()), func(q gen.Querier) error {
		rec.RecordOrLog(context.WithoutCancel(r.Context()), q, audit.Entry{
			Action: audit.DatabaseBackup, Result: result,
			TargetType: "database", Detail: detail,
		})
		return nil
	})
}

// restoreResponse は 11.12 の応答。
type restoreResponse struct {
	RestoredAt       time.Time          `json:"restored_at"`
	Backup           restoreBackupMeta  `json:"backup"`
	MigrationVersion int64              `json:"migration_version"`
	SessionKept      bool               `json:"session_kept"`
	Tables           []restoreTableView `json:"tables"`
	Mismatched       []string           `json:"mismatched"`
}

type restoreBackupMeta struct {
	FormatVersion    int       `json:"format_version"`
	MigrationVersion int64     `json:"migration_version"`
	CreatedAt        time.Time `json:"created_at"`
	PBVersion        string    `json:"pb_version"`
}

type restoreTableView struct {
	Name     string `json:"name"`
	Expected int64  `json:"expected"`
	Rows     int64  `json:"rows"`
}

// maxCredentialBytes はロール名とパスワードのパートの上限。
//
// **書庫は流して読むので上限を持たない**が、資格情報は短い。**先に読むパートに
// 上限を置かないと、書庫を資格情報として送り込まれたときにメモリへ載る。**
const maxCredentialBytes = 4 * 1024

// restoreBackup は POST /api/v1/admin/restore を処理する（11.12）。
func (h *handler) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if h.backups == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /admin/restore に取り込みの口が渡されていない")))
		return
	}
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /admin/restore が認証ミドルウェアを通っていない")))
		return
	}

	// **受信と応答の期限を外す**（11.12）。書庫の受信は ReadTimeout（30 秒）を、
	// 取り込みそのものは WriteTimeout（60 秒）を超える。**歯止めは保守モードで、
	// このあいだ他の要求は入らない。**
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
	}

	mr, e := multipartReader(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **パートは先頭から順に読む**（11.12）。`archive` が最後でないと、
	// 資格情報を読む前に書庫が流れ込む。
	ownerUser, e := readTextPart(mr, "owner_user")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	ownerPassword, e := readTextPart(mr, "owner_password")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	archive, e := nextPart(mr, "archive")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **保守モードに入る。** ここから先は、他の要求を受け付けない（11.13）。
	leave, err := h.maintenance.Enter("バックアップの取り込み")
	if errors.Is(err, maintenance.ErrBusy) {
		apierr.WriteCode(w, r, apierr.Maintenance)
		return
	} else if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	defer leave()

	// **操作者のセッションを控えるのは、取り込みを始める前である**（11.12）。
	// 始めてからでは、控える相手の表がもう無い。
	kept, err := h.backups.CaptureSession(r.Context(), p.TokenID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("セッションを控えられない: %w", err)))
		return
	}

	rec := audit.FromRequest(r)
	res, err := h.backups.Restore(r.Context(),
		h.backups.OwnerURL(ownerUser, ownerPassword), archive, kept)
	if err != nil {
		h.recordRestore(r, rec, audit.Failure, map[string]any{"cause": err.Error()})
		apierr.Write(w, r, restoreError(err))
		return
	}

	// **監査は取り込みが終わったあと、別のトランザクションで書く**（11.12）。
	// audit_log 自身が入れ替わるので、同じトランザクションで書くと行ごと消える。
	h.recordRestore(r, rec, audit.Success, map[string]any{
		"backup_created_at":        res.Backup.CreatedAt.Format(time.RFC3339),
		"backup_migration_version": res.Backup.MigrationVersion,
		"migration_version":        res.MigrationVersion,
		"mismatched":               len(res.Mismatched),
		"session_kept":             res.SessionKept,
		// **owner_user は残すが owner_password は残さない**（11.12）。
		"owner_user": ownerUser,
	})
	WriteJSON(w, http.StatusOK, buildRestoreResponse(res))
}

func (h *handler) recordRestore(r *http.Request, rec *audit.Recorder, result audit.Result, detail map[string]any) {
	if h.tx == nil {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		rec.RecordOrLog(ctx, q, audit.Entry{
			Action: audit.DatabaseRestore, Result: result,
			TargetType: "database", Detail: detail,
		})
		return nil
	}); err != nil {
		// **失敗しても応答は返す。** 取り込みそのものは終わっている。
		slog.ErrorContext(ctx, "取り込みの監査ログを書けない",
			slog.String("cause", err.Error()))
	}
}

// restoreError は backup パッケージの誤りを 11.12 の応答へ写す。
func restoreError(err error) *apierr.Error {
	switch {
	case errors.Is(err, backup.ErrTooNew):
		return apierr.New(apierr.BackupTooNew).
			WithMessage("このバックアップは、いまの PB より新しいバージョンで作られています。PB を新しくしてから取り込んでください").
			WithCause(err)
	case errors.Is(err, backup.ErrBadOwnerCredentials):
		return apierr.New(apierr.ValidationFailed).
			WithMessage("DB のオーナー権限で接続できませんでした。ロール名とパスワードを確かめてください").
			WithDetails(apierr.Detail{
				Field: "owner_password", Code: "invalid",
				Message: "この資格情報では DB へ接続できません",
			}).
			WithCause(err)
	case errors.Is(err, backup.ErrBadArchive):
		return apierr.New(apierr.ValidationFailed).
			WithMessage("バックアップファイルを読めませんでした。PB が書き出したファイルを選んでください").
			WithDetails(apierr.Detail{
				Field: "archive", Code: "invalid",
				Message: "このファイルは PB のバックアップとして読めません",
			}).
			WithCause(err)
	default:
		// **DB が中途半端なまま残っている**（11.12）。表が落ちたあと、行が入る前で
		// 止まりうる。**そう書き、同じ書庫でのやり直しを案内する。**
		return apierr.New(apierr.InternalError).
			WithMessage("取り込みの途中で失敗しました。データベースは中途半端な状態です。同じファイルでもう一度取り込んでください").
			WithCause(err)
	}
}

func buildRestoreResponse(res backup.Result) restoreResponse {
	tables := make([]restoreTableView, 0, len(res.Tables))
	for _, t := range res.Tables {
		tables = append(tables, restoreTableView{Name: t.Name, Expected: t.Expected, Rows: t.Rows})
	}
	// **空でも [] で返す**（null にしない）。画面が配列として回せるように。
	mismatched := res.Mismatched
	if mismatched == nil {
		mismatched = []string{}
	}
	return restoreResponse{
		RestoredAt: res.RestoredAt.UTC(),
		Backup: restoreBackupMeta{
			FormatVersion:    res.Backup.FormatVersion,
			MigrationVersion: res.Backup.MigrationVersion,
			CreatedAt:        res.Backup.CreatedAt.UTC(),
			PBVersion:        res.Backup.PBVersion,
		},
		MigrationVersion: res.MigrationVersion,
		SessionKept:      res.SessionKept,
		Tables:           tables,
		Mismatched:       mismatched,
	}
}

// multipartReader は multipart/form-data の読み手を返す。
func multipartReader(r *http.Request) (*multipart.Reader, *apierr.Error) {
	ct := r.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/form-data" {
		return nil, apierr.New(apierr.BadRequest).
			WithMessage("multipart/form-data で送ってください")
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, apierr.New(apierr.BadRequest).
			WithMessage("multipart/form-data の boundary がありません")
	}
	return multipart.NewReader(r.Body, boundary), nil
}

// nextPart は次のパートを取り、名前が想定どおりかを見る。
//
// **順序を検査する。** 名前で探し回らないのは、`archive` より先に資格情報を
// 読み終える必要があるためである（11.12）。
func nextPart(mr *multipart.Reader, want string) (*multipart.Part, *apierr.Error) {
	part, err := mr.NextPart()
	if err != nil {
		return nil, apierr.New(apierr.BadRequest).
			WithMessage(fmt.Sprintf("%s が送られていません", want))
	}
	if part.FormName() != want {
		return nil, apierr.New(apierr.BadRequest).
			WithMessage(fmt.Sprintf("%s を %d 番目に送ってください（届いたのは %s）",
				want, 0, part.FormName()))
	}
	return part, nil
}

// readTextPart は短いパートを読み切って文字列にする。
func readTextPart(mr *multipart.Reader, want string) (string, *apierr.Error) {
	part, e := nextPart(mr, want)
	if e != nil {
		return "", e
	}
	defer func() { _ = part.Close() }()
	b, err := io.ReadAll(io.LimitReader(part, maxCredentialBytes+1))
	if err != nil {
		return "", apierr.New(apierr.BadRequest).
			WithMessage(fmt.Sprintf("%s を読めません", want))
	}
	if len(b) > maxCredentialBytes {
		return "", apierr.New(apierr.BadRequest).
			WithMessage(fmt.Sprintf("%s が長すぎます", want))
	}
	return strings.TrimSpace(string(b)), nil
}
