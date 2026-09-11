// アプリケーション設定API（ApiDesign.md 11章）。pb-2。
//
//	GET /api/v1/admin/settings   11.1
//	PUT /api/v1/admin/settings   11.2
//
// **必要権限はどちらも system.settings。** この権限は Phase 1 のシード（0010）から
// 存在していたが、本章が最初の利用者である。
//
// **本章が扱うのは「何が実効値で、それがどこから来たか」である。** 設定は3層に
// 分かれており（Design.md 10.3）、画面から変えられるのは第2層のうち
// ファイルも環境変数も与えていないものだけである。
package v1

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// settingItem は 11.1 の items[] の要素。
//
// **value を型付きの JSON にせず常に文字列で返す。** 真偽値を JSON の true に
// すると value の型が設定ごとに変わり、生成した型が共用体になる。画面は
// value_type を見て解釈する。
type settingItem struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`

	Layer     int      `json:"layer"`
	Value     *string  `json:"value"`
	ValueType string   `json:"value_type"`
	Allowed   []string `json:"allowed"`
	// DefaultValue は必須の設定では null になる。
	DefaultValue *string `json:"default_value"`

	Source          string `json:"source"`
	Editable        bool   `json:"editable"`
	RestartRequired bool   `json:"restart_required"`
	Secret          bool   `json:"secret"`

	// EnvKey は **Source が何であっても必ず返す**。いま環境変数で与えられて
	// いない設定にも、上書きする道を画面が示せるようにするためである。
	EnvKey string `json:"env_key"`
	// ConfigFileKey は pb.yaml に書くときのキー。Key と同じ値だが、画面が
	// 説明文を組み立てるために持つ。
	ConfigFileKey string `json:"config_file_key"`

	// UpdatedAt / UpdatedBy は app_setting の行があるときだけ埋まる。
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *actorRef  `json:"updated_by"`
}

// settingsResponse は 11.1 と 11.2 の応答。
//
// **どちらも同じ形を返す。** 保存のあとに画面が実効値と source を描き直せる
// ようにするためで、保存専用の応答を作らない。
type settingsResponse struct {
	Items []settingItem `json:"items"`
	// ConfigFilePath は効いている設定ファイルの位置（使っていなければ null）。
	// 「どこを直せばよいか」を画面が言うために返す。
	ConfigFilePath *string `json:"config_file_path"`
}

// settingsUpdateRequest は 11.2 のリクエスト本体。
//
// **items は配列である。** 設定画面の保存ボタンは1回なので、1件ずつの PUT に
// すると3件変えたときに3回の往復と3行の監査ログが出て、途中で失敗したときに
// 画面と DB がずれる。
type settingsUpdateRequest struct {
	Items []settingsUpdateItem `json:"items"`
}

// settingsUpdateItem の Value は **null を区別する必要がある**ためポインタで受ける。
// null は「行を消して既定へ戻す」を意味する（11.2）。
type settingsUpdateItem struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// listSettings は GET /api/v1/admin/settings を処理する（ApiDesign.md 11.1）。
//
// **DB を引き直して最新の行を重ねる。** Live が持つのはこのレプリカが最後に
// 読んだ状態であり、他のレプリカが変えた行を見るには引き直す必要がある。
func (h *handler) listSettings(w http.ResponseWriter, r *http.Request) {
	set, rows, e := h.settingsSnapshot(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	WriteJSON(w, http.StatusOK, buildSettingsResponse(set, rows))
}

// updateSettings は PUT /api/v1/admin/settings を処理する（ApiDesign.md 11.2）。
func (h *handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("PUT /admin/settings が認証ミドルウェアを通っていない")))
		return
	}

	var req settingsUpdateRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **いまの実効値を先に取る。** 検証には「その設定が編集できるか」が要り、
	// それは実効値の出どころで決まる（ファイルや環境変数で固定されていれば
	// 編集できない）。監査ログの from もここから取る。
	set, _, e := h.settingsSnapshot(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	changes, e := validateSettingsUpdate(req, set)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	// **1トランザクションで書き、監査ログも1行にする**（11.2）。
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		for _, c := range changes {
			if c.To == nil {
				if err := q.DeleteAppSetting(ctx, c.Key); err != nil {
					return fmt.Errorf("設定 %s を消せない: %w", c.Key, err)
				}
				continue
			}
			if err := q.UpsertAppSetting(ctx, gen.UpsertAppSettingParams{
				Key:       c.Key,
				Value:     *c.To,
				UpdatedBy: pgtype.Text{String: p.ActorID, Valid: true},
			}); err != nil {
				return fmt.Errorf("設定 %s を書けない: %w", c.Key, err)
			}
		}

		// **変更が無ければ監査ログも書かない。** 何も起きていない操作を
		// 記録すると、監査ログの読み手が「誰かが設定を変えた」と読む。
		if len(changes) == 0 {
			return nil
		}

		// **平文の秘密は detail に入らない。** 第1層と第3層は editable が
		// false なのでこの経路を通らず、通るのは第2層だけである
		// （第2層に秘密は無い。DbDesign.md 6.14）。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.SettingUpdate,
			Result:     audit.Success,
			TargetType: "app_setting",
			Detail:     map[string]any{"changes": changes},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// **書いたあとに引き直して Live を入れ替える。** これで次のリクエストから
	// 新しい値が効く（ApiDesign.md 11.3）。
	newSet, rows, e := h.settingsSnapshot(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if h.settings != nil {
		h.settings.Replace(newSet)
	}
	if h.onSettingsChanged != nil {
		h.onSettingsChanged(newSet)
	}

	WriteJSON(w, http.StatusOK, buildSettingsResponse(newSet, rows))
}

// settingsSnapshot は「ファイル・環境変数・既定値」の層に DB の行を重ねた
// 実効値と、重ねた元の行を返す。
func (h *handler) settingsSnapshot(r *http.Request) (*config.Set, []gen.ListAppSettingsRow, *apierr.Error) {
	// **重ねる土台は必ず Base（DB を含まない Set）である。** Snapshot を土台に
	// すると、前回重ねた DB 由来の値が残り、**行を消しても実効値が戻らない。**
	base := h.settings.Base()

	rows, err := h.q.ListAppSettings(r.Context())
	if err != nil {
		return nil, nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("設定の行を引けない: %w", err))
	}

	overlay := make([]config.Row, 0, len(rows))
	for _, row := range rows {
		overlay = append(overlay, config.Row{Key: row.Key, Value: row.Value})
	}
	return config.OverlayDatabase(base, overlay), rows, nil
}

// settingChange は監査ログの detail.changes[] の要素（ApiDesign.md 11.2）。
//
// From は変更前の**実効値**、To は書いた値（null は既定へ戻したこと）。
type settingChange struct {
	Key  string  `json:"key"`
	From *string `json:"from"`
	To   *string `json:"to"`
}

// validateSettingsUpdate は 11.2 の入力を検証し、実際に変わるものだけを返す。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateSettingsUpdate(req settingsUpdateRequest, set *config.Set) ([]settingChange, *apierr.Error) {
	if len(req.Items) == 0 {
		return nil, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "items", Code: "required",
			Message: "変更する設定を1件以上指定してください",
		})
	}

	var details []apierr.Detail
	var changes []settingChange
	seen := make(map[string]bool, len(req.Items))

	for i, item := range req.Items {
		field := fmt.Sprintf("items[%d].key", i)

		cur, ok := set.Get(item.Key)
		if !ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "unknown",
				Message: fmt.Sprintf("%q という設定はありません", item.Key),
			})
			continue
		}
		if seen[item.Key] {
			details = append(details, apierr.Detail{
				Field: field, Code: "duplicated",
				Message: fmt.Sprintf("%s が2回指定されています", cur.Def.DisplayName),
			})
			continue
		}
		seen[item.Key] = true

		// **編集できない設定は 409 conflict。** 値の誤りではなく、
		// 「いまその設定は他の層で固定されている」という状態の衝突である。
		if !cur.Editable() {
			return nil, apierr.New(apierr.Conflict).
				WithMessage(notEditableMessage(cur))
		}

		if item.Value == nil {
			// 既定へ戻す。**既に既定ならば何も変わらない。**
			if cur.Source == config.SourceDefault {
				continue
			}
			from := cur.Value
			changes = append(changes, settingChange{Key: item.Key, From: &from, To: nil})
			continue
		}

		v := cur.Def.Normalize(*item.Value)
		if err := cur.Def.Validate(v); err != nil {
			details = append(details, apierr.Detail{
				Field:   fmt.Sprintf("items[%d].value", i),
				Code:    "invalid",
				Message: fmt.Sprintf("%sは %s", cur.Def.DisplayName, err.Error()),
			})
			continue
		}
		// **変わらないものは changes に入れない**（11.2）。
		if v == cur.Value && cur.Source == config.SourceDatabase {
			continue
		}
		from := cur.Value
		to := v
		changes = append(changes, settingChange{Key: item.Key, From: &from, To: &to})
	}

	if len(details) > 0 {
		return nil, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return changes, nil
}

// notEditableMessage は「なぜ編集できないか」をそのまま画面に出せる日本語で返す。
//
// **理由が2つあり、利用者への説明が違う**（ApiDesign.md 11.1）。第1層は
// 構造的に扱えず、第2層は他の層で固定されている。前者は環境変数を外しても
// 編集できるようにならない。
func notEditableMessage(v config.Value) string {
	if v.Def.Layer != config.LayerRuntime {
		return fmt.Sprintf("%sは起動前に要る設定のため、画面からは変更できません。%s か設定ファイルの %s で与えてください",
			v.Def.DisplayName, v.Def.EnvKey, v.Def.Key)
	}
	switch v.Source {
	case config.SourceSecretFile:
		return fmt.Sprintf("%sは %s_FILE が指すファイルで固定されているため、画面からは変更できません",
			v.Def.DisplayName, v.Def.EnvKey)
	case config.SourceConfigFile:
		return fmt.Sprintf("%sは設定ファイルの %s で固定されているため、画面からは変更できません",
			v.Def.DisplayName, v.Def.Key)
	default:
		return fmt.Sprintf("%sは環境変数 %s で固定されているため、画面からは変更できません",
			v.Def.DisplayName, v.Def.EnvKey)
	}
}

// buildSettingsResponse は実効値と行から 11.1 の応答を組み立てる。
func buildSettingsResponse(set *config.Set, rows []gen.ListAppSettingsRow) settingsResponse {
	byKey := make(map[string]gen.ListAppSettingsRow, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}

	items := make([]settingItem, 0, len(set.All()))
	for _, v := range set.All() {
		item := settingItem{
			Key:             v.Def.Key,
			DisplayName:     v.Def.DisplayName,
			Description:     v.Def.Description,
			Layer:           int(v.Def.Layer),
			ValueType:       string(v.Def.Type),
			Allowed:         v.Def.Allowed,
			Source:          string(v.Source),
			Editable:        v.Editable(),
			RestartRequired: v.Def.RestartRequired,
			Secret:          v.Def.Secret,
			EnvKey:          v.Def.EnvKey,
			ConfigFileKey:   v.Def.Key,
		}

		// **秘密は value を返さない**（11.1）。画面は ●●●● を出す。
		if !v.Def.Secret {
			value := v.Value
			item.Value = &value
		}
		if !v.Def.Required {
			def := v.Def.Default
			item.DefaultValue = &def
		}

		// **行があるときだけ「誰がいつ」を埋める。** 実効値が DB 由来でない
		// ときは、行があっても効いていないので返さない。
		if v.Source == config.SourceDatabase {
			if row, ok := byKey[v.Def.Key]; ok {
				if row.UpdatedAt.Valid {
					at := row.UpdatedAt.Time.UTC()
					item.UpdatedAt = &at
				}
				if row.UpdatedBy.Valid {
					item.UpdatedBy = &actorRef{
						ID:          row.UpdatedBy.String,
						Kind:        row.UpdatedByKind.String,
						DisplayName: row.UpdatedByDisplayName.String,
					}
				}
			}
		}

		items = append(items, item)
	}

	resp := settingsResponse{Items: items}
	if set.ConfigFilePath != "" {
		path := set.ConfigFilePath
		resp.ConfigFilePath = &path
	}
	return resp
}
