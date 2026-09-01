// 認証済みプリンシパルと、そのコンテキスト受け渡し（Design.md 6.2.2）。
//
// 「actor をロードしてリクエストコンテキストに載せる」の実体。認証ミドルウェアが
// 載せ、ハンドラ・認可ミドルウェア・監査ログが読む。
//
// 本型が持つのは、実効権限（Design.md 6.4.1）の計算に要る素材
// （システムロール・トークンスコープ・プロジェクト）と、**セッションに
// キャッシュ済みの実効権限**である。後者は手順6b で足した。計算そのものは
// 引き続き持たず、permissions.go と認可ミドルウェアが行う。
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// アクター種別（DbDesign.md 6.2 actor.kind の CHECK 制約）。
const (
	ActorKindUser   = "user"
	ActorKindAgent  = "agent"
	ActorKindSystem = "system"
)

// トークン種別（DbDesign.md 6.2 access_token.token_type の CHECK 制約）。
const (
	TokenTypeSession = "session"
	TokenTypeAPI     = "api"
	TokenTypeAgent   = "agent"
)

// CredentialSource は、どの経路で資格情報が送られてきたかを表す。
//
// CSRF の要否がこれで決まる。ApiDesign.md 2.4 は「Cookie 認証の状態変更系
// リクエストにのみ CSRF トークンを要求する。Bearer 認証では不要」としており、
// 手順5の CSRF ミドルウェアはトークン種別ではなく**この値**で判定する。
// api トークンを Cookie に入れて送ることも技術的には可能なので、
// token_type で代用してはならない。
type CredentialSource string

const (
	SourceCookie CredentialSource = "cookie"
	SourceBearer CredentialSource = "bearer"
)

// SessionCookieName は Cookie 認証で使う名前（ApiDesign.md 2.3）。
const SessionCookieName = "pb_session"

// CSRFCookieName は CSRF トークンの Cookie 名（ApiDesign.md 2.4）。
// HttpOnly ではない（JS から読んで X-PB-CSRF に載せるため）。手順5で発行する。
const CSRFCookieName = "pb_csrf"

// CSRFHeaderName は CSRF トークンを載せるヘッダ名（ApiDesign.md 2.4）。
const CSRFHeaderName = "X-PB-CSRF"

// Principal は認証を通ったリクエストの主体。
type Principal struct {
	// ActorID は actor.id。監査ログの actor_id になる。
	ActorID string
	// ActorKind は user / agent / system。
	ActorKind string
	// DisplayName は actor.display_name。
	DisplayName string
	// Email は app_user.email。user 以外では空。
	//
	// **エージェントでは所有者のメールが入る。** SystemRole と同じく
	// COALESCE(agent.owner_actor_id, actor.id) で引いた app_user の行から
	// 来るためである。GET /me の応答には出さない（me.go は actor の
	// プロフィールを別に引く）。
	Email string
	// SystemRole は app_user.system_role（operator / administrator）。
	// 認可の第1層（Design.md 6.4.1）。
	//
	// **エージェントでは所有者のロールが入る**（Design.md 6.5 の委譲、0019）。
	// エージェント自身は app_user の行を持たないため、これが無いと
	// 第1層が必ず空になり、権限0件で全部 403 になる。
	// system アクターでは空のままである。
	SystemRole string
	// OwnerActorID は agent.owner_actor_id。エージェント以外では空。
	//
	// **認可でどのアクターのロールを読むかを決める**（AuthzActorID）。
	// ActorID とは別に持つ——監査と書き手はエージェント自身であり、
	// 借りるのは権限の根拠だけだからである（Design.md 6.5）。
	OwnerActorID string

	// TokenID は access_token.id。監査ログの token_id になる。
	TokenID string
	// TokenType は session / api / agent。
	TokenType string
	// Scopes は access_token.scopes。**権限の上限**であり、空スライスは
	// 「絞り込みなし（ロールの権限をそのまま使う）」を意味する（Design.md 6.4.1）。
	Scopes []string
	// ProjectID は access_token.project_id。空文字は全プロジェクト。
	ProjectID string
	// ExpiresAt は access_token.expires_at。nil は無期限（APIトークンは
	// 期限が任意のため。ApiDesign.md 4.5）。GET /me が返す expires_at に
	// なるため（4.1 は「3.1 と同一構造」）、認証の時点で載せておく。
	ExpiresAt *time.Time
	// Source は資格情報の送出経路。CSRF の要否判定に使う。
	Source CredentialSource

	// CachedPermissions は access_token.cached_permissions（Design.md 6.4.5）。
	//
	// 入っているのは 6.4.1 の式のうち**システムロールの層**、すなわち
	// システムロールの権限 ∩ トークンのスコープ である。スコープとの積を
	// 取った後の値を入れてよいのは、トークンのスコープが発行後に変わらず、
	// かつ ( A ∪ B ) ∩ S = ( A ∩ S ) ∪ ( B ∩ S ) が成り立つためである
	// （プロジェクト層と合成しても 6.4.1 と同じ結果になる）。
	//
	// nil は「キャッシュが無い」。空スライスは「権限0件」であり別の状態。
	CachedPermissions []string
	// PermissionsCachedAt は access_token.permissions_cached_at。
	// nil ならキャッシュが無い。PermissionCacheTTL の起点になる。
	PermissionsCachedAt *time.Time
}

// FreshPermissions は、使えるキャッシュがあればそれを返す（Design.md 6.4.5）。
//
// 2つ目の戻り値が偽なら、呼び出し側はロールから計算し直す。
//
// permissions_cached_at はDBの now() で書かれ、ここでの now はアプリの時計に
// なる。両者がずれてもキャッシュが有効に見える時間が TTL ± ずれ幅で収まる
// だけで、破綻はしない。**ずれを気にして期限を長く取らない。**
func (p *Principal) FreshPermissions(now time.Time) ([]string, bool) {
	if p == nil || p.CachedPermissions == nil || p.PermissionsCachedAt == nil {
		return nil, false
	}
	if now.Sub(*p.PermissionsCachedAt) >= PermissionCacheTTL {
		return nil, false
	}
	return p.CachedPermissions, true
}

// IsUser は人間ユーザーかどうかを返す。
func (p *Principal) IsUser() bool { return p != nil && p.ActorKind == ActorKindUser }

// IsAgent はエージェントかどうかを返す。
func (p *Principal) IsAgent() bool { return p != nil && p.ActorKind == ActorKindAgent }

// AuthzActorID は「誰のロールを読むか」を返す（Design.md 6.5 の委譲、0019）。
//
// エージェントは app_user の行も project_member の行も持たないため、
// 6.4.1 の式の左辺2層をそのままでは埋められない。所有者のロールを用いる。
//
//	実効権限 = ( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ スコープ
//
// **ActorID とは使い分ける。** 監査ログ・コメントの書き手・activity は
// ActorID（エージェント自身）であり、借りるのは権限の根拠だけである。
// これを取り違えると、エージェントの操作が所有者の名前で記録される。
//
// 人間・システムのアクターでは ActorID をそのまま返すので、呼び出し側は
// 種別で分岐しなくてよい。
func (p *Principal) AuthzActorID() string {
	if p == nil {
		return ""
	}
	if p.OwnerActorID != "" {
		return p.OwnerActorID
	}
	return p.ActorID
}

// IsAdministrator はシステムロールがアドミニストレータかどうかを返す。
func (p *Principal) IsAdministrator() bool {
	return p != nil && p.SystemRole == SystemRoleAdministrator
}

// CanReachProject は、このトークンで projectID に触れてよいかを返す。
//
// access_token.project_id は「NULL = 全プロジェクト」（DbDesign.md 6.2）であり、
// 値が入っているトークンは**そのプロジェクト専用**である。Design.md 6.5 は
// エージェントトークンの禁止事項に「他プロジェクトへのアクセス」を挙げており、
// その実施点が RequireProjectPermission になる。
//
// **ロールや権限とは独立した軸である。** 当人がプロジェクトBのメンバーであっても、
// プロジェクトAに紐づくトークンで送られたリクエストはBに触れられない。
// トークンスコープ（6.4.1）が「何をしてよいか」を絞るのに対し、こちらは
// 「どのプロジェクトに対してか」を絞る。
func (p *Principal) CanReachProject(projectID string) bool {
	if p == nil {
		return false
	}
	return p.ProjectID == "" || p.ProjectID == projectID
}

// システムロール（DbDesign.md 6.2 app_user.system_role の CHECK 制約）。
const (
	SystemRoleOperator      = "operator"
	SystemRoleAdministrator = "administrator"
)

// AuditLabel は audit_log.actor_label に入れる文字列を返す。
//
// actor を消した後も「誰の操作か」を追えるようにするための非正規化列であり
// （DbDesign.md 6.8、ApiDesign.md 6.5）、表示名とメールの両方を残す。
func (p *Principal) AuditLabel() string {
	if p == nil {
		return ""
	}
	if p.Email == "" {
		return p.DisplayName
	}
	return p.DisplayName + " <" + p.Email + ">"
}

// DecodeScopes は access_token.scopes（jsonb の文字列配列）を読む。
//
// **解釈できなければエラーを返す。** 空スライスに倒してはならない。
// スコープは「権限の上限」であり、空は「絞り込みなし」＝ロールの全権限を
// 意味するため（Design.md 6.4.1）、壊れた値を黙って空に読み替えると
// 絞ったはずのエージェントトークンが全権限で通ってしまう。
func DecodeScopes(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil, fmt.Errorf("access_token.scopes を解釈できない: %w", err)
	}
	return scopes, nil
}

// EncodeScopes は access_token.scopes に入れる jsonb を作る。
// nil でも JSON の null ではなく空配列 [] にする（列は NOT NULL）。
func EncodeScopes(scopes []string) ([]byte, error) {
	if scopes == nil {
		scopes = []string{}
	}
	b, err := json.Marshal(scopes)
	if err != nil {
		return nil, fmt.Errorf("access_token.scopes を JSON にできない: %w", err)
	}
	return b, nil
}

type principalContextKey struct{}

// NewPrincipalContext はプリンシパルを載せたコンテキストを返す。
func NewPrincipalContext(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext はプリンシパルを取り出す。未認証なら nil を返す。
func PrincipalFromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalContextKey{}).(*Principal)
	return p
}
