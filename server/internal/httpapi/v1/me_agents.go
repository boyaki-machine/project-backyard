// 自分のエージェント（ApiDesign.md 4.5、Design.md 6.5、DbDesign.md 8.2.1）。
//
// **登録するのは本人である。** Requirements.md 10.9.1 の系統B（メンバーの参画）が
// 「誰が」の欄に「参加する本人」を挙げており、トークンは 10.10.3 のとおり
// 参加者ごとに発行する。**4.4（/me/tokens）と同じく権限キーを要求しない**
// ——agent.register / agent.token.issue は「他人のエージェントを管理する」側の
// 権限であり、自分のものには要らない。
//
// **1件が表すのは「ある参加者の手元で動くクライアント1つ」である。** 人ではない。
// キーは（所有者・クライアント種別・プロジェクト）の3つ組で、同じ人が
// Claude Code と VS Code を使えば2件になる。
//
// **権限は所有者から導く（委譲）。** エージェントの実効権限は
// ( 自分のシステムロール ∪ 自分のプロジェクトロール ) ∩ トークンのスコープ
// であり、**自分の権限を超えるエージェントは作れない**。実施点は
// auth.Principal.AuthzActorID と middleware/authz.go にある。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// エージェントの制約（ApiDesign.md 4.5.2）。
const (
	// agentNameMaxLen は display_name の上限。actor.display_name の
	// CHECK（1〜60。DbDesign.md 6.2）に揃える。**DBの制約と別の数を書かない。**
	agentNameMaxLen = 60

	// agentModelMaxLen は model_name / model_version の上限。
	// agent 表の列に CHECK は無く（DbDesign.md 8.2.1）、アプリ側が持つ。
	agentModelMaxLen = 100
)

// client_kind の値域は Go の定数で持たない（0020）。
//
// **正本は agent_client_kind の行である**（DbDesign.md 8.2.1.1）。0019 までは
// DBの CHECK と同じ綴りを Go 側にも並べていたが、**値域が「世の中のエージェント一覧」に
// なった以上、二重に持つと必ず食い違う**——食い違えば検証を通った値が INSERT で
// 落ちて 500 になる。検証は AgentClientKindExists で行い、最後の砦は FK である。

// agentDefaultScopes はエージェント用トークンの既定スコープ（Design.md 6.5）。
//
// **語彙は権限カタログのキーそのものである**（6.4.1）。改訂前の 6.5 は
// ticket:read / context:read / ticket:claim / note:write / result:submit /
// proposal:create という別語彙を挙げていたが、**その語彙で発行すると
// 実効権限が0件になる**——積は権限キーどうしの完全一致で取るためである。
//
// **ticket.close を入れない**（6.5 の禁止）。**doc.edit も入れない**——
// pb_put_doc に要る権限だが、載せるかは「そのエージェントが誰に付いているか」で
// 決まる（Design.md 8.2）。そもそも所有者が持たなければ積で消える。
//
// **昇順で持つ。** auth.EffectivePermissions が昇順で返すので、応答の並びと
// 突き合わせるときに並べ替えが要らない。
var agentDefaultScopes = []string{
	"agent.run",
	"comment.create",
	"doc.view",
	"project.view",
	"ticket.assign",
	"ticket.create",
	"ticket.transition",
	"ticket.view",
}

// projectRef は応答の project 部分（ApiDesign.md 4.5.1）。
type projectRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// agentTokenView は 4.5.1 の token 部分。**平文を持たない。**
type agentTokenView struct {
	ID          string `json:"id"`
	TokenPrefix string `json:"token_prefix"`
	IssuedAt    Time   `json:"issued_at"`
	LastUsedAt  *Time  `json:"last_used_at"`
	ExpiresAt   *Time  `json:"expires_at"`
	Status      string `json:"status"`
}

// myAgentView は 4.5.1 / 4.5.2 / 4.5.4 が返す1件。
//
// **users.go の agentView とは別物である。** あちらは 6.1（管理者の一覧）が
// 返す形で、所有者と client_kind を持つが名前や状態は親（userListItem）側に
// ある。こちらは本人の一覧で、エージェント1件を単独で表す。
type myAgentView struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name"`
	ClientKind   string          `json:"client_kind"`
	ModelName    *string         `json:"model_name"`
	ModelVersion *string         `json:"model_version"`
	Project      *projectRef     `json:"project"`
	TrustLevel   int32           `json:"trust_level"`
	IsActive     bool            `json:"is_active"`
	CreatedAt    Time            `json:"created_at"`
	Token        *agentTokenView `json:"token"`
}

// myAgentListView は 4.5.1 の応答。
//
// **ページネーションも ETag も持たない**（4.4.1 と同じ）。1人が持つ件数は
// 端末とプロジェクトの積であり、絞り込みも差分取得も意味を持たない。
type myAgentListView struct {
	Items []myAgentView `json:"items"`
}

// issuedAgentTokenView は 4.5.3 の応答。**token を持つ唯一の形**である。
type issuedAgentTokenView struct {
	ID          string   `json:"id"`
	Token       string   `json:"token"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	IssuedAt    Time     `json:"issued_at"`
	ExpiresAt   *Time    `json:"expires_at"`
	Status      string   `json:"status"`
}

// ── GET /api/v1/me/agents（4.5.1）───────────────────────────

func (h *handler) listMyAgents(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /me/agents が認証ミドルウェアを通っていない")))
		return
	}

	rows, err := h.q.ListMyAgents(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("エージェント一覧を取得できない: %w", err)))
		return
	}

	now := time.Now()
	items := make([]myAgentView, 0, len(rows))
	for _, row := range rows {
		v := myAgentView{
			ID:           row.ActorID,
			DisplayName:  row.DisplayName,
			ClientKind:   row.ClientKind,
			ModelName:    textPtr(row.ModelName),
			ModelVersion: textPtr(row.ModelVersion),
			TrustLevel:   row.TrustLevel,
			IsActive:     row.IsActive,
			CreatedAt:    Time(row.CreatedAt.Time),
		}
		if row.ProjectKey.Valid {
			v.Project = &projectRef{Key: row.ProjectKey.String, Name: row.ProjectName.String}
		}
		// **空文字は「有効なトークンが無い」**（agent.sql の token_id を参照）。
		// sqlc に NULL 可と推論させられないため、値で表している。
		if row.TokenID != "" {
			v.Token = &agentTokenView{
				ID:          row.TokenID,
				TokenPrefix: row.TokenPrefix.String,
				IssuedAt:    Time(row.TokenIssuedAt.Time),
				LastUsedAt:  apiTimestamptz(row.TokenLastUsedAt),
				ExpiresAt:   apiTimestamptz(row.TokenExpiresAt),
				Status:      tokenStatus(row.TokenExpiresAt, now),
			}
		}
		items = append(items, v)
	}
	WriteJSON(w, http.StatusOK, myAgentListView{Items: items})
}

// ── GET /api/v1/agent-client-kinds（4.5.7）──────────────────

// agentClientKindView はカタログの1件（ApiDesign.md 4.5.7）。
type agentClientKindView struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
}

// listAgentClientKinds はクライアント種別のカタログを返す（ApiDesign.md 4.5.7）。
//
// **必要権限は無い**（認証済みであればよい）。消費者は 5.8.2 の画面で、
// そこの必要権限は「本人」である。カタログ自体は秘密ではない。
//
// **画面に対応表を持たせないために在る。** GET /roles が display_name を
// 返すようになった時点で lib/roles.ts を廃止したのと同じ形（GuiDesign.md 5.6）
// ——値域は今後も増えるので、写しを置くと必ず腐る。
//
// **ページネーションも ETag も持たない**（7.1 / 7.2 と同じ扱い）。
func (h *handler) listAgentClientKinds(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListAgentClientKinds(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("クライアント種別の一覧を取得できない: %w", err)))
		return
	}
	items := make([]agentClientKindView, 0, len(rows))
	for _, row := range rows {
		items = append(items, agentClientKindView{Key: row.Key, DisplayName: row.DisplayName})
	}
	WriteJSON(w, http.StatusOK, catalog[agentClientKindView]{Items: items})
}

// ── POST /api/v1/me/agents（4.5.2）──────────────────────────

type createAgentRequest struct {
	DisplayName  string `json:"display_name"`
	ProjectKey   string `json:"project_key"`
	ClientKind   string `json:"client_kind"`
	ModelName    string `json:"model_name"`
	ModelVersion string `json:"model_version"`
}

type createAgentFields struct {
	displayName  string
	projectKey   string
	clientKind   string
	modelName    string
	modelVersion string
}

func (h *handler) createMyAgent(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /me/agents が認証ミドルウェアを通っていない")))
		return
	}

	var req createAgentRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	f, e := validateCreateAgent(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()

	if e := h.checkClientKind(ctx, f.clientKind); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **自分がメンバーであるプロジェクトに限る**（4.5.2）。エージェントの権限は
	// 所有者から導かれるので、自分が入っていないプロジェクトのエージェントを
	// 作っても権限0件になる。**作れてしまうほうが分かりにくい。**
	//
	// **404 ではなく 422 に倒す。** project_key は本体のフィールドであり、
	// Design.md 6.4.5 の「存在を隠す」はパスで指した資源についての規約である。
	proj, err := h.q.FindMyProjectByKey(ctx, gen.FindMyProjectByKeyParams{
		ActorID:    p.ActorID,
		ProjectKey: f.projectKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "project_key", Code: "not_found",
				Message: "参加しているプロジェクトの中から選んでください",
			}))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクトを読めない: %w", err)))
		return
	}

	agentID := ulidgen.New()
	rec := audit.FromRequest(r)
	createdAt := time.Now()

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **数えるのと入れるのを同じトランザクションで行う**（4.4.2 と同じ議論）。
		exists, err := q.AgentExistsWithName(ctx, gen.AgentExistsWithNameParams{
			OwnerActorID: p.ActorID,
			ProjectID:    text(proj.ID),
			ClientKind:   f.clientKind,
			DisplayName:  f.displayName,
		})
		if err != nil {
			return fmt.Errorf("エージェントの重複を確かめられない: %w", err)
		}
		if exists {
			return apierr.New(apierr.AlreadyExists).
				WithMessage("同じ名前のエージェントが、このプロジェクトに既に登録されています")
		}

		if err := q.CreateAgentActor(ctx, gen.CreateAgentActorParams{
			ID:          agentID,
			DisplayName: f.displayName,
		}); err != nil {
			return fmt.Errorf("エージェントのアクターを作れない: %w", err)
		}
		if err := q.CreateAgent(ctx, gen.CreateAgentParams{
			ActorID:      agentID,
			OwnerActorID: p.ActorID,
			ProjectID:    text(proj.ID),
			ClientKind:   f.clientKind,
			ModelName:    text(f.modelName),
			ModelVersion: text(f.modelVersion),
			// trust_level と capabilities は受け取らない（4.5.2）。既定値で作る。
		}); err != nil {
			return fmt.Errorf("エージェントを登録できない: %w", err)
		}

		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.AgentRegister,
			Result:     audit.Success,
			TargetType: "agent",
			TargetID:   agentID,
			Detail: map[string]any{
				"display_name": f.displayName,
				"client_kind":  f.clientKind,
				"project_key":  proj.Key,
			},
		})
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	WriteJSON(w, http.StatusCreated, myAgentView{
		ID:           agentID,
		DisplayName:  f.displayName,
		ClientKind:   f.clientKind,
		ModelName:    nullable(f.modelName),
		ModelVersion: nullable(f.modelVersion),
		Project:      &projectRef{Key: proj.Key, Name: proj.Name},
		TrustLevel:   1,
		IsActive:     true,
		CreatedAt:    Time(createdAt),
		// **登録と発行を分ける**（4.5.2）。再発行が必要になったときに
		// 同じ経路（4.5.3）を通すため、ここでは token を作らない。
		Token: nil,
	})
}

// checkClientKind は client_kind が値域にあるかを DB で確かめる（ApiDesign.md 4.5.2）。
//
// **値域の正本は agent_client_kind の行である**（DbDesign.md 8.2.1.1）。Go 側に
// 一覧を持たないのは、**値が今後も増える**ためで、二重に持つと必ず食い違う。
//
// 値域外は 422（`details[].field` は client_kind）。**FK が最後の砦として残る**ので、
// ここを通り抜けても DB で落ちる——ただしその場合は 500 になるため、ここで先に見る。
func (h *handler) checkClientKind(ctx context.Context, kind string) *apierr.Error {
	ok, err := h.q.AgentClientKindExists(ctx, kind)
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("クライアント種別を確かめられない: %w", err))
	}
	if !ok {
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "client_kind", Code: "invalid",
			Message: "クライアントの種別を一覧から選んでください",
		})
	}
	return nil
}

// validateCreateAgent は 4.5.2 の入力を検証する。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateCreateAgent(req createAgentRequest) (createAgentFields, *apierr.Error) {
	var details []apierr.Detail
	var f createAgentFields

	f.displayName = strings.TrimSpace(req.DisplayName)
	switch {
	case f.displayName == "":
		details = append(details, apierr.Detail{
			Field: "display_name", Code: "required",
			Message: "エージェントの名前を入力してください",
		})
	case len([]rune(f.displayName)) > agentNameMaxLen:
		details = append(details, apierr.Detail{
			Field: "display_name", Code: "too_long",
			Message: fmt.Sprintf("エージェントの名前は%d文字以内で入力してください", agentNameMaxLen),
		})
	}

	f.projectKey = strings.TrimSpace(req.ProjectKey)
	if f.projectKey == "" {
		details = append(details, apierr.Detail{
			Field: "project_key", Code: "required",
			Message: "プロジェクトを選んでください",
		})
	}

	// **値域はここで判定しない**（0020）。正本は agent_client_kind の行なので、
	// ハンドラが DB へ問い合わせて確かめる（checkClientKind）。
	f.clientKind = strings.TrimSpace(req.ClientKind)
	if f.clientKind == "" {
		details = append(details, apierr.Detail{
			Field: "client_kind", Code: "required",
			Message: "クライアントの種別を選んでください",
		})
	}

	f.modelName = strings.TrimSpace(req.ModelName)
	if len([]rune(f.modelName)) > agentModelMaxLen {
		details = append(details, apierr.Detail{
			Field: "model_name", Code: "too_long",
			Message: fmt.Sprintf("モデル名は%d文字以内で入力してください", agentModelMaxLen),
		})
	}
	f.modelVersion = strings.TrimSpace(req.ModelVersion)
	if len([]rune(f.modelVersion)) > agentModelMaxLen {
		details = append(details, apierr.Detail{
			Field: "model_version", Code: "too_long",
			Message: fmt.Sprintf("モデルバージョンは%d文字以内で入力してください", agentModelMaxLen),
		})
	}

	if len(details) > 0 {
		return createAgentFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

// ── PATCH /api/v1/me/agents/:id（4.5.4）─────────────────────

// updateAgentRequest はポインタで受ける。**送られた項目だけを更新する**ため、
// 「空文字を送った」と「送らなかった」を区別する必要がある。
type updateAgentRequest struct {
	DisplayName *string `json:"display_name"`
	// ClientKind は 0020 から変更できる（ApiDesign.md 4.5.4）。**値域が今後も
	// 増える**ので、other で登録した人が、PB がその種別に対応した日に移れる
	// 必要がある。**project_key は変えられないまま**——仕事はプロジェクトに属する。
	ClientKind   *string `json:"client_kind"`
	ModelName    *string `json:"model_name"`
	ModelVersion *string `json:"model_version"`
	IsActive     *bool   `json:"is_active"`
}

func (h *handler) updateMyAgent(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("PATCH /me/agents/:id が認証ミドルウェアを通っていない")))
		return
	}
	agentID := chi.URLParam(r, "id")

	var req updateAgentRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	var details []apierr.Detail
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		switch {
		case name == "":
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "required",
				Message: "エージェントの名前を入力してください",
			})
		case len([]rune(name)) > agentNameMaxLen:
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "too_long",
				Message: fmt.Sprintf("エージェントの名前は%d文字以内で入力してください", agentNameMaxLen),
			})
		}
		req.DisplayName = &name
	}
	for _, c := range []struct {
		field string
		label string
		value *string
	}{
		{"model_name", "モデル名", req.ModelName},
		{"model_version", "モデルバージョン", req.ModelVersion},
	} {
		if c.value == nil {
			continue
		}
		v := strings.TrimSpace(*c.value)
		if len([]rune(v)) > agentModelMaxLen {
			details = append(details, apierr.Detail{
				Field: c.field, Code: "too_long",
				Message: fmt.Sprintf("%sは%d文字以内で入力してください", c.label, agentModelMaxLen),
			})
		}
		*c.value = v
	}
	if req.ClientKind != nil {
		kind := strings.TrimSpace(*req.ClientKind)
		if kind == "" {
			details = append(details, apierr.Detail{
				Field: "client_kind", Code: "required",
				Message: "クライアントの種別を選んでください",
			})
		}
		req.ClientKind = &kind
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	ctx := r.Context()
	if req.ClientKind != nil {
		if e := h.checkClientKind(ctx, *req.ClientKind); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}
	rec := audit.FromRequest(r)
	var updated gen.FindMyAgentRow

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **先に存在と持ち主を確かめる。** UPDATE の影響行数だけで判断すると、
		// 「他人のもの」と「値が同じで更新不要」を区別できない。
		cur, err := q.FindMyAgent(ctx, gen.FindMyAgentParams{
			ActorID: agentID, OwnerActorID: p.ActorID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apierr.New(apierr.NotFound)
			}
			return fmt.Errorf("エージェントを読めない: %w", err)
		}

		// **更新でも重複しうる**（ApiDesign.md 4.5.4）。キーは（所有者・
		// プロジェクト・クライアント種別・表示名）の4つ組で、**display_name と
		// client_kind はどちらも変えられる**ためである。**変わる項目があるときだけ
		// 数える**——値を送っていない項目は現在値のまま比較する。
		if req.DisplayName != nil || req.ClientKind != nil {
			name := cur.DisplayName
			if req.DisplayName != nil {
				name = *req.DisplayName
			}
			kind := cur.ClientKind
			if req.ClientKind != nil {
				kind = *req.ClientKind
			}
			dup, err := q.AgentExistsWithNameExcept(ctx, gen.AgentExistsWithNameExceptParams{
				OwnerActorID:   p.ActorID,
				ProjectID:      cur.ProjectID,
				ClientKind:     kind,
				DisplayName:    name,
				ExcludeActorID: agentID,
			})
			if err != nil {
				return fmt.Errorf("エージェントの重複を確かめられない: %w", err)
			}
			if dup {
				return apierr.New(apierr.AlreadyExists).
					WithMessage("同じ名前のエージェントが、このプロジェクトに既に登録されています")
			}
		}

		if req.DisplayName != nil || req.IsActive != nil {
			if _, err := q.UpdateAgentActor(ctx, gen.UpdateAgentActorParams{
				ActorID:      agentID,
				OwnerActorID: p.ActorID,
				DisplayName:  nargText(req.DisplayName),
				IsActive:     nargBool(req.IsActive),
			}); err != nil {
				return fmt.Errorf("エージェントを更新できない: %w", err)
			}
		}
		if req.ModelName != nil || req.ModelVersion != nil || req.ClientKind != nil {
			if _, err := q.UpdateAgentModel(ctx, gen.UpdateAgentModelParams{
				ActorID:      agentID,
				OwnerActorID: p.ActorID,
				ModelName:    nargText(req.ModelName),
				ModelVersion: nargText(req.ModelVersion),
				ClientKind:   nargText(req.ClientKind),
			}); err != nil {
				return fmt.Errorf("エージェントのモデルを更新できない: %w", err)
			}
		}

		// **無効化するとトークンも失効させる**（4.5.4）。無効化したのに
		// 一覧で「有効」と出続けるのは利用者の期待に反する。
		if req.IsActive != nil && !*req.IsActive {
			if _, err := q.RevokeAllAgentTokens(ctx, agentID); err != nil {
				return fmt.Errorf("エージェントのトークンを失効できない: %w", err)
			}
		}

		detail := map[string]any{}
		if req.DisplayName != nil {
			detail["display_name"] = *req.DisplayName
		}
		if req.ClientKind != nil {
			detail["client_kind"] = *req.ClientKind
		}
		if req.ModelName != nil {
			detail["model_name"] = *req.ModelName
		}
		if req.ModelVersion != nil {
			detail["model_version"] = *req.ModelVersion
		}
		if req.IsActive != nil {
			detail["is_active"] = *req.IsActive
		}
		if err := rec.Record(ctx, q, audit.Entry{
			Action:     audit.AgentUpdate,
			Result:     audit.Success,
			TargetType: "agent",
			TargetID:   agentID,
			Detail:     detail,
		}); err != nil {
			return err
		}

		// **応答は同じトランザクションの中で読む。** プールの側を使うと、
		// まだコミットしていない更新が応答に載らない（me.go と同じ議論）。
		row, err2 := q.FindMyAgent(ctx, gen.FindMyAgentParams{
			ActorID: agentID, OwnerActorID: p.ActorID,
		})
		if err2 != nil {
			return fmt.Errorf("更新後のエージェントを読めない: %w", err2)
		}
		updated = row
		return nil
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}

	v := myAgentView{
		ID:           updated.ActorID,
		DisplayName:  updated.DisplayName,
		ClientKind:   updated.ClientKind,
		ModelName:    textPtr(updated.ModelName),
		ModelVersion: textPtr(updated.ModelVersion),
		TrustLevel:   updated.TrustLevel,
		IsActive:     updated.IsActive,
		CreatedAt:    Time(updated.CreatedAt.Time),
	}
	if updated.ProjectKey.Valid {
		v.Project = &projectRef{Key: updated.ProjectKey.String, Name: updated.ProjectName.String}
	}
	WriteJSON(w, http.StatusOK, v)
}

// ── POST /api/v1/me/agents/:id/tokens（4.5.3）───────────────

type createAgentTokenRequest struct {
	ExpiresInDays *int `json:"expires_in_days"`
}

func (h *handler) createMyAgentToken(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /me/agents/:id/tokens が認証ミドルウェアを通っていない")))
		return
	}
	agentID := chi.URLParam(r, "id")

	var req createAgentTokenRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	// **値域は 4.4.2 と同じ 1〜365 を使う**（tokenMinExpiresInDays）。
	// 無期限を許さないのは Design.md 6.5 が「有効期限必須」と定めるためで、
	// CLI トークンと規則を分ける理由が無い。
	if req.ExpiresInDays == nil {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "expires_in_days", Code: "required",
			Message: "有効期限を指定してください",
		}))
		return
	}
	days := *req.ExpiresInDays
	if days < tokenMinExpiresInDays || days > tokenMaxExpiresInDays {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "expires_in_days", Code: "invalid",
			Message: fmt.Sprintf("有効期限は%d〜%d日で指定してください",
				tokenMinExpiresInDays, tokenMaxExpiresInDays),
		}))
		return
	}

	plaintext, err := auth.NewToken(auth.AgentTokenPrefix)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	encodedScopes, err := auth.EncodeScopes(agentDefaultScopes)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	ctx := r.Context()
	tokenID := ulidgen.New()
	issuedAt := time.Now()
	expiresAt := issuedAt.AddDate(0, 0, days)
	rec := audit.FromRequest(r)
	var projectKey string

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		ag, err := q.FindMyAgent(ctx, gen.FindMyAgentParams{
			ActorID: agentID, OwnerActorID: p.ActorID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apierr.New(apierr.NotFound)
			}
			return fmt.Errorf("エージェントを読めない: %w", err)
		}
		if !ag.IsActive {
			return apierr.New(apierr.Conflict).
				WithMessage("無効化されたエージェントにはトークンを発行できません。先に有効化してください")
		}
		projectKey = ag.ProjectKey.String

		// **有効なトークンは1件につき1本**（4.5.3）。再発行のときは
		// それまでの有効なトークンを失効させる。**同じトランザクションで
		// 行う**——分けると、失効だけ済んで発行に失敗したときに
		// 「動くトークンが1本も無い」状態が残る。
		olds, err := q.ListActiveAgentTokens(ctx, gen.ListActiveAgentTokensParams{
			ActorID: agentID, OwnerActorID: p.ActorID,
		})
		if err != nil {
			return fmt.Errorf("既存のトークンを読めない: %w", err)
		}
		for _, old := range olds {
			if _, err := q.RevokeAgentToken(ctx, gen.RevokeAgentTokenParams{
				ID: old.ID, ActorID: agentID, OwnerActorID: p.ActorID,
			}); err != nil {
				return fmt.Errorf("既存のトークンを失効できない: %w", err)
			}
			// **暗黙の失効も監査に残す**（4.5.6）。応答に出ない出来事ほど
			// 記録が要る——利用者は「再発行した」としか認識していない。
			if err := rec.Record(ctx, q, audit.Entry{
				Action:     audit.TokenRevoke,
				Result:     audit.Success,
				TargetType: "access_token",
				TargetID:   old.ID,
				Detail: map[string]any{
					"token_prefix": old.TokenPrefix.String,
					"reason":       "reissue",
				},
			}); err != nil {
				return err
			}
		}

		if err := q.CreateAccessToken(ctx, gen.CreateAccessTokenParams{
			ID:          tokenID,
			ActorID:     agentID,
			TokenType:   auth.TokenTypeAgent,
			TokenHash:   auth.HashToken(plaintext),
			TokenPrefix: text(auth.TokenPrefix(plaintext)),
			Name:        text(ag.DisplayName),
			// **project_id を入れる**（Design.md 6.5「プロジェクトスコープ必須」）。
			// 他プロジェクトへのアクセスは 404 になる（6.4.5 の実施点は
			// Principal.CanReachProject）。
			ProjectID: ag.ProjectID,
			Scopes:    encodedScopes,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
			// **client_info にクライアント種別を入れる**（ApiDesign.md 4.5.3）。
			// api トークンでは空だった列で、エージェントには入れる値がある。
			ClientInfo: text(ag.ClientKind),
		}); err != nil {
			return fmt.Errorf("エージェント用トークンを発行できない: %w", err)
		}

		// **平文は detail に入れない**（4.4.4 と同じ）。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TokenIssue,
			Result:     audit.Success,
			TargetType: "access_token",
			TargetID:   tokenID,
			Detail: map[string]any{
				"client_kind": ag.ClientKind,
				"project_key": ag.ProjectKey.String,
				"scopes":      agentDefaultScopes,
				"expires_at":  expiresAt.UTC().Format(time.RFC3339),
			},
		})
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}
	_ = projectKey

	WriteJSON(w, http.StatusCreated, issuedAgentTokenView{
		ID:          tokenID,
		Token:       plaintext,
		TokenPrefix: auth.TokenPrefix(plaintext),
		Scopes:      agentDefaultScopes,
		IssuedAt:    Time(issuedAt),
		ExpiresAt:   apiTime(&expiresAt),
		Status:      tokenStatusActive,
	})
}

// ── DELETE /api/v1/me/agents/:id/tokens/:token_id（4.5.5）───

func (h *handler) deleteMyAgentToken(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("DELETE /me/agents/:id/tokens/:token_id が認証ミドルウェアを通っていない")))
		return
	}
	agentID := chi.URLParam(r, "id")
	tokenID := chi.URLParam(r, "token_id")

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **他人のエージェントを 404 に倒す**（Design.md 6.4.5）。
		if _, err := q.FindMyAgent(ctx, gen.FindMyAgentParams{
			ActorID: agentID, OwnerActorID: p.ActorID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apierr.New(apierr.NotFound)
			}
			return fmt.Errorf("エージェントを読めない: %w", err)
		}

		// **失効済みでも 204 を返す（冪等）。** ただし監査は二重に書かない——
		// RevokeAgentToken は revoked_at IS NULL を条件に持つので、
		// 更新行数が 0 なら既に失効済みである。
		n, err := q.RevokeAgentToken(ctx, gen.RevokeAgentTokenParams{
			ID: tokenID, ActorID: agentID, OwnerActorID: p.ActorID,
		})
		if err != nil {
			return fmt.Errorf("トークンを失効できない: %w", err)
		}
		if n == 0 {
			return nil
		}
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TokenRevoke,
			Result:     audit.Success,
			TargetType: "access_token",
			TargetID:   tokenID,
			Detail:     map[string]any{"reason": "manual"},
		})
	})
	if err != nil {
		writeAgentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeAgentError は RunInTx が返した誤りを応答へ写す。
//
// **apierr.Error はそのまま通す。** トランザクションの中で組み立てた
// 409 / 404 を 500 に潰さないためである（createMyToken と同じ形）。
func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		apierr.Write(w, r, apiErr)
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
}
