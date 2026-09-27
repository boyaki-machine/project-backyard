package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// コメントAPI（ApiDesign.md 9.8）の単体テスト。手順18a。
//
// 認可（ticket.view / comment.create / comment.edit_own / comment.delete_any）は
// ミドルウェアの責務なのでここでは通さない（routes_test.go が宣言を見ている）。
// **ただし「自分のものか」の判定はハンドラの責務**なので、ここで確かめる。

const (
	testCommentID  = "01K2CMT0000000000000000001"
	testCommentID2 = "01K2CMT0000000000000000002"
	testOtherActor = "01K2F8QW3H7YRJ4M5N6P7Q8OTH"
)

// ── 素材 ────────────────────────────────────────────────────

// commentReq は /tickets/{seq}/comments 系のリクエストを組み立てる。
//
// actorID を引数に取るのは、**PATCH / DELETE の「自分のもの」判定**を
// 両側から測るためである（refReq は1人しか居ない前提だった）。
func commentReq(method, target, body, seq, id, actorID, actorKind string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	if seq != "" {
		rc.URLParams.Add("seq", seq)
	}
	if id != "" {
		rc.URLParams.Add("id", id)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{
		ActorID: actorID, ActorKind: actorKind,
	})
	return req.WithContext(ctx)
}

// cmtReq は testActorID（人）として叩く既定の形。
func cmtReq(method, target, body, seq, id string) *http.Request {
	return commentReq(method, target, body, seq, id, testActorID, "user")
}

// commentFake は seq=31 のチケットが解決できる状態のフェイクを返す。
func commentFake() *fakeQuerier {
	q := ticketFake()
	q.ticket.idBySeq[31] = testTicketID
	q.ticket.repliable = map[string]bool{}
	return q
}

// withDeleteAny は comment.delete_any を持つ認可結果をコンテキストに載せる。
//
// **ミドルウェアが計算済みのものをハンドラが読む形**（comments.go）なので、
// 単体テストでも同じ入れ物へ入れる。載せなければ「持っていない」になる。
func withDeleteAny(req *http.Request, permissions ...string) *http.Request {
	ctx := auth.NewProjectAuthzContext(req.Context(), &auth.ProjectAuthz{
		ProjectID: testProjectID, Key: "demo", Reachable: true,
		Permissions: permissions,
	})
	return req.WithContext(ctx)
}

// sampleComment は 9.8 の応答例に合わせた行。
func sampleComment(id, body, kind, authorID string, at time.Time) gen.GetTicketCommentRow {
	name := "田中"
	if authorID != testActorID {
		name = "佐藤"
	}
	return gen.GetTicketCommentRow{
		ID: id, BodyMd: body, Kind: kind, Origin: originHuman,
		CreatedAt: ts(at), UpdatedAt: ts(at),
		AuthorID: authorID, AuthorKind: "user", AuthorName: name,
	}
}

// commentJSON は応答を読むための型（v1.Time は読み戻せないので時刻は string）。
type commentJSON struct {
	ID        string   `json:"id"`
	BodyMd    *string  `json:"body_md"`
	Kind      string   `json:"kind"`
	InReplyTo *string  `json:"in_reply_to"`
	Origin    string   `json:"origin"`
	Author    actorRef `json:"author"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	DeletedAt *int64   `json:"deleted_at"`
}

type commentListJSON struct {
	Items      []commentJSON `json:"items"`
	Page       int           `json:"page"`
	PerPage    int           `json:"per_page"`
	Total      int           `json:"total"`
	TotalPages int           `json:"total_pages"`
}

func decodeComment(t *testing.T, rec *httptest.ResponseRecorder) commentJSON {
	t.Helper()
	var v commentJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

func decodeCommentList(t *testing.T, rec *httptest.ResponseRecorder) commentListJSON {
	t.Helper()
	var v commentListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// baseTime は素材の基準時刻。
var baseTime = time.Date(2026, 8, 27, 2, 10, 0, 0, time.UTC)

// ── GET（9.8）───────────────────────────────────────────────

func TestListCommentsDefaultsToOldestFirst(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID2, "あとの発言", "discussion", testActorID, baseTime.Add(time.Hour)),
		sampleComment(testCommentID, "さきの発言", "progress", testActorID, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeCommentList(t, rec)
	// 9.8 の既定は created_at の昇順。素材は新しいほうを先に置いてあるので、
	// **並べ替えが効いていなければこの順は再現しない。**
	if len(got.Items) != 2 || got.Items[0].ID != testCommentID {
		t.Fatalf("items = %+v, want 古い順（%s が先頭）", got.Items, testCommentID)
	}
	if got.PerPage != 50 {
		t.Errorf("per_page = %d, want 50（9.8 の既定）", got.PerPage)
	}
	if got.Total != 2 {
		t.Errorf("total = %d, want 2", got.Total)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag が空（2.7 / 9.8）")
	}
}

func TestListCommentsHonorsDescOrder(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "さきの発言", "discussion", testActorID, baseTime),
		sampleComment(testCommentID2, "あとの発言", "discussion", testActorID, baseTime.Add(time.Hour)),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments?order=desc", "", "31", ""))

	got := decodeCommentList(t, rec)
	if len(got.Items) != 2 || got.Items[0].ID != testCommentID2 {
		t.Fatalf("items = %+v, want 新しい順（%s が先頭）", got.Items, testCommentID2)
	}
}

// **削除済みは items に残り、body_md が null になる**（9.8）。
func TestListCommentsKeepsDeletedWithNullBody(t *testing.T) {
	q := commentFake()
	row := sampleComment(testCommentID, "消された発言", "discussion", testActorID, baseTime)
	row.DeletedAt = tsp(baseTime.Add(time.Hour))
	q.ticket.commentRows = []gen.GetTicketCommentRow{row}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments", "", "31", ""))

	got := decodeCommentList(t, rec)
	if len(got.Items) != 1 {
		t.Fatalf("items = %d件, want 1（削除済みも残す）", len(got.Items))
	}
	if got.Items[0].BodyMd != nil {
		t.Errorf("body_md = %v, want null（削除済み）", *got.Items[0].BodyMd)
	}
	if got.Items[0].DeletedAt == nil {
		t.Error("deleted_at が null（削除済みなら値が入る）")
	}
	// **total には数える**（items に残す以上、外すとページの件数と合わない）。
	if got.Total != 1 {
		t.Errorf("total = %d, want 1（削除済みも数える）", got.Total)
	}
}

// 0件でも ETag と total を返す（ウィンドウ関数が1行も返さない経路）。
func TestListCommentsEmptyStillHasETag(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments", "", "31", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeCommentList(t, rec)
	if got.Total != 0 || len(got.Items) != 0 {
		t.Errorf("total/items = %d/%d, want 0/0", got.Total, len(got.Items))
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag が空（0件でも要る）")
	}
	// SummarizeTicketComments を通ったこと（別クエリの経路）を確かめる。
	if !containsOp(q.opLog, "SummarizeTicketComments") {
		t.Errorf("opLog = %v, want SummarizeTicketComments を含む", q.opLog)
	}
}

// **ページが違えば ETag も違う。** ETag は応答本文を指す検証子である。
func TestCommentsETagVariesByPage(t *testing.T) {
	last := ts(baseTime)
	p1 := Page{Page: 1, PerPage: 50, Sort: "created_at", Order: OrderAsc}
	p2 := Page{Page: 2, PerPage: 50, Sort: "created_at", Order: OrderAsc}
	desc := Page{Page: 1, PerPage: 50, Sort: "created_at", Order: OrderDesc}

	if commentsETag(p1, 3, last) == commentsETag(p2, 3, last) {
		t.Error("1ページ目と2ページ目の ETag が同じ")
	}
	if commentsETag(p1, 3, last) == commentsETag(desc, 3, last) {
		t.Error("asc と desc の ETag が同じ")
	}
}

func TestListCommentsRejectsUnknownSort(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments?sort=kind", "", "31", ""))

	// 9.8 が許すのは created_at だけ。丸めずに 422（2.6）。
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// ── POST（9.8）──────────────────────────────────────────────

func TestCreateCommentDefaultsKindToDiscussion(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec,
		cmtReq(http.MethodPost, "/comments", `{"body_md":"レビューをお願いします"}`, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.comments) != 1 {
		t.Fatalf("CreateComment = %d回, want 1", len(q.ticket.comments))
	}
	if got := q.ticket.comments[0].Kind; got != commentKindDefault {
		t.Errorf("kind = %q, want %q（9.8 の既定）", got, commentKindDefault)
	}
	if got := q.ticket.comments[0].Origin; got != originHuman {
		t.Errorf("origin = %q, want human（呼び出し元が人）", got)
	}
	if rec.Header().Get("Location") == "" {
		t.Error("Location が空")
	}
}

// **origin はリクエストではなく呼び出し元のアクター種別から決まる**（9.8）。
func TestCreateCommentOriginFollowsActorKind(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec, commentReq(http.MethodPost, "/comments",
		`{"body_md":"実装しました","origin":"human"}`, "31", "", testActorID, actorKindAgent))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	// 本文に origin:"human" を入れても、エージェントなら agent になる。
	if got := q.ticket.comments[0].Origin; got != originAgent {
		t.Errorf("origin = %q, want agent（本文の指定は効かない）", got)
	}
}

func TestCreateCommentRejectsEmptyBody(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec, cmtReq(http.MethodPost, "/comments", `{"body_md":"   "}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"body_md"`) {
		t.Errorf("details に body_md が無い: %s", rec.Body.String())
	}
}

func TestCreateCommentRejectsUnknownKind(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec,
		cmtReq(http.MethodPost, "/comments", `{"body_md":"x","kind":"memo"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// in_reply_to は「同じチケットの、削除されていないコメント」であること（9.8）。
func TestCreateCommentRejectsUnknownReplyTarget(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec, cmtReq(http.MethodPost, "/comments",
		`{"body_md":"返信","in_reply_to":"`+testCommentID+`"}`, "31", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("details[].code に not_found が無い: %s", rec.Body.String())
	}
	// **弾いたなら作られていない。**
	if len(q.ticket.comments) != 0 {
		t.Errorf("CreateComment = %d回, want 0", len(q.ticket.comments))
	}
}

func TestCreateCommentAcceptsValidReplyTarget(t *testing.T) {
	q := commentFake()
	q.ticket.repliable[testCommentID] = true
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec, cmtReq(http.MethodPost, "/comments",
		`{"body_md":"返信","in_reply_to":"`+testCommentID+`"}`, "31", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if got := q.ticket.comments[0].InReplyTo; !got.Valid || got.String != testCommentID {
		t.Errorf("in_reply_to = %+v, want %q", got, testCommentID)
	}
}

// activity は action='update' / field='comment'、new_value は要約（9.8）。
func TestCreateCommentRecordsActivity(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.createTicketComment(rec, cmtReq(http.MethodPost, "/comments",
		`{"body_md":"レビューをお願いします","kind":"decision"}`, "31", ""))

	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.Action != "update" {
		t.Errorf("action = %q, want update", a.Action)
	}
	if !a.Field.Valid || a.Field.String != "comment" {
		t.Errorf("field = %+v, want comment", a.Field)
	}
	if a.EntityID != testTicketID {
		t.Errorf("entity_id = %q, want 親チケット %q", a.EntityID, testTicketID)
	}
	if a.OldValue.Valid {
		t.Errorf("old_value = %v, want NULL（投稿）", a.OldValue.String)
	}
	if got := a.NewValue.String; got != "決定: レビューをお願いします" {
		t.Errorf("new_value = %q, want %q", got, "決定: レビューをお願いします")
	}
}

// **要約は40字で切る**（9.8）。履歴の1行を本文で埋め尽くさない。
func TestCommentSummaryTruncates(t *testing.T) {
	long := strings.Repeat("あ", 50)
	got := commentSummaryOf(commentView{Kind: "discussion", BodyMd: &long})
	want := "議論: " + strings.Repeat("あ", commentActivityBodyLimit) + "…"
	if got != want {
		t.Errorf("要約 = %q, want %q", got, want)
	}
}

// 改行は空白へ畳む（9.13.2 の一覧が縦に伸びないように）。
func TestCommentSummaryFoldsNewlines(t *testing.T) {
	body := "1行目\n2行目\n\n3行目"
	got := commentSummaryOf(commentView{Kind: "caveat", BodyMd: &body})
	if got != "注意: 1行目 2行目 3行目" {
		t.Errorf("要約 = %q", got)
	}
}

// ── PATCH（9.8）─────────────────────────────────────────────

func TestPatchCommentUpdatesOwn(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "もとの本文", "discussion", testActorID, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"body_md":"直した本文","kind":"decision"}`, "31", testCommentID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeComment(t, rec)
	if got.BodyMd == nil || *got.BodyMd != "直した本文" {
		t.Errorf("body_md = %v, want 直した本文", got.BodyMd)
	}
	if got.Kind != "decision" {
		t.Errorf("kind = %q, want decision", got.Kind)
	}
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "議論: もとの本文" || a.NewValue.String != "決定: 直した本文" {
		t.Errorf("activity = %q -> %q", a.OldValue.String, a.NewValue.String)
	}
}

// **他人のコメントは 403**（404 に倒さない。9.8）。
func TestPatchCommentRejectsOthers(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "他人の本文", "discussion", testOtherActor, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"body_md":"書き換え"}`, "31", testCommentID))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.commentUpdate) != 0 {
		t.Errorf("UpdateComment = %d回, want 0", len(q.ticket.commentUpdate))
	}
}

// **in_reply_to は immutable_field**（9.8）。
func TestPatchCommentRejectsInReplyTo(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "本文", "discussion", testActorID, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"in_reply_to":"`+testCommentID2+`"}`, "31", testCommentID))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "immutable_field") {
		t.Errorf("details[].code に immutable_field が無い: %s", rec.Body.String())
	}
}

// **削除済みへの PATCH は 404**（9.8）。
func TestPatchCommentOnDeletedIsNotFound(t *testing.T) {
	q := commentFake()
	row := sampleComment(testCommentID, "消えた本文", "discussion", testActorID, baseTime)
	row.DeletedAt = tsp(baseTime.Add(time.Hour))
	q.ticket.commentRows = []gen.GetTicketCommentRow{row}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"body_md":"復活"}`, "31", testCommentID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// 値が変わっていなければ activity を書かない（9.5.2 と同じ扱い）。
func TestPatchCommentSkipsActivityWhenUnchanged(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "同じ本文", "discussion", testActorID, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"body_md":"同じ本文"}`, "31", testCommentID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0（値が変わっていない）", len(q.ticket.activities))
	}
}

// ── DELETE（9.8）────────────────────────────────────────────

func TestDeleteOwnComment(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "自分の本文", "discussion", testActorID, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	// comment.edit_own しか持たないが、自分のものなので通る（9.8）。
	h.deleteTicketComment(rec, withDeleteAny(
		cmtReq(http.MethodDelete, "/comments/"+testCommentID, "", "31", testCommentID),
		"comment.edit_own"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.commentDelete) != 1 {
		t.Fatalf("SoftDeleteComment = %d回, want 1", len(q.ticket.commentDelete))
	}
	// 削除の activity は old_value だけ（new_value は NULL）。
	if len(q.ticket.activities) != 1 {
		t.Fatalf("activity = %d行, want 1", len(q.ticket.activities))
	}
	a := q.ticket.activities[0]
	if a.OldValue.String != "議論: 自分の本文" {
		t.Errorf("old_value = %q", a.OldValue.String)
	}
	if a.NewValue.Valid {
		t.Errorf("new_value = %q, want NULL（削除）", a.NewValue.String)
	}
}

// **delete_any を持たない人は他人のコメントを消せない**（9.8）。
func TestDeleteOthersCommentWithoutDeleteAny(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "他人の本文", "discussion", testOtherActor, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketComment(rec, withDeleteAny(
		cmtReq(http.MethodDelete, "/comments/"+testCommentID, "", "31", testCommentID),
		"comment.edit_own"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.commentDelete) != 0 {
		t.Errorf("SoftDeleteComment = %d回, want 0", len(q.ticket.commentDelete))
	}
}

// **delete_any を持てば他人のコメントも消せる**（9.8）。
func TestDeleteOthersCommentWithDeleteAny(t *testing.T) {
	q := commentFake()
	q.ticket.commentRows = []gen.GetTicketCommentRow{
		sampleComment(testCommentID, "他人の本文", "discussion", testOtherActor, baseTime),
	}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketComment(rec, withDeleteAny(
		cmtReq(http.MethodDelete, "/comments/"+testCommentID, "", "31", testCommentID),
		"comment.delete_any", "comment.edit_own"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
}

// **二重削除は 404**（論理削除でも「もう無い」。9.8）。
func TestDeleteCommentTwiceIsNotFound(t *testing.T) {
	q := commentFake()
	row := sampleComment(testCommentID, "本文", "discussion", testActorID, baseTime)
	row.DeletedAt = tsp(baseTime.Add(time.Hour))
	q.ticket.commentRows = []gen.GetTicketCommentRow{row}
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.deleteTicketComment(rec, withDeleteAny(
		cmtReq(http.MethodDelete, "/comments/"+testCommentID, "", "31", testCommentID),
		"comment.edit_own"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if len(q.ticket.activities) != 0 {
		t.Errorf("activity = %d行, want 0", len(q.ticket.activities))
	}
}

// 存在しない ID は 404。
func TestPatchCommentNotFound(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.patchTicketComment(rec, cmtReq(http.MethodPatch, "/comments/"+testCommentID,
		`{"body_md":"x"}`, "31", testCommentID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// 他チケットの seq は 404（ticketScope が倒す）。
func TestCommentsOnUnknownTicketIsNotFound(t *testing.T) {
	q := commentFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.listTicketComments(rec, cmtReq(http.MethodGet, "/comments", "", "999", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// containsOp は opLog に名前が含まれるかを見る。
func containsOp(log []string, name string) bool {
	for _, op := range log {
		if op == name {
			return true
		}
	}
	return false
}

// 未使用の import を避けるための型参照（pgtype はフェイクの素材で使う）。
var _ = pgtype.Text{}
