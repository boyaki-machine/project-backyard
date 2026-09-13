package v1

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// passkeyFakeState はパスキーが触るものを持つ（pb-104。ApiDesign.md 3.5 / 3.6 / 4.7 / 6.10）。
//
// **挑戦は作ったときの引数から引き返す。** 3.5 → 3.6、4.7.2 → 4.7.3 の流れを
// ルータ越しにそのまま通すためで、テストが挑戦の行を手で組むと、SessionData の
// 中身（rpId・UV・期限）が本物とずれる。**消費は1回だけ通す**（実 DB の
// consumed_at IS NULL と同じ振る舞い）。
type passkeyFakeState struct {
	// list は ListPasskeys が返す行。count は CountPasskeys が返す件数。
	list  []gen.ListPasskeysRow
	count int64
	// descriptors は ListPasskeyDescriptors が返す行。
	descriptors     []gen.ListPasskeyDescriptorsRow
	descriptorCalls []gen.ListPasskeyDescriptorsParams
	nameTaken       bool

	// login は FindPasskeyLogin が返す行。credential_id が一致しなければ pgx.ErrNoRows。
	login      *gen.FindPasskeyLoginRow
	loginCalls int

	created   []gen.CreatePasskeyParams
	createErr error
	touched   []gen.TouchPasskeyUsedParams

	deleted    []gen.DeletePasskeyParams
	deleteName string // 空なら pgx.ErrNoRows（他人の・存在しない）

	allDeletedFor  []string
	allDeletedRows int64

	challengesCreated    []gen.CreateWebauthnChallengeParams
	expiredDeleted       int
	challengesDeletedFor []pgtype.Text
	// challenge を置くと、作成済みの挑戦より優先して返す（期限切れなどを作るため）。
	challenge *gen.FindWebauthnChallengeRow
	consumed  []string
}

func (q *fakeQuerier) ListPasskeys(_ context.Context, _ string) ([]gen.ListPasskeysRow, error) {
	return q.passkey.list, nil
}

func (q *fakeQuerier) CountPasskeys(_ context.Context, _ string) (int64, error) {
	return q.passkey.count, nil
}

func (q *fakeQuerier) ListPasskeyDescriptors(_ context.Context, arg gen.ListPasskeyDescriptorsParams) ([]gen.ListPasskeyDescriptorsRow, error) {
	q.passkey.descriptorCalls = append(q.passkey.descriptorCalls, arg)
	return q.passkey.descriptors, nil
}

func (q *fakeQuerier) FindPasskeyByName(_ context.Context, _ gen.FindPasskeyByNameParams) (string, error) {
	if q.passkey.nameTaken {
		return "01K2PK0000000000000000TAKEN", nil
	}
	return "", pgx.ErrNoRows
}

func (q *fakeQuerier) CreatePasskey(_ context.Context, arg gen.CreatePasskeyParams) (pgtype.Timestamptz, error) {
	q.opLog = append(q.opLog, "CreatePasskey")
	if q.passkey.createErr != nil {
		return pgtype.Timestamptz{}, q.passkey.createErr
	}
	q.passkey.created = append(q.passkey.created, arg)
	return ts(time.Now()), nil
}

func (q *fakeQuerier) FindPasskeyLogin(_ context.Context, credentialID []byte) (gen.FindPasskeyLoginRow, error) {
	q.passkey.loginCalls++
	if q.passkey.login == nil || !bytes.Equal(q.passkey.login.CredentialID, credentialID) {
		return gen.FindPasskeyLoginRow{}, pgx.ErrNoRows
	}
	return *q.passkey.login, nil
}

func (q *fakeQuerier) TouchPasskeyUsed(_ context.Context, arg gen.TouchPasskeyUsedParams) error {
	q.passkey.touched = append(q.passkey.touched, arg)
	return nil
}

func (q *fakeQuerier) DeletePasskey(_ context.Context, arg gen.DeletePasskeyParams) (string, error) {
	q.passkey.deleted = append(q.passkey.deleted, arg)
	if q.passkey.deleteName == "" {
		return "", pgx.ErrNoRows
	}
	return q.passkey.deleteName, nil
}

func (q *fakeQuerier) DeleteAllPasskeys(_ context.Context, userID string) (int64, error) {
	q.passkey.allDeletedFor = append(q.passkey.allDeletedFor, userID)
	return q.passkey.allDeletedRows, nil
}

func (q *fakeQuerier) CreateWebauthnChallenge(_ context.Context, arg gen.CreateWebauthnChallengeParams) error {
	q.passkey.challengesCreated = append(q.passkey.challengesCreated, arg)
	return nil
}

func (q *fakeQuerier) DeleteExpiredWebauthnChallenges(context.Context) error {
	q.passkey.expiredDeleted++
	return nil
}

func (q *fakeQuerier) DeleteWebauthnChallengesForUser(_ context.Context, userID pgtype.Text) (int64, error) {
	q.passkey.challengesDeletedFor = append(q.passkey.challengesDeletedFor, userID)
	return 0, nil
}

func (q *fakeQuerier) FindWebauthnChallenge(_ context.Context, arg gen.FindWebauthnChallengeParams) (gen.FindWebauthnChallengeRow, error) {
	if c := q.passkey.challenge; c != nil {
		return *c, nil
	}
	for _, c := range q.passkey.challengesCreated {
		if c.Challenge != arg.Challenge || c.Purpose != arg.Purpose {
			continue
		}
		row := gen.FindWebauthnChallengeRow{
			ID: c.ID, Purpose: c.Purpose, UserID: c.UserID,
			Session: c.Session, ExpiresAt: c.ExpiresAt,
		}
		if slices.Contains(q.passkey.consumed, c.ID) {
			row.ConsumedAt = ts(time.Now())
		}
		return row, nil
	}
	return gen.FindWebauthnChallengeRow{}, pgx.ErrNoRows
}

func (q *fakeQuerier) ConsumeWebauthnChallenge(_ context.Context, id string) (int64, error) {
	if slices.Contains(q.passkey.consumed, id) {
		return 0, nil
	}
	q.passkey.consumed = append(q.passkey.consumed, id)
	return 1, nil
}
