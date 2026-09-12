// 締め出されうる設定の「確認しないと元に戻す」仕組み（pb-97、Design.md 10.3）。
//
//	① 設定を変える            → 「未確認」として記録し、期限を置く
//	② 新しい設定で画面へ入る   → 「アクセスできました」を押す
//	③ 押されたら確定          → 未確認の記録を消す。以後は戻らない
//	③' 期限までに押されなければ → 元の値へ戻す
//
// **対象は「その変更で、その画面へ戻れなくなるか」だけで決める**（重要さでは
// 決めない。重要さは人によって違う）。判定は config の NeedsConfirm が持つ。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ConfirmWindow は未確認のままでいられる時間（Design.md 10.3）。
//
// **設定にしない**（利用者の判断、2026-09-12）。設定にすると、**その設定自身を
// 誤ったときに戻せない**——10.3 の「反映の間隔を設定にしない」と同じ形である。
const ConfirmWindow = 300 * time.Second

// pendingView は 11.1 / 11.2 の pending_confirmation。
type pendingView struct {
	// Keys は確認を待っているキー。**1回の保存を1件として扱う**（11.2）。
	Keys []string `json:"keys"`
	// ExpiresAt を過ぎると元へ戻る。画面は残り時間をここから出す。
	ExpiresAt time.Time `json:"expires_at"`
}

// confirmKeys は「確認が要る」設定のキー集合。
func confirmKeys() map[string]bool {
	out := make(map[string]bool, 2)
	for _, d := range config.Definitions() {
		if d.NeedsConfirm {
			out[d.Key] = true
		}
	}
	return out
}

// decodePrevious は行の previous を読む。**null は「行が無かった」**を表す。
func decodePrevious(blob []byte) (map[string]*string, error) {
	var out map[string]*string
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil, fmt.Errorf("戻す値を読めない: %w", err)
	}
	return out, nil
}

// pendingFromRow は行を画面向けの形にする。
func pendingFromRow(row gen.PendingSettingChange) (*pendingView, error) {
	prev, err := decodePrevious(row.Previous)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(prev))
	for k := range prev {
		keys = append(keys, k)
	}
	// **並びを固定する。** 応答が呼ぶたびに変わると、画面の差分が無駄に動く。
	sortStrings(keys)
	return &pendingView{Keys: keys, ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// confirmSettings は POST /api/v1/admin/settings/confirm を処理する（11.8）。
//
// **「新しい設定を通って届いたか」を見る**（Design.md 10.3）。ここを見ないと、
// **切り替えが失敗していても確定してしまう。**
func (h *handler) confirmSettings(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(errors.New("POST /admin/settings/confirm が認証ミドルウェアを通っていない")))
		return
	}
	ctx := r.Context()

	row, err := h.q.GetPendingSettingChange(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound).
			WithMessage("確認を待っている設定変更はありません"))
		return
	} else if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("未確認の変更を引けない: %w", err)))
		return
	}

	prev, err := decodePrevious(row.Previous)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	if e := h.checkConfirmReached(r, prev); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := audit.FromRequest(r)
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		n, err := q.DeletePendingSettingChange(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("未確認の記録を消せない: %w", err)
		}
		if n == 0 {
			// **すでに戻されていた。** 期限とちょうど競合した場合である。
			return apierr.New(apierr.Conflict).
				WithMessage("期限が切れて元の設定に戻りました。もう一度変更してください")
		}
		keys := make([]string, 0, len(prev))
		for k := range prev {
			keys = append(keys, k)
		}
		sortStrings(keys)
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.SettingUpdate,
			Result:     audit.Success,
			TargetType: "app_setting",
			Detail:     map[string]any{"confirmed": keys},
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

	slog.Info("設定変更を確定した（以後は元へ戻らない）",
		slog.Any("keys", pendingKeys(prev)))
	w.WriteHeader(http.StatusNoContent)
}

// checkConfirmReached は「新しい設定を通って届いたか」をキーごとに見る（11.8）。
//
// **キーごとに条件が違う。**
//
//   - tls_enabled：**接続が設定どおりか。** 平文で届いた確認を受け取ると、
//     切り替えが失敗していても確定してしまう
//   - cookie_secure：**ここに届いたこと自体で足りる。** この口は認証が要り、
//     cookie_secure が有効なのに Cookie が届いているなら、ブラウザは HTTPS で
//     繋いでいる。**前段にプロキシを置く構成でも成立する**（PB には平文で届く）
func (h *handler) checkConfirmReached(r *http.Request, prev map[string]*string) *apierr.Error {
	if _, ok := prev[config.KeyTLSEnabled]; !ok {
		return nil
	}
	set := h.settings.Snapshot()
	want := set.Bool(config.KeyTLSEnabled)
	if got := r.TLS != nil; want != got {
		if want {
			return apierr.New(apierr.Conflict).
				WithMessage("この確認は HTTPS で届いていません。https で開き直してから押してください")
		}
		return apierr.New(apierr.Conflict).
			WithMessage("この確認は HTTPS で届いています。http で開き直してから押してください")
	}
	return nil
}

func pendingKeys(prev map[string]*string) []string {
	keys := make([]string, 0, len(prev))
	for k := range prev {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

// SettingsGuard は期限の切れた未確認の変更を元へ戻す（pb-97）。
//
// **起動時とプロセス内のタイマの両方から呼ぶ。** 期限は DB の expires_at が
// 正本で、どちらも同じ行を見る——**プロセス内のタイマだけでは、再起動を
// またいだときに戻す主体がいなくなる。**
type SettingsGuard struct {
	Tx        TxRunner
	Q         gen.Querier
	Settings  *config.Live
	OnChanged func(*config.Set)
}

// RevertExpired は期限が切れた分を戻す。**戻した件数を返す。**
func (g SettingsGuard) RevertExpired(ctx context.Context) (int, error) {
	rows, err := g.Q.ListExpiredPendingSettingChanges(ctx)
	if err != nil {
		return 0, fmt.Errorf("期限切れの未確認を引けない: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	reverted := 0
	for _, row := range rows {
		prev, err := decodePrevious(row.Previous)
		if err != nil {
			// **読めない行を残さない。** 残すと毎回この経路を通る。
			slog.Error("未確認の記録を読めないため捨てる",
				slog.String("id", row.ID), slog.String("error", err.Error()))
			if _, derr := g.Q.DeletePendingSettingChange(ctx, row.ID); derr != nil {
				return reverted, derr
			}
			continue
		}

		err = g.Tx.RunInTx(ctx, func(q gen.Querier) error {
			for key, value := range prev {
				if value == nil {
					if err := q.DeleteAppSetting(ctx, key); err != nil {
						return fmt.Errorf("設定 %s を消せない: %w", key, err)
					}
					continue
				}
				if err := q.UpsertAppSetting(ctx, gen.UpsertAppSettingParams{
					Key: key, Value: *value,
				}); err != nil {
					return fmt.Errorf("設定 %s を戻せない: %w", key, err)
				}
			}
			if _, err := q.DeletePendingSettingChange(ctx, row.ID); err != nil {
				return fmt.Errorf("未確認の記録を消せない: %w", err)
			}
			return nil
		})
		if err != nil {
			return reverted, err
		}

		// **利用者に分かる形でログへ残す**（チケットの制約条件）。黙って戻すと
		// 「なぜ HTTP に戻ったのか」が追えない。
		slog.Warn("設定変更が期限内に確認されなかったため元へ戻した",
			slog.Any("keys", pendingKeys(prev)),
			slog.String("hint", "新しい設定で画面へ入り、「アクセスできました」を押すと確定する"))
		reverted++
	}

	if reverted > 0 {
		if err := g.reload(ctx); err != nil {
			return reverted, err
		}
	}
	return reverted, nil
}

// reload は戻した結果を実効値へ反映し、フックを呼ぶ。
//
// **フックを呼ぶのが要点である。** tls_enabled を戻したなら、ここで待受が
// 張り替わる（pb-106）。呼ばないと DB だけが戻り、待受は新しいままになる。
func (g SettingsGuard) reload(ctx context.Context) error {
	rows, err := g.Q.ListAppSettings(ctx)
	if err != nil {
		return fmt.Errorf("設定の行を引けない: %w", err)
	}
	overlay := make([]config.Row, 0, len(rows))
	for _, row := range rows {
		overlay = append(overlay, config.Row{Key: row.Key, Value: row.Value})
	}
	newSet := config.OverlayDatabase(g.Settings.Base(), overlay)
	g.Settings.Replace(newSet)
	if g.OnChanged != nil {
		g.OnChanged(newSet)
	}
	return nil
}

// recordPending は未確認として記録する。**updateSettings のトランザクションの
// 中から呼ぶ**（設定を書くのと1つの単位にする）。
//
// **戻す値は DB の行から採る。** settingChange.From は実効値（既定値や環境変数を
// 含む）なので、**「行が無かった」を表せない。**
func recordPending(
	ctx context.Context, q gen.Querier, actorID string,
	changes []settingChange, rows []gen.ListAppSettingsRow,
) (*pgtype.Timestamptz, error) {
	risky := confirmKeys()
	previous := make(map[string]*string)
	for _, c := range changes {
		if risky[c.Key] {
			previous[c.Key] = nil
		}
	}
	if len(previous) == 0 {
		return nil, nil
	}

	// **未確認が残っている間は次を受け付けない**（11.2）。重なると
	// 「どれを戻すか」が利用者にも追えなくなる。
	n, err := q.CountPendingSettingChanges(ctx)
	if err != nil {
		return nil, fmt.Errorf("未確認の件数を数えられない: %w", err)
	}
	if n > 0 {
		return nil, apierr.New(apierr.Conflict).WithMessage(
			"まだ確認されていない設定変更があります。「アクセスできました」を押すか、期限が切れるのを待ってください")
	}

	for _, row := range rows {
		if _, ok := previous[row.Key]; ok {
			v := row.Value
			previous[row.Key] = &v
		}
	}

	blob, err := json.Marshal(previous)
	if err != nil {
		return nil, fmt.Errorf("戻す値を書けない: %w", err)
	}
	expires := pgtype.Timestamptz{Time: time.Now().Add(ConfirmWindow), Valid: true}
	if _, err := q.CreatePendingSettingChange(ctx, gen.CreatePendingSettingChangeParams{
		ID:        ulidgen.New(),
		Previous:  blob,
		ExpiresAt: expires,
		CreatedBy: pgtype.Text{String: actorID, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("未確認の記録を作れない: %w", err)
	}
	return &expires, nil
}

// withPending は応答に未確認の変更を載せる（11.1 / 11.2。pb-97）。
//
// **引けなくても応答を落とさない。** 未確認の有無は設定一覧の付随情報であり、
// ここで 500 にすると**締め出しの手当てが、設定画面自体を壊す**ことになる。
func (h *handler) withPending(r *http.Request, res settingsResponse) settingsResponse {
	row, err := h.q.GetPendingSettingChange(r.Context())
	if errors.Is(err, pgx.ErrNoRows) {
		return res
	} else if err != nil {
		slog.Warn("未確認の変更を引けないため、設定一覧には載せない",
			slog.String("error", err.Error()))
		return res
	}
	view, err := pendingFromRow(row)
	if err != nil {
		slog.Warn("未確認の変更を読めないため、設定一覧には載せない",
			slog.String("error", err.Error()))
		return res
	}
	res.PendingConfirmation = view
	return res
}
