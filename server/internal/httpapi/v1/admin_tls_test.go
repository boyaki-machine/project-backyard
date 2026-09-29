package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
)

// certPEM は SAN を指定した自己署名の証明書を PEM で作る。
func certPEM(t *testing.T, cn string, dns []string, ips []net.IP) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func row(id, pemText string) gen.ListTLSCertificatesRow {
	now := time.Now()
	return gen.ListTLSCertificatesRow{
		ID:        id,
		CertPem:   pemText,
		NotBefore: ts(now.Add(-time.Hour)),
		NotAfter:  ts(now.Add(24 * time.Hour)),
		CreatedAt: ts(now),
	}
}

// TestBuildCertificateListListenHostMatch は 11.4 の突き合わせの結線を見る。
//
// **照合そのものは tlscert の試験で見ている**（host_test.go）。ここで見るのは
// **いま出している1枚を選んで、その PEM を照合へ渡せているか**である。
func TestBuildCertificateListListenHostMatch(t *testing.T) {
	dnsOnly := certPEM(t, "pb.example.com", []string{"pb.example.com"}, nil)
	withIP := certPEM(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})

	cases := []struct {
		name      string
		listenURL string
		rows      []gen.ListTLSCertificatesRow
		wantMatch tlscert.ListenHostMatch
		wantHost  string // 空なら null を期待する
	}{
		{
			name:      "IP の SAN があれば覆っている",
			listenURL: "https://127.0.0.1:8443",
			rows:      []gen.ListTLSCertificatesRow{row("a", withIP)},
			wantMatch: tlscert.MatchCovered,
			wantHost:  "127.0.0.1",
		},
		{
			// **落とし穴2**：DNS の SAN だけでは IP に一致しない。
			name:      "DNS の SAN だけでは IP を覆えない",
			listenURL: "https://127.0.0.1:8443",
			rows:      []gen.ListTLSCertificatesRow{row("a", dnsOnly)},
			wantMatch: tlscert.MatchUncovered,
			wantHost:  "127.0.0.1",
		},
		{
			// **落とし穴1**：0.0.0.0 は接続先のホスト名ではない。
			name:      "全アドレスの待受では突き合わせない",
			listenURL: "https://0.0.0.0:8443",
			rows:      []gen.ListTLSCertificatesRow{row("a", withIP)},
			wantMatch: tlscert.MatchUnspecific,
			wantHost:  "",
		},
		{
			name:      "証明書が無ければ判定しない",
			listenURL: "https://127.0.0.1:8443",
			rows:      nil,
			wantMatch: tlscert.MatchNoCertificate,
			wantHost:  "127.0.0.1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &handler{listenURL: c.listenURL}
			got := h.buildCertificateList(c.rows, nil, tlscert.KeyOrigin("generated"))

			if got.ListenHostMatch != c.wantMatch {
				t.Errorf("listen_host_match = %q, want %q", got.ListenHostMatch, c.wantMatch)
			}
			switch {
			case c.wantHost == "" && got.ListenHost != nil:
				t.Errorf("listen_host = %q, want null", *got.ListenHost)
			case c.wantHost != "" && got.ListenHost == nil:
				t.Errorf("listen_host = null, want %q", c.wantHost)
			case c.wantHost != "" && *got.ListenHost != c.wantHost:
				t.Errorf("listen_host = %q, want %q", *got.ListenHost, c.wantHost)
			}
		})
	}
}

// TestBuildCertificateListDecryptable は 11.4 の decryptable を見る。
//
// **key_id の突き合わせでは検出できない**ので、行ごとに復号を試している。
// **鍵を変えたあとに登録したものと混在しうる**ので、行ごとに出ることを確かめる。
func TestBuildCertificateListDecryptable(t *testing.T) {
	keyA := bytes.Repeat([]byte{0xA1}, tlscert.KeySize)
	keyB := bytes.Repeat([]byte{0xB2}, tlscert.KeySize)

	sealed := func(t *testing.T, id string, key []byte) gen.ListTLSCertificatesRow {
		t.Helper()
		ct, nonce, err := tlscert.Seal(key, "-----BEGIN PRIVATE KEY-----\n…")
		if err != nil {
			t.Fatal(err)
		}
		r := row(id, certPEM(t, "pb.example.com", []string{"pb.example.com"}, nil))
		r.KeyCiphertext, r.KeyNonce = ct, nonce
		return r
	}

	rows := []gen.ListTLSCertificatesRow{
		sealed(t, "a", keyA), // いまの鍵で封をした
		sealed(t, "b", keyB), // 別の鍵で封をした
	}

	h := &handler{listenURL: "https://pb.example.com:8443"}
	got := h.buildCertificateList(rows, keyA, tlscert.KeyOrigin("generated"))

	want := map[string]bool{"a": true, "b": false}
	for _, v := range got.Items {
		if v.Decryptable != want[v.ID] {
			t.Errorf("%s の decryptable = %v, want %v", v.ID, v.Decryptable, want[v.ID])
		}
	}

	// **鍵が無いときは復号できない。** 「確かめていない」を真で返さない。
	none := h.buildCertificateList(rows, nil, tlscert.KeyOrigin(""))
	for _, v := range none.Items {
		if v.Decryptable {
			t.Errorf("鍵が無いのに %s が decryptable になった", v.ID)
		}
	}
}

// TestDownloadTLSCertificateZip は 11.7 の取り出し口が zip を返すことを見る。
//
// **`.crt` をそのまま返すとブラウザが拒む**ので包んでいる。**包んだ中身が元の PEM と
// 一致することまで見る**——形式だけ変えて中身を落としたら、渡した先で使えない。
func TestDownloadTLSCertificateZip(t *testing.T) {
	pemText := certPEM(t, "pb.example.com", []string{"pb.example.com"}, nil)
	h := &handler{q: &fakeQuerier{certPEMRow: gen.GetTLSCertificatePEMRow{
		CommonName: "pb.example.com", CertPem: pemText,
	}}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/tls/certificates/01K2F8QW3H7YRJ4M5N6P7Q8R9S/certificate.zip", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", "01K2F8QW3H7YRJ4M5N6P7Q8R9S")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	h.downloadTLSCertificate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type: got %q", got)
	}
	want := `attachment; filename="pb-cert-pb.example.com.zip"`
	if got := rec.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition:\n got %q\nwant %q", got, want)
	}

	blob := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を開けない: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("中身は1件だけのはず: %d 件", len(zr.File))
	}
	if zr.File[0].Name != "pb.example.com.crt" {
		t.Errorf("中の名前: got %q", zr.File[0].Name)
	}
	f, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pemText {
		t.Errorf("中身が元の PEM と違う:\n got %q\nwant %q", string(got), pemText)
	}
}

// TestDownloadTLSCertificateNotFound は無い ID が 404 になることを見る（11.7）。
func TestDownloadTLSCertificateNotFound(t *testing.T) {
	h := &handler{q: &fakeQuerier{certPEMErr: pgx.ErrNoRows}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/tls/certificates/nosuch/certificate.zip", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", "nosuch")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	h.downloadTLSCertificate(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}
