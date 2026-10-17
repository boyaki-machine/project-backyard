package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// fakeQuerier は渡された値を控えるだけの Querier。
type fakeQuerier struct {
	created    []gen.CreateNotificationParams
	readBefore time.Time
}

func (f *fakeQuerier) CreateNotification(_ context.Context, arg gen.CreateNotificationParams) error {
	f.created = append(f.created, arg)
	return nil
}

func (f *fakeQuerier) WithdrawNotifications(context.Context, string) (int64, error) { return 0, nil }

func (f *fakeQuerier) DeleteExpiredNotifications(_ context.Context, readBefore time.Time) (int64, error) {
	f.readBefore = readBefore
	return 0, nil
}

func valid() Notification {
	return Notification{
		RecipientID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", Kind: "delegation.confirmation",
		Title: "確認してください", Link: "/p/my-app/tickets/52", LinkLabel: "my-app-52 題名",
	}
}

func TestCreateRejectsInvalidShape(t *testing.T) {
	cases := map[string]func(*Notification){
		"宛先が空":         func(n *Notification) { n.RecipientID = "" },
		"種類に点が無い":      func(n *Notification) { n.Kind = "confirmation" },
		"種類に大文字":       func(n *Notification) { n.Kind = "Delegation.confirmation" },
		"題名が空":         func(n *Notification) { n.Title = "" },
		"題名が201文字":     func(n *Notification) { n.Title = strings.Repeat("あ", 201) },
		"遷移先が別のホスト":    func(n *Notification) { n.Link = "//evil.example.com/x" },
		"遷移先が逆斜線":      func(n *Notification) { n.Link = `/\evil.example.com` },
		"遷移先が URL":     func(n *Notification) { n.Link = "https://evil.example.com/" },
		"遷移先の名前が空":     func(n *Notification) { n.LinkLabel = "" },
		"通知元の名前が101文字": func(n *Notification) { n.SourceID = "x"; n.SourceName = strings.Repeat("a", 101) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			n := valid()
			mutate(&n)
			f := &fakeQuerier{}
			if err := Create(context.Background(), f, n); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v、want ErrInvalid", err)
			}
			if len(f.created) != 0 {
				t.Error("形の誤った通知を DB へ渡した")
			}
		})
	}
}

func TestCreateFillsPBAsSourceAndNullsEmptyFields(t *testing.T) {
	f := &fakeQuerier{}
	if err := Create(context.Background(), f, valid()); err != nil {
		t.Fatal(err)
	}
	got := f.created[0]
	if got.SourceName != SourceNamePB || got.SourceID.Valid {
		t.Errorf("通知元 = %q（id の有無 %v）、want PB で id は NULL", got.SourceName, got.SourceID.Valid)
	}
	if got.SubjectKey.Valid || got.ProjectID.Valid {
		t.Error("空の対象のキーかプロジェクトが NULL になっていない")
	}
	if got.ID == "" {
		t.Error("ID を振っていない")
	}
}

func TestWithdrawRejectsEmptyKey(t *testing.T) {
	if _, err := Withdraw(context.Background(), &fakeQuerier{}, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("空のキーの取り下げ err = %v、want ErrInvalid", err)
	}
}

func TestSweepUsesRetention(t *testing.T) {
	f := &fakeQuerier{}
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	if _, err := Sweep(context.Background(), f, now); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-30 * 24 * time.Hour); !f.readBefore.Equal(want) {
		t.Errorf("消す境目 = %v、want %v", f.readBefore, want)
	}
}
