// 設定の解決——5段の優先順を1か所で実装する（Design.md 10.3）。
//
//	<KEY>_FILE ＞ PB_CONFIG_FILE の YAML ＞ 環境変数 <KEY> ＞ app_setting の行 ＞ 既定値
//
// **DB の層はここでは見ない。** 接続文字列が解決するまで DB へ問い合わせられない
// ためで、DB の行は Live.OverlayDatabase が後から重ねる。
package config

import (
	"fmt"
	"sort"
)

// Value は設定1件の実効値と、その出どころ。
type Value struct {
	Def    Definition
	Value  string
	Source Source
}

// Editable は画面から変更できるかを返す。
func (v Value) Editable() bool { return v.Def.Editable(v.Source) }

// Set は全設定の実効値。
type Set struct {
	values map[string]Value
	// ConfigFilePath は読んだ設定ファイルの位置（空なら使っていない）。
	// 画面と起動ログが「どのファイルが効いているか」を言うために持つ。
	ConfigFilePath string
}

// Resolve はファイル・環境変数・既定値から実効値を組み立てる。
//
// **DB の層はまだ重なっていない。** 第2層のキーは、ファイルにも環境変数にも
// 無ければ SourceDefault で返る——OverlayDatabase がそこへ DB の行を重ねる。
func Resolve() (*Set, error) {
	fileValues, path, err := loadConfigFile()
	if err != nil {
		return nil, err
	}

	set := &Set{values: make(map[string]Value, len(definitions)), ConfigFilePath: path}

	// **設定ファイルに未知のキーがあれば起動を失敗させる。**
	// 綴り違いを黙って無視すると、書いたのに効かない状態になる。
	// （DB の行は逆に無視する——古いバイナリへ戻したときに落ちないため。
	// ファイルは人が書いたものなので、誤りはその場で知らせるほうがよい。）
	var unknown []string
	for key := range fileValues {
		if _, ok := Lookup(key); !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s に知らないキーがある: %v（%s）", ConfigFileEnv, unknown, path)
	}

	for _, d := range definitions {
		v, src, err := resolveOne(d, fileValues)
		if err != nil {
			return nil, err
		}
		set.values[d.Key] = Value{Def: d, Value: v, Source: src}
	}
	return set, nil
}

// resolveOne は1件の設定について、優先順の高い層から順に値を探す。
func resolveOne(d Definition, fileValues map[string]string) (string, Source, error) {
	// ① <KEY>_FILE（秘密の1値ファイル）
	if v, ok, err := lookupSecretFile(d.EnvKey); err != nil {
		return "", "", err
	} else if ok {
		v = d.Normalize(v)
		return v, SourceSecretFile, validated(d, v, SourceSecretFile)
	}

	// ② 設定ファイルの YAML
	if v, ok := fileValues[d.Key]; ok {
		v = d.Normalize(v)
		return v, SourceConfigFile, validated(d, v, SourceConfigFile)
	}

	// ③ 環境変数
	if v, ok := lookupEnv(d.EnvKey); ok {
		v = d.Normalize(v)
		return v, SourceEnv, validated(d, v, SourceEnv)
	}

	// ④ DB はここでは見ない（OverlayDatabase が重ねる）

	// ⑤ 既定値
	if d.Required {
		return "", "", fmt.Errorf("%s を設定してください（%s / %s_FILE / %s の %s のいずれか）",
			d.DisplayName, d.EnvKey, d.EnvKey, ConfigFileEnv, d.Key)
	}
	return d.Default, SourceDefault, nil
}

// validated は値を検証し、誤りに出どころを添える。**どこを直せばよいかを
// 誤りが言えなければ、起動失敗は行き止まりになる。**
func validated(d Definition, v string, src Source) error {
	if err := d.Validate(v); err != nil {
		return fmt.Errorf("%s（%s）が正しくない: %w", d.DisplayName, describeSource(d, src), err)
	}
	return nil
}

// describeSource は出どころを日本語で言う。画面と起動ログの両方で使う。
func describeSource(d Definition, src Source) string {
	switch src {
	case SourceSecretFile:
		return d.EnvKey + "_FILE が指すファイル"
	case SourceConfigFile:
		return ConfigFileEnv + " の " + d.Key
	case SourceEnv:
		return "環境変数 " + d.EnvKey
	case SourceDatabase:
		return "画面で設定した値"
	default:
		return "既定値"
	}
}

// Get は1件の実効値を返す。レジストリに無いキーでは第2返り値が false になる。
func (s *Set) Get(key string) (Value, bool) {
	v, ok := s.values[key]
	return v, ok
}

// String は1件の実効値を返す。**レジストリにあるキーしか呼ばれない**ので、
// 無いキーでは空文字を返す（呼び出し側はコード中の定数キーを使う）。
func (s *Set) String(key string) string { return s.values[key].Value }

// Bool は真偽値の設定を返す。検証済みなので "true" との比較で足りる。
func (s *Set) Bool(key string) bool { return s.values[key].Value == "true" }

// All は全件を定義順（層の昇順、層の中は定義順）で返す。
// **API の items[] の順がこれである**（ApiDesign.md 11.1）。
func (s *Set) All() []Value {
	out := make([]Value, 0, len(definitions))
	for _, d := range definitions {
		out = append(out, s.values[d.Key])
	}
	return out
}

// clone は Set の浅い複製を返す。OverlayDatabase が元を壊さないために使う。
func (s *Set) clone() *Set {
	c := &Set{values: make(map[string]Value, len(s.values)), ConfigFilePath: s.ConfigFilePath}
	for k, v := range s.values {
		c.values[k] = v
	}
	return c
}
