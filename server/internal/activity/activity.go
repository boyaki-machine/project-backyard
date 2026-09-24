// Package activity は activity への記録を担う（ApiDesign.md 9.1.1、DbDesign.md 6.8）。
//
// **audit（audit_log）と役割が違う。** ApiDesign.md 2.10 が audit_log の対象と
// するのは認証・権限・トークン・ユーザー管理で、いずれもインスタンス管理者が
// 追うべき事象である。チケットの変更は業務履歴であり、読み手はプロジェクトの
// メンバー（GuiDesign.md 5.5 の「変更履歴」）である。両者を混ぜると、監査ログが
// チケット更新で埋まって本来の用途に使えなくなる。
//
// 構造は audit.Recorder に合わせてある——1リクエストに紐づく値（アクター・
// request_id）を Recorder に固定し、呼び出しのたびに引き回さない。
//
// # 記録しないもの
//
//   - **タグ・スプリントの定義変更**（9.1.1）。activity の読み手はチケットの
//     変更履歴であり、9.13.2 の entity は `ticket:31` の形しか受け付けない
//   - **並べ替え（POST /tickets/:seq/move）**。
//     sort_key だけの更新であり、記録するとバックログを一度並べ替えただけで
//     チケット詳細の変更履歴が埋まる。「誰がどこへドラッグしたか」は業務履歴
//     として読む価値が薄い
package activity

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// Action は activity.action。DbDesign.md 6.8 の CHECK 制約と一対一で対応する。
type Action string

const (
	Create     Action = "create"
	Update     Action = "update"
	Delete     Action = "delete"
	Transition Action = "transition"
)

// actions は DbDesign.md 6.8 の CHECK が許す4件。
//
// audit と同じく、綴り誤りが黙って入らないようアプリ側でも弾く。DB の CHECK が
// 最後の砦だが、そこで落ちると業務トランザクションごと失敗して原因が読みにくい。
var actions = map[Action]bool{
	Create: true, Update: true, Delete: true, Transition: true,
}

// EntityTicket は entity_type の値。Phase 1 で記録するのはチケットだけである
// （9.1.1。コメント・DoD・リンクは手順18 で足す）。
const EntityTicket = "ticket"

// Entry は1件の業務履歴。
//
// **Field / OldValue / NewValue はポインタである。** 「送られていない」と
// 「空にした」を区別する必要があるため（説明を空文字へ変えた記録と、説明を
// 触っていない記録は別物である）。作成（Create）では3つとも nil になる。
type Entry struct {
	ProjectID  string
	EntityType string
	EntityID   string
	Action     Action
	Field      *string
	OldValue   *string
	NewValue   *string
}

// Recorder は1リクエストに紐づく記録者。
type Recorder struct {
	actorID   string
	requestID string
}

// FromRequest は HTTP リクエストから Recorder を作る。
//
// activity を書く経路はすべて認証済みである（チケットAPIは ticket.* の権限を
// 要する）。プリンシパルが無い場合は actor_id が NULL になるが、それは
// ルート定義の誤りであってここで弾く事柄ではない。
func FromRequest(r *http.Request) *Recorder {
	rec := &Recorder{requestID: apierr.RequestIDFromContext(r.Context())}
	if p := auth.PrincipalFromContext(r.Context()); p != nil {
		rec.actorID = p.ActorID
	}
	return rec
}

// Record は履歴を1件書き、失敗をそのまま返す。
//
// **業務トランザクションの中で呼ぶこと。** q にトランザクションの Queries を
// 渡せば、チケットと履歴が同時に確定するか同時に消えるかのどちらかになり、
// 「作成したのに履歴が無い」状態を作らない（ApiDesign.md 9.3）。
func (rec *Recorder) Record(ctx context.Context, q gen.Querier, e Entry) error {
	if !actions[e.Action] {
		return fmt.Errorf("未知の活動 %q（DbDesign.md 6.8 の CHECK にない）", e.Action)
	}
	if e.ProjectID == "" || e.EntityType == "" || e.EntityID == "" {
		return fmt.Errorf("活動 %s の project_id / entity_type / entity_id が欠けている", e.Action)
	}

	if err := q.InsertActivity(ctx, gen.InsertActivityParams{
		ID:         ulidgen.New(),
		ProjectID:  e.ProjectID,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		ActorID:    text(rec.actorID),
		Action:     string(e.Action),
		Field:      textPtr(e.Field),
		OldValue:   textPtr(e.OldValue),
		NewValue:   textPtr(e.NewValue),
		RequestID:  text(rec.requestID),
	}); err != nil {
		return fmt.Errorf("業務履歴を記録できない（action=%s entity=%s:%s）: %w",
			e.Action, e.EntityType, e.EntityID, err)
	}
	return nil
}

// text は空文字を SQL の NULL に写す。
func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// textPtr は nil を SQL の NULL に写す。**空文字は空文字のまま入れる**
// （「空にした」という記録が NULL に潰れないようにするため）。
func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
