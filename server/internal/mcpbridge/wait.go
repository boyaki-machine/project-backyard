package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// 待ち受けをモデルの外へ出す道具（pb-wait）の本体（Delegation.md 10.3。pb-303）。
//
// **エージェントの MCP の接続とは別に動く。** 自分で PB の MCP へ pb_wait を繰り返し送り、
// 変化があったら結果を書いて終わる。Claude Code はこれをバックグラウンドで走らせ、終わったときに
// モデルが起こされるので、待つ間にトークンを使わない。pb_wait を直接呼ぶと、50秒か4分ごとに
// モデルが1ターン動く。
//
// **PB の MCP は状態を持たない**（Stateless）ので、initialize を挟まず tools/call を送る。
// 進捗の通知は求めない——1回の pb_wait は50秒で返り、HTTP クライアントの60秒に収まる。

// WaitOptions は1回の待ち受けの指定。
type WaitOptions struct {
	Seq   int
	Since string
	// Max は待つ上限。過ぎたら変化なしとして、最後の cursor を書いて終わる。
	Max time.Duration
}

// waitOutcome は pb_wait の応答のうち、繰り返しの判断に使う部分。
type waitOutcome struct {
	Changed bool   `json:"changed"`
	Cursor  string `json:"cursor"`
}

// Wait は変化があるか Max が過ぎるまで pb_wait を繰り返す。最後の pb_wait の応答（JSON）を
// out へ書き、変化があったかを返す。
func Wait(ctx context.Context, out io.Writer, cfg Config, opt WaitOptions) (bool, error) {
	endpoint, err := validate(cfg)
	if err != nil {
		return false, err
	}
	if opt.Seq < 1 {
		return false, errors.New("--seq に委任の根のチケット番号を指定してください")
	}
	token := os.Getenv(cfg.TokenEnvName)
	if token == "" {
		return false, fmt.Errorf("環境変数 %s にPBトークンがありません", cfg.TokenEnvName)
	}
	client, err := newClient(cfg.CAFile)
	if err != nil {
		return false, err
	}

	deadline := time.Now().Add(opt.Max)
	cursor := opt.Since
	for id := 1; ; id++ {
		text, err := callWaitOnce(ctx, client, endpoint.String(), token, id, opt.Seq, cursor)
		if err != nil {
			return false, err
		}
		var got waitOutcome
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			return false, fmt.Errorf("pb_wait の応答を解釈できない: %w", err)
		}
		if got.Cursor != "" {
			cursor = got.Cursor
		}
		if got.Changed || !time.Now().Before(deadline) {
			if _, err := io.WriteString(out, text+"\n"); err != nil {
				return false, fmt.Errorf("結果を書けない: %w", err)
			}
			return got.Changed, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
}

// callWaitOnce は pb_wait を1回送り、結果の本文（content[0].text）を返す。
func callWaitOnce(ctx context.Context, client *http.Client, endpoint, token string, id, seq int, since string) (string, error) {
	args := map[string]any{"seq": seq}
	if since != "" {
		args["since"] = since
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "pb_wait", "arguments": args},
	})
	if err != nil {
		return "", err
	}
	contentType, raw, err := forward(ctx, client, endpoint, token, body)
	if err != nil {
		return "", err
	}
	messages, err := decodeMessages(contentType, raw)
	if err != nil {
		return "", fmt.Errorf("PB MCPの応答を解釈できない: %w", err)
	}
	for _, m := range messages {
		var res struct {
			Result *struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(m, &res); err != nil {
			continue
		}
		if res.Error != nil {
			return "", fmt.Errorf("pb_wait が失敗した: %s", res.Error.Message)
		}
		if res.Result == nil || len(res.Result.Content) == 0 {
			continue
		}
		if res.Result.IsError {
			return "", fmt.Errorf("pb_wait が失敗した: %s", res.Result.Content[0].Text)
		}
		return res.Result.Content[0].Text, nil
	}
	return "", errors.New("pb_wait の応答に結果がありません")
}
