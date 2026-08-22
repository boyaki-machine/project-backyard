package v1

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Time は応答に載せる日時。ApiDesign.md 2.2 の「ISO8601 UTC
// （2026-08-11T09:03:12Z）」に揃えて JSON 化する。
//
// time.Time をそのまま出すと、格納されているマイクロ秒とDBのタイムゾーンが
// そのまま現れ、応答ごとに桁数の違う文字列になる。表記を1か所に固定する。
type Time time.Time

// MarshalJSON は UTC・秒精度の RFC3339 文字列を返す。
func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format(time.RFC3339))
}

// apiTime は *time.Time を応答用に写す。nil はそのまま nil（JSON の null）。
func apiTime(t *time.Time) *Time {
	if t == nil {
		return nil
	}
	v := Time(*t)
	return &v
}

// apiTimestamptz は NULL 可能な timestamptz 列を応答用に写す。
// 無効（SQL の NULL）は nil を返し、JSON では null になる。
//
// NOT NULL の列は Time(row.X.Time) で直接書いてよい。本関数が要るのは
// app_user.last_login_at のように「まだ一度も無い」を null で表す列である。
func apiTimestamptz(t pgtype.Timestamptz) *Time {
	if !t.Valid {
		return nil
	}
	v := Time(t.Time)
	return &v
}

// Date は応答に載せる日付（`date` 列）。ApiDesign.md 9.12 / 9.3 の例が
// "2026-08-05" の形であり、時刻とタイムゾーンを持たない。
//
// **Time と分けてある。** date 列を timestamptz として扱うと、UTC へ寄せる
// 過程で前日へずれることがある（Asia/Tokyo の 00:00 は UTC の前日15:00）。
// 期限は「その日」であって「その瞬間」ではない。
type Date time.Time

// MarshalJSON は YYYY-MM-DD を返す。
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(d).Format(time.DateOnly))
}

// apiDate は NULL 可能な date 列を応答用に写す。無効（SQL の NULL）は nil。
func apiDate(d pgtype.Date) *Date {
	if !d.Valid {
		return nil
	}
	v := Date(d.Time)
	return &v
}

// parseAPIDate は "YYYY-MM-DD" を date 列へ写す。空文字は NULL 扱い。
//
// **time.DateOnly で厳密に読む。** time.RFC3339 を許すと "2026-08-05T00:00:00Z"
// が通り、日付として送るべき値に時刻が混ざる経路ができる。
func parseAPIDate(s string) (pgtype.Date, bool) {
	if s == "" {
		return pgtype.Date{}, true
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}, false
	}
	return pgtype.Date{Time: t, Valid: true}, true
}
