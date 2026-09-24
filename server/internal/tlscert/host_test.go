package tlscert

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
)

// makeCertSAN は SAN を指定して自己署名の証明書を作る。
//
// **makeCert は DNSNames を CN と localhost に固定している**ので、IP の SAN と
// 「SAN が1つも無い」を試せない。落とし穴の再現（DNS:127.0.0.1 は IP で
// 一致しない）にはこちらを使う。
func makeCertSAN(t *testing.T, cn string, dns []string, ips []net.IP) string {
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
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestListenHost(t *testing.T) {
	cases := []struct {
		name      string
		listenURL string
		want      string
	}{
		// **0.0.0.0 は待受の表記であって接続先のホスト名ではない**。
		{"全アドレス（IPv4）", "https://0.0.0.0:8443", ""},
		{"全アドレス（平文）", "http://0.0.0.0:8080", ""},
		{"全アドレス（IPv6）", "https://[::]:8443", ""},
		{"ループバック", "https://127.0.0.1:8443", "127.0.0.1"},
		{"ホスト名", "https://pb.example.com:8443", "pb.example.com"},
		{"ポートなし", "https://pb.example.com", "pb.example.com"},
		{"空", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ListenHost(c.listenURL); got != c.want {
				t.Errorf("ListenHost(%q) = %q, want %q", c.listenURL, got, c.want)
			}
		})
	}
}

func TestCoversHost(t *testing.T) {
	dnsOnly := makeCertSAN(t, "pb.example.com", []string{"pb.example.com"}, nil)
	wildcard := makeCertSAN(t, "example.com", []string{"*.example.com"}, nil)
	withIP := makeCertSAN(t, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	// **落とし穴2の再現**：IP を DNS: として入れた証明書。
	ipAsDNS := makeCertSAN(t, "127.0.0.1", []string{"127.0.0.1"}, nil)
	// **SAN が1つも無い証明書。** CN だけでは現代のブラウザが受けない。
	cnOnly := makeCertSAN(t, "pb.example.com", nil, nil)

	cases := []struct {
		name    string
		certPEM string
		host    string
		want    bool
	}{
		{"DNS が一致する", dnsOnly, "pb.example.com", true},
		{"DNS が一致しない", dnsOnly, "other.example.com", false},
		{"ワイルドカードが1段に一致する", wildcard, "a.example.com", true},
		{"ワイルドカードは2段に一致しない", wildcard, "a.b.example.com", false},
		{"IP の SAN が一致する", withIP, "127.0.0.1", true},
		{"IP の SAN があってもホスト名は別", withIP, "localhost", true},
		{"IP を DNS: で入れても一致しない", ipAsDNS, "127.0.0.1", false},
		{"SAN が無ければ CN では一致しない", cnOnly, "pb.example.com", false},
		{"ホスト名が空なら一致しない", dnsOnly, "", false},
		{"PEM として読めなければ一致しない", "not a pem", "pb.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CoversHost(c.certPEM, c.host); got != c.want {
				t.Errorf("CoversHost(_, %q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestIPAddresses(t *testing.T) {
	withIP := makeCertSAN(t, "localhost", []string{"localhost"},
		[]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	dnsOnly := makeCertSAN(t, "pb.example.com", []string{"pb.example.com"}, nil)

	got := IPAddresses(withIP)
	want := []string{"127.0.0.1", "::1"}
	if len(got) != len(want) {
		t.Fatalf("IPAddresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("IPAddresses[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(IPAddresses(dnsOnly)) != 0 {
		t.Errorf("IP の SAN が無ければ空である: %v", IPAddresses(dnsOnly))
	}
	if len(IPAddresses("not a pem")) != 0 {
		t.Errorf("読めない PEM では空である")
	}
}

func TestFileName(t *testing.T) {
	cases := []struct {
		commonName string
		want       string
	}{
		{"pb.example.com", "pb.example.com.crt"},
		// **先頭の記号は落とす。** 先頭が . だと隠しファイルになる。
		{"*.example.com", "example.com.crt"},
		// **パスの区切りが入ると保存先がずれる**（ApiDesign.md 11.7）。
		{"../../etc/passwd", "etc_passwd.crt"},
		{"日本語の名前", "certificate.crt"},
		{"", "certificate.crt"},
		{"***", "certificate.crt"},
	}
	for _, c := range cases {
		t.Run(c.commonName, func(t *testing.T) {
			if got := FileName(c.commonName); got != c.want {
				t.Errorf("FileName(%q) = %q, want %q", c.commonName, got, c.want)
			}
		})
	}
}
