// Live は実行中の実効値を保持し、画面からの変更で入れ替える（Design.md 10.3）。
//
// **第2層はリクエストごとに DB を引かない。** 設定はプロセス内に持ち、
// PUT /api/v1/admin/settings が成功した時点で入れ替える。複数のレプリカでは、
// 他のレプリカが次に読み直すまで古い値が残る——**読み直しの間隔は設定項目に
// しない**（設定の反映を設定で決めると、その設定自身の反映が説明できなくなる）。
package config

import (
	"log/slog"
	"sync"
)

// Row は app_setting の1行。store 層に依存しないための最小の形である。
type Row struct {
	Key   string
	Value string
}

// Live は入れ替え可能な Set の入れ物。
//
// **読みが圧倒的に多いので RWMutex で足りる。** 書きは設定画面からの保存だけで、
// 1インスタンスにつき1日に数回を超えない。
type Live struct {
	mu sync.RWMutex

	// base はファイル・環境変数・既定値だけからなる Set。**DB を含まない。**
	//
	// **これを分けて持つのが要点である。** DB の行を重ねるときは必ず base から
	// やり直す——前回の重ね結果を土台にすると、**行を消しても実効値が
	// DB 由来のまま残る**（OverlayDatabase は足すだけで、消す術を持たない）。
	// 実サーバ検証で「既定に戻す」が応答に反映されない形で出た（2026-09-11）。
	base *Set

	// set は base に DB の行を重ねたもの。リクエストが読むのはこちら。
	set *Set
}

// NewLive は base を包む。**渡すのは DB を重ねる前の Set である。**
func NewLive(base *Set) *Live { return &Live{base: base, set: base} }

// Base は DB を重ねる前の Set を返す。**行を重ね直す土台はいつもこれである。**
func (l *Live) Base() *Set {
	if l == nil {
		return Defaults()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.base
}

// ApplyRows は base に行を重ね直して保持し、その Set を返す。
func (l *Live) ApplyRows(rows []Row) *Set {
	set := OverlayDatabase(l.Base(), rows)
	l.Replace(set)
	return set
}

// Snapshot はいまの Set を返す。**設定APIの一覧の材料である。**
//
// **nil レシーバでも既定値の Set を返す。** Live を渡さずに組まれた
// ハンドラが、レジストリの既定で動くようにするためである——第2層の既定は
// どれも安全側（Secure なし・ログは info・バージョン非表示）なので、
// 落ちるより既定で動くほうがよい。
func (l *Live) Snapshot() *Set {
	if l == nil {
		return Defaults()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.set
}

// Replace は set を入れ替える。**base は動かさない**——次に行を重ねるときの
// 土台が汚れると、行を消しても実効値が戻らなくなる。
//
// **nil レシーバでは何もしない。**
func (l *Live) Replace(set *Set) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.set = set
	l.mu.Unlock()
}

// CookieSecure は pb_session / pb_csrf に Secure を付けるか。
func (l *Live) CookieSecure() bool { return l.Snapshot().Bool(KeyCookieSecure) }

// HealthShowVersion は GET /healthcheck にバージョンを含めるか。
func (l *Live) HealthShowVersion() bool { return l.Snapshot().Bool(KeyHealthShowVersion) }

// LogLevel と LogFormat はロガーの入れ替えに使う。
func (l *Live) LogLevel() string  { return l.Snapshot().String(KeyLogLevel) }
func (l *Live) LogFormat() string { return l.Snapshot().String(KeyLogFormat) }

// OverlayDatabase は DB の行を重ねた新しい Set を返す。
//
// **上の層が値を持っているキーには重ねない。** 優先順が
// `<KEY>_FILE` ＞ 設定ファイル ＞ 環境変数 ＞ DB だからで、
// 実効値の出どころが SourceDefault のものだけが DB に置き換わる。
//
// **第1層のキーには重ねない。** 接続文字列や待受を DB から読むことはない。
//
// **レジストリに無いキーの行は、警告を1行出して無視する。** 古いバイナリへ
// 戻したときに落ちないためである——新しい版が書いた行が、知らないキーとして残る。
//
// **値が検証を通らない行も無視する。** 直接 SQL で書かれた行や、
// レジストリの値域を狭めた版へ戻したときに、起動できなくなるのを避ける。
func OverlayDatabase(base *Set, rows []Row) *Set {
	out := base.clone()
	for _, row := range rows {
		def, ok := Lookup(row.Key)
		if !ok {
			slog.Warn("知らない設定キーの行を無視した",
				slog.String("key", row.Key),
				slog.String("hint", "新しい版が書いた行か、綴り違いである"))
			continue
		}
		if def.Layer != LayerRuntime {
			slog.Warn("第2層でない設定の行を無視した",
				slog.String("key", row.Key),
				slog.Int("layer", int(def.Layer)))
			continue
		}
		cur := out.values[row.Key]
		if cur.Source != SourceDefault {
			// ファイルか環境変数で固定されている。**画面は編集させないので、
			// 通常ここへは来ない**——直接 SQL で書いた行か、あとから
			// 環境変数を足した場合である。
			continue
		}
		v := def.Normalize(row.Value)
		if err := def.Validate(v); err != nil {
			slog.Warn("設定の行が値域に合わないため無視した",
				slog.String("key", row.Key), slog.String("error", err.Error()))
			continue
		}
		out.values[row.Key] = Value{Def: def, Value: v, Source: SourceDatabase}
	}
	return out
}

// Defaults はレジストリの既定値だけからなる Set を返す。
//
// **ファイルも環境変数も DB も見ない。** 設定を渡さないテストと、
// Live が未設定のまま組まれた場合の受け皿である。**必須の設定は空文字**に
// なるので、起動経路では使わない（Resolve が誤りを返すべきである）。
func Defaults() *Set {
	set := &Set{values: make(map[string]Value, len(definitions))}
	for _, d := range definitions {
		set.values[d.Key] = Value{Def: d, Value: d.Default, Source: SourceDefault}
	}
	return set
}

// LiveDefaults は Defaults を包んだ Live を返す。
func LiveDefaults() *Live { return NewLive(Defaults()) }

// LiveWith は既定値に上書きを重ねた Live を返す。
//
// **OverlayDatabase と同じ規則で重ねる**ので、第2層でないキーや値域に合わない
// 値は無視される。設定を明示して組みたい場合（主に試験）のための口である。
func LiveWith(overrides ...Row) *Live {
	return NewLive(OverlayDatabase(Defaults(), overrides))
}
