package middleware

import (
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// DelegationDisabledMessage は自律実行が無効のときに書き込みを拒む文面
// （Delegation.md 1.3）。MCP のエージェントにもそのまま届くので、次の一手
// （設定で有効にする）まで書く。
const DelegationDisabledMessage = "自律実行は無効になっています。アプリケーション設定で有効にしてください"

// RequireDelegationEnabled は自律実行が無効なら 403 で拒む
// （Design.md 10.3「成熟途上の機能は、設定で有効にしたときだけ出す」）。
//
// **権限と同じくルート定義に宣言する。** 書き込みの口にだけ掛け、読み取りは
// 通す——無効化は終了していない委任があると拒まれる（ApiDesign.md 11.2）
// ので、無効の間に読まれるのは終わった委任だけである。
//
// **判定はリクエストごとに設定の実効値を読む。** 画面から切り替えたら次の
// リクエストから効く（ApiDesign.md 11.3）。
func RequireDelegationEnabled(settings *config.Live) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if settings == nil || !settings.DelegationEnabled() {
				apierr.Write(w, r, apierr.New(apierr.Forbidden).WithMessage(DelegationDisabledMessage))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
