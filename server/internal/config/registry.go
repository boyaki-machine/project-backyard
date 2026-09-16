// 設定レジストリ——PB の設定の正本（Design.md 10.3、ApiDesign.md 11章）。
//
// **キー・型・既定値・検証規則をここ1か所だけが持つ。** DB の app_setting は
// 値だけを持ち（DbDesign.md 6.14）、型の列を置かない。2か所に持つと、
// 'integer' と書かれた行に 'true' が入ったときにどちらが正しいかを
// 決める根拠が無くなる。
//
// **設定を1件足すときに触るのはこのファイルだけである。** マイグレーションは
// 要らない（app_setting はキーと値の表で、許可リストを CHECK に書いていない）。
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Layer は設定の層（Design.md 10.3）。
//
// 層を決めるのは「その値がいつ必要か」と「複数のレプリカが一致していなければ
// ならないか」の2点である。
type Layer int

const (
	// LayerBoot は起動前に要る設定。DB に到れないのでファイルと環境変数にしか置けない。
	// **接続文字列は DB の中にあり得ない**——これは選択ではなく循環である。
	LayerBoot Layer = 1
	// LayerRuntime は実行時の共有設定。DB の app_setting に置き、画面から変えられる。
	LayerRuntime Layer = 2
	// LayerSharedSecret は共有される秘密（TLS 証明書と秘密鍵）。**未実装**。pb-3 の範囲。
	LayerSharedSecret Layer = 3
)

// ValueType は値の型。**API は値を常に文字列で返し、この型と対で読ませる**
// （ApiDesign.md 11.1）。真偽値を JSON の true にすると value の型が設定ごとに
// 変わり、生成した型が共用体になる。
type ValueType string

const (
	TypeString ValueType = "string"
	TypeBool   ValueType = "bool"
	TypeEnum   ValueType = "enum"
)

// Source は実効値の出どころ。**優先順の高い順に並べてある**（Design.md 10.3）。
type Source string

const (
	// SourceSecretFile は <KEY>_FILE が指すファイル。compose の secrets: と
	// K8s の Secret ボリュームがこの形で配る。**設定ファイルより強い**——
	// プラットフォームが配る秘密が、リポジトリに入りうるファイルに
	// 上書きされてはならない。
	SourceSecretFile Source = "secret_file"
	// SourceConfigFile は PB_CONFIG_FILE が指す YAML。運用者の明示的な宣言なので
	// 環境変数より強い。引きたいときは ${環境変数名} で引ける。
	SourceConfigFile Source = "config_file"
	// SourceEnv は環境変数 <KEY>。ファイルを編集せずに1件だけ差し替える口。
	SourceEnv Source = "env"
	// SourceDatabase は app_setting の行。画面から変えたもの。
	SourceDatabase Source = "database"
	// SourceDefault はレジストリが持つ既定値。
	SourceDefault Source = "default"
)

// Definition は設定1件の定義。
type Definition struct {
	// Key は app_setting.key と API の key。**PB_ 接頭辞を付けない平らな名前**で、
	// pb.yaml もこの名前を使う。
	Key string
	// EnvKey は環境変数の名前。**Source が何であっても API は必ず返す**——
	// いま環境変数で与えられていない設定にも、上書きする道を画面が示せるように。
	EnvKey string

	Layer Layer
	Type  ValueType
	// Allowed は Type が TypeEnum のときの値域。
	Allowed []string

	// Default は既定値。Required が真のときは空にする。
	Default string
	// Required が真なら、どの層からも値が来なかったときに起動を失敗させる。
	Required bool

	// Secret が真なら API は value を返さない（ApiDesign.md 11.1）。
	Secret bool
	// RestartRequired が真なら、変更が効くまでに再起動が要る。
	RestartRequired bool
	// NeedsConfirm が真なら、**変えたあと確認しないと元へ戻す**（pb-97、Design.md 10.3）。
	//
	// **判定の基準は「その変更で、その画面へ戻れなくなるか」だけである。**
	// 重要かどうかで決めない——重要さは人によって違う。
	NeedsConfirm bool

	// DisplayName と Description は画面にそのまま出す日本語（GuiDesign.md 5.12）。
	DisplayName string
	Description string
}

// Editable は画面から変更できるかを返す（ApiDesign.md 11.1）。
//
// **第2層で、かつ実効値が DB か既定値のときだけ真である。** ファイルや環境変数で
// 与えられている項目は「固定」として表示し、編集させない——優先順により、
// 書いても効かないためである。
func (d Definition) Editable(src Source) bool {
	return d.Layer == LayerRuntime && (src == SourceDatabase || src == SourceDefault)
}

// Normalize は検証の前に値を整える。
//
// **列挙は小文字へ倒す。** 旧実装が PB_LOG_LEVEL を ToLower していたため、
// DEBUG と書いてある環境を落とさないために引き継ぐ。log_format も同じ扱いに
// するのは、片方だけ大文字を受けるほうが説明できないからである。
func (d Definition) Normalize(value string) string {
	switch d.Type {
	case TypeEnum:
		return strings.ToLower(strings.TrimSpace(value))
	case TypeBool:
		// **旧実装は strconv.ParseBool だったので 1 / 0 / TRUE も受けていた。**
		// 受けたうえで true / false へ正規化する——**DB と API に入るのは
		// 正規形だけ**にしたいためで、そうしないと Set.Bool の比較が
		// 値の書き方に依存する。
		if b, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return strconv.FormatBool(b)
		}
		return value // 受けられない値は Validate が言う
	}
	return value
}

// Validate は値が定義に合うかを見る。合わないときの誤りは**そのまま画面に出せる
// 日本語**にする（規約「命名と形式」）。
func (d Definition) Validate(value string) error {
	switch d.Type {
	case TypeBool:
		if value != "true" && value != "false" {
			return fmt.Errorf("true / false で指定してください")
		}
	case TypeEnum:
		for _, a := range d.Allowed {
			if value == a {
				return nil
			}
		}
		return fmt.Errorf("%s のいずれかを指定してください", joinJa(d.Allowed))
	case TypeString:
		if value == "" {
			return fmt.Errorf("空にできません")
		}
	}
	return nil
}

// definitions は設定の全件。**並び順がそのまま API の items[] の順になる**
// （層の昇順、層の中は定義順）。
var definitions = []Definition{
	{
		Key: "database_url", EnvKey: "PB_DATABASE_URL",
		Layer: LayerBoot, Type: TypeString,
		Required: true, Secret: true, RestartRequired: true,
		DisplayName: "DB接続文字列",
		Description: "pb_app での接続文字列。起動時に接続プールを張るため、変更には再起動が要ります",
	},
	{
		Key: "bind", EnvKey: "PB_BIND",
		// **第2層へ移した**（pb-99）。**待受は「起動の順序をそう決めている」だけ**
		// であり、接続文字列のように原理的に DB へ置けないものではない——
		// サーバは DB へ繋いだあとに待受を張っている。
		//
		// **再起動は要らない**（pb-106 で張り替えられるようにした）。
		// **確認しないと元へ戻す**（pb-97）——ポートを誤ると画面へ到達できない。
		//
		// **既定はこの端末からだけ届く形にする**（`Requirements.md` 10.10.2
		// 「既定では 127.0.0.1 にのみバインドする」。pb-125）。**何も設定しないまま
		// 起動したときに、平文ですべてのアドレスへ出さない**ためである。
		// **コンテナは自分で `PB_BIND=0.0.0.0:8080` を明示する**（`deploy/Dockerfile`
		// の `ENV` と `deploy/base/compose.yaml`）——中で 127.0.0.1 に閉じると、
		// 公開範囲を決めるはずの `ports` を通っても外から届かない。
		Layer: LayerRuntime, Type: TypeString,
		Default: "127.0.0.1:8080", NeedsConfirm: true,
		DisplayName: "待受アドレス",
		Description: "HTTP を待ち受けるアドレスとポート。切り替えは即時で、期限内に確認しないと元へ戻ります。コンテナで動かしている場合は、公開側の設定（compose の ports や Service の targetPort）も合わせて変えてください",
	},
	{
		Key: "secret_key", EnvKey: "PB_SECRET_KEY",
		Layer: LayerBoot, Type: TypeString,
		Secret: true, RestartRequired: true,
		DisplayName: "秘密の暗号鍵",
		Description: "TLS の秘密鍵を暗号化するための鍵。32バイトを base64 で与えます。証明書を登録しないなら不要です",
	},
	{
		Key: "tls_enabled", EnvKey: "PB_TLS_ENABLED",
		Layer: LayerRuntime, Type: TypeBool,
		// **再起動は要らない**（pb-106 で待受を張り替えられるようにした）。
		// **確認しないと元へ戻す**（pb-97）——http で入っていた人が https へ
		// 移れないと締め出される。
		Default: "false", NeedsConfirm: true,
		DisplayName: "TLS で待ち受ける",
		Description: "有効にすると HTTPS で待ち受けます。証明書の登録が別途必要です。切り替えは即時で、期限内に確認しないと元へ戻ります",
	},
	{
		Key: "log_format", EnvKey: "PB_LOG_FORMAT",
		Layer: LayerRuntime, Type: TypeEnum, Allowed: []string{"json", "text"},
		Default:     "json",
		DisplayName: "ログ形式",
		Description: "標準出力へ書くログの形式。text は開発時に人が読むためのものです",
	},
	{
		Key: "log_level", EnvKey: "PB_LOG_LEVEL",
		Layer: LayerRuntime, Type: TypeEnum, Allowed: []string{"debug", "info", "warn", "error"},
		Default:     "info",
		DisplayName: "ログレベル",
		Description: "記録するログの最低レベル。debug では /healthcheck のアクセスログも出ます",
	},
	{
		Key: "health_show_version", EnvKey: "PB_HEALTH_SHOW_VERSION",
		Layer: LayerRuntime, Type: TypeBool,
		Default:     "false",
		DisplayName: "ヘルスチェックにバージョンを含める",
		Description: "GET /healthcheck の応答にバージョンを入れます。未認証の呼び出し元への情報開示になるため既定は無効です",
	},
	{
		Key: "cookie_secure", EnvKey: "PB_COOKIE_SECURE",
		Layer: LayerRuntime, Type: TypeBool,
		// **確認しないと元へ戻す**（pb-97）——http で有効にすると Cookie が
		// 送られず、**ログインが黙って失敗する。**
		Default: "false", NeedsConfirm: true,
		DisplayName: "Cookie に Secure を付ける",
		// **結果（ログインできなくなる）は書かない。** 画面が ⚠ の1行で出すので
		// （GuiDesign.md 5.12）、ここに書くと同じことを2度言うことになる。
		Description: "HTTPS で公開する環境では有効にします",
	},
}

// Definitions は設定の全件を定義順で返す。
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

// Lookup はキーから定義を引く。**レジストリに無いキーは設定ではない。**
func Lookup(key string) (Definition, bool) {
	for _, d := range definitions {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// joinJa は候補を「a / b / c」の形に並べる。
func joinJa(vs []string) string {
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += " / "
		}
		out += v
	}
	return out
}
