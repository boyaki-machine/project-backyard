// トークンの生成とハッシュ化（Design.md 6.2.1 / 6.2.2、ApiDesign.md 2.3）。
//
// 平文トークンは Cookie か Authorization ヘッダにのみ存在し、DB にもログにも
// 残さない。DB が持つのは SHA-256 ハッシュだけで、認証はそのハッシュを
// access_token.token_hash（UNIQUE 索引）で1回引くことで成立する
// （DbDesign.md 6.2）。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// トークンの種別ごとの接頭辞。
//
// 平文に接頭辞を付けるのは、①漏れた文字列を見て何のトークンか判別できる、
// ②GitHub 等のシークレットスキャンに引っかけられる、の2点のため。
// 接頭辞は検索キーではない（検索はハッシュで行う）。
const (
	// SessionTokenPrefix はブラウザのセッション（Design.md 6.2.1 手順6）。
	SessionTokenPrefix = "pb_sess_"
	// APITokenPrefix は CLI・スクリプト用の Bearer トークン（ApiDesign.md 4.5）。
	APITokenPrefix = "pb_api_"
)

// tokenRandomBytes は乱数部のバイト数。Design.md 6.2.1 が定める32バイト。
const tokenRandomBytes = 32

// TokenPrefixLen は access_token.token_prefix に保存する先頭の文字数
// （DbDesign.md 6.2「一覧表示用の先頭8文字」）。
const TokenPrefixLen = 8

// NewToken は平文トークンを生成する。
//
//	<prefix> + base64url(random 32 bytes)
//
// パディングなしの base64url を使うのは、Cookie 値・URL・ヘッダのいずれでも
// エスケープが要らないようにするため。
func NewToken(prefix string) (string, error) {
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("トークンの乱数を生成できない: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken は平文トークンを SHA-256 の小文字16進（64文字）にする。
//
// access_token.token_hash に入れる値であり、**この関数の出力だけが DB に載る**。
// 16進にしているのは、psql で目視・コピーしやすく、照合が単純な文字列比較で
// 済むため。トークン自体が128ビット以上の乱数なので、パスワードのような
// ストレッチ（Argon2id）は不要である。
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// TokenPrefix は一覧表示用の先頭8文字を返す（DbDesign.md 6.2）。
// 8文字に満たない場合はその全体を返す。
func TokenPrefix(plaintext string) string {
	if len(plaintext) <= TokenPrefixLen {
		return plaintext
	}
	return plaintext[:TokenPrefixLen]
}

// EqualTokenHash はトークンハッシュを定数時間で比較する。
//
// 通常の認証経路は UNIQUE 索引による検索なので本関数を通らないが、
// 取得済みの2つのハッシュを突き合わせる場面（現在のセッションかどうかの
// 判定など）で早期リターンによる差を作らないために用意する。
func EqualTokenHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BearerToken は Authorization ヘッダの値から Bearer トークンを取り出す。
// 形式が違えば空文字を返す。スキーム名の大小は区別しない（RFC 7235 §2.1）。
func BearerToken(header string) string {
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(header[len(scheme):])
}
