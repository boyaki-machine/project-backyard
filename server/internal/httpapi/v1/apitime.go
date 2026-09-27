package v1

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Time は応答に載せる日時。ApiDesign.md 2.2 の「エポックミリ秒（64bit 整数）」で
// JSON 化する（pb-224。以前は ISO8601 UTC の文字列だった）。
//
// **REST の日時はすべてこの型を通す。** time.Time をそのまま出すと RFC3339 の
// 文字列になり、項目ごとに形が混ざる。表記を1か所に固定する
// （apitime_test.go が、v1 の応答に time.Time を直に置いていないことを見る）。
type Time time.Time

// MarshalJSON はエポックミリ秒の整数を返す。
func (t Time) MarshalJSON() ([]byte, error) {
	return strconv.AppendInt(nil, time.Time(t).UnixMilli(), 10), nil
}

// apiTime は *time.Time を応答用に写す。nil（SQL の NULL）はそのまま nil（JSON の null）。
//
// NOT NULL の列は Time(row.X) で直接書いてよい。本関数が要るのは
// app_user.last_login_at のように「まだ一度も無い」を null で表す列である。
func apiTime(t *time.Time) *Time {
	if t == nil {
		return nil
	}
	v := Time(*t)
	return &v
}

// etagStamp は ETag の材料にする時刻（ナノ秒）。0件のときの番兵（1970-01-01。
// 集計の SQL が COALESCE で返す）とゼロ値は、どちらも 0 になる。
func etagStamp(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

// parseAPIInstant はエポックミリ秒の文字列（問い合わせパラメータ）を読む。
// 空文字は「指定なし」で ok=true・nil を返す。
func parseAPIInstant(s string) (*time.Time, bool) {
	if s == "" {
		return nil, true
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, false
	}
	t := time.UnixMilli(ms).UTC()
	return &t, true
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
