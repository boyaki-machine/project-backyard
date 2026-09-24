package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// makeCert は試験用の証明書と鍵を PEM で作る。
//
// **openssl を呼ばない。** 端末に openssl が無くても走るようにするためで、
// 実物の openssl 出力に対する検証は TestSelfSignedCommand（画面のサンプルを走らせる）と
// 実サーバで行う。
//
// **issuerCN を渡すと、その名前の CA を作って署名する。** `Issuer` を
// テンプレートに書くだけでは効かない——`x509.CreateCertificate` は親の
// `Subject` から `Issuer` を埋めるので、親に自分を渡すと必ず自己署名になる。
func makeCert(t *testing.T, cn string, notBefore, notAfter time.Time, issuerCN string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{cn, "localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	parent, signer := tmpl, any(key)
	if issuerCN != "" {
		caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		caTmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(time.Now().UnixNano() + 1),
			Subject:               pkix.Name{CommonName: issuerCN},
			NotBefore:             notBefore,
			NotAfter:              notAfter,
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		ca, err := x509.ParseCertificate(caDER)
		if err != nil {
			t.Fatal(err)
		}
		parent, signer = ca, caKey
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func TestParse(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM := makeCert(t, "pb.example.com", now.Add(-time.Hour), now.Add(24*time.Hour), "")

	got, err := Parse(certPEM, keyPEM, now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.CommonName != "pb.example.com" {
		t.Errorf("CommonName = %q", got.CommonName)
	}
	if len(got.DNSNames) != 2 {
		t.Errorf("DNSNames = %v", got.DNSNames)
	}
	// **発行者と主体が一致するので自己署名である**（DbDesign.md 6.15）。
	if !got.IsSelfSigned {
		t.Error("自己署名と判定されなかった")
	}
	if !strings.HasPrefix(got.Fingerprint, "") || len(got.Fingerprint) != 95 {
		t.Errorf("指紋の形が違う（%d 文字）: %q", len(got.Fingerprint), got.Fingerprint)
	}
	if !strings.Contains(got.CertPEM, "BEGIN CERTIFICATE") {
		t.Error("CertPEM が PEM でない")
	}
}

// 発行者が違えば自己署名としない。
func TestParseNotSelfSigned(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM := makeCert(t, "pb.example.com", now.Add(-time.Hour), now.Add(24*time.Hour), "Example CA")
	got, err := Parse(certPEM, keyPEM, now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.IsSelfSigned {
		t.Error("発行者が違うのに自己署名と判定した")
	}
}

// 落ちる側（ApiDesign.md 11.5）。
func TestParseRejects(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM := makeCert(t, "pb.example.com", now.Add(-time.Hour), now.Add(24*time.Hour), "")
	otherCert, otherKey := makeCert(t, "other.example.com", now.Add(-time.Hour), now.Add(24*time.Hour), "")
	_ = otherCert
	expiredCert, expiredKey := makeCert(t, "old.example.com", now.Add(-48*time.Hour), now.Add(-time.Hour), "")

	cases := []struct {
		name string
		cert string
		key  string
		want string
	}{
		{"証明書が空", "", keyPEM, "証明書を入力してください"},
		{"秘密鍵が空", certPEM, "", "秘密鍵を入力してください"},
		{"PEM でない", "not a pem", keyPEM, "読めません"},
		// **鍵が対応しない組を弾く。** ハンドシェイクの時刻まで誤りが
		// 見つからないと、直す手段が無い。
		{"鍵が対応しない", certPEM, otherKey, "読めません"},
		{"期限切れ", expiredCert, expiredKey, "期限が切れています"},
		{"パスフレーズ付き", certPEM,
			"-----BEGIN ENCRYPTED PRIVATE KEY-----\nx\n-----END ENCRYPTED PRIVATE KEY-----",
			"パスフレーズ付き"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.cert, tc.key, now)
			if err == nil {
				t.Fatal("誤りにならなかった")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("誤りの文面に %q が無い: %v", tc.want, err)
			}
			// **そのまま画面に出せる日本語であること**（規約「命名と形式」）。
			var v *ErrValidation
			if !asValidation(err, &v) {
				t.Errorf("ErrValidation でない: %T", err)
			}
		})
	}
}

func asValidation(err error, target **ErrValidation) bool {
	if v, ok := err.(*ErrValidation); ok {
		*target = v
		return true
	}
	return false
}

// 鍵の読み込みと暗号化の往復。
func TestSealOpen(t *testing.T) {
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(key)

	got, err := DecodeKey(encoded)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if len(got) != KeySize {
		t.Fatalf("鍵長 = %d", len(got))
	}

	const secret = "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----"
	ct, nonce, err := Seal(got, secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(string(ct), "PRIVATE KEY") {
		t.Error("暗号文に平文が残っている")
	}

	plain, err := Open(got, ct, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plain != secret {
		t.Errorf("往復で値が変わった: %q", plain)
	}

	t.Run("違う鍵では復号できない", func(t *testing.T) {
		other := make([]byte, KeySize)
		if _, err := Open(other, ct, nonce); err == nil {
			t.Fatal("違う鍵で復号できた")
		}
	})

	t.Run("改竄された暗号文は復号で失敗する", func(t *testing.T) {
		// **GCM は認証付きなので、1ビット変えれば落ちる。**
		bad := make([]byte, len(ct))
		copy(bad, ct)
		bad[0] ^= 0xff
		if _, err := Open(got, bad, nonce); err == nil {
			t.Fatal("改竄された暗号文が通った")
		}
	})

	t.Run("nonce は行ごとに違う", func(t *testing.T) {
		_, n2, err := Seal(got, secret)
		if err != nil {
			t.Fatal(err)
		}
		if string(nonce) == string(n2) {
			t.Error("nonce が再利用されている")
		}
	})
}

func TestDecodeKeyRejects(t *testing.T) {
	cases := map[string]string{
		"空":          "",
		"base64 でない": "not base64 !!!",
		"32バイトでない":   base64.StdEncoding.EncodeToString([]byte("short")),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeKey(in); err == nil {
				t.Fatal("誤りにならなかった")
			}
		})
	}
}
