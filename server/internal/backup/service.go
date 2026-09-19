package backup

import (
	"context"
	"io"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service は書き出しと取り込みをまとめた口（httpapi/v1.Backups の実装）。
//
// **書き出しは pb_app のプールで行い、取り込みは呼び出しごとに受け取った
// pb_owner の資格情報で繋ぐ**（DbDesign.md 3.4）。**PB はオーナーの資格情報を
// 持ち続けない。**
type Service struct {
	pool     *pgxpool.Pool
	dumper   *Dumper
	restorer *Restorer
	// appURL は PB がいま繋いでいる接続文字列。**接続先だけを使い、
	// ロールとパスワードは取り込みのたびに差し替える。**
	appURL string
	// sessionTable と sessionCol は控えるセッション行の在処。
	sessionTable, sessionCol string
}

// NewService は pool から読み書きする Service を返す。
//
// appURL は PB_DATABASE_URL（pb_app）。**接続先を取り出すためだけに使う。**
func NewService(pool *pgxpool.Pool, appURL, pbVersion string) *Service {
	return &Service{
		pool:         pool,
		dumper:       NewDumper(pool, pbVersion),
		restorer:     NewRestorer(),
		appURL:       appURL,
		sessionTable: "access_token",
		sessionCol:   "id",
	}
}

// Dump は書庫を w へ流す（ApiDesign.md 11.11）。
func (s *Service) Dump(ctx context.Context, w io.Writer) error {
	return s.dumper.Dump(ctx, w)
}

// CaptureSession は操作者のセッション行を控える（11.12）。
func (s *Service) CaptureSession(ctx context.Context, tokenID string) (*KeptRow, error) {
	if tokenID == "" {
		return nil, nil
	}
	return CaptureRow(ctx, s.pool, s.sessionTable, s.sessionCol, tokenID)
}

// Restore は書庫を取り込む（11.12）。
func (s *Service) Restore(ctx context.Context, ownerURL string, src io.Reader, keep *KeptRow) (Result, error) {
	return s.restorer.Restore(ctx, ownerURL, src, keep)
}

// OwnerURL は、いま繋いでいる接続先のロールとパスワードだけを差し替える（11.12）。
//
// **接続先を画面から渡させない。** 渡させると、PB の DB ではないところを
// 戻す経路ができる。
func (s *Service) OwnerURL(user, password string) string {
	u, err := url.Parse(s.appURL)
	if err != nil {
		return s.appURL
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
