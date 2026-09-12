package tlscert

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// TestZipName は zip の名前が FileName と同じ規則で均されることを見る（11.7）。
func TestZipName(t *testing.T) {
	for _, c := range []struct {
		commonName string
		want       string
	}{
		{"pb.example.com", "pb-cert-pb.example.com.zip"},
		// **パスの区切りは _ になる**（FileName と同じ規則）。
		{"pb/../etc", "pb-cert-pb_.._etc.zip"},
		// **均した結果が空なら certificate になる。**
		{"...", "pb-cert-certificate.zip"},
	} {
		if got := ZipName(c.commonName); got != c.want {
			t.Errorf("ZipName(%q) = %q, want %q", c.commonName, got, c.want)
		}
	}
}

// TestZip は包んだ中身が元の PEM のままであることを見る（11.7）。
func TestZip(t *testing.T) {
	const body = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	blob, err := Zip("pb.example.com.crt", body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を開けない: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("中身は1件だけのはず: %d 件", len(zr.File))
	}
	if zr.File[0].Name != "pb.example.com.crt" {
		t.Errorf("名前: got %q", zr.File[0].Name)
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
	if string(got) != body {
		t.Errorf("中身: got %q, want %q", string(got), body)
	}
}
