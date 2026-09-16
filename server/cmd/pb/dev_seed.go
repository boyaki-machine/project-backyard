// pb dev seed / pb dev info — 開発用デモデータ（DbDesign.md 7.6）。
//
// 本番シード（7.1〜7.4、マイグレーション 0010）とは完全に分ける。権限カタログや
// ワークフローテンプレートはどの環境でも要るが、デモユーザーとサンプルプロジェクトは
// 開発端末でしか使わないため、マイグレーションには載せない。
//
// SQL ではなく CLI で投入するのは、パスワードを Argon2id でハッシュ化する必要が
// あるためである（7.6.1）。事前計算したハッシュを SQL に埋め込む方法は、
// ハッシュパラメータを変えた時点で無効になる。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/lexorank"
	"github.com/boyaki-machine/project-backyard/server/internal/project"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

const (
	// defaultDevDataFile は定義ファイルの既定パス（DbDesign.md 7.6.2）。
	// リポジトリルートからの相対。make 経由では絶対パスが渡る。
	defaultDevDataFile = "deploy/dev/seed/dev-data.yaml"

	// devSeedAllowEnv は 7.6.3 の安全装置その1。
	devSeedAllowEnv = "PB_ALLOW_DEV_SEED"

	// maxAppUserForDevSeed は 7.6.3 の「50件を超えていたら開発環境ではない」判定。
	maxAppUserForDevSeed = 50

	// devSeedAuditLabel は audit_log.actor_label に残す実行経路（手順4b の規約）。
	devSeedAuditLabel = "pb dev seed (CLI)"

	// devAppURL は dev info が案内する URL。make run の PB_BIND に合わせている。
	devAppURL = "http://127.0.0.1:8080"
)

// devSeedAllowedHosts は 7.6.3 の安全装置その2。
// db は compose のサービス名（コンテナ内から実行した場合）。
var devSeedAllowedHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"db":        true,
}

// devSystemRoles は app_user.system_role の CHECK 制約（DbDesign.md 6.2）。
var devSystemRoles = map[string]bool{"operator": true, "administrator": true}

// devData は deploy/dev/seed/dev-data.yaml の形（DbDesign.md 7.6.4）。
//
// 宣言的な定義ファイルにしてあるのは、Go を触らずにユーザーやプロジェクトを
// 増やせるようにするためである。
type devData struct {
	Password string       `yaml:"password"`
	Users    []devUser    `yaml:"users"`
	Projects []devProject `yaml:"projects"`
}

type devUser struct {
	Email       string `yaml:"email"`
	DisplayName string `yaml:"display_name"`
	SystemRole  string `yaml:"system_role"`
}

type devProject struct {
	Key              string      `yaml:"key"`
	Name             string      `yaml:"name"`
	Description      string      `yaml:"description"`
	WorkflowTemplate string      `yaml:"workflow_template"`
	Members          []devMember `yaml:"members"`
	Tags             []devTag    `yaml:"tags"`
	Sprints          []devSprint `yaml:"sprints"`
	Agents           []devAgent  `yaml:"agents"`
	Tickets          []devTicket `yaml:"tickets"`
}

// devAgent はエージェントの定義（DbDesign.md 8.2.1、手順24a）。
//
// **owner は必須。** エージェントの権限は所有者から導かれる（Design.md 6.5）ので、
// 誰にも紐づかないエージェントは作れない（DDL の owner_actor_id も NOT NULL）。
//
// **トークンは持たない。** 平文は発行応答にしか存在せず（ApiDesign.md 4.5.3）、
// 定義ファイルに書けば秘密をリポジトリへ置くことになる。
type devAgent struct {
	DisplayName string `yaml:"display_name"`
	Owner       string `yaml:"owner"`
	ClientKind  string `yaml:"client_kind"`
	ModelName   string `yaml:"model_name"`
}

type devMember struct {
	Email string `yaml:"email"`
	Role  string `yaml:"role"`
}

// devTag はタグの定義（DbDesign.md 6.10、手順16a）。
//
// **色を持たない**（GuiDesign.md 8.6）。プロジェクト内で name が一意。
type devTag struct {
	Name      string `yaml:"name"`
	SortOrder int32  `yaml:"sort_order"`
}

// devSprint はスプリントの定義（DbDesign.md 6.9、手順16a）。
//
// 日付は YYYY-MM-DD。status は planned / active / completed。
type devSprint struct {
	Name      string `yaml:"name"`
	Goal      string `yaml:"goal"`
	StartDate string `yaml:"start_date"`
	EndDate   string `yaml:"end_date"`
	Status    string `yaml:"status"`
}

// devSprintStatuses は sprint.status の CHECK 制約（DbDesign.md 6.9）。
var devSprintStatuses = map[string]bool{"planned": true, "active": true, "completed": true}

// devTicket はチケットの定義（DbDesign.md 6.6 / 7.6.4、手順16b）。
//
// **参照はすべて名前で書く。** parent はチケットのタイトル、assignee はメール、
// tags はタグ名、sprint はスプリント名で指す。定義ファイルに ULID を書かせない
// ためで、タグ・スプリントと同じ考え方である。
//
// status は**プロジェクトのワークフローに定義されたキー**（省略すると入口の
// ステータス）。API は初期ステータスを選ばせない（ApiDesign.md 9.3）が、
// デモデータは「進行中」「完了」が並んだ画面を作れないと意味が無いため、
// ここだけは指定できるようにしてある。
type devTicket struct {
	Title         string   `yaml:"title"`
	Type          string   `yaml:"type"`
	Status        string   `yaml:"status"`
	Priority      string   `yaml:"priority"`
	Assignee      string   `yaml:"assignee"`
	Parent        string   `yaml:"parent"`
	Tags          []string `yaml:"tags"`
	Sprint        string   `yaml:"sprint"`
	BodyMd        string   `yaml:"body_md"`
	EstimatePoint float64  `yaml:"estimate_point"`
	StartDate     string   `yaml:"start_date"`
	DueDate       string   `yaml:"due_date"`
	// Staged は「オンステージ」に置くか（GuiDesign.md 5.4、手順16d）。
	// **表示上のトップレベルにしか置けない**——親を持たないもの、または
	// 親がエピックのもの（ApiDesign.md 9.4.1）。検証は validateSeedData で行う。
	Staged bool `yaml:"staged"`

	// References は外部参照（DbDesign.md 6.12、手順17c）。
	//
	// **1件も無いと画面に何も出ない。** kind='code' は画面から追加できず
	// （GuiDesign.md 5.5）、seed が唯一の供給源である。
	References []devReference `yaml:"references"`

	// ── 手順18a（ApiDesign.md 9.8 / 9.9 / 9.10.1）──────────────
	//
	// **手順18b の画面が「何も無い」状態から始まらないようにする。**
	// 3セクション（完了条件・関連チケット・コメント）はどれも空のときの
	// 見え方が別に決まっており（GuiDesign.md 5.5）、**中身のある状態と
	// 空の状態の両方が seed に無いと、片方しか画面で確かめられない。**

	// DoD は完了条件（DbDesign.md 6.11）。satisfied を立てた行を混ぜると、
	// チェック済みと未チェックの見分けを画面で確かめられる。
	DoD []devDoDItem `yaml:"dod"`

	// Comments はコメント（DbDesign.md 6.7）。**author は必須**
	// （comment.author_id が NOT NULL のため）。
	Comments []devComment `yaml:"comments"`

	// Links は関連チケット（DbDesign.md 6.6）。**target は同じプロジェクトの
	// チケットのタイトル**で書く——定義ファイルに ULID も seq も書かせない
	// （seq は投入するまで決まらない）。
	Links []devLink `yaml:"links"`
}

// devDoDItem はチケットの完了条件（DbDesign.md 6.11、手順18a）。
//
// **type を書かせない。** Phase 1 が受け付けるのは manual だけであり
// （ApiDesign.md 9.9）、選べない項目を定義ファイルに置くと「他も書ける」に見える。
type devDoDItem struct {
	Body string `yaml:"body"`
	// Satisfied は充足済みか。**満たした人は投入しない**——seed は
	// 「誰が」を持たない（satisfied_by は ON DELETE SET NULL で NULL 可）。
	Satisfied bool `yaml:"satisfied"`
}

// devComment はチケットのコメント（DbDesign.md 6.7、手順18a）。
//
// **origin は書かせない。** 呼び出し元のアクター種別から決まる規則
// （ApiDesign.md 9.8）を seed でも守り、author のメールから引く。
// Phase 1 のデモアカウントはすべて人なので human になる。
//
// **返信と削除済みも書ける**（pb-24）。`in_reply_to` の両向きリンクと「削除されました」は
// 実装済みでも、seed に無ければ画面で一度も踏まれない。返信先は位置ではなく**参照名**
// （`ref`）で指す——親チケットをタイトルで指すのと同じで、並べ替えても壊れない。
type devComment struct {
	Author string `yaml:"author"`
	Kind   string `yaml:"kind"`
	Body   string `yaml:"body"`
	// Ref は同じチケットの中で返信先として指すための名前。省略できる
	Ref string `yaml:"ref"`
	// ReplyTo は返信先の Ref。**同じチケットの、自分より前の、削除しないコメント**を
	// 指す（ApiDesign.md 9.8 が未削除のコメントにしか返信させないため）
	ReplyTo string `yaml:"reply_to"`
	// Deleted は投入したあとに論理削除する。**ほかのコメントの返信先にはできない**
	Deleted bool `yaml:"deleted"`
}

// devLink はチケット間リンク（DbDesign.md 6.6、手順18a）。
//
// **当該チケットが常に source になる**（ApiDesign.md 9.10.1）。incoming の行は
// 相手側のチケットに書けば作られるので、定義ファイルに向きの概念を持たせない。
type devLink struct {
	Target string `yaml:"target"`
	Type   string `yaml:"type"`
}

// devReference はチケットの外部参照（DbDesign.md 6.12、手順17c）。
//
// **kind ごとに必須が違う**——code は repository、doc は url。検証は
// validateSeedData で行い、DB の CHECK に落とす前に定義ファイルの誤りとして返す。
//
// **created_by は書かない。** 定義ファイルに ULID を書かせない方針（タグ・
// スプリントと同じ）で、投入時はチケットの reporter を据える。Phase 1 の
// 書き手は人の API トークンを持つクライアントなので（ApiDesign.md 9.10.2）、
// 人のアクターが入るのが実態に合う。
type devReference struct {
	Kind       string `yaml:"kind"`
	Label      string `yaml:"label"`
	URL        string `yaml:"url"`
	Repository string `yaml:"repository"`
	Branch     string `yaml:"branch"`
	CommitSha  string `yaml:"commit_sha"`
	Note       string `yaml:"note"`
}

// devReferenceKinds は ticket_reference.kind の CHECK（DbDesign.md 6.12）。
var devReferenceKinds = map[string]bool{"code": true, "doc": true}

// devCommentKinds は comment.kind の CHECK（DbDesign.md 6.7、手順18a）。
var devCommentKinds = map[string]bool{
	"discussion": true, "decision": true, "artifact": true,
	"caveat": true, "reference": true, "progress": true,
}

// devLinkTypes は ticket_link.link_type の CHECK（DbDesign.md 6.6、手順18a）。
//
// **7種すべてを許す。** 画面が3種に絞るのは GuiDesign.md 5.5 の話であり、
// seed は API と同じ値域で書ける（ApiDesign.md 9.10.1）。
var devLinkTypes = map[string]bool{
	"FS": true, "SS": true, "FF": true, "SF": true,
	"relates": true, "duplicates": true, "blocks": true,
}

// devTicketTypes / devTicketPriorities は ticket の CHECK 制約（DbDesign.md 6.6）。
var (
	devTicketTypes = map[string]bool{
		"epic": true, "story": true, "task": true,
	}
	devTicketPriorities = map[string]bool{
		"lowest": true, "low": true, "medium": true, "high": true, "highest": true,
	}
)

// runDev は dev サブコマンドを振り分ける。
func runDev(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("dev のサブコマンドを指定してください（seed / info）")
	}
	switch args[0] {
	case "seed":
		return devSeed(ctx, args[1:])
	case "info":
		return devInfo(args[1:])
	default:
		return fmt.Errorf("未知のサブコマンド: dev %s", args[0])
	}
}

// devSeed はデモデータを投入する。
//
// 全体を1トランザクションで実行し、途中で失敗したら何も入らない（7.6.2）。
func devSeed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev seed", flag.ContinueOnError)
	file := fs.String("file", defaultDevDataFile, "定義ファイルのパス")
	resetDemo := fs.Bool("reset-demo", false, "定義ファイルに載っているデモデータを削除してから投入し直す")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, err := loadDevData(*file)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := checkDevSeedAllowed(cfg.DatabaseURL); err != nil {
		return err
	}

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// 件数によるガードはトランザクションの外で見る（7.6.3）。
	users, err := gen.New(pool).CountAppUsers(ctx)
	if err != nil {
		return fmt.Errorf("app_user の件数を取得できない: %w", err)
	}
	if users > maxAppUserForDevSeed {
		return fmt.Errorf("app_user が %d 件あります。開発環境ではない可能性が高いため中止しました（上限 %d 件）",
			users, maxAppUserForDevSeed)
	}

	result, err := applyDevData(ctx, pool, data, *resetDemo)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "定義ファイル: %s\n\n", *file)
	result.print(os.Stdout)
	fmt.Fprintln(os.Stdout)
	printDevAccounts(os.Stdout, data)
	return nil
}

// devInfo は URL とデモアカウントの一覧を表示する（7.6.6）。
// パスワードや URL を探す時間をなくすためのもので、DBには接続しない。
func devInfo(args []string) error {
	fs := flag.NewFlagSet("dev info", flag.ContinueOnError)
	file := fs.String("file", defaultDevDataFile, "定義ファイルのパス")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, err := loadDevData(*file)
	if err != nil {
		return err
	}
	printDevAccounts(os.Stdout, data)
	return nil
}

// loadDevData は定義ファイルを読んで検証する。
func loadDevData(path string) (*devData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		abs, _ := filepath.Abs(path)
		return nil, fmt.Errorf("定義ファイルを読めない（%s）: %w", abs, err)
	}

	var data devData
	// KnownFields で綴り違いのキーを黙って捨てないようにする。
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("定義ファイルを解釈できない（%s）: %w", path, err)
	}
	if err := data.validate(); err != nil {
		return nil, fmt.Errorf("定義ファイルの内容が不正（%s）: %w", path, err)
	}
	return &data, nil
}

// validate は投入前に定義ファイルを検証する。
//
// DBの制約でも弾けるが、そちらのエラーは何行目の何が悪いのかが分からない。
// ロールとワークフローテンプレートの存在確認はDBに問い合わせる（二重管理を避けるため）。
func (d *devData) validate() error {
	if err := auth.ValidatePassword(d.Password); err != nil {
		return fmt.Errorf("password: %w", err)
	}
	if len(d.Users) == 0 {
		return errors.New("users が空です")
	}

	seen := make(map[string]bool, len(d.Users))
	for i, u := range d.Users {
		where := fmt.Sprintf("users[%d]", i)
		if _, err := mail.ParseAddress(u.Email); err != nil {
			return fmt.Errorf("%s: メールアドレスの形式が正しくありません（%q）", where, u.Email)
		}
		if seen[strings.ToLower(u.Email)] {
			return fmt.Errorf("%s: メールアドレスが重複しています（%q）", where, u.Email)
		}
		seen[strings.ToLower(u.Email)] = true

		if u.DisplayName == "" {
			return fmt.Errorf("%s: display_name が空です", where)
		}
		if !devSystemRoles[u.SystemRole] {
			return fmt.Errorf("%s: system_role は operator か administrator です（%q）", where, u.SystemRole)
		}
	}

	keys := make(map[string]bool, len(d.Projects))
	for i, p := range d.Projects {
		where := fmt.Sprintf("projects[%d]", i)
		if p.Key == "" || keys[p.Key] {
			return fmt.Errorf("%s: key が空か重複しています（%q）", where, p.Key)
		}
		keys[p.Key] = true

		if p.Name == "" {
			return fmt.Errorf("%s: name が空です", where)
		}
		if p.WorkflowTemplate == "" {
			return fmt.Errorf("%s: workflow_template が空です", where)
		}
		for j, m := range p.Members {
			if !seen[strings.ToLower(m.Email)] {
				return fmt.Errorf("%s.members[%d]: users に定義されていないメールアドレスです（%q）", where, j, m.Email)
			}
			if m.Role == "" {
				return fmt.Errorf("%s.members[%d]: role が空です", where, j)
			}
		}

		// タグは name がプロジェクト内で一意（DbDesign.md 6.10 の uq_tag_project_name）。
		// DBの制約でも弾けるが、そちらのエラーは何行目が悪いのかが分からない。
		tagNames := make(map[string]bool, len(p.Tags))
		for j, tag := range p.Tags {
			at := fmt.Sprintf("%s.tags[%d]", where, j)
			if tag.Name == "" {
				return fmt.Errorf("%s: name が空です", at)
			}
			if n := utf8.RuneCountInString(tag.Name); n > 30 {
				return fmt.Errorf("%s: name は30文字以内です（%d文字）", at, n)
			}
			if tagNames[tag.Name] {
				return fmt.Errorf("%s: name が重複しています（%q）", at, tag.Name)
			}
			tagNames[tag.Name] = true
		}

		// **スプリントは name の重複そのものは許す**（6.9 に一意制約が無い）。
		// ただし投入の冪等性を名前で判定しているため、定義ファイルの中で
		// 重複していると2回目以降に作られない行ができる。定義側は弾く。
		sprintNames := make(map[string]bool, len(p.Sprints))
		for j, sp := range p.Sprints {
			at := fmt.Sprintf("%s.sprints[%d]", where, j)
			if sp.Name == "" {
				return fmt.Errorf("%s: name が空です", at)
			}
			if n := utf8.RuneCountInString(sp.Name); n > 50 {
				return fmt.Errorf("%s: name は50文字以内です（%d文字）", at, n)
			}
			if sprintNames[sp.Name] {
				return fmt.Errorf("%s: name が重複しています（%q。冪等性の判定に使うため定義側では許さない）", at, sp.Name)
			}
			sprintNames[sp.Name] = true

			if sp.Status != "" && !devSprintStatuses[sp.Status] {
				return fmt.Errorf("%s: status は planned / active / completed です（%q）", at, sp.Status)
			}
			start, err := devDate(sp.StartDate)
			if err != nil {
				return fmt.Errorf("%s.start_date: %w", at, err)
			}
			end, err := devDate(sp.EndDate)
			if err != nil {
				return fmt.Errorf("%s.end_date: %w", at, err)
			}
			if start.Valid && end.Valid && start.Time.After(end.Time) {
				return fmt.Errorf("%s: end_date は start_date 以降にしてください（ck_sprint_dates）", at)
			}
		}

		// チケット（手順16b）。**参照は定義ファイルの中で閉じている必要がある**
		// ——parent はここまでに現れたタイトル、tags / sprint はこのプロジェクトの
		// 定義、assignee は members に居ること。DBの FK でも弾けるが、そちらの
		// エラーは何行目が悪いのかが分からない。
		members := make(map[string]bool, len(p.Members))
		for _, m := range p.Members {
			members[strings.ToLower(m.Email)] = true
		}
		ticketTitles := make(map[string]bool, len(p.Tickets))
		// 種別をタイトル引きで覚える。staged の判定に親の種別が要る。
		ticketTypes := make(map[string]string, len(p.Tickets))
		for j, tk := range p.Tickets {
			at := fmt.Sprintf("%s.tickets[%d]", where, j)
			if tk.Title == "" {
				return fmt.Errorf("%s: title が空です", at)
			}
			if n := utf8.RuneCountInString(tk.Title); n > 200 {
				return fmt.Errorf("%s: title は200文字以内です（%d文字）", at, n)
			}
			// **タイトルは冪等性の判定に使う**（ticket に title の一意制約は無い）。
			if ticketTitles[tk.Title] {
				return fmt.Errorf("%s: title が重複しています（%q。冪等性の判定に使うため定義側では許さない）", at, tk.Title)
			}
			ticketTitles[tk.Title] = true
			ticketTypes[tk.Title] = tk.Type

			if !devTicketTypes[tk.Type] {
				return fmt.Errorf("%s: type は epic / story / task です（%q）", at, tk.Type)
			}
			if tk.Priority != "" && !devTicketPriorities[tk.Priority] {
				return fmt.Errorf("%s: priority は lowest 〜 highest です（%q）", at, tk.Priority)
			}
			// **親は自分より前に書かれていること。** 前から順に作るので、
			// 後ろを指されると解決できない（循環も同時に防げる）。
			if tk.Parent != "" && !ticketTitles[tk.Parent] {
				return fmt.Errorf("%s: parent は自分より前のチケットの title を指してください（%q）", at, tk.Parent)
			}
			if tk.Parent == tk.Title {
				return fmt.Errorf("%s: parent が自分自身です（ck_ticket_not_self_parent）", at)
			}
			// **段に置けるのは表示上のトップレベルだけ**（ApiDesign.md 9.4.1）
			// ——親を持たないもの、または親がエピックのもの。エピック自身も
			// 置けない（バックログに行として出さないため。GuiDesign.md 5.4）。
			if tk.Staged {
				switch {
				case tk.Type == "epic":
					return fmt.Errorf("%s: エピックはオンステージに置けません（行として出さないため）", at)
				case tk.Parent != "" && ticketTypes[tk.Parent] != "epic":
					return fmt.Errorf("%s: staged にできるのは親を持たないものか、親がエピックのものだけです（親=%q はエピックではありません）",
						at, tk.Parent)
				}
			}
			if tk.Assignee != "" && !members[strings.ToLower(tk.Assignee)] {
				return fmt.Errorf("%s: assignee は members に居るメールアドレスにしてください（%q）", at, tk.Assignee)
			}
			for k, name := range tk.Tags {
				if !tagNames[name] {
					return fmt.Errorf("%s.tags[%d]: このプロジェクトに定義の無いタグです（%q）", at, k, name)
				}
			}
			if tk.Sprint != "" && !sprintNames[tk.Sprint] {
				return fmt.Errorf("%s: このプロジェクトに定義の無いスプリントです（%q）", at, tk.Sprint)
			}
			if tk.EstimatePoint < 0 {
				return fmt.Errorf("%s: estimate_point は0以上です（%v）", at, tk.EstimatePoint)
			}
			start, err := devDate(tk.StartDate)
			if err != nil {
				return fmt.Errorf("%s.start_date: %w", at, err)
			}
			due, err := devDate(tk.DueDate)
			if err != nil {
				return fmt.Errorf("%s.due_date: %w", at, err)
			}
			if start.Valid && due.Valid && start.Time.After(due.Time) {
				return fmt.Errorf("%s: due_date は start_date 以降にしてください（ck_ticket_dates）", at)
			}

			// 外部参照（手順17c）。**kind ごとに必須が違う**（DbDesign.md 6.12）。
			// DB の CHECK でも弾けるが、そちらのエラーは何行目が悪いか分からない。
			for k, ref := range tk.References {
				refAt := fmt.Sprintf("%s.references[%d]", at, k)
				if !devReferenceKinds[ref.Kind] {
					return fmt.Errorf("%s: kind は code / doc です（%q）", refAt, ref.Kind)
				}
				if ref.Kind == "code" && ref.Repository == "" {
					return fmt.Errorf("%s: kind=code には repository が要ります（ck_ticket_reference_code）", refAt)
				}
				if ref.Kind == "doc" && ref.URL == "" {
					return fmt.Errorf("%s: kind=doc には url が要ります（ck_ticket_reference_doc）", refAt)
				}
			}

			// 完了条件（手順18a）。**body は必須**（dod_item.body が NOT NULL）。
			for k, item := range tk.DoD {
				if strings.TrimSpace(item.Body) == "" {
					return fmt.Errorf("%s.dod[%d]: body が空です", at, k)
				}
			}

			// コメント（手順18a）。**author は必須**（comment.author_id が
			// NOT NULL かつ ON DELETE RESTRICT。DbDesign.md 6.7）。
			// 参照名 → 削除するか。**前から順に作る**ので、返信先は自分より前にしか置けない
			commentRefs := map[string]bool{}
			for k, c := range tk.Comments {
				cAt := fmt.Sprintf("%s.comments[%d]", at, k)
				if strings.TrimSpace(c.Body) == "" {
					return fmt.Errorf("%s: body が空です", cAt)
				}
				if c.Author == "" {
					return fmt.Errorf("%s: author が空です（comment.author_id は NOT NULL）", cAt)
				}
				if !members[strings.ToLower(c.Author)] {
					return fmt.Errorf("%s: author %q がこのプロジェクトの members に居ません", cAt, c.Author)
				}
				if c.Kind != "" && !devCommentKinds[c.Kind] {
					return fmt.Errorf("%s: kind が不正です（%q）", cAt, c.Kind)
				}
				if c.ReplyTo != "" {
					deleted, ok := commentRefs[c.ReplyTo]
					if !ok {
						return fmt.Errorf("%s: reply_to %q がこのチケットの、自分より前のコメントの ref にありません", cAt, c.ReplyTo)
					}
					if deleted {
						return fmt.Errorf("%s: reply_to %q は削除するコメントです（削除済みには返信できない。ApiDesign.md 9.8）", cAt, c.ReplyTo)
					}
				}
				if c.Ref != "" {
					if _, dup := commentRefs[c.Ref]; dup {
						return fmt.Errorf("%s: ref %q が重複しています", cAt, c.Ref)
					}
					commentRefs[c.Ref] = c.Deleted
				}
			}

			// 関連チケット（手順18a）。**target は同じプロジェクトの、自分より前に
			// 書かれたチケットのタイトル**である。前から順に作るので後ろは解決
			// できない——parent と同じ制約で、**逆向きが要るなら相手側に書く**
			// （incoming の行は source 側から作られる。ApiDesign.md 9.10.1）。
			// 自分自身は指せない（ck_ticket_link_diff）。
			for k, l := range tk.Links {
				lAt := fmt.Sprintf("%s.links[%d]", at, k)
				if !devLinkTypes[l.Type] {
					return fmt.Errorf("%s: type が不正です（%q）", lAt, l.Type)
				}
				if l.Target == tk.Title {
					return fmt.Errorf("%s: 自分自身は指せません（ck_ticket_link_diff）", lAt)
				}
				if !ticketTitles[l.Target] {
					return fmt.Errorf("%s: target は自分より前のチケットの title を指してください（%q）", lAt, l.Target)
				}
			}
		}
	}
	return nil
}

// checkDevSeedAllowed は 7.6.3 の二重のガードを掛ける。
// いずれかに掛かったら何もせず終了する。
func checkDevSeedAllowed(databaseURL string) error {
	if os.Getenv(devSeedAllowEnv) != "1" {
		return fmt.Errorf("%s=1 が設定されていないため中止しました（開発用デモデータの投入。DbDesign.md 7.6.3）", devSeedAllowEnv)
	}

	host, err := databaseHost(databaseURL)
	if err != nil {
		return err
	}
	if !devSeedAllowedHosts[host] {
		return fmt.Errorf("接続先 %q は開発用として許可されていないため中止しました（許可: localhost / 127.0.0.1 / db）", host)
	}
	return nil
}

// databaseHost は接続文字列からホスト名を取り出す。
//
// 判定できない形式（keyword/value 形式の DSN など）はエラーにする。
// ガードは判定不能を「たぶん開発環境」に倒してはならない。
func databaseHost(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", errors.New("PB_DATABASE_URL の接続先ホストを判定できないため中止しました")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("PB_DATABASE_URL が postgres:// 形式でないため接続先を判定できません（scheme=%q）", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("PB_DATABASE_URL にホストが含まれていないため中止しました")
	}
	return u.Hostname(), nil
}

// seedResult は投入結果の件数（7.6.2 の「作成／スキップした件数」）。
type seedResult struct {
	usersCreated    int
	usersSkipped    int
	usersDeleted    int
	projectsCreated int
	projectsSkipped int
	projectsDeleted int
	membersAdded    int
	tagsCreated     int
	tagsSkipped     int
	sprintsCreated  int
	sprintsSkipped  int
	ticketsCreated  int
	ticketsSkipped  int
	agentsCreated   int
	agentsSkipped   int
}

func (r seedResult) print(w io.Writer) {
	if r.usersDeleted > 0 || r.projectsDeleted > 0 {
		fmt.Fprintf(w, "削除       : ユーザー %d / プロジェクト %d\n", r.usersDeleted, r.projectsDeleted)
	}
	fmt.Fprintf(w, "ユーザー   : 作成 %d / スキップ %d\n", r.usersCreated, r.usersSkipped)
	fmt.Fprintf(w, "プロジェクト: 作成 %d / スキップ %d（メンバー登録 %d）\n",
		r.projectsCreated, r.projectsSkipped, r.membersAdded)
	if r.tagsCreated+r.tagsSkipped+r.sprintsCreated+r.sprintsSkipped > 0 {
		fmt.Fprintf(w, "タグ       : 作成 %d / スキップ %d\n", r.tagsCreated, r.tagsSkipped)
		fmt.Fprintf(w, "スプリント : 作成 %d / スキップ %d\n", r.sprintsCreated, r.sprintsSkipped)
	}
	if r.agentsCreated+r.agentsSkipped > 0 {
		fmt.Fprintf(w, "エージェント: 作成 %d / スキップ %d\n", r.agentsCreated, r.agentsSkipped)
	}
	if r.ticketsCreated+r.ticketsSkipped > 0 {
		fmt.Fprintf(w, "チケット   : 作成 %d / スキップ %d\n", r.ticketsCreated, r.ticketsSkipped)
	}
}

// applyDevData は削除と投入を1トランザクションで実行する（7.6.2）。
func applyDevData(ctx context.Context, pool *pgxpool.Pool, data *devData, resetDemo bool) (seedResult, error) {
	var result seedResult

	tx, err := pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("トランザクションを開始できない: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // Commit 済みなら no-op

	q := gen.New(tx)
	rec := audit.FromCLI(devSeedAuditLabel)

	if resetDemo {
		if err := resetDemoData(ctx, q, rec, data, &result); err != nil {
			return result, err
		}
	}

	// パスワードは全アカウント共通なので1回だけハッシュ化する（Argon2id は重い）。
	passwordHash, err := auth.HashPassword(data.Password)
	if err != nil {
		return result, err
	}

	actorIDs := make(map[string]string, len(data.Users)) // 小文字のメール → actor.id
	for _, u := range data.Users {
		actorID, created, err := seedUser(ctx, q, rec, u, passwordHash)
		if err != nil {
			return result, err
		}
		actorIDs[strings.ToLower(u.Email)] = actorID
		if created {
			result.usersCreated++
		} else {
			result.usersSkipped++
		}
	}

	for _, p := range data.Projects {
		if err := seedProject(ctx, q, rec, p, actorIDs, &result); err != nil {
			return result, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("コミットに失敗した: %w", err)
	}
	return result, nil
}

// resetDemoData は定義ファイルに載っている対象だけを削除する（7.6.2）。
// 手で作ったデータには触れない。
func resetDemoData(ctx context.Context, q gen.Querier, rec *audit.Recorder, data *devData, result *seedResult) error {
	for _, p := range data.Projects {
		n, err := q.DeleteProjectByKey(ctx, p.Key)
		if err != nil {
			return fmt.Errorf("プロジェクト %s を削除できない: %w", p.Key, err)
		}
		result.projectsDeleted += int(n)
		// プロジェクトの削除は監査ログに残さない。ApiDesign.md 2.10 のアクション
		// 一覧に project.delete が無く（project.create / project.archive のみ）、
		// 一覧外のアクションは audit パッケージが弾くため。
	}

	for _, u := range data.Users {
		n, err := q.DeleteActorByEmail(ctx, u.Email)
		if err != nil {
			return fmt.Errorf("ユーザー %s を削除できない: %w", u.Email, err)
		}
		if n == 0 {
			continue
		}
		result.usersDeleted += int(n)
		if err := rec.Record(ctx, q, audit.Entry{
			Action:     audit.UserDelete,
			Result:     audit.Success,
			TargetType: "app_user",
			Detail:     map[string]any{"email": u.Email, "via": "pb dev seed --reset-demo"},
		}); err != nil {
			return err
		}
	}
	return nil
}

// seedUser は1アカウントを作る。既に同じメールがあれば作らず、その actor.id を返す。
//
// actor → app_user → user_identity → local_credential の4テーブルを作るのは
// pb admin create と同じ経路（DbDesign.md 7.5）。違うのは system_role を
// 定義ファイルから受け取る点である。
func seedUser(ctx context.Context, q gen.Querier, rec *audit.Recorder, u devUser, passwordHash string) (string, bool, error) {
	actorID, err := q.FindActorIDByEmail(ctx, u.Email)
	if err == nil {
		return actorID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("ユーザー %s を確認できない: %w", u.Email, err)
	}

	actorID = ulidgen.New()
	identityID := ulidgen.New()

	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID:          actorID,
		DisplayName: u.DisplayName,
	}); err != nil {
		return "", false, fmt.Errorf("actor を作成できない（%s）: %w", u.Email, err)
	}
	if err := q.CreateAppUser(ctx, gen.CreateAppUserParams{
		ActorID:    actorID,
		Email:      u.Email,
		SystemRole: u.SystemRole,
	}); err != nil {
		return "", false, fmt.Errorf("app_user を作成できない（%s）: %w", u.Email, err)
	}
	// subject には app_user.email と同じ表記をそのまま入れる（手順3の判断。
	// ログインは citext が引き当てた app_user.email の値で subject を引く）。
	if err := q.CreateUserIdentity(ctx, gen.CreateUserIdentityParams{
		ID:          identityID,
		UserID:      actorID,
		ProviderKey: localProviderKey,
		Subject:     u.Email,
	}); err != nil {
		return "", false, fmt.Errorf("user_identity を作成できない（%s）: %w", u.Email, err)
	}
	if err := q.CreateLocalCredential(ctx, gen.CreateLocalCredentialParams{
		IdentityID:   identityID,
		PasswordHash: passwordHash,
		// デモアカウントは共通パスワードで何度もログインし直すため、
		// 初回変更を要求しない（DbDesign.md 7.6.5）。
		MustChange: false,
	}); err != nil {
		return "", false, fmt.Errorf("local_credential を作成できない（%s）: %w", u.Email, err)
	}

	if err := rec.Record(ctx, q, audit.Entry{
		Action:     audit.UserCreate,
		Result:     audit.Success,
		TargetType: "app_user",
		TargetID:   actorID,
		Detail: map[string]any{
			"system_role": u.SystemRole,
			"via":         "pb dev seed",
		},
	}); err != nil {
		return "", false, err
	}
	return actorID, true, nil
}

// seedProject は1プロジェクトを作り、メンバーを登録する。
//
// 既に同じキーがあればプロジェクトの作成はスキップするが、メンバーの登録は
// 毎回行う（ON CONFLICT DO NOTHING）。定義ファイルにメンバーを足して
// 流し直したときに反映されるようにするためである。
func seedProject(ctx context.Context, q gen.Querier, rec *audit.Recorder, p devProject, actorIDs map[string]string, result *seedResult) error {
	projectID, err := q.FindProjectIDByKey(ctx, p.Key)
	switch {
	case err == nil:
		result.projectsSkipped++
	case errors.Is(err, pgx.ErrNoRows):
		projectID, err = createProject(ctx, q, rec, p, actorIDs)
		if err != nil {
			return err
		}
		result.projectsCreated++
	default:
		return fmt.Errorf("プロジェクト %s を確認できない: %w", p.Key, err)
	}

	for _, m := range p.Members {
		ok, err := q.IsProjectScopedRole(ctx, m.Role)
		if err != nil {
			return fmt.Errorf("ロール %s を確認できない: %w", m.Role, err)
		}
		if !ok {
			return fmt.Errorf("プロジェクト %s のメンバー %s: %q はプロジェクトロールではありません",
				p.Key, m.Email, m.Role)
		}
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: projectID,
			ActorID:   actorIDs[strings.ToLower(m.Email)],
			RoleKey:   m.Role,
		}); err != nil {
			return fmt.Errorf("プロジェクト %s に %s を追加できない: %w", p.Key, m.Email, err)
		}
		result.membersAdded++
	}

	if err := seedTags(ctx, q, projectID, p, result); err != nil {
		return err
	}
	if err := seedSprints(ctx, q, projectID, p, result); err != nil {
		return err
	}
	if err := seedAgents(ctx, q, projectID, p, actorIDs, result); err != nil {
		return err
	}
	return seedTickets(ctx, q, projectID, p, actorIDs, result)
}

// seedAgents はプロジェクトのエージェントを投入する（DbDesign.md 8.2.1、手順24a）。
//
// **冪等**（7.6.2）。同じ（所有者・クライアント種別・表示名）の組が既にあれば作らない
// ——ApiDesign.md 4.5.2 の 409 と同じ判定にそろえてある。
//
// **トークンは発行しない。** 平文は発行応答にしか存在しない（4.5.3）。ここで作るのは
// 「画面にエージェントが1件並ぶ」状態までで、実際に MCP でつなぐときは画面か API から
// 発行する。**入れておかないと、/admin/users のエージェント行も Avatar の角丸四角も
// 一度も描かれない。**
func seedAgents(
	ctx context.Context, q gen.Querier, projectID string, p devProject,
	actorIDs map[string]string, result *seedResult,
) error {
	for _, a := range p.Agents {
		ownerID := actorIDs[strings.ToLower(a.Owner)]
		if ownerID == "" {
			return fmt.Errorf("プロジェクト %s のエージェント %s: 所有者 %s が users に居ません",
				p.Key, a.DisplayName, a.Owner)
		}

		exists, err := q.AgentExistsWithName(ctx, gen.AgentExistsWithNameParams{
			OwnerActorID: ownerID,
			ProjectID:    nullText(projectID),
			ClientKind:   a.ClientKind,
			DisplayName:  a.DisplayName,
		})
		if err != nil {
			return fmt.Errorf("エージェント %s を確認できない: %w", a.DisplayName, err)
		}
		if exists {
			result.agentsSkipped++
			continue
		}

		agentID := ulidgen.New()
		if err := q.CreateAgentActor(ctx, gen.CreateAgentActorParams{
			ID:          agentID,
			DisplayName: a.DisplayName,
		}); err != nil {
			return fmt.Errorf("エージェント %s のアクターを作れない: %w", a.DisplayName, err)
		}
		if err := q.CreateAgent(ctx, gen.CreateAgentParams{
			ActorID:      agentID,
			OwnerActorID: ownerID,
			ProjectID:    nullText(projectID),
			ClientKind:   a.ClientKind,
			ModelName:    nullText(a.ModelName),
		}); err != nil {
			return fmt.Errorf("エージェント %s を登録できない: %w", a.DisplayName, err)
		}
		result.agentsCreated++
	}
	return nil
}

// seedTags はプロジェクトのタグを投入する（DbDesign.md 6.10 / 7.6.4）。
//
// **冪等**（7.6.2）。既にある名前は作らずスキップする。判定を名前で行うのは、
// uq_tag_project_name がその単位で一意を保証しているためである。
func seedTags(ctx context.Context, q gen.Querier, projectID string, p devProject, result *seedResult) error {
	if len(p.Tags) == 0 {
		return nil
	}
	rows, err := q.ListTagsByProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("プロジェクト %s のタグを読めない: %w", p.Key, err)
	}
	existing := make(map[string]bool, len(rows))
	for _, row := range rows {
		existing[row.Name] = true
	}

	for _, tag := range p.Tags {
		if existing[tag.Name] {
			result.tagsSkipped++
			continue
		}
		if err := q.CreateTag(ctx, gen.CreateTagParams{
			ID:        ulidgen.New(),
			ProjectID: projectID,
			Name:      tag.Name,
			SortOrder: tag.SortOrder,
		}); err != nil {
			return fmt.Errorf("プロジェクト %s にタグ %s を作れない: %w", p.Key, tag.Name, err)
		}
		result.tagsCreated++
	}
	return nil
}

// seedSprints はプロジェクトのスプリントを投入する（DbDesign.md 6.9 / 7.6.4）。
//
// **冪等**（7.6.2）。**sprint には name の一意制約が無い**（6.9）ため、
// 名前で突き合わせないと再実行のたびに増える。
func seedSprints(ctx context.Context, q gen.Querier, projectID string, p devProject, result *seedResult) error {
	if len(p.Sprints) == 0 {
		return nil
	}
	rows, err := q.ListSprintsByProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("プロジェクト %s のスプリントを読めない: %w", p.Key, err)
	}
	existing := make(map[string]bool, len(rows))
	for _, row := range rows {
		existing[row.Name] = true
	}

	for _, sp := range p.Sprints {
		if existing[sp.Name] {
			result.sprintsSkipped++
			continue
		}
		start, err := devDate(sp.StartDate)
		if err != nil {
			return fmt.Errorf("スプリント %s の start_date: %w", sp.Name, err)
		}
		end, err := devDate(sp.EndDate)
		if err != nil {
			return fmt.Errorf("スプリント %s の end_date: %w", sp.Name, err)
		}
		status := sp.Status
		if status == "" {
			status = "planned"
		}
		if err := q.CreateSprint(ctx, gen.CreateSprintParams{
			ID:        ulidgen.New(),
			ProjectID: projectID,
			Name:      sp.Name,
			Goal:      nullText(sp.Goal),
			StartDate: start,
			EndDate:   end,
			Status:    status,
		}); err != nil {
			return fmt.Errorf("プロジェクト %s にスプリント %s を作れない: %w", p.Key, sp.Name, err)
		}
		result.sprintsCreated++
	}
	return nil
}

// seedTickets はプロジェクトのチケットを投入する（DbDesign.md 6.6 / 7.6.4、手順16b）。
//
// **冪等**（7.6.2）。既にあるタイトルは作らずスキップする。ticket に title の
// 一意制約は無いが、定義ファイル側で重複を禁じている（validate）ので、
// この単位で突き合わせられる。
//
// **API（ApiDesign.md 9.3）と同じ順で組み立てる**——採番・ワークフロー解決・
// sort_key の採番・タグ付与。違うのは3点で、①CLI に実行者がいないため
// reporter を project_admin に据える ②status をデモの都合で指定できる
// ③完了済みを表すために closed_at を直接書く（本来は遷移の副作用。手順17）。
//
// **activity には記録しない。** デモデータの投入は業務上の出来事ではなく、
// 変更履歴に「開発PMが48件作成した」が並んでも読み手の役に立たない。
func seedTickets(
	ctx context.Context, q gen.Querier, projectID string, p devProject,
	actorIDs map[string]string, result *seedResult,
) error {
	if len(p.Tickets) == 0 {
		return nil
	}

	existing, err := q.ListTicketTitlesByProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("プロジェクト %s のチケットを読めない: %w", p.Key, err)
	}
	// タイトル → ticket.id。親の解決にも使う（既にある行の子を作れるようにする）。
	idByTitle := make(map[string]string, len(existing)+len(p.Tickets))
	for _, row := range existing {
		idByTitle[row.Title] = row.ID
	}

	statuses, err := projectStatuses(ctx, q, p.Key)
	if err != nil {
		return err
	}
	tagIDs, err := projectTagIDs(ctx, q, projectID, p.Key)
	if err != nil {
		return err
	}
	sprintIDs, err := projectSprintIDs(ctx, q, projectID, p.Key)
	if err != nil {
		return err
	}
	reporterID := projectCreator(p, actorIDs)

	// sort_key は定義ファイルの並び順で、末尾へ足していく（ApiDesign.md 9.4）。
	sortKey, err := q.MaxTicketSortKey(ctx, projectID)
	if err != nil {
		return fmt.Errorf("プロジェクト %s の並び順を読めない: %w", p.Key, err)
	}

	for _, tk := range p.Tickets {
		if _, ok := idByTitle[tk.Title]; ok {
			result.ticketsSkipped++
			continue
		}

		status, ok := statuses[tk.Status]
		if tk.Status == "" {
			status, ok = entryStatus(statuses), true
		}
		if !ok {
			return fmt.Errorf("プロジェクト %s のチケット %q: ワークフローに %q というステータスがありません",
				p.Key, tk.Title, tk.Status)
		}

		next, ok := lexorank.Between(sortKey, "")
		if !ok {
			return fmt.Errorf("プロジェクト %s のチケット %q: 並び順のキーを作れません（末尾=%q）",
				p.Key, tk.Title, sortKey)
		}
		sortKey = next

		seq, err := q.NextTicketSeq(ctx, projectID)
		if err != nil {
			return fmt.Errorf("プロジェクト %s のチケット番号を採番できない: %w", p.Key, err)
		}

		start, err := devDate(tk.StartDate)
		if err != nil {
			return fmt.Errorf("チケット %q の start_date: %w", tk.Title, err)
		}
		due, err := devDate(tk.DueDate)
		if err != nil {
			return fmt.Errorf("チケット %q の due_date: %w", tk.Title, err)
		}

		var parentID pgtype.Text
		if tk.Parent != "" {
			id, ok := idByTitle[tk.Parent]
			if !ok {
				return fmt.Errorf("チケット %q の parent %q が見つかりません", tk.Title, tk.Parent)
			}
			parentID = pgtype.Text{String: id, Valid: true}
		}
		var assigneeID pgtype.Text
		if tk.Assignee != "" {
			assigneeID = nullText(actorIDs[strings.ToLower(tk.Assignee)])
		}
		var estimate pgtype.Float8
		if tk.EstimatePoint > 0 {
			estimate = pgtype.Float8{Float64: tk.EstimatePoint, Valid: true}
		}

		ticketID := ulidgen.New()
		if err := q.CreateTicket(ctx, gen.CreateTicketParams{
			ID:            ticketID,
			ProjectID:     projectID,
			Seq:           seq,
			ParentID:      parentID,
			Type:          tk.Type,
			Title:         tk.Title,
			BodyMd:        nullText(tk.BodyMd),
			StatusKey:     status.key,
			Priority:      nullText(tk.Priority),
			AssigneeID:    assigneeID,
			ReporterID:    nullText(reporterID),
			EstimatePoint: estimate,
			StartDate:     start,
			DueDate:       due,
			SortKey:       pgtype.Text{String: sortKey, Valid: true},
		}); err != nil {
			return fmt.Errorf("プロジェクト %s にチケット %q を作れない: %w", p.Key, tk.Title, err)
		}

		for _, name := range tk.Tags {
			if err := q.AttachTicketTag(ctx, gen.AttachTicketTagParams{
				TicketID: ticketID, TagID: tagIDs[name],
			}); err != nil {
				return fmt.Errorf("チケット %q にタグ %q を付けられない: %w", tk.Title, name, err)
			}
		}

		// 完了ステータスなら closed_at を入れる。一覧の open フィルタ（9.2.1）と
		// スプリントの進捗（9.12）が closed_at を基準にしているため。
		if status.category == "done" {
			if err := q.SetTicketClosedAt(ctx, gen.SetTicketClosedAtParams{
				ID: ticketID, ClosedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			}); err != nil {
				return fmt.Errorf("チケット %q を完了にできない: %w", tk.Title, err)
			}
		}

		// スプリントの所属（DbDesign.md 6.9.1。pb-6）。
		//
		// **CreateTicket では入れられなくなった**（ApiDesign.md 9.3）。
		// スプリントは開始のときに決まるものになったので、seed も
		// 「作ってから所属させる」順序に揃える。**関連表と ticket.sprint_id の
		// 両方を書く**——片方だけだと 9.12 の集計と 9.2.1 の判定が食い違う。
		if sid := sprintIDs[tk.Sprint]; tk.Sprint != "" && sid != "" {
			if err := q.AddTicketsToSprint(ctx, gen.AddTicketsToSprintParams{
				TicketIds: []string{ticketID}, SprintID: sid,
			}); err != nil {
				return fmt.Errorf("チケット %q をスプリント %q に入れられない: %w",
					tk.Title, tk.Sprint, err)
			}
			if err := q.SetTicketsSprintID(ctx, gen.SetTicketsSprintIDParams{
				SprintID:  nullText(sid),
				ProjectID: projectID,
				TicketIds: []string{ticketID},
			}); err != nil {
				return fmt.Errorf("チケット %q のスプリントを設定できない: %w", tk.Title, err)
			}
		}

		// オンステージ（GuiDesign.md 5.4）。seed 直後にここが空だと、
		// 二段が動いていることを画面で確かめられない。
		if tk.Staged {
			if err := q.SetTicketStagedAt(ctx, gen.SetTicketStagedAtParams{
				ID: ticketID, StagedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			}); err != nil {
				return fmt.Errorf("チケット %q をオンステージにできない: %w", tk.Title, err)
			}
		}

		if err := seedTicketReferences(ctx, q, ticketID, tk, reporterID); err != nil {
			return err
		}
		if err := seedTicketDoD(ctx, q, ticketID, tk); err != nil {
			return err
		}
		if err := seedTicketComments(ctx, q, ticketID, tk, actorIDs); err != nil {
			return err
		}
		if err := seedTicketLinks(ctx, q, ticketID, tk, idByTitle, reporterID); err != nil {
			return err
		}

		idByTitle[tk.Title] = ticketID
		result.ticketsCreated++
	}
	return nil
}

// seedTicketReferences はチケットの外部参照を投入する（DbDesign.md 6.12、手順17c）。
//
// **チケットを作った直後にだけ呼ぶ。** 冪等の単位はチケットであり（既にある
// タイトルはスキップされる）、参照だけを後から突き合わせる手段は持たない
// ——ticket_reference に自然キーが無いためである。
//
// sort_order は定義ファイルの並び順で 10 刻み。API が省略時に使う既定
// （現在の最大値 + 10。ApiDesign.md 9.10.2）と同じ間隔に揃えてある。
func seedTicketReferences(
	ctx context.Context, q gen.Querier, ticketID string, tk devTicket, reporterID string,
) error {
	for i, ref := range tk.References {
		if err := q.CreateTicketReference(ctx, gen.CreateTicketReferenceParams{
			ID:         ulidgen.New(),
			TicketID:   ticketID,
			Kind:       ref.Kind,
			Label:      nullText(ref.Label),
			Url:        nullText(ref.URL),
			Repository: nullText(ref.Repository),
			Branch:     nullText(ref.Branch),
			CommitSha:  nullText(ref.CommitSha),
			Note:       nullText(ref.Note),
			CreatedBy:  nullText(reporterID),
			SortOrder:  int32((i + 1) * 10),
		}); err != nil {
			return fmt.Errorf("チケット %q に外部参照を足せない: %w", tk.Title, err)
		}
	}
	return nil
}

// seedTicketDoD はチケットの完了条件を投入する（DbDesign.md 6.11、手順18a）。
//
// **チケットを作った直後にだけ呼ぶ**（seedTicketReferences と同じ）。冪等の
// 単位はチケットであり、DoD だけを後から突き合わせる手段は持たない。
//
// **satisfied_by は入れない。** 定義ファイルは「誰が満たしたか」を持たず、
// 列は NULL 可である（ON DELETE SET NULL）——条件を満たした事実は残り、
// 誰が満たしたかだけが分からない状態になる。
//
// sort_order は定義ファイルの並び順で 10 刻み。API が省略時に使う既定
// （現在の最大値 + 10。ApiDesign.md 9.9）と同じ間隔に揃えてある。
func seedTicketDoD(
	ctx context.Context, q gen.Querier, ticketID string, tk devTicket,
) error {
	for i, item := range tk.DoD {
		if err := q.CreateDoDItem(ctx, gen.CreateDoDItemParams{
			ID:          ulidgen.New(),
			TicketID:    ticketID,
			Type:        "manual",
			Body:        item.Body,
			SortOrder:   int32((i + 1) * 10),
			IsSatisfied: item.Satisfied,
		}); err != nil {
			return fmt.Errorf("チケット %q に完了条件を足せない: %w", tk.Title, err)
		}
	}
	return nil
}

// seedTicketComments はチケットのコメントを投入する（DbDesign.md 6.7、手順18a）。
//
// **origin は author のアクター種別から決まる**（ApiDesign.md 9.8）。Phase 1 の
// デモアカウントはすべて人なので human になるが、**規則そのものを seed でも
// 守る**——ここで固定値を書くと、エージェントが増えたときに嘘になる。
//
// **kind を省略したら discussion**（DbDesign.md 6.7 の DEFAULT と同じ）。
// 既定に頼らず明示で渡すのは comment.sql の方針に揃えたためである。
func seedTicketComments(
	ctx context.Context, q gen.Querier, ticketID string, tk devTicket,
	actorIDs map[string]string,
) error {
	// 参照名 → comment.id。返信先の解決に使う（検証で前にあることを確かめてある）
	idByRef := map[string]string{}
	for _, c := range tk.Comments {
		authorID := actorIDs[strings.ToLower(c.Author)]
		if authorID == "" {
			return fmt.Errorf("チケット %q のコメントの author %q を解決できない", tk.Title, c.Author)
		}
		kind := c.Kind
		if kind == "" {
			kind = "discussion"
		}
		id := ulidgen.New()
		if err := q.CreateComment(ctx, gen.CreateCommentParams{
			ID:        id,
			TicketID:  ticketID,
			AuthorID:  authorID,
			BodyMd:    c.Body,
			Kind:      kind,
			Origin:    "human",
			InReplyTo: nullText(idByRef[c.ReplyTo]),
		}); err != nil {
			return fmt.Errorf("チケット %q にコメントを足せない: %w", tk.Title, err)
		}
		if c.Ref != "" {
			idByRef[c.Ref] = id
		}
		if c.Deleted {
			if _, err := q.SoftDeleteComment(ctx, gen.SoftDeleteCommentParams{TicketID: ticketID, ID: id}); err != nil {
				return fmt.Errorf("チケット %q のコメントを削除済みにできない: %w", tk.Title, err)
			}
		}
	}
	return nil
}

// seedTicketLinks はチケット間リンクを投入する（DbDesign.md 6.6、手順18a）。
//
// **当該チケットが常に source になる**（ApiDesign.md 9.10.1）。相手は
// idByTitle から引く——定義ファイルの検証が「自分より前」を要求しているので、
// ここまでに必ず入っている。
//
// **origin は human 固定である。** ticket_link.origin の値域は
// human / ai_suggested で、ai_suggested は「AIが提案し人がまだ採用していない」
// という状態を表す（Phase 2）。書き手の種別ではないので、comment とは違う。
func seedTicketLinks(
	ctx context.Context, q gen.Querier, ticketID string, tk devTicket,
	idByTitle map[string]string, reporterID string,
) error {
	for _, l := range tk.Links {
		targetID := idByTitle[l.Target]
		if targetID == "" {
			return fmt.Errorf("チケット %q の関連先 %q を解決できない", tk.Title, l.Target)
		}
		if err := q.CreateTicketLink(ctx, gen.CreateTicketLinkParams{
			ID:             ulidgen.New(),
			SourceTicketID: ticketID,
			TargetTicketID: targetID,
			LinkType:       l.Type,
			LagDays:        0,
			Origin:         "human",
			CreatedBy:      nullText(reporterID),
		}); err != nil {
			return fmt.Errorf("チケット %q に関連チケットを足せない: %w", tk.Title, err)
		}
	}
	return nil
}

// devStatus はワークフローのステータス1件（キーと分類）。
type devStatus struct {
	key      string
	category string
	order    int32
}

// projectStatuses はプロジェクトのワークフローのステータスをキー引きで返す。
func projectStatuses(
	ctx context.Context, q gen.Querier, key string,
) (map[string]devStatus, error) {
	// **変数名を pj にしてある。** internal/project を import したので、
	// project という局所変数はパッケージ名を関数の中で覆い隠す。
	pj, err := q.GetProjectByKey(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("プロジェクト %s を読めない: %w", key, err)
	}
	if !pj.WorkflowID.Valid {
		return nil, fmt.Errorf("プロジェクト %s にワークフローがありません", key)
	}
	rows, err := q.ListWorkflowStatuses(ctx, pj.WorkflowID.String)
	if err != nil {
		return nil, fmt.Errorf("プロジェクト %s のワークフローを読めない: %w", key, err)
	}
	out := make(map[string]devStatus, len(rows))
	for _, row := range rows {
		out[row.Key] = devStatus{key: row.Key, category: row.Category, order: row.SortOrder}
	}
	return out, nil
}

// entryStatus は status 省略時の既定。ApiDesign.md 9.3 と同じ決め方
// （category='todo' かつ sort_order 最小。該当が無ければ sort_order 最小）。
func entryStatus(statuses map[string]devStatus) devStatus {
	var best devStatus
	found := false
	for _, s := range statuses {
		if !found || betterEntry(s, best) {
			best, found = s, true
		}
	}
	return best
}

// betterEntry は入口としてふさわしいほうを選ぶ。
// todo を優先し、同じ分類なら sort_order の小さいほうを採る。
func betterEntry(a, b devStatus) bool {
	if (a.category == "todo") != (b.category == "todo") {
		return a.category == "todo"
	}
	return a.order < b.order
}

// projectTagIDs はタグ名 → tag.id。
func projectTagIDs(ctx context.Context, q gen.Querier, projectID, key string) (map[string]string, error) {
	rows, err := q.ListTagsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("プロジェクト %s のタグを読めない: %w", key, err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Name] = row.ID
	}
	return out, nil
}

// projectSprintIDs はスプリント名 → sprint.id。
func projectSprintIDs(ctx context.Context, q gen.Querier, projectID, key string) (map[string]string, error) {
	rows, err := q.ListSprintsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("プロジェクト %s のスプリントを読めない: %w", key, err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Name] = row.ID
	}
	return out, nil
}

// devDate は YYYY-MM-DD を date 列へ写す。空文字は NULL。
func devDate(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("日付は YYYY-MM-DD で書いてください（%q）", s)
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

// createProject は project / project_counter とワークフローと文書を作り、project.id を返す。
//
// **手順の実体は internal/project にある**（ApiDesign.md 5.3）。POST /projects と
// 同じ1本を通るので、片方だけ直って差が開くことがない。
//
// **作成者のメンバー登録だけは行わない。** 定義ファイルの members を seedProject が
// そのまま登録するため、ここで先に入れると役割の出所が2つになる。created_by には
// 定義ファイルで project_admin を与えられたメンバーを据える。
func createProject(ctx context.Context, q gen.Querier, rec *audit.Recorder, p devProject, actorIDs map[string]string) (string, error) {
	projectID := ulidgen.New()
	err := project.Create(ctx, q, rec, project.CreateParams{
		ID:               projectID,
		Key:              p.Key,
		Name:             p.Name,
		Description:      nullText(p.Description),
		WorkflowTemplate: p.WorkflowTemplate,
		CreatedBy:        nullText(projectCreator(p, actorIDs)),
		AuditVia:         "pb dev seed",
	})
	if err != nil {
		return "", err
	}
	return projectID, nil
}

// projectCreator は project.created_by に入れる actor.id を返す。
//
// POST /projects では作成者が project_admin になる（ApiDesign.md 5.2）ので、
// 定義ファイルで project_admin を与えられたメンバーを作成者とみなす。
// 該当が無ければ空文字（＝ NULL）。
func projectCreator(p devProject, actorIDs map[string]string) string {
	for _, m := range p.Members {
		if m.Role == "project_admin" {
			return actorIDs[strings.ToLower(m.Email)]
		}
	}
	return ""
}

// printDevAccounts はログイン用のアカウント一覧を表示する（7.6.2 / 7.6.6）。
func printDevAccounts(w io.Writer, data *devData) {
	fmt.Fprintf(w, "URL: %s\n", devAppURL)
	fmt.Fprintf(w, "共通パスワード: %s\n\n", data.Password)

	// 見出しは1行の凡例にする。全角を含む文字列を %-22s で揃えても、
	// 表示幅は端末側で2桁になるため列がずれる。行のほうは ASCII のみ。
	fmt.Fprintln(w, "  メールアドレス / システムロール / プロジェクトでの役割")
	for _, u := range data.Users {
		fmt.Fprintf(w, "  %-22s %-14s %s\n", u.Email, u.SystemRole, projectRolesOf(u.Email, data.Projects))
	}
}

// projectRolesOf は「demo: project_admin」のような表示用の文字列を組み立てる。
func projectRolesOf(email string, projects []devProject) string {
	var roles []string
	for _, p := range projects {
		for _, m := range p.Members {
			if strings.EqualFold(m.Email, email) {
				roles = append(roles, p.Key+": "+m.Role)
			}
		}
	}
	if len(roles) == 0 {
		return "—"
	}
	return strings.Join(roles, ", ")
}

// nullText は空文字を NULL として渡す。
func nullText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
