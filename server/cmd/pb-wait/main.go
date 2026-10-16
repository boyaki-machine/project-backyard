// pb-wait は、PB の変化を待って終わるだけの道具（Delegation.md 10.3。pb-303）。
//
// エージェントの MCP の接続とは別に動く。Claude Code はバックグラウンドで走らせ、終わったときに
// モデルが起こされるので、待つ間にトークンを使わない。
//
// 終了コード：0 = 変化があった、2 = --max まで変化が無かった（最後の cursor を書く）、
// 3 = 評価役が待つ間に main が止まっていた（Delegation.md 10.4）、1 = 失敗。
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
	// pb-wait hook：Codex のコマンドフックから呼ばれ、承認待ちを PB に知らせる（DbDesign.md 6.34）。
	// **常に終了コード0で終わり、標準出力に書かない**——承認の流れを変えないため。失敗は標準エラーへ書く。
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		var hc mcpbridge.HookConfig
		fs := flag.NewFlagSet("hook", flag.ContinueOnError)
		fs.StringVar(&hc.HoldURL, "hold-url", "", "PB の work-hold の URL")
		fs.StringVar(&hc.TokenEnvName, "token-env", "PB_TOKEN", "PB のトークンを入れた環境変数の名前")
		fs.StringVar(&hc.CAFile, "ca-file", "", "追加の CA 証明書（PEM）")
		if err := fs.Parse(os.Args[2:]); err == nil {
			if err := mcpbridge.Hook(context.Background(), os.Stdin, hc); err != nil {
				fmt.Fprintln(os.Stderr, "pb-wait hook:", err)
			}
		}
		return
	}
	var cfg mcpbridge.Config
	var opt mcpbridge.WaitOptions
	flag.StringVar(&cfg.URL, "url", "", "PB MCP HTTPS URL（.mcp.json と同じ）")
	flag.StringVar(&cfg.TokenEnvName, "token-env", "PB_TOKEN", "PB のトークンを入れた環境変数の名前")
	flag.StringVar(&cfg.CAFile, "ca-file", "", "追加の CA 証明書（PEM）")
	flag.IntVar(&opt.Seq, "seq", 0, "待つチケット（委任の根）の番号")
	flag.StringVar(&opt.Since, "since", "", "前回の pb_wait / pb-wait が返した cursor")
	flag.DurationVar(&opt.Max, "max", time.Hour, "待つ上限（例：60m、3h）")
	flag.Parse()
	result, err := mcpbridge.Wait(context.Background(), os.Stdout, cfg, opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pb-wait:", err)
		os.Exit(1)
	}
	switch result {
	case mcpbridge.WaitTimedOut:
		os.Exit(2)
	case mcpbridge.WaitMainStopped:
		os.Exit(3)
	}
}
