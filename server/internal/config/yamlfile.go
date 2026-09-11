// 設定ファイル（PB_CONFIG_FILE が指す YAML）の読み込み（Design.md 10.3）。
//
// **キーは平らに並べる。** app_setting.key と API の key と同じ名前空間で、
// 入れ子にしない——DB の行・API の項目・レジストリのどれも平らな1つの名前空間
// なので、ファイルだけを入れ子にすると対応表を維持する仕事が生まれる。
//
// **${環境変数名} と書くとその環境変数を引く。** これは秘密をこのファイルに
// 書かないための口である（DbDesign.md 3.2）。ファイルは Git やコンテナ
// イメージに入りうるので、接続文字列やトークンを直接書ける形にしない。
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFileEnv は設定ファイルの位置を渡す環境変数。
//
// **この1件だけは lookup を通さない。** lookup は <KEY>_FILE を見に行くが、
// これ自身がファイルを指す環境変数であり、PB_CONFIG_FILE_FILE は意味を持たない。
const ConfigFileEnv = "PB_CONFIG_FILE"

// interpolation は ${NAME} を拾う。名前は環境変数として妥当な文字だけを許す。
//
// **$$ を先に畳むため、ここでは $ が1つの場合だけを見る。**
var interpolation = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// loadConfigFile は PB_CONFIG_FILE が指す YAML を読み、キーと値の対を返す。
//
// 環境変数が未設定なら nil を返す（ファイルは任意である）。
func loadConfigFile() (map[string]string, string, error) {
	path := os.Getenv(ConfigFileEnv)
	if path == "" {
		return nil, "", nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("%s が指すファイルを読めない（%s）: %w", ConfigFileEnv, path, err)
	}

	// **値は文字列として受ける。** YAML の true / 12 をそのまま Go の型へ落とすと、
	// レジストリが持つ型と二重に解釈することになる。yaml.Node で受けて
	// スカラの文字列表現を取り、検証はレジストリ1か所で行う。
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, path, fmt.Errorf("%s が指すファイルを YAML として読めない（%s）: %w", ConfigFileEnv, path, err)
	}

	out := make(map[string]string, len(doc))
	for key, node := range doc {
		if node.Kind != yaml.ScalarNode {
			// **入れ子を受けない。** 受けると平らな名前空間との対応表が要る。
			return nil, path, fmt.Errorf("%s の %q は入れ子になっている。設定ファイルのキーは平らに並べること（%s）", ConfigFileEnv, key, path)
		}
		v, err := interpolate(node.Value, key, path)
		if err != nil {
			return nil, path, err
		}
		out[key] = v
	}
	return out, path, nil
}

// interpolate は ${NAME} を環境変数の値へ置き換える。
//
// **未設定の環境変数を空文字へ倒さない。** 書いてあるのに効いていない状態を
// 黙って作らないためで、起動を失敗させる（Design.md 10.3）。
func interpolate(raw, key, path string) (string, error) {
	// **$$ を先に退避する。** 補間を1文字も持たないと $ を含む値が書けなくなる。
	const escaped = "\x00PB_DOLLAR\x00"
	s := strings.ReplaceAll(raw, "$$", escaped)

	var missing []string
	s = interpolation.ReplaceAllStringFunc(s, func(m string) string {
		name := interpolation.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s の %q が参照する環境変数 %s が未設定である（%s）",
			ConfigFileEnv, key, strings.Join(missing, " / "), path)
	}

	return strings.ReplaceAll(s, escaped, "$"), nil
}
