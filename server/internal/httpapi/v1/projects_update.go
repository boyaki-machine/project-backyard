// プロジェクトの更新（ApiDesign.md 5.5 / 5.6）。
//
//	PATCH /api/v1/projects/{key}            5.5   部分更新・If-Match 必須
//	POST  /api/v1/projects/{key}/archive    5.6   status を archived へ
//	POST  /api/v1/projects/{key}/unarchive  5.6   status を active へ
//
// **3本とも応答は 5.4 と同形式**（更新後のプロジェクト）である。組み立ては
// project_view.go の buildProjectDetail が持ち、更新の直後に同じトランザクション
// から読む。別トランザクションで読むと、返した version が既に古いことがありうる。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// updateProjectRequest は 5.5 のリクエスト本体。
//
// **「送られなかった」と「送られた」を区別できる型で受ける。** 5.5 は
// 「送られたフィールドだけを更新する」と定めるため、ゼロ値では足りない。
//
// name は *string で足りる（nil＝未送信）。description は
// `"description": null`（説明を消す）という3つ目の状態があり、*string では
// 未送信と区別できないので json.RawMessage で受け、キーの有無と値が null か
// どうかを parseDescriptionField が分けて読む。
//
// Key は更新できないフィールドだが、**送られてきたことを検出するために持つ**
// （5.5：送られた場合は 422 immutable_field）。黙って無視すると、フロントが
// キーを変えたつもりで変わっていない状態になる。
type updateProjectRequest struct {
	Key         *string         `json:"key"`
	Name        *string         `json:"name"`
	Description json.RawMessage `json:"description"`
	Settings    json.RawMessage `json:"settings"`
}

// patchProject は PATCH /api/v1/projects/{key} を処理する（ApiDesign.md 5.5）。
func (h *handler) patchProject(w http.ResponseWriter, r *http.Request) {
	p, key, ok := projectRequestContext(w, r, "PATCH /projects/{key}")
	if !ok {
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("PATCH /projects/{key} にトランザクション実行口が渡っていない")))
		return
	}

	var req updateProjectRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	version, versionErr := parseIfMatch(r)
	params, validationErr := buildUpdateProjectParams(&req, key, version)
	// 2.5 の details は「項目ごとの誤り」を並べるものなので、If-Match の欠落と
	// 本文の誤りを別々の応答に分けず、1つの 422 にまとめる（listProjects と同じ）。
	if e := mergeValidationErrors(versionErr, validationErr); e != nil {
		apierr.Write(w, r, e)
		return
	}

	systemPerms, ctx, err := middleware.SystemPermissions(r.Context(), h.q, p)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	var view projectDetailView
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		rows, err := q.UpdateProject(ctx, params)
		if err != nil {
			return fmt.Errorf("プロジェクト %q を更新できない: %w", key, err)
		}
		if rows == 0 {
			// 0 行の理由は2つある。version 不一致（409）か、プロジェクトが
			// 消えた（404）か。**行が在るかどうかを引いて区別する。**
			// 一律に 409 と返すと、削除済みのプロジェクトに対して
			// 「競合している」という誤った説明を返すことになる。
			return classifyUpdateMiss(ctx, q, key)
		}
		view, err = buildProjectDetail(ctx, q, p, systemPerms, key)
		return err
	})
	if err != nil {
		writeProjectUpdateError(w, r, key, err)
		return
	}

	WriteJSON(w, http.StatusOK, view)
}

// archiveProject / unarchiveProject は 5.6 の2本。**中身は setProjectStatus
// 1つ**で、渡す status だけが違う。片方にしか入らない分岐を作らないため。
func (h *handler) archiveProject(w http.ResponseWriter, r *http.Request) {
	h.setProjectStatus(w, r, projectStatusArchived, "POST /projects/{key}/archive")
}

func (h *handler) unarchiveProject(w http.ResponseWriter, r *http.Request) {
	h.setProjectStatus(w, r, projectStatusActive, "POST /projects/{key}/unarchive")
}

// setProjectStatus は archive / unarchive を処理する（ApiDesign.md 5.6）。
//
// **If-Match は要求しない**（2.8）。冪等であり、競合しても失われる編集内容が
// ないためである。**既にその状態なら何も変えずに現状を返す**（5.6）。
//
// 監査は archive / unarchive のどちらも project.archive として記録する
// （2.10 のカタログにこの1つしかない）。detail の status で区別できる。
func (h *handler) setProjectStatus(w http.ResponseWriter, r *http.Request, status, route string) {
	p, key, ok := projectRequestContext(w, r, route)
	if !ok {
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s にトランザクション実行口が渡っていない", route)))
		return
	}

	systemPerms, ctx, err := middleware.SystemPermissions(r.Context(), h.q, p)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	rec := audit.FromRequest(r)

	var view projectDetailView
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		rows, err := q.SetProjectStatus(ctx, gen.SetProjectStatusParams{Key: key, Status: status})
		if err != nil {
			return fmt.Errorf("プロジェクト %q の状態を %s にできない: %w", key, status, err)
		}

		// 応答の材料であると同時に、行が在るかの確認でもある。0 行だった理由が
		// 「既にその状態」なのか「消えた」のかは、ここで ErrNoRows になるかで分かる。
		view, err = buildProjectDetail(ctx, q, p, systemPerms, key)
		if err != nil {
			return err
		}

		// **状態が実際に変わったときだけ監査に残す。** 冪等な空振りを記録すると、
		// 二重送信のたびに監査ログが増えて、本当の切り替えが埋もれる。
		if rows == 0 {
			return nil
		}
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.ProjectArchive,
			Result:     audit.Success,
			TargetType: "project",
			TargetID:   view.ID,
			Detail:     map[string]any{"key": key, "status": status},
		})
	})
	if err != nil {
		writeProjectUpdateError(w, r, key, err)
		return
	}

	WriteJSON(w, http.StatusOK, view)
}

// ── 入力の検証 ──────────────────────────────────────────────

// errVersionConflict は If-Match が現在の version と食い違ったことを表す
// （ApiDesign.md 2.8 の 409 conflict）。トランザクションの中から外へ
// 「これは 500 ではない」と伝えるためだけの番兵である。
var errVersionConflict = errors.New("version が一致しない")

// parseIfMatch は If-Match ヘッダから version を取り出す（ApiDesign.md 2.8）。
//
// **省略は 422**（2.8）。ヘッダを付け忘れた実装が黙って上書きできると、
// 楽観ロックが「掛かっているつもり」の状態になる。
//
// 値は 5.5 の例のとおり `"3"` という引用符付きの entity-tag で来る。RFC 9110
// 8.8.3 の構文どおり引用符を外して読むが、**引用符無しの `3` も受け付ける。**
// curl や CLI から手で叩いたときに引用符が落ちやすく、ここで弾いても
// 得られる安全性が無いためである。
func parseIfMatch(r *http.Request) (int32, *apierr.Error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, apierr.New(apierr.ValidationFailed).
			WithMessage("更新には If-Match ヘッダが必要です。取得時の version を指定してください").
			WithDetails(apierr.Detail{
				Field: "If-Match", Code: "required",
				Message: "取得時の version を If-Match ヘッダに指定してください",
			})
	}

	// 弱い検証子は楽観ロックに使えない（RFC 9110 13.1.1 は If-Match に
	// strong comparison を求める）。project の version は更新のたびに必ず
	// 変わるので、そもそも弱い検証子として出すことがない。
	unquoted := strings.Trim(raw, `"`)
	version, err := strconv.ParseInt(unquoted, 10, 32)
	if err != nil || version < 1 {
		return 0, apierr.New(apierr.ValidationFailed).
			WithDetails(apierr.Detail{
				Field: "If-Match", Code: "invalid",
				Message: "If-Match には取得時の version（正の整数）を指定してください",
			})
	}
	return int32(version), nil
}

// buildUpdateProjectParams は 5.5 の本文を検証してクエリ引数へ写す。
//
// **details には見つかった誤りをすべて載せる**（2.5）。フォームの各入力欄に
// 紐づけるため、最初の1件で打ち切らない（validateCreateProject と同じ方針）。
func buildUpdateProjectParams(
	req *updateProjectRequest, key string, version int32,
) (gen.UpdateProjectParams, *apierr.Error) {
	params := gen.UpdateProjectParams{Key: key, Version: version}
	var details []apierr.Detail

	// key は変更できない（5.5）。**値が現在と同じでも 422 にする。**
	// 「送っても効かないことがある」という曖昧な仕様にしないため。
	if req.Key != nil {
		details = append(details, apierr.Detail{
			Field: "key", Code: "immutable_field",
			Message: "プロジェクトキーは作成後に変更できません",
		})
	}

	if req.Name != nil {
		switch n := utf8.RuneCountInString(*req.Name); {
		case n == 0:
			details = append(details, apierr.Detail{
				Field: "name", Code: "required", Message: "プロジェクト名を入力してください",
			})
		case n > maxProjectNameLength:
			details = append(details, apierr.Detail{
				Field: "name", Code: "too_long",
				Message: fmt.Sprintf("プロジェクト名は%d文字以内で入力してください", maxProjectNameLength),
			})
		default:
			params.Name = pgtype.Text{String: *req.Name, Valid: true}
		}
	}

	if d := parseDescriptionField(req.Description, &details); d != nil {
		params.DescriptionSet = true
		params.Description = *d
	}

	if req.Settings != nil {
		// settings は jsonb をそのまま入れる（5.4 も列の内容をそのまま返す）。
		// 中身の構造は Phase 1 では定義されていない（5.4 の例にある
		// max_concurrent_agents は Phase 2）。**JSON オブジェクトであることだけ**
		// を確かめる。配列や数値を入れると、後から項目を足せなくなる。
		var probe map[string]any
		if err := json.Unmarshal(req.Settings, &probe); err != nil {
			details = append(details, apierr.Detail{
				Field: "settings", Code: "invalid",
				Message: "settings は JSON オブジェクトで指定してください",
			})
		} else {
			params.Settings = req.Settings
		}
	}

	if len(details) == 0 {
		return params, nil
	}
	return gen.UpdateProjectParams{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
}

// parseDescriptionField は description の3通りを見分ける。
//
//	キーが無い        → nil（据え置く）
//	"description": null → NULL（説明を消す）
//	文字列            → その値
//
// **空文字は NULL に倒す。** POST /projects（optionalText）と同じ扱いにして、
// 作成と更新で `description` の有無の意味がずれないようにする。
func parseDescriptionField(raw json.RawMessage, details *[]apierr.Detail) *pgtype.Text {
	if raw == nil {
		return nil
	}
	if string(raw) == "null" {
		return &pgtype.Text{}
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		*details = append(*details, apierr.Detail{
			Field: "description", Code: "invalid",
			Message: "説明は文字列で指定してください",
		})
		return nil
	}
	if utf8.RuneCountInString(s) > maxProjectDescriptionLength {
		*details = append(*details, apierr.Detail{
			Field: "description", Code: "too_long",
			Message: fmt.Sprintf("説明は%d文字以内で入力してください", maxProjectDescriptionLength),
		})
		return nil
	}
	t := optionalText(s)
	return &t
}

// ── 失敗の応答 ──────────────────────────────────────────────

// classifyUpdateMiss は UPDATE が 0 行だった理由を 409 と 404 に分ける。
//
// UpdateProject の WHERE は key と version の両方を見るため、0 行は
// 「version が違う」か「その key が無い」のどちらかである。
func classifyUpdateMiss(ctx context.Context, q gen.Querier, key string) error {
	exists, err := q.ProjectKeyExists(ctx, key)
	if err != nil {
		return fmt.Errorf("プロジェクト %q の存在を確認できない: %w", key, err)
	}
	if exists {
		return errVersionConflict
	}
	return pgx.ErrNoRows
}

// writeProjectUpdateError は更新の失敗を応答に写す（5.5.1）。
func writeProjectUpdateError(w http.ResponseWriter, r *http.Request, key string, err error) {
	if errors.Is(err, errVersionConflict) {
		apierr.Write(w, r, apierr.New(apierr.Conflict).
			WithMessage("他の利用者がこのプロジェクトを更新しました。内容を読み直してからやり直してください").
			WithCause(fmt.Errorf("プロジェクト %q の version が一致しない", key)))
		return
	}
	writeProjectDetailError(w, r, key, err)
}
