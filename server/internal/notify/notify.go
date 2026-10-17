// Package notify は通知の作成・取り下げ・期限切れの削除を担う（DbDesign.md 6.33）。
//
// **通知を作る REST の口は持たない**（ApiDesign.md 4.8）。宛先と文面を決めるのは
// 常に使い手の処理であり、使い手はこのパッケージの関数を、自分の処理と同じ
// トランザクションの Querier で呼ぶ（6.33.1 の5）。出来事だけが確定して通知が無い、
// またはその逆を作らないためである。
//
// 読む・既読にする・人が消すのは httpapi/v1 の me_notifications.go が持つ。
package notify

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 保持と点検の間隔（DbDesign.md 6.33）。**設定にしない**（Design.md 10.3）。
const (
	// Retention は既読にしてから消すまでの期間。未読は消さない。
	Retention = 30 * 24 * time.Hour
	// SweepInterval はサーバの定期処理が期限切れを消す間隔。
	SweepInterval = time.Hour
)

// SourceNamePB は PB 自身が出す通知の通知元の名前（DbDesign.md 6.33）。
const SourceNamePB = "PB"

// Querier は通知を作る・取り下げる・消すのに要るクエリ。
//
// 使い手のトランザクションの gen.Queries をそのまま渡せる。
type Querier interface {
	CreateNotification(ctx context.Context, arg gen.CreateNotificationParams) error
	WithdrawNotifications(ctx context.Context, subjectKey string) (int64, error)
	DeleteExpiredNotifications(ctx context.Context, readBefore time.Time) (int64, error)
}

// Notification は使い手が作る通知1件（DbDesign.md 6.33.1）。
//
// 空の SubjectKey・ProjectID・SourceID は NULL として保存する。
// SourceID が空のとき SourceName を省けば、PB 自身が出した通知になる。
type Notification struct {
	RecipientID string
	Kind        string // <領域>.<出来事>
	Title       string
	Body        string
	Link        string // PB の画面のパス
	LinkLabel   string // 一覧で遷移のリンクとして出す文字列
	SubjectKey  string // 取り下げの単位。<種類>:<対象の ID>
	ProjectID   string
	SourceID    string
	SourceName  string
}

// kindPattern と linkPattern は DB の CHECK と同じ形（DbDesign.md 6.33）。
//
// **DB の CHECK が最後の砦だが、アプリ側でも先に弾く。** CHECK で落ちると、
// 使い手の業務トランザクションごと失敗して原因が読みにくい（activity と同じ考え方）。
var (
	kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	linkPattern = regexp.MustCompile(`^/[^/\\]`)
)

// ErrInvalid は通知の形が DB の制約を満たさないことを表す。使い手の実装の誤りである。
var ErrInvalid = errors.New("通知の形が正しくない")

// Create は通知を1件作る。
func Create(ctx context.Context, q Querier, n Notification) error {
	if n.SourceID == "" && n.SourceName == "" {
		n.SourceName = SourceNamePB
	}
	if err := validate(n); err != nil {
		return err
	}
	err := q.CreateNotification(ctx, gen.CreateNotificationParams{
		ID:          ulidgen.New(),
		RecipientID: n.RecipientID,
		Kind:        n.Kind,
		Title:       n.Title,
		Body:        n.Body,
		Link:        n.Link,
		LinkLabel:   n.LinkLabel,
		SubjectKey:  nullable(n.SubjectKey),
		ProjectID:   nullable(n.ProjectID),
		SourceID:    nullable(n.SourceID),
		SourceName:  n.SourceName,
	})
	if err != nil {
		return fmt.Errorf("通知を作れない（%s）: %w", n.Kind, err)
	}
	return nil
}

// Withdraw は同じ対象の通知を、宛先を問わずまとめて取り下げる（物理削除）。
// 消した件数を返す。
func Withdraw(ctx context.Context, q Querier, subjectKey string) (int64, error) {
	if subjectKey == "" {
		return 0, fmt.Errorf("%w: 取り下げる対象のキーが空", ErrInvalid)
	}
	n, err := q.WithdrawNotifications(ctx, subjectKey)
	if err != nil {
		return 0, fmt.Errorf("通知を取り下げられない（%s）: %w", subjectKey, err)
	}
	return n, nil
}

// Sweep は既読から Retention を過ぎた通知を消す。消した件数を返す。
//
// **未読は消さない。** サーバの定期処理（serve.go）が SweepInterval ごとに呼ぶ。
func Sweep(ctx context.Context, q Querier, now time.Time) (int64, error) {
	n, err := q.DeleteExpiredNotifications(ctx, now.Add(-Retention))
	if err != nil {
		return 0, fmt.Errorf("期限切れの通知を消せない: %w", err)
	}
	return n, nil
}

func validate(n Notification) error {
	var bad []string
	if n.RecipientID == "" {
		bad = append(bad, "宛先が空")
	}
	if len(n.Kind) > 64 || !kindPattern.MatchString(n.Kind) {
		bad = append(bad, fmt.Sprintf("種類 %q が <領域>.<出来事> の形でない", n.Kind))
	}
	if l := utf8.RuneCountInString(n.Title); l < 1 || l > 200 {
		bad = append(bad, "題名は1〜200文字")
	}
	if utf8.RuneCountInString(n.Body) > 2000 {
		bad = append(bad, "本文は2000文字まで")
	}
	if utf8.RuneCountInString(n.Link) > 500 || !linkPattern.MatchString(n.Link) {
		bad = append(bad, fmt.Sprintf("遷移先 %q が PB の画面のパスでない", n.Link))
	}
	if l := utf8.RuneCountInString(n.LinkLabel); l < 1 || l > 100 {
		bad = append(bad, "遷移先の名前は1〜100文字")
	}
	if utf8.RuneCountInString(n.SubjectKey) > 200 {
		bad = append(bad, "対象のキーは200文字まで")
	}
	if l := utf8.RuneCountInString(n.SourceName); l < 1 || l > 100 {
		bad = append(bad, "通知元の名前は1〜100文字")
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(bad, "、"))
	}
	return nil
}

// nullable は空文字を NULL にする。
func nullable(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
