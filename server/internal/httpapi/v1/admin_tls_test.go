package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
		NotBefore: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		NotAfter:  pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
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
			// **落とし穴2**：DNS の SAN だけでは IP に一致しない（pb-100）。
			name:      "DNS の SAN だけでは IP を覆えない",
			listenURL: "https://127.0.0.1:8443",
			rows:      []gen.ListTLSCertificatesRow{row("a", dnsOnly)},
			wantMatch: tlscert.MatchUncovered,
			wantHost:  "127.0.0.1",
		},
		{
			// **落とし穴1**：0.0.0.0 は接続先のホスト名ではない（pb-100）。
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
			got := h.buildCertificateList(c.rows, tlscert.KeyOrigin("generated"))

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
