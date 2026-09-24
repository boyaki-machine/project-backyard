// 秘密の暗号鍵の解決（Design.md 6.6.1、DbDesign.md 6.16）。
//
// **既定では PB が初回に鍵を作って DB に保存する。** 利用者の操作は要らず、
// 画面だけで証明書を登録できる。`PB_SECRET_KEY` を与えたときはそちらを使う。
//
// **環境変数を必須にしない。** 必須にすると、証明書を1枚登録するために環境変数の
// 設定と再起動を要求することになり、「設定は WebGUI を第一の口とする」という方針
// （憲章「判断の記録」）と矛盾する。
package tlscert

import (
	"context"
	"crypto/rand"
	"fmt"
)

// GeneratedKeyID は PB が生成した鍵の識別子。
//
// **鍵を交換する日に2つ目が要る**（古い鍵で暗号化された行が残っているあいだ）。
// いまはこの1つだけを使う。
const GeneratedKeyID = "generated"

// KeyOrigin は鍵がどこから来たか。**画面に代償を出すために要る**
// （生成した鍵は DB にあるので、pg_dump に鍵と暗号文の両方が入る）。
type KeyOrigin string

const (
	// OriginEnv は PB_SECRET_KEY で与えられた鍵。
	OriginEnv KeyOrigin = "env"
	// OriginGenerated は PB が生成して DB に保存した鍵。
	OriginGenerated KeyOrigin = "generated"
)

// SecretStore は鍵の読み書き。store 層に依存しないための最小の口である。
type SecretStore interface {
	GetSecret(ctx context.Context, keyID string) ([]byte, error)
	PutSecretIfAbsent(ctx context.Context, keyID string, secret []byte) error
}

// ResolveKey は使う鍵とその出どころを返す。
//
//	PB_SECRET_KEY があればそれ ＞ DB の行 ＞ 生成して DB へ保存
//
// **環境変数が勝つ。** 与えた側が強いという優先順（Design.md 10.3）と揃える。
//
// **生成は競合しても安全である。** 複数のレプリカが同時に起動しても、
// 書き込みは「既にあれば何もしない」で、**書いたあと必ず読み直す**ので
// 全台が同じ鍵を使う。
func ResolveKey(ctx context.Context, store SecretStore, envValue string) ([]byte, KeyOrigin, error) {
	if envValue != "" {
		key, err := DecodeKey(envValue)
		if err != nil {
			return nil, "", err
		}
		return key, OriginEnv, nil
	}

	if key, err := store.GetSecret(ctx, GeneratedKeyID); err != nil {
		return nil, "", fmt.Errorf("秘密の暗号鍵を引けない: %w", err)
	} else if len(key) == KeySize {
		return key, OriginGenerated, nil
	} else if len(key) != 0 {
		// 行はあるが長さが違う。**黙って作り直さない**——作り直すと
		// 既存の証明書が復号できなくなるので、気づかせる。
		return nil, "", fmt.Errorf("保存されている秘密の暗号鍵の長さが違う（%d バイト）。"+
			"PB_SECRET_KEY を与えるか、証明書を登録し直すこと", len(key))
	}

	// 初回。作って保存し、**必ず読み直す**（他のレプリカが先に入れている可能性がある）。
	fresh := make([]byte, KeySize)
	if _, err := rand.Read(fresh); err != nil {
		return nil, "", fmt.Errorf("秘密の暗号鍵を生成できない: %w", err)
	}
	if err := store.PutSecretIfAbsent(ctx, GeneratedKeyID, fresh); err != nil {
		return nil, "", fmt.Errorf("秘密の暗号鍵を保存できない: %w", err)
	}
	key, err := store.GetSecret(ctx, GeneratedKeyID)
	if err != nil {
		return nil, "", fmt.Errorf("保存した秘密の暗号鍵を読み直せない: %w", err)
	}
	if len(key) != KeySize {
		return nil, "", fmt.Errorf("保存した秘密の暗号鍵を読み直せない（%d バイト）", len(key))
	}
	return key, OriginGenerated, nil
}
