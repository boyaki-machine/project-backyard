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
