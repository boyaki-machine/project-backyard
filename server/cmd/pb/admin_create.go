// pb admin create — 初期管理者の作成（DbDesign.md 7.5）。
//
// シードに管理者アカウントを含めない代わりに、CLI で対話的に作成する。
// actor → app_user → user_identity → local_credential を1トランザクションで作る。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/term"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// localProviderKey は 0010 のシードが投入する認証プロバイダ（DbDesign.md 7.1）。
const localProviderKey = "local"

// displayNameMaxLen は actor.display_name の CHECK 制約に対応する（DbDesign.md 6.2）。
const displayNameMaxLen = 60

func adminCreate(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("DBに接続できない: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	p := newPrompter(os.Stdin, os.Stdout)

	// 既に管理者が存在する場合は警告を出して確認を求める（DbDesign.md 7.5）。
	var admins int
	err = conn.QueryRow(ctx,
		`SELECT count(*) FROM app_user WHERE system_role = 'administrator'`,
	).Scan(&admins)
	if err != nil {
		return fmt.Errorf("既存の管理者を確認できない: %w", err)
	}
	if admins > 0 {
		fmt.Fprintf(os.Stdout, "警告: アドミニストレータが既に %d 件存在します。\n", admins)
		ok, err := p.confirm("それでも新しいアドミニストレータを作成しますか？")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stdout, "作成を中止しました。")
			return nil
		}
	}

	displayName, err := p.askDisplayName()
	if err != nil {
		return err
	}
	email, err := p.askEmail()
	if err != nil {
		return err
	}
	password, err := p.askPassword()
	if err != nil {
		return err
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	actorID, err := insertAdmin(ctx, conn, displayName, email, passwordHash)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "→ アドミニストレータを作成しました（%s）\n", actorID)
	return nil
}

// insertAdmin は4テーブルへの INSERT を1トランザクションで実行し、作成した actor.id を返す。
func insertAdmin(ctx context.Context, conn *pgx.Conn, displayName, email, passwordHash string) (string, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("トランザクションを開始できない: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // Commit 済みなら no-op

	actorID := ulidgen.New()
	identityID := ulidgen.New()

	_, err = tx.Exec(ctx,
		`INSERT INTO actor (id, kind, display_name) VALUES ($1, 'user', $2)`,
		actorID, displayName)
	if err != nil {
		return "", wrapInsertErr("actor", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO app_user (actor_id, email, system_role) VALUES ($1, $2, 'administrator')`,
		actorID, email)
	if err != nil {
		return "", wrapInsertErr("app_user", err)
	}

	// subject には正規化済みのメールを入れる（Design.md 6.2.1 のログイン経路が
	// (provider_key='local', subject=email) で引くため）。
	_, err = tx.Exec(ctx,
		`INSERT INTO user_identity (id, user_id, provider_key, subject) VALUES ($1, $2, $3, $4)`,
		identityID, actorID, localProviderKey, email)
	if err != nil {
		return "", wrapInsertErr("user_identity", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO local_credential (identity_id, password_hash) VALUES ($1, $2)`,
		identityID, passwordHash)
	if err != nil {
		return "", wrapInsertErr("local_credential", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("コミットに失敗した: %w", err)
	}
	return actorID, nil
}

// wrapInsertErr は一意制約違反と外部キー違反を、そのまま画面に出せる日本語にする。
func wrapInsertErr(table string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("このメールアドレスは既に登録されています（%s）", table)
		case "23503": // foreign_key_violation
			return fmt.Errorf("参照先が見つかりません。マイグレーション（make migrate）が適用済みか確認してください（%s）", table)
		}
	}
	return fmt.Errorf("%s への INSERT に失敗した: %w", table, err)
}

// prompter は対話入力を扱う。
//
// パスワードは端末なら非エコーで読む（DbDesign.md 7.5）。端末でない場合
// （検証スクリプトからのパイプ入力など）は通常の行として読む。
type prompter struct {
	in  *bufio.Reader
	fd  int
	out io.Writer
}

func newPrompter(in *os.File, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), fd: int(in.Fd()), out: out}
}

func (p *prompter) readLine(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	line, err := p.in.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return strings.TrimSpace(line), nil
		}
		return "", fmt.Errorf("入力を読み取れない: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (p *prompter) readPassword(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)

	if !term.IsTerminal(p.fd) {
		// 非端末では bufio 経由で1行読む。端末でないためエコーの制御は不要。
		line, err := p.in.ReadString('\n')
		if err != nil && !(err == io.EOF && line != "") {
			return "", fmt.Errorf("入力を読み取れない: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// 端末はカノニカルモードで1行ずつ返すため、bufio が次行を先読みすることはない。
	b, err := term.ReadPassword(p.fd)
	fmt.Fprintln(p.out)
	if err != nil {
		return "", fmt.Errorf("入力を読み取れない: %w", err)
	}
	return string(b), nil
}

func (p *prompter) askDisplayName() (string, error) {
	for {
		name, err := p.readLine("  表示名: ")
		if err != nil {
			return "", err
		}
		n := utf8.RuneCountInString(name)
		if n == 0 {
			fmt.Fprintln(p.out, "  表示名を入力してください。")
			continue
		}
		if n > displayNameMaxLen {
			fmt.Fprintf(p.out, "  表示名は%d文字以内にしてください（入力は%d文字）。\n", displayNameMaxLen, n)
			continue
		}
		return name, nil
	}
}

func (p *prompter) askEmail() (string, error) {
	for {
		input, err := p.readLine("  メールアドレス: ")
		if err != nil {
			return "", err
		}
		addr, err := mail.ParseAddress(input)
		if err != nil {
			fmt.Fprintln(p.out, "  メールアドレスの形式が正しくありません。")
			continue
		}
		// app_user.email は citext だが user_identity.subject は text のため、
		// 双方で同じ値になるよう小文字へ正規化して格納する。
		return strings.ToLower(addr.Address), nil
	}
}

func (p *prompter) askPassword() (string, error) {
	for {
		password, err := p.readPassword("  パスワード: ")
		if err != nil {
			return "", err
		}
		if err := auth.ValidatePassword(password); err != nil {
			fmt.Fprintln(p.out, "  "+err.Error())
			continue
		}
		// 入力誤りに気づかないまま唯一の管理者が作られると誰もログインできなくなるため、
		// 確認のためもう一度入力させる。
		again, err := p.readPassword("  パスワード（確認）: ")
		if err != nil {
			return "", err
		}
		if password != again {
			fmt.Fprintln(p.out, "  パスワードが一致しません。もう一度入力してください。")
			continue
		}
		return password, nil
	}
}

// confirm は [y/N] を尋ねる。既定は no。
func (p *prompter) confirm(question string) (bool, error) {
	answer, err := p.readLine(question + " [y/N]: ")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
