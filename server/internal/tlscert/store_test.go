package tlscert

import (
	"testing"
	"time"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// 出す証明書の選定（Design.md 6.6.1）。
//
// **新証明書が有効になったら即切り替える。** 「旧証明書期限で切り替え」の形は、
// 有効な証明書が1枚も無い窓を作りうる——本試験の「期間が連続していない」の節がその状況である。
func TestSelect(t *testing.T) {
	old := Entry{ID: "old", NotBefore: at(2026, 9, 1), NotAfter: at(2026, 12, 1)}
	new_ := Entry{ID: "new", NotBefore: at(2026, 11, 1), NotAfter: at(2027, 3, 1)}

	cases := []struct {
		name      string
		entries   []Entry
		now       time.Time
		wantActiv string
		wantStat  map[string]Status
	}{
		{
			name: "新旧が重なって有効なら新しいほうを出す",
			// **これが本チケットの中心である。** 旧が 12-01 まで有効でも、
			// 新が 11-01 から有効になった時点で新へ移る。
			entries: []Entry{old, new_}, now: at(2026, 11, 15),
			wantActiv: "new",
			wantStat:  map[string]Status{"old": StatusSuperseded, "new": StatusActive},
		},
		{
			name:    "新がまだ有効でないなら旧を出す",
			entries: []Entry{old, new_}, now: at(2026, 10, 1),
			wantActiv: "old",
			wantStat:  map[string]Status{"old": StatusActive, "new": StatusPending},
		},
		{
			name:    "旧が切れたら新を出す",
			entries: []Entry{old, new_}, now: at(2026, 12, 15),
			wantActiv: "new",
			wantStat:  map[string]Status{"old": StatusExpired, "new": StatusActive},
		},
		{
			name:    "登録順に依らない",
			entries: []Entry{new_, old}, now: at(2026, 11, 15),
			wantActiv: "new",
			wantStat:  map[string]Status{"old": StatusSuperseded, "new": StatusActive},
		},
		{
			name:    "1枚だけなら有効な間はそれを出す",
			entries: []Entry{old}, now: at(2026, 10, 1),
			wantActiv: "old",
			wantStat:  map[string]Status{"old": StatusActive},
		},
		{
			name: "**期間が連続していないと有効なものが無い窓ができる**",
			// 旧の期限（12-01）と新の開始（2027-01-01）の間。
			// **「旧証明書期限で切り替え」を採ると、ここで壊れる。**
			entries: []Entry{
				old,
				{ID: "gap", NotBefore: at(2027, 1, 1), NotAfter: at(2027, 6, 1)},
			},
			now:       at(2026, 12, 15),
			wantActiv: "",
			wantStat:  map[string]Status{"old": StatusExpired, "gap": StatusPending},
		},
		{
			name:    "全部期限切れなら出すものが無い",
			entries: []Entry{old}, now: at(2027, 1, 1),
			wantActiv: "",
			wantStat:  map[string]Status{"old": StatusExpired},
		},
		{
			name:    "1枚も無ければ出すものが無い",
			entries: nil, now: at(2026, 10, 1),
			wantActiv: "", wantStat: map[string]Status{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, activeID := Select(tc.entries, tc.now)
			if activeID != tc.wantActiv {
				t.Errorf("active = %q, want %q", activeID, tc.wantActiv)
			}
			if len(got) != len(tc.wantStat) {
				t.Fatalf("件数 = %d, want %d", len(got), len(tc.wantStat))
			}
			for id, want := range tc.wantStat {
				if got[id] != want {
					t.Errorf("%s = %s, want %s", id, got[id], want)
				}
			}
			// **active は必ず1枚以下である**（ApiDesign.md 11.4）。
			n := 0
			for _, s := range got {
				if s == StatusActive {
					n++
				}
			}
			if n > 1 {
				t.Errorf("active が %d 枚ある", n)
			}
		})
	}
}

// 有効な証明書が無いとき、平文へ落とさずハンドシェイクを失敗させる。
func TestHolderRefusesWithoutUsableCertificate(t *testing.T) {
	h := NewHolder()

	// 1枚も無い状態。
	if _, err := h.GetCertificate(nil); err == nil {
		t.Fatal("証明書が無いのにハンドシェイクを通した")
	}

	// 期限切れだけがある状態。
	h.Replace([]Entry{{ID: "old", NotBefore: at(2020, 1, 1), NotAfter: at(2020, 2, 1)}})
	if _, err := h.GetCertificate(nil); err == nil {
		t.Fatal("期限切れだけなのにハンドシェイクを通した")
	}
	if h.Count() != 1 {
		t.Errorf("Count = %d, want 1", h.Count())
	}
}
