// Package tlscert は TLS 証明書の解析・秘密鍵の暗号化・出す証明書の選定を扱う
// （Design.md 6.6.1、DbDesign.md 6.15）。
//
// **新しい依存を持たない。** 解析は crypto/x509、暗号化は crypto/aes と
// crypto/cipher、いずれも標準ライブラリである。
package tlscert

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// KeySize は secret_key の長さ。AES-256-GCM の鍵長そのものである。
const KeySize = 32

// CurrentKeyID はいま暗号化に使う鍵の識別子。
//
// **行に持たせるのは、鍵を交換する日にどの行がまだ古い鍵かを引けるようにするため**
// である（Design.md 6.6.1）。交換の手順は pb-3 では作らないので、いまは1つだけ。
const CurrentKeyID = "v1"

// Parsed は登録する証明書から取り出した値。DbDesign.md 6.15 の列に一対一で対応する。
type Parsed struct {
	CommonName   string
	DNSNames     []string
	NotBefore    time.Time
	NotAfter     time.Time
	SerialNumber string
	Fingerprint  string
	IsSelfSigned bool
	CertPEM      string
}

// ErrValidation は利用者の入力の誤り。**そのまま画面に出せる日本語**を持つ
// （規約「命名と形式」）。
type ErrValidation struct{ Msg string }

func (e *ErrValidation) Error() string { return e.Msg }

func invalid(format string, a ...any) error {
	return &ErrValidation{Msg: fmt.Sprintf(format, a...)}
}

// Parse は証明書と秘密鍵の PEM を検証し、列に入れる値を返す。
//
// **証明書と鍵が対応することをここで確かめる。** ハンドシェイクの時刻まで
// 誤りが見つからないと、そのときにはもう画面も API も TLS の向こう側にあり、
// 直す手段が無い（ApiDesign.md 11.5）。
func Parse(certPEM, keyPEM string, now time.Time) (*Parsed, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" {
		return nil, invalid("証明書を入力してください")
	}
	if keyPEM == "" {
		return nil, invalid("秘密鍵を入力してください")
	}

	// **暗号化された秘密鍵は受けない**（ApiDesign.md 11.5）。パスフレーズの
	// 置き場という問いが増え、第1層の鍵が2つになる。
	if strings.Contains(keyPEM, "ENCRYPTED") {
		return nil, invalid("パスフレーズ付きの秘密鍵は登録できません。復号してから貼り付けてください")
	}

	// tls.X509KeyPair が証明書と鍵の対応まで見る。**自分で照合を書かない。**
	pair, err := tls.X509KeyPair([]byte(certPEM+"\n"), []byte(keyPEM+"\n"))
	if err != nil {
		return nil, invalid("証明書と秘密鍵を読めません。対応する組であることと、PEM 形式であることを確かめてください（%s）", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, invalid("証明書が含まれていません")
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, invalid("証明書を解析できません（%s）", err)
	}

	// **期限切れを受けない**（ApiDesign.md 11.5）。受けても active になれず、
	// 利用者は「登録したのに効かない」としか読めない。
	if now.After(leaf.NotAfter) {
		return nil, invalid("この証明書は %s に期限が切れています", leaf.NotAfter.UTC().Format("2006-01-02"))
	}

	sum := sha256.Sum256(leaf.Raw)
	return &Parsed{
		CommonName:   leaf.Subject.CommonName,
		DNSNames:     leaf.DNSNames,
		NotBefore:    leaf.NotBefore.UTC(),
		NotAfter:     leaf.NotAfter.UTC(),
		SerialNumber: leaf.SerialNumber.Text(16),
		Fingerprint:  colonHex(sum[:]),
		IsSelfSigned: isSelfSigned(leaf),
		// **正規化した PEM を保存する。** 貼り付けの前後の空白や改行の違いで
		// 同じ証明書が別物に見えないようにする。指紋は Raw から採るので
		// 一意制約には影響しない。
		CertPEM: encodeChain(pair.Certificate),
	}, nil
}

// isSelfSigned は発行者と主体が一致するかを見る。
//
// **署名の検証までは行わない。** この値は画面に「自己署名」と出すためだけのもので、
// 振る舞いを変えない（DbDesign.md 6.15）。
func isSelfSigned(c *x509.Certificate) bool {
	return c.Issuer.String() == c.Subject.String()
}

// encodeChain は証明書の連鎖を PEM へ書き戻す。中間証明書も保つ。
func encodeChain(chain [][]byte) string {
	var b strings.Builder
	for _, der := range chain {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	return b.String()
}

// colonHex は指紋を ab:cd:ef… の形にする。openssl の出力に揃えてある。
func colonHex(b []byte) string {
	h := hex.EncodeToString(b)
	parts := make([]string, 0, len(b))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// DecodeKey は secret_key（base64 の32バイト）を鍵として読む。
func DecodeKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("秘密の暗号鍵が設定されていません")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("秘密の暗号鍵を base64 として読めない: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("秘密の暗号鍵は %d バイトである必要がある（%d バイトだった）", KeySize, len(key))
	}
	return key, nil
}

// Seal は秘密鍵を AES-256-GCM で暗号化する。nonce は行ごとに新しく作る。
func Seal(key []byte, plaintext string) (ciphertext, nonce []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce を生成できない: %w", err)
	}
	return gcm.Seal(nil, nonce, []byte(plaintext), nil), nonce, nil
}

// Open は暗号化された秘密鍵を復号する。
//
// **GCM は認証付きなので、改竄された行は復号で失敗する。**
func Open(key, ciphertext, nonce []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("nonce の長さが違う（%d バイト）", len(nonce))
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("秘密鍵を復号できない（鍵が違うか、行が壊れている）: %w", err)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("鍵の長さが違う（%d バイト）", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("暗号を初期化できない: %w", err)
	}
	return cipher.NewGCM(block)
}

// ListenHostMatch は待受のホスト名と、いま出す証明書の突き合わせの結果
// （ApiDesign.md 11.4）。**判定はサーバの1か所に置く**——画面が dns_names と
// 照合していたときは IP の SAN が抜け落ちていた（pb-100 で実測）。
type ListenHostMatch string

const (
	// MatchCovered は証明書が待受のホスト名を覆っている。
	MatchCovered ListenHostMatch = "covered"
	// MatchUncovered は覆っていない。**ブラウザが警告を出す状態である。**
	MatchUncovered ListenHostMatch = "uncovered"
	// MatchUnspecific は待受が全アドレスで、突き合わせる相手が決まらない。
	MatchUnspecific ListenHostMatch = "unspecific"
	// MatchNoCertificate はいま出す証明書が無い。
	MatchNoCertificate ListenHostMatch = "no_certificate"
)

// ListenHost は待受の URL から、証明書と突き合わせるホスト名を取り出す。
//
// **0.0.0.0 と :: では空を返す。** あれらは待受の表記であって接続先のホスト名では
// なく、**すべてのアドレスで待ち受けるという意味しか持たない**（ApiDesign.md 11.4）。
// ここで 0.0.0.0 を返すと、利用者は 0.0.0.0 を SAN に入れた証明書を作ってしまう——
// **その証明書はどのクライアントからも一致しない**（pb-100 で実測）。
func ListenHost(listenURL string) string {
	u, err := url.Parse(listenURL)
	if err != nil {
		return ""
	}
	switch h := u.Hostname(); h {
	case "", "0.0.0.0", "::":
		return ""
	default:
		return h
	}
}

// CoversHost は証明書が host を覆っているかを返す。
//
// **照合は crypto/x509 の VerifyHostname に任せる。** ワイルドカードと IP の規則を
// 自分で書くと、標準ライブラリと同じものを2度実装することになる。**IP アドレスは
// IPAddresses の SAN と照合される**ので、DNS:127.0.0.1 では一致しない（pb-100）。
//
// **CN へのフォールバックはしない**（Go 1.15 以降の VerifyHostname がそうである）。
// **現代のブラウザも CN を見ない**ので、SAN の無い証明書は覆っていないと判定する
// （Development.md 14.2）。
func CoversHost(certPEM, host string) bool {
	if host == "" {
		return false
	}
	leaf := parseLeaf(certPEM)
	if leaf == nil {
		return false
	}
	return leaf.VerifyHostname(host) == nil
}

// IPAddresses は証明書の IP の SAN を返す（ApiDesign.md 11.4）。
//
// **DB の列には無い。** `dns_names` は `DNS:` の SAN だけを持つ（DbDesign.md 6.15）。
// **列を足さずに cert_pem から採る**ので、登録済みの行にもそのまま効く。
//
// **CoversHost と出どころが同じである。** 画面に出る根拠と判定が食い違わない
// ——`SAN: localhost` と出しながら `127.0.0.1 を覆っています` と言う状態を作らない。
func IPAddresses(certPEM string) []string {
	leaf := parseLeaf(certPEM)
	if leaf == nil {
		return nil
	}
	out := make([]string, 0, len(leaf.IPAddresses))
	for _, ip := range leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

// parseLeaf は PEM の先頭のブロックを証明書として読む。
//
// **先頭がリーフである**（Parse が encodeChain でその順に書いている）。
func parseLeaf(certPEM string) *x509.Certificate {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return leaf
}

// FileName は証明書を保存するときのファイル名を作る（ApiDesign.md 11.7）。
//
// **common_name は任意の文字列である。** パスの区切りが入ると保存先がずれるので、
// 英数字・ハイフン・ドット・アンダースコア以外は _ に置き換える。
func FileName(commonName string) string {
	var b strings.Builder
	for _, r := range commonName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := strings.Trim(b.String(), "_.")
	if name == "" {
		return "certificate.crt"
	}
	return name + ".crt"
}
