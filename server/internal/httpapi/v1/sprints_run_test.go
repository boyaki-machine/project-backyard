package v1

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// スプリントの運用（ApiDesign.md 9.12.1 / 9.12.2）の単体テスト。
//
// **見るのは「盤面がどう動いたか」である。** 応答の形は 9.12 の CRUD と同じなので
// あちらが押さえており、ここで確かめる価値があるのは
//
//	開始：対象がオンステージの部分木か／進行中が2本にならないか
//	終了：所属を閉じたか／**完了した根だけ**を段から降ろしたか
//
// の4点である。

func startFake() *fakeQuerier {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 4", Status: "active",
	}
	return q
}

// ── 開始（9.12.1）────────────────────────────────────────────

// オンステージに載っているものが、そのまま対象になる。
//
// **ListOnstageTicketIDs が返したものを、そのまま2本の書き込みへ渡すこと。**
// 画面側で staged_at を見て選び直す実装だと配下が落ちるので、サーバが
// 部分木ごと取る形になっているかをここで固定する。
func TestStartSprintEnrollsOnstageSubtree(t *testing.T) {
	q := startFake()
	q.sprint.onstageIDs = []string{"01K2T00000000000000000001", "01K2T00000000000000000002"}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.startSprint(rec, sprintReq(http.MethodPost, "/projects/demo/sprints/start",
		`{"name":"Sprint 4","start_date":"2026-09-08","end_date":"2026-09-21"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("コミットしていない")
	}
	if len(q.sprint.added) != 1 {
		t.Fatalf("AddTicketsToSprint の回数 = %d（1回でまとめて書くこと）", len(q.sprint.added))
	}
	if got := q.sprint.added[0].TicketIds; !slices.Equal(got, q.sprint.onstageIDs) {
		t.Errorf("対象 = %v, want %v", got, q.sprint.onstageIDs)
	}
	if len(q.sprint.assigned) != 1 {
		t.Fatalf("SetTicketsSprintID の回数 = %d", len(q.sprint.assigned))
	}
	if got := q.sprint.assigned[0].TicketIds; !slices.Equal(got, q.sprint.onstageIDs) {
		t.Errorf("sprint_id を付ける相手 = %v, want %v", got, q.sprint.onstageIDs)
	}
	// 作ったスプリントは active である（planned を経由しない）。
	if len(q.createdSprints) != 1 || q.createdSprints[0].Status != sprintStatusActive {
		t.Errorf("作ったスプリントの status = %+v, want active", q.createdSprints)
	}
}

// **オンステージが空でも開始できる**（9.12.1）。期間を先に切ってから積む
// 進め方があるので、開始できない理由にしない。
func TestStartSprintAllowsEmptyOnstage(t *testing.T) {
	q := startFake()
	q.sprint.onstageIDs = nil
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.startSprint(rec, sprintReq(http.MethodPost, "/projects/demo/sprints/start",
		`{"name":"Sprint 4"}`, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("コミットしていない")
	}
	// **空の配列で書きに行かない。** 0件の INSERT は意味が無く、
	// unnest(空) は0行なので害は無いが、往復を1つ増やす理由も無い。
	if len(q.sprint.added) != 0 {
		t.Errorf("対象が無いのに AddTicketsToSprint を呼んでいる: %v", q.sprint.added)
	}
}

// **進行中は同時に1本だけである**（9.12.1）。既に在れば 409 で、何も作らない。
func TestStartSprintRejectsSecondActive(t *testing.T) {
	q := startFake()
	q.sprint.active = gen.GetActiveSprintRow{ID: "01K2SPRINT000000000000009", Name: "Sprint 3"}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.startSprint(rec, sprintReq(http.MethodPost, "/projects/demo/sprints/start",
		`{"name":"Sprint 4"}`, ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// **進行中のスプリント名を出す**——次に何をすればよいかが文面で分かる（2.5）。
	if !strings.Contains(rec.Body.String(), "Sprint 3") {
		t.Errorf("進行中のスプリント名が応答に無い: %s", rec.Body.String())
	}
	if tx.committed {
		t.Error("409 なのにコミットしている")
	}
	if len(q.createdSprints) != 0 {
		t.Errorf("409 なのにスプリントを作っている: %v", q.createdSprints)
	}
}

// 名前は必須で、上限は 50 文字（9.12 と同じ検証を通す）。
func TestStartSprintValidatesName(t *testing.T) {
	q := startFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.startSprint(rec, sprintReq(http.MethodPost, "/projects/demo/sprints/start",
		`{"name":"  "}`, ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "required") {
		t.Errorf("details[].code が required でない: %s", rec.Body.String())
	}
}

// ── 終了（9.12.2）────────────────────────────────────────────

// 終了は3つを同じトランザクションで行う——状態・所属・段。
func TestFinishSprintClosesMembershipAndUnstages(t *testing.T) {
	q := startFake()
	q.sprint.finishRows = 1
	q.sprint.unstagedRows = 2
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.finishSprint(rec, sprintReq(http.MethodPost,
		"/projects/demo/sprints/"+testSprintID+"/finish", "", testSprintID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !tx.committed {
		t.Error("コミットしていない")
	}
	if len(q.sprint.finished) != 1 {
		t.Fatalf("FinishSprint の回数 = %d", len(q.sprint.finished))
	}
	if !slices.Contains(q.sprint.membershipClosed, testSprintID) {
		t.Errorf("所属を閉じていない: %v", q.sprint.membershipClosed)
	}
	if len(q.sprint.unstaged) != 1 {
		t.Fatalf("UnstageClosedTicketsInSprint の回数 = %d", len(q.sprint.unstaged))
	}
	if got := q.sprint.unstaged[0].SprintID; got.String != testSprintID {
		t.Errorf("段を降ろす対象のスプリント = %q, want %q", got.String, testSprintID)
	}
}

// **進行中でなければ終えられない**（9.12.2）。404 ではなく 409 である
// ——権限でも不在でもなく、盤面がその操作を許さない。
func TestFinishSprintRejectsNonActive(t *testing.T) {
	q := sprintFake()
	q.sprintByID[testSprintID] = gen.GetSprintByIDRow{
		ID: testSprintID, Name: "Sprint 2", Status: "completed",
	}
	h, tx := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.finishSprint(rec, sprintReq(http.MethodPost,
		"/projects/demo/sprints/"+testSprintID+"/finish", "", testSprintID))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tx.committed {
		t.Error("409 なのにコミットしている")
	}
	if len(q.sprint.membershipClosed) != 0 || len(q.sprint.unstaged) != 0 {
		t.Error("409 なのに盤面を触っている")
	}
}

// 無いスプリントは 404（他プロジェクトの ID も同じ。Design.md 6.4.5）。
func TestFinishSprintNotFound(t *testing.T) {
	q := sprintFake()
	h, _ := ticketHandler(q)

	rec := httptest.NewRecorder()
	h.finishSprint(rec, sprintReq(http.MethodPost,
		"/projects/demo/sprints/"+testSprintID+"/finish", "", testSprintID))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
