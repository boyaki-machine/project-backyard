package tlscert

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// ZipName は取り出した証明書を収める zip のファイル名を作る（ApiDesign.md 11.7）。
//
// **FileName と同じ規則で common_name を均す。** 冠を付けるだけの違いなので、
// 置き換えの規則を2か所に持たない。
func ZipName(commonName string) string {
	return "pb-cert-" + strings.TrimSuffix(FileName(commonName), ".crt") + ".zip"
}

// Zip は証明書1枚だけを収めた zip を作る（ApiDesign.md 11.7）。
//
// **`.crt` をそのまま返すとブラウザが「不審なファイル」として拒む**ので包む。
// **PB は 200 を返しているため、拒まれたことは画面にもログにも
// 残らない**——利用者に見えるのは「押しても何も起きない」だけである。
//
// **中身は1枚だけにする。** 手引きの類を入れると、**渡すべきものがどれかを
// 受け取った側が選ぶことになる。**
func Zip(entryName, certPEM string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	e, err := w.Create(entryName)
	if err != nil {
		return nil, fmt.Errorf("zip に %s を作れない: %w", entryName, err)
	}
	if _, err := e.Write([]byte(certPEM)); err != nil {
		return nil, fmt.Errorf("zip へ %s を書けない: %w", entryName, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip を閉じられない: %w", err)
	}
	return buf.Bytes(), nil
}
