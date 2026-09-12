// 秘密の暗号鍵の読み書きを tlscert.SecretStore に適合させる（DbDesign.md 6.16）。
package v1

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// SecretStore は gen.Querier を tlscert.SecretStore として使う薄い包み。
//
// **tlscert が store/gen に依存しないための境界である。** あちらは鍵の
// 生成と検証だけを知っていればよい。
type SecretStore struct{ Q gen.Querier }

// GetSecret は鍵を引く。**無いときは空を返し、誤りにしない**——
// 初回は行が無いのが正常である。
func (s SecretStore) GetSecret(ctx context.Context, keyID string) ([]byte, error) {
	row, err := s.Q.GetAppSecret(ctx, keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.Secret, nil
}

// PutSecretIfAbsent は鍵を作る。既にあれば何もしない。
func (s SecretStore) PutSecretIfAbsent(ctx context.Context, keyID string, secret []byte) error {
	return s.Q.CreateAppSecretIfAbsent(ctx, gen.CreateAppSecretIfAbsentParams{
		KeyID: keyID, Secret: secret,
	})
}
