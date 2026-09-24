// Package maintenance は保守モードの旗を持つ（Design.md 10.4）。
//
// **旗はプロセスの中に持ち、DB には置かない。** 保守モードに入るのは書庫の取り込みの
// あいだであり、取り込みは DB そのものを入れ替える。app_setting に置くと、**落とす表の
// 中に入っている**ことになり、途中で旗自身が消える。
//
// **人が切り替える口を持たない。** 取り込みの要求が自分で立てて自分で降ろすもので、
// 設定ではない（Design.md 10.3 の「設定は WebGUI を第一の口とする」の対象外）。
//
// **プロセスが落ちれば旗も消える。** 取り込みが途中で落ちると DB は中途半端なまま
// 残るが、同じ書庫をもう一度取り込めば表を落とすところからやり直すので回復できる。
//
// **複数のプロセスでは、1つしか止まらない。** PB は単一プロセス・少人数利用を前提と
// しており（DbDesign.md 3.5）、この限界は塞がない。
package maintenance

import (
	"errors"
	"sync"
	"time"
)

// ErrBusy は、既に保守モードに入っているときに Enter が返す。
//
// **取り込みが2つ同時に走らないための鍵でもある。** 旗を立てられた側だけが進む。
var ErrBusy = errors.New("既に保守モード中である")

// Flag は保守モードの旗。ゼロ値で使える。
type Flag struct {
	mu       sync.RWMutex
	on       bool
	since    time.Time
	reason   string
	nowFunc  func() time.Time
	onChange func(bool)
}

// New は旗を返す。onChange は状態が変わったときに呼ばれる（任意。ログ用）。
func New(onChange func(bool)) *Flag {
	return &Flag{nowFunc: time.Now, onChange: onChange}
}

// Enter は保守モードに入る。**既に入っていれば ErrBusy を返す。**
//
// 返された leave を呼ぶと出る。**defer で呼ぶこと**——落ちた経路で旗が残ると、
// プロセスを落とすまで誰も PB を使えない。
func (f *Flag) Enter(reason string) (leave func(), err error) {
	if f == nil {
		return func() {}, nil
	}
	f.mu.Lock()
	if f.on {
		f.mu.Unlock()
		return nil, ErrBusy
	}
	f.on = true
	f.reason = reason
	f.since = f.now()
	f.mu.Unlock()

	if f.onChange != nil {
		f.onChange(true)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.on = false
			f.reason = ""
			f.mu.Unlock()
			if f.onChange != nil {
				f.onChange(false)
			}
		})
	}, nil
}

// On は保守モード中かを返す。**nil でも呼べる**（旗を渡していない構成のため）。
func (f *Flag) On() bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.on
}

// Since は保守モードに入った時刻を返す。入っていなければゼロ値。
func (f *Flag) Since() time.Time {
	if f == nil {
		return time.Time{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.since
}

// Reason は保守モードに入った理由を返す。**画面には出さない**（ログ用）。
func (f *Flag) Reason() string {
	if f == nil {
		return ""
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.reason
}

func (f *Flag) now() time.Time {
	if f.nowFunc != nil {
		return f.nowFunc()
	}
	return time.Now()
}
