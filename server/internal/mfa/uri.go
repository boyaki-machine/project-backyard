// otpauth URI の組み立て（ApiDesign.md 4.6.2）。
//
// **QR にする文字列をサーバが作り、画像はクライアントが描く**（GuiDesign.md 5.8）。
// 画像を返すと Content-Type の分岐とキャッシュ禁止の指定がこの1本のために要るうえ、
// **画像の履歴やキャッシュに共有秘密が残る経路ができる。**
package mfa

import (
	"fmt"
	"net/url"
)

// Issuer は otpauth URI に載せる発行者名。
//
// **インスタンス名の設定がまだ無いので固定である**（Design.md 10.3 の第2層に
// 「インスタンス名」は無い）。**設定が生まれたらそこから引く**——それが
// この定数を消す条件である。
const Issuer = "Project Backyard"

// OtpauthURI は認証アプリが読む URI を組み立てる。
//
//	otpauth://totp/<issuer>:<account>?secret=…&issuer=…&algorithm=SHA1&digits=6&period=30
//
// **ラベルに発行者を含め、クエリにも `issuer` を置く。** 前者だけを見るアプリと
// 後者だけを見るアプリの両方があるためで、Google Authenticator の Key Uri Format が
// 両方書くことを推奨している。
//
// **パラメータを省略しない。** 既定と同じ値でも書くのは、**アプリ側の既定に
// 依存しないため**である（SHA-256 を既定にするアプリがあると壊れる）。
func OtpauthURI(account, secret string) string {
	label := url.PathEscape(Issuer + ":" + account)

	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", Issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(Digits))
	q.Set("period", fmt.Sprint(PeriodSec))

	return "otpauth://totp/" + label + "?" + q.Encode()
}
