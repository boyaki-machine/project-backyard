// pb-wait は、PB の変化を待って終わるだけの道具（Delegation.md 10.3。pb-303）。
//
// エージェントの MCP の接続とは別に動く。Claude Code はバックグラウンドで走らせ、終わったときに
// モデルが起こされるので、待つ間にトークンを使わない。
//
// 終了コード：0 = 変化があった、2 = --max まで変化が無かった（最後の cursor を書く）、1 = 失敗。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/mcpbridge"
)

func main() {
	var cfg mcpbridge.Config
	var opt mcpbridge.WaitOptions
	flag.StringVar(&cfg.URL, "url", "", "PB MCP HTTPS URL（.mcp.json と同じ）")
	flag.StringVar(&cfg.TokenEnvName, "token-env", "PB_TOKEN", "PB のトークンを入れた環境変数の名前")
	flag.StringVar(&cfg.CAFile, "ca-file", "", "追加の CA 証明書（PEM）")
	flag.IntVar(&opt.Seq, "seq", 0, "待つチケット（委任の根）の番号")
	flag.StringVar(&opt.Since, "since", "", "前回の pb_wait / pb-wait が返した cursor")
	flag.DurationVar(&opt.Max, "max", time.Hour, "待つ上限（例：60m、3h）")
	flag.Parse()
	changed, err := mcpbridge.Wait(context.Background(), os.Stdout, cfg, opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pb-wait:", err)
		os.Exit(1)
	}
	if !changed {
		os.Exit(2)
	}
}
