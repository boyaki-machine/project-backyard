package auth

import (
	"strconv"
	"strings"
	"testing"
)

// TestGeneratedPasswordStrength は語彙の数と強度の下限を見る（ApiDesign.md 6.2.1）。
//
// **強度は語彙数から決まる。** 語を足したり語彙を削ったりしたときに、
// **文書に書いた数と実装がずれる**のをここで止める。
func TestGeneratedPasswordStrength(t *testing.T) {
	const want = 64
	if len(genAdjectives) != want {
		t.Errorf("形容詞が %d 語（ApiDesign.md 6.2.1 は %d 語）", len(genAdjectives), want)
	}
	if len(genNouns) != want {
		t.Errorf("名詞が %d 語（ApiDesign.md 6.2.1 は %d 語）", len(genNouns), want)
	}

	// <形容詞>-<名詞>-<4桁>-<名詞> の候補数。**2^31 を下回らないこと。**
	combos := float64(len(genAdjectives)) * float64(len(genNouns)) * 10000 * float64(len(genNouns))
	const min = 1 << 31
	if combos < min {
		t.Errorf("候補が %.0f 通りしかない（2^31 = %d 以上であること）", combos, int64(min))
	}
}

// TestWordListQuality は語彙の選定基準を機械に守らせる（genpassword.go の冒頭）。
//
// **基準は書いてあっても、読んだだけでは守られない。** 語を足す人が
// 踏み外したことをここで気づける。
func TestWordListQuality(t *testing.T) {
	for _, tc := range []struct {
		name  string
		words []string
	}{
		{"形容詞", genAdjectives},
		{"名詞", genNouns},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[string]bool{}
			for _, w := range tc.words {
				if seen[w] {
					t.Errorf("%q が重複している", w)
				}
				seen[w] = true

				// 英小文字のみ。大小・記号の打ち間違いを避ける。
				for _, r := range w {
					if r < 'a' || r > 'z' {
						t.Errorf("%q に英小文字以外が混ざっている", w)
						break
					}
				}
				// 4文字以上。連結して常に MinPasswordLength を超えさせる。
				if len(w) < 4 {
					t.Errorf("%q が4文字未満である", w)
				}
			}

			// **同音・類似の綴りを混ぜない。** 1文字の違いしかない組は、
			// 口頭で伝えたときに取り違える（arbor と harbor など）。
			for i := 0; i < len(tc.words); i++ {
				for j := i + 1; j < len(tc.words); j++ {
					if editDistanceIsOne(tc.words[i], tc.words[j]) {
						t.Errorf("%q と %q は1文字しか違わない", tc.words[i], tc.words[j])
					}
				}
			}
		})
	}
}

// TestGeneratePasswordFormat は形式を見る。**最小長は下の試験で別に見る。**
func TestGeneratePasswordFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		parts := strings.Split(pw, "-")
		if len(parts) != 4 {
			t.Fatalf("%q は <形容詞>-<名詞>-<4桁数字>-<名詞> でない", pw)
		}
		if !contains(genAdjectives, parts[0]) {
			t.Errorf("%q の1語目が形容詞の語彙にない", pw)
		}
		if !contains(genNouns, parts[1]) || !contains(genNouns, parts[3]) {
			t.Errorf("%q の名詞が語彙にない", pw)
		}
		if len(parts[2]) != 4 {
			t.Errorf("%q の数字が4桁でない", pw)
		}
		if _, err := strconv.Atoi(parts[2]); err != nil {
			t.Errorf("%q の3要素目が数字でない", pw)
		}
		// **生成したものが必ず通ること。** 最小長を割ると、作った直後に
		// 弾かれるパスワードを利用者へ渡すことになる。
		if err := ValidatePassword(pw); err != nil {
			t.Errorf("生成した %q が検証を通らない: %v", pw, err)
		}
	}
}

func contains(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}

// editDistanceIsOne は1文字の挿入・削除・置換で一致するかを返す。
func editDistanceIsOne(a, b string) bool {
	if a == b {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > 1 {
		return false
	}
	if len(a) == len(b) {
		diff := 0
		for i := range a {
			if a[i] != b[i] {
				diff++
			}
		}
		return diff == 1
	}
	// 長さが1違う。短いほうを1文字挿入して一致するか。
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return a[i:] == b[i+1:]
		}
	}
	return true
}
