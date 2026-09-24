package tlscert

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// selfSignedCommandPath は画面が出す openssl のサンプル（GuiDesign.md 5.12.1）の位置。
// server/internal/tlscert → リポジトリルート。**画面はこのファイルを ?raw で読む**ので、
// ここで走らせるものと利用者がコピーするものは同じである。
const selfSignedCommandPath = "../../../client/src/pages/tls-self-signed.sh"

// 拡張領域の OID（RFC 5280 4.2.1）。critical の有無は x509.Certificate の欄に出ないので、
// Extensions から直接見る。
var (
	oidKeyUsage         = asn1.ObjectIdentifier{2, 5, 29, 15}
	oidBasicConstraints = asn1.ObjectIdentifier{2, 5, 29, 19}
)

// TestSelfSignedCommand は画面の openssl サンプルを実際に走らせ、出来た証明書が
// サーバ証明書としてだけ使える形であることと、PB が登録を受け付けることを確かめる。
//
// **openssl が無い端末では skip する。** 他の試験（makeCert）は openssl を呼ばずに
// 走るので、この1本だけが openssl に依存する。macOS は LibreSSL を標準で持つ。
func TestSelfSignedCommand(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl が無い: ", err)
	}
	src, err := os.ReadFile(selfSignedCommandPath)
	if err != nil {
		t.Fatal(err)
	}

	// 利用者が端末へ貼るのと同じく、シェルに1コマンドとして渡す（行末の \ の継続を含む）。
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", string(src))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("サンプルが失敗した: %v\n%s", err, out)
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, "pb.crt"))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "pb.key"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("pb.crt が PEM ではない")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	// SAN：**期待値はサンプルの本文から読む。** 名前を直したときに試験を直し忘れないため。
	m := regexp.MustCompile(`subjectAltName=([^"]+)"`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("サンプルに subjectAltName が無い")
	}
	var wantDNS []string
	for _, v := range strings.Split(m[1], ",") {
		if name, ok := strings.CutPrefix(v, "DNS:"); ok {
			wantDNS = append(wantDNS, name)
		}
	}
	if len(wantDNS) == 0 || !slices.Equal(cert.DNSNames, wantDNS) {
		t.Errorf("SAN の DNS = %v、サンプルは %v", cert.DNSNames, wantDNS)
	}
	if !slices.Contains(cert.DNSNames, cert.Subject.CommonName) {
		t.Errorf("CN %q が SAN に無い（%v）", cert.Subject.CommonName, cert.DNSNames)
	}

	// Basic Constraints：CA にならない。critical で付ける。
	if !cert.BasicConstraintsValid || cert.IsCA {
		t.Errorf("Basic Constraints: valid=%v isCA=%v、CA:FALSE を明示すること", cert.BasicConstraintsValid, cert.IsCA)
	}
	// Key Usage：署名と鍵の暗号化だけ。critical で付ける。
	if want := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment; cert.KeyUsage != want {
		t.Errorf("Key Usage = %b、want %b", cert.KeyUsage, want)
	}
	for _, oid := range []asn1.ObjectIdentifier{oidBasicConstraints, oidKeyUsage} {
		if !extensionCritical(cert, oid) {
			t.Errorf("拡張 %v が critical でない", oid)
		}
	}
	// Extended Key Usage：サーバ認証だけ。
	if !slices.Equal(cert.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Errorf("Extended Key Usage = %v、want [serverAuth]", cert.ExtKeyUsage)
	}

	// サーバ証明書として検証が通る（自分自身を信頼したとき）。SAN の名前ごとに見る。
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	for _, name := range wantDNS {
		if _, err := cert.Verify(x509.VerifyOptions{
			DNSName:   name,
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Errorf("%s で検証が通らない: %v", name, err)
		}
	}

	// PB が登録を受け付ける（画面の「証明書」と「秘密鍵」の欄に貼ったときと同じ検証）。
	p, err := Parse(string(certPEM), string(keyPEM), time.Now())
	if err != nil {
		t.Fatalf("PB が受け付けない: %v", err)
	}
	if !p.IsSelfSigned {
		t.Error("自己署名として識別されない")
	}
}

func extensionCritical(c *x509.Certificate, oid asn1.ObjectIdentifier) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return e.Critical
		}
	}
	return false
}
