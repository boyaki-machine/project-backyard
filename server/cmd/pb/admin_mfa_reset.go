// pb admin mfa-reset — 第2要素の解除（Design.md 6.7.5、ApiDesign.md 6.9）。
//
// **画面側の口（POST /admin/users/:id/mfa/reset）が成立しない場合のためにある。**
// 管理者が1人しかいない構成で、その人が認証アプリとリカバリコードの両方を
// 失ったとき、**画面へ入れる人が誰も残らない。** 端末と DB に到達できる人が
// 使う最後の手段であり、環境変数で設定を上から押さえる復旧経路と同じ位置づけ
// である（Design.md 10.3）。
//
// **パスワードには触らない。** 締め出しの原因が違うので、`pb admin create` や
// 画面のパスワードリセットとは別の操作にしてある。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/mail"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// mfaResetAuditLabel は audit_log.actor_label に残す実行経路（DbDesign.md 6.8）。
//
// **CLI から来たことが監査で読めるようにする。** 画面の解除（6.9）と同じ
// `mfa.reset` を記録するので、経路の違いはこのラベルだけが持つ。
const mfaResetAuditLabel = "pb admin mfa-reset (CLI)"

func adminMFAReset(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("admin mfa-reset", flag.ContinueOnError)
	email := fs.String("email", "", "対象のメールアドレス（必須）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email を指定してください")
	}
	addr, err := mail.ParseAddress(*email)
	if err != nil {
		return fmt.Errorf("メールアドレスの形式が正しくありません: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	q := gen.New(pool)

	// **メールアドレスから引く。** ULID を端末で調べさせない——この口を使う人は
	// 画面に入れないので、id を調べる手段も無い。
	row, err := q.FindLocalLoginByEmail(ctx, addr.Address)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("そのメールアドレスのユーザーが見つかりません: %s", addr.Address)
	} else if err != nil {
		return fmt.Errorf("ユーザーを引けない: %w", err)
	}

	credentials, err := q.DeleteAllMfaCredentials(ctx, row.ActorID)
	if err != nil {
		return fmt.Errorf("認証器を消せない: %w", err)
	}
	codes, err := q.DeleteRecoveryCodes(ctx, row.ActorID)
	if err != nil {
		return fmt.Errorf("リカバリコードを消せない: %w", err)
	}
	if err := q.DeleteMfaLoginChallengesForUser(ctx, row.ActorID); err != nil {
		return fmt.Errorf("挑戦を消せない: %w", err)
	}

	// **記録に残す。** 端末から第2要素を外せる操作であり、事後に誰かが
	// 「いつ・誰の分が外れたか」を追えないと危ない。
	// **アクターは system のままにする。** 端末を操作した人が誰かは分からず、
	// 対象の本人を actor に据えると偽の帰属になる（audit.FromCLI の注記）。
	// 対象は target_id に入る。
	audit.FromCLI(mfaResetAuditLabel).
		RecordOrLog(ctx, q, audit.Entry{
			Action:     audit.MFAReset,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   row.ActorID,
			Detail: map[string]any{
				"email":                  row.Email,
				"removed_credentials":    credentials,
				"removed_recovery_codes": codes,
				"via":                    "cli",
			},
		})

	fmt.Fprintf(os.Stdout,
		"%s の第2要素を解除しました（認証器 %d 件、リカバリコード %d 件）。\n"+
			"次のログインからパスワードだけで入れます。必要なら画面から登録し直してください。\n",
		row.Email, credentials, codes)
	return nil
}
