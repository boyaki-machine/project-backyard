// Command pb は Project Backyard のサーバ兼管理CLI（Design.md 4.1）。
//
// 手順3の時点で実装しているのは admin create のみ。
// serve（APIサーバの起動）は手順5以降で追加する。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version はリリースビルド時に -X main.version=<VERSION> で埋め込む（Design.md 4.5 / 11.1）。
var version = "dev"

func main() {
	// Ctrl-C で対話入力とDB接続を打ち切れるようにする。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "\n中断しました")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "エラー: "+err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("サブコマンドを指定してください")
	}

	switch args[0] {
	case "admin":
		return runAdmin(ctx, args[1:])
	case "version":
		fmt.Println("pb v" + version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("未知のサブコマンド: %s", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pb — Project Backyard

使い方:
  pb admin create   初期管理者（アドミニストレータ）を対話的に作成する
  pb version        バージョンを表示する
  pb help           このヘルプを表示する

環境変数:
  PB_DATABASE_URL       接続文字列（pb_app）
  PB_DATABASE_URL_FILE  同上をファイル経由で渡す場合のパス（こちらを優先）
`)
}

func runAdmin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("admin のサブコマンドを指定してください（create）")
	}
	switch args[0] {
	case "create":
		return adminCreate(ctx)
	default:
		return fmt.Errorf("未知のサブコマンド: admin %s", args[0])
	}
}
