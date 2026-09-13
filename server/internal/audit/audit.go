// Package audit は audit_log への記録を担う（ApiDesign.md 2.10、DbDesign.md 6.8）。
//
// 監査ログは**DBに残す正式な記録**であり、揮発してよいアプリケーションログとは
// 役割が違う（Design.md 10.1）。両者は request_id で突き合わせる。
//
// 記録すべき操作は ApiDesign.md 2.10 が15件を列挙している。本パッケージは
// それを Action 定数として持ち、未知の action を弾く。audit_log.action には
// DB 側の CHECK 制約が無いため、綴り誤りが黙って入ると後から集計できなくなる。
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// Action は audit_log.action。ApiDesign.md 2.10 の列挙と一対一で対応する。
type Action string

const (
	LoginSuccess     Action = "login.success"
	LoginFailure     Action = "login.failure"
	Logout           Action = "logout"
	PasswordChange   Action = "password.change"
	PasswordReset    Action = "password.reset"
	TokenIssue       Action = "token.issue"
	TokenRevoke      Action = "token.revoke"
	SessionRevoke    Action = "session.revoke"
	UserCreate       Action = "user.create"
	UserUpdate       Action = "user.update"
	UserDelete       Action = "user.delete"
	RoleChange       Action = "role.change"
	ProjectCreate    Action = "project.create"
	ProjectArchive   Action = "project.archive"
	PermissionDenied Action = "permission.denied"
	// 以下は 0019（Phase 2）で加わった（ApiDesign.md 4.5.6）。
	// エージェントの登録と更新はアカウントの作成・変更と同じ重みを持つ。
	AgentRegister Action = "agent.register"
	AgentUpdate   Action = "agent.update"
	// agent.delete は手順26a で加わった（ApiDesign.md 4.5.6）。
	// write 系ツールが入り、資格情報を「完全に取り消す」操作が要るようになった。
	AgentDelete Action = "agent.delete"

	// SettingUpdate は pb-2 で加わった（ApiDesign.md 11.2）。**1回の保存が1行**で、
	// detail.changes[] に変更したキーと新旧の実効値を並べる。サーバ全体の設定を
	// 変える操作であり、影響範囲が1プロジェクトに収まらないため記録する。
	SettingUpdate Action = "setting.update"

	// TLSCertificateUpload / TLSCertificateDelete は pb-3（ApiDesign.md 11.5 / 11.6）。
	// **detail には指紋・common_name・有効期間を入れ、PEM と秘密鍵は入れない。**
	TLSCertificateUpload Action = "tls.certificate.upload"
	TLSCertificateDelete Action = "tls.certificate.delete"

	// 以下5件は pb-103（ApiDesign.md 4.6.6）。
	//
	// **detail に共有秘密・otpauth URI・リカバリコードを入れない。** audit_log は
	// 管理者が読めるため（DbDesign.md 6.8）、入れると他人の第2要素を作れる。
	MFARegister               Action = "mfa.register"
	MFAUnregister             Action = "mfa.unregister"
	MFARecoveryCodeRegenerate Action = "mfa.recovery_codes.regenerate"
	MFAReset                  Action = "mfa.reset"
	// LoginMFAFailure は第2要素の照合失敗。**login.failure と分けてある**
	// ——パスワードは通っているので、総当たりの調査で見る対象が違う。
	LoginMFAFailure Action = "login.mfa_failure"
)

// actions は ApiDesign.md 2.10 が列挙する26件。
var actions = map[Action]bool{
	LoginSuccess: true, LoginFailure: true, Logout: true,
	PasswordChange: true, PasswordReset: true,
	TokenIssue: true, TokenRevoke: true, SessionRevoke: true,
	UserCreate: true, UserUpdate: true, UserDelete: true, RoleChange: true,
	ProjectCreate: true, ProjectArchive: true, PermissionDenied: true,
	AgentRegister: true, AgentUpdate: true, AgentDelete: true,
	SettingUpdate:        true,
	TLSCertificateUpload: true, TLSCertificateDelete: true,
	MFARegister: true, MFAUnregister: true,
	MFARecoveryCodeRegenerate: true, MFAReset: true,
	LoginMFAFailure: true,
}

// Result は audit_log.result（DbDesign.md 6.8 の CHECK 制約）。
type Result string

const (
	Success Result = "success"
	Failure Result = "failure"
)

// recordTimeout は RecordOrLog が使う書き込みの制限時間。
// pb_app の statement_timeout は 15s（DbDesign.md 3.4）なので、それより短くする。
const recordTimeout = 5 * time.Second

// Entry は1件の監査記録。アクター・IP・User-Agent・request_id は Recorder が補う。
type Entry struct {
	Action     Action
	Result     Result
	TargetType string         // 空なら NULL
	TargetID   string         // 空なら NULL
	Detail     map[string]any // nil なら {}
}

// Recorder は1リクエスト（または1回のCLI実行）に紐づく記録者。
//
// ip / user_agent / request_id はリクエストごとに変わらないため、
// 呼び出しのたびに引き回さずここに固定する。
type Recorder struct {
	actorID    string
	actorKind  string
	actorLabel string
	tokenID    string
	ip         *netip.Addr
	userAgent  string
	requestID  string
}

// FromRequest は HTTP リクエストから Recorder を作る。
//
// 認証済みならプリンシパル（Design.md 6.2.2 でコンテキストに載る）から
// アクターを取る。未認証の経路（ログイン失敗など）ではアクターが空になり、
// actor_id は NULL のまま ip と user_agent だけが残る。
func FromRequest(r *http.Request) *Recorder {
	rec := &Recorder{
		userAgent: r.UserAgent(),
		requestID: apierr.RequestIDFromContext(r.Context()),
	}
	if addr := ClientIP(r); addr.IsValid() {
		rec.ip = &addr
	}
	if p := auth.PrincipalFromContext(r.Context()); p != nil {
		rec.actorID = p.ActorID
		rec.actorKind = p.ActorKind
		rec.actorLabel = p.AuditLabel()
		rec.tokenID = p.TokenID
	}
	return rec
}

// FromCLI は HTTP リクエストを持たない経路（pb admin create など）用の Recorder。
//
// **actor_id は NULL、actor_kind は 'system' になる。** 端末の操作者には
// まだアカウントが無い（初期管理者の作成がまさにその場面）ため、作成された
// 本人を actor に据えると偽の帰属になる。誰が実行したかは label に残す。
// ip / user_agent / request_id は NULL。
func FromCLI(label string) *Recorder {
	return &Recorder{
		actorKind:  auth.ActorKindSystem,
		actorLabel: label,
	}
}

// WithActor はアクターを差し替えた複製を返す。
//
// ログイン成功のように、認証ミドルウェアを通っていないが誰の操作かは
// 判明している場面で使う。
func (rec *Recorder) WithActor(actorID, kind, label string) *Recorder {
	c := *rec
	c.actorID, c.actorKind, c.actorLabel = actorID, kind, label
	return &c
}

// WithToken は token_id を差し替えた複製を返す。発行直後のトークンを
// 記録する場面（login.success / token.issue）で使う。
func (rec *Recorder) WithToken(tokenID string) *Recorder {
	c := *rec
	c.tokenID = tokenID
	return &c
}

// Record は監査行を1件書き、失敗をそのまま返す。
//
// **業務トランザクションを持つ操作はこちらを使う。** q にトランザクションの
// Queries（gen.New(tx)）を渡せば、業務データと監査記録が同時に確定するか
// 同時に消えるかのどちらかになり、「変更したのに記録が無い」状態を作らない。
func (rec *Recorder) Record(ctx context.Context, q gen.Querier, e Entry) error {
	params, err := rec.params(e)
	if err != nil {
		return err
	}
	if err := q.InsertAuditLog(ctx, params); err != nil {
		return fmt.Errorf("監査ログを記録できない（action=%s）: %w", e.Action, err)
	}
	return nil
}

// RecordOrLog は Record の失敗を ERROR ログに落として続行する。
//
// **業務トランザクションを持たない認証イベント（login.* / logout）で使う。**
// 監査DBの一時的な障害でログインそのものを止めると、可用性の低下が
// 監査の欠落より重い被害になるため。ただし失敗は必ず ERROR で残す。
//
// コンテキストのキャンセルを引き継がない（context.WithoutCancel）。
// クライアントが接続を切っただけで login.failure の記録を落とせてしまうと、
// 総当たり攻撃の痕跡を攻撃者側から消せることになるため。
func (rec *Recorder) RecordOrLog(ctx context.Context, q gen.Querier, e Entry) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	if err := rec.Record(writeCtx, q, e); err != nil {
		slog.ErrorContext(ctx, "監査ログの記録に失敗した",
			slog.String("request_id", rec.requestID),
			slog.String("action", string(e.Action)),
			slog.String("result", string(e.Result)),
			slog.String("actor_id", rec.actorID),
			slog.String("cause", err.Error()),
		)
	}
}

// params は Entry と Recorder を INSERT の引数へ組み立てる。
func (rec *Recorder) params(e Entry) (gen.InsertAuditLogParams, error) {
	if !actions[e.Action] {
		return gen.InsertAuditLogParams{},
			fmt.Errorf("未知の監査アクション %q（ApiDesign.md 2.10 の一覧にない）", e.Action)
	}
	if e.Result != Success && e.Result != Failure {
		return gen.InsertAuditLogParams{},
			fmt.Errorf("監査アクション %s の result が不正: %q", e.Action, e.Result)
	}

	// detail は NOT NULL DEFAULT '{}'。nil を渡すと NULL になり制約違反になる。
	detail := []byte("{}")
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return gen.InsertAuditLogParams{},
				fmt.Errorf("監査アクション %s の detail を JSON にできない: %w", e.Action, err)
		}
		detail = b
	}

	return gen.InsertAuditLogParams{
		ID:         ulidgen.New(),
		ActorID:    text(rec.actorID),
		ActorKind:  text(rec.actorKind),
		ActorLabel: text(rec.actorLabel),
		TokenID:    text(rec.tokenID),
		Ip:         rec.ip,
		UserAgent:  text(rec.userAgent),
		Action:     string(e.Action),
		TargetType: text(e.TargetType),
		TargetID:   text(e.TargetID),
		Result:     string(e.Result),
		Detail:     detail,
		RequestID:  text(rec.requestID),
	}, nil
}

// text は空文字を NULL に写す。監査ログの各列は「不明」と「空文字」を
// 区別する意味がないため、空は NULL に倒す。
func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ClientIP は接続元アドレスを返す。ポートは落とす。
//
// audit_log.ip（inet 型）とアクセスログの ip を**同じ値**にするため、
// 両者がこの関数を共有する（Design.md 10.1）。
//
// プロキシ経由の実IP解決（X-Forwarded-For 等）は Phase 1 では行わない。
// 詐称可能なヘッダを検証なしに信じると、監査ログの発信元を偽装できてしまう。
func ClientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	// IPv4-mapped IPv6（::ffff:127.0.0.1）は IPv4 に畳む。
	// inet 列に同じ相手が2通りで入るのを避けるため。
	return addr.Unmap()
}
