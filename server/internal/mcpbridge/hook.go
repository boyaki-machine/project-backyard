package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// フックの受け口（pb-wait hook。DbDesign.md 6.34、ApiDesign.md「共通作業タイマー」の work-hold）。
//
// Codex のコマンドフックが、承認を求める直前と次のツール実行の後に呼ぶ。標準入力のイベントの JSON から
// hook_event_name だけを拾い、PB の work-hold へ送る。**承認の流れには口を出さない**——標準出力に
// 何も書かず、失敗しても呼び出し側は終了コード0で終わる。

// HookConfig は pb-wait hook の設定。
type HookConfig struct {
	// HoldURL は PB の work-hold の URL（http / https）。
	HoldURL      string
	TokenEnvName string
	CAFile       string
}

// hookTimeout は PB へ送る期限。**フックはツールを1回呼ぶたびに走る**ので、PB に届かないときに
// エージェントを待たせない。
const hookTimeout = 3 * time.Second

// hookReadLimit は hook_event_name を探して読む上限。PostToolUse はツールの出力を丸ごと含む。
const hookReadLimit = 1 << 20

// HookEventName は本文の先頭から hook_event_name だけを拾う。見つからなければ空文字。
func HookEventName(body io.Reader) string {
	dec := json.NewDecoder(io.LimitReader(body, hookReadLimit))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return ""
		}
		if key, _ := t.(string); key == "hook_event_name" {
			var name string
			if dec.Decode(&name) != nil {
				return ""
			}
			return name
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return ""
		}
	}
	return ""
}

// Hook は標準入力のイベントを PB へ知らせる。
func Hook(ctx context.Context, in io.Reader, cfg HookConfig) error {
	if cfg.TokenEnvName == "" {
		return errors.New("--token-env を指定してください")
	}
	u, err := url.Parse(cfg.HoldURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("--hold-url には PB の work-hold の URL を指定してください")
	}
	name := HookEventName(in)
	// 残りは読み捨てる。読まずに終わると、書き込み側（Codex）がパイプの詰まりで待つことがある。
	_, _ = io.Copy(io.Discard, in)
	if name == "" {
		return nil
	}
	token := os.Getenv(cfg.TokenEnvName)
	if token == "" {
		return fmt.Errorf("環境変数 %s が空です", cfg.TokenEnvName)
	}
	client, err := newClient(cfg.CAFile)
	if err != nil {
		return err
	}
	client.Timeout = hookTimeout
	body, _ := json.Marshal(map[string]string{"hook_event_name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("PB へ知らせられない: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("PB が HTTP %d を返しました", res.StatusCode)
	}
	return nil
}
