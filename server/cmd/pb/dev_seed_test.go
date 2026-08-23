package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoSeedFile はリポジトリにコミットしてある定義ファイル（DbDesign.md 7.6.4）。
const repoSeedFile = "../../../deploy/dev/seed/dev-data.yaml"

// コミット済みの定義ファイルが常に読める・通ることを保つ。
// 手で編集して壊れたら make test で気づける。
func TestLoadDevDataRepositoryFile(t *testing.T) {
	data, err := loadDevData(filepath.FromSlash(repoSeedFile))
	if err != nil {
		t.Fatalf("定義ファイルを読めない: %v", err)
	}

	if len(data.Users) != 4 {
		t.Errorf("デモアカウントは4件のはず（7.6.5）。実際は %d 件", len(data.Users))
	}
	if len(data.Projects) != 1 || data.Projects[0].Key != "demo" {
		t.Fatalf("demo プロジェクトが定義されていない: %+v", data.Projects)
	}

	// 7.6.5 の表のとおり、権限の違う4アカウントが揃っていること。
	// これが崩れると手順8のメニュー出し分けを検証できなくなる。
	roles := map[string]string{}
	for _, u := range data.Users {
		roles[u.Email] = u.SystemRole
	}
	if roles["admin@example.com"] != "administrator" {
		t.Errorf("admin@example.com は administrator のはず。実際は %q", roles["admin@example.com"])
	}
	for _, email := range []string{"pm@example.com", "member@example.com", "viewer@example.com"} {
		if roles[email] != "operator" {
			t.Errorf("%s は operator のはず。実際は %q", email, roles[email])
		}
	}

	want := map[string]string{
		"pm@example.com":     "project_admin",
		"member@example.com": "project_member",
		"viewer@example.com": "project_viewer",
	}
	got := map[string]string{}
	for _, m := range data.Projects[0].Members {
		got[m.Email] = m.Role
	}
	for email, role := range want {
		if got[email] != role {
			t.Errorf("%s の demo での役割は %q のはず。実際は %q", email, role, got[email])
		}
	}

	// **定義ファイルそのものが規則を満たすこと**（手順16d）。ここを通しておくと、
	// dev-data.yaml を書き換えたときに make test で気づける——DBを触らずに済む。
	if err := data.validate(); err != nil {
		t.Fatalf("定義ファイルが検証を通らない: %v", err)
	}

	tickets := data.Projects[0].Tickets
	stagedCount, typeCount := 0, map[string]int{}
	for _, tk := range tickets {
		typeCount[tk.Type]++
		if tk.Staged {
			stagedCount++
		}
	}
	// 種別は3値だけ（DbDesign.md 6.6）。bug / phase / wbs は廃止した。
	for _, gone := range []string{"bug", "phase", "wbs"} {
		if typeCount[gone] > 0 {
			t.Errorf("廃止した種別 %q が %d 件残っている", gone, typeCount[gone])
		}
	}
	// **オンステージが空だと二段が動いていることを画面で確かめられない**
	// （GuiDesign.md 5.4）。
	if stagedCount == 0 {
		t.Error("staged: true のチケットが1件も無い（二段を目で確かめられない）")
	}
	// **バグはタグで表す**（DbDesign.md 6.10）。受け皿のタグが要る。
	hasBugTag := false
	for _, tg := range data.Projects[0].Tags {
		if tg.Name == "バグ" {
			hasBugTag = true
		}
	}
	if !hasBugTag {
		t.Error("タグ「バグ」が定義されていない（種別 bug の受け皿。DbDesign.md 6.10）")
	}
}

func TestLoadDevDataRejectsUnknownKey(t *testing.T) {
	// 綴り違いを黙って捨てると、投入されないまま気づけない。
	path := writeTempSeed(t, `
password: pbdev-password
users:
  - email: a@example.com
    display_name: A
    system_roles: operator
`)
	if _, err := loadDevData(path); err == nil {
		t.Fatal("未知のキーはエラーになるはず")
	}
}

func TestDevDataValidate(t *testing.T) {
	valid := func() devData {
		return devData{
			Password: "pbdev-password",
			Users: []devUser{
				{Email: "admin@example.com", DisplayName: "管理者", SystemRole: "administrator"},
				{Email: "pm@example.com", DisplayName: "PM", SystemRole: "operator"},
			},
			Projects: []devProject{{
				Key: "demo", Name: "デモ", WorkflowTemplate: "simple",
				Members: []devMember{{Email: "pm@example.com", Role: "project_admin"}},
			}},
		}
	}

	ok := valid()
	if err := ok.validate(); err != nil {
		t.Fatalf("正しい定義が弾かれた: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(d *devData)
		want   string
	}{
		{"パスワードが短い", func(d *devData) { d.Password = "short" }, "password"},
		{"users が空", func(d *devData) { d.Users = nil }, "users"},
		{"メールの形式が不正", func(d *devData) { d.Users[0].Email = "not-an-email" }, "メールアドレスの形式"},
		{"メールの重複", func(d *devData) { d.Users[1].Email = "admin@example.com" }, "重複"},
		{"表示名が空", func(d *devData) { d.Users[0].DisplayName = "" }, "display_name"},
		{"未知のシステムロール", func(d *devData) { d.Users[0].SystemRole = "superuser" }, "system_role"},
		{"プロジェクトキーが空", func(d *devData) { d.Projects[0].Key = "" }, "key"},
		{"プロジェクト名が空", func(d *devData) { d.Projects[0].Name = "" }, "name"},
		{"テンプレート未指定", func(d *devData) { d.Projects[0].WorkflowTemplate = "" }, "workflow_template"},
		{"users にないメンバー", func(d *devData) { d.Projects[0].Members[0].Email = "ghost@example.com" }, "users に定義されていない"},
		{"メンバーのロールが空", func(d *devData) { d.Projects[0].Members[0].Role = "" }, "role"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid()
			tt.mutate(&d)
			err := d.validate()
			if err == nil {
				t.Fatal("エラーになるはず")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("エラーに %q を含むはず。実際: %v", tt.want, err)
			}
		})
	}
}

// オンステージと種別3値の検証（手順16d。ApiDesign.md 9.4.1、DbDesign.md 6.6）。
//
// **「置ける」ことを先に確かめてから「置けない」を測る**（LEARNINGS #35）。
// 順序を逆にすると、実装が常に弾いていてもガードの検証が通ってしまう。
func TestDevDataValidateStaged(t *testing.T) {
	base := func(tickets ...devTicket) devData {
		return devData{
			Password: "pbdev-password",
			Users: []devUser{
				{Email: "admin@example.com", DisplayName: "管理者", SystemRole: "administrator"},
			},
			Projects: []devProject{{
				Key: "demo", Name: "デモ", WorkflowTemplate: "simple",
				Tickets: tickets,
			}},
		}
	}

	t.Run("親を持たないものは置ける", func(t *testing.T) {
		d := base(devTicket{Title: "単独", Type: "task", Staged: true})
		if err := d.validate(); err != nil {
			t.Fatalf("置けるはずのものが弾かれた: %v", err)
		}
	})

	t.Run("親がエピックなら置ける", func(t *testing.T) {
		d := base(
			devTicket{Title: "まとまり", Type: "epic"},
			devTicket{Title: "配下", Type: "task", Parent: "まとまり", Staged: true},
		)
		if err := d.validate(); err != nil {
			t.Fatalf("置けるはずのものが弾かれた: %v", err)
		}
	})

	t.Run("親がタスクなら置けない", func(t *testing.T) {
		d := base(
			devTicket{Title: "親", Type: "task"},
			devTicket{Title: "子", Type: "task", Parent: "親", Staged: true},
		)
		err := d.validate()
		if err == nil {
			t.Fatal("エラーになるはず")
		}
		if !strings.Contains(err.Error(), "staged") {
			t.Errorf("エラーに staged を含むはず。実際: %v", err)
		}
	})

	t.Run("エピック自身は置けない", func(t *testing.T) {
		d := base(devTicket{Title: "まとまり", Type: "epic", Staged: true})
		err := d.validate()
		if err == nil {
			t.Fatal("エラーになるはず")
		}
		if !strings.Contains(err.Error(), "エピック") {
			t.Errorf("エラーに理由が無い: %v", err)
		}
	})

	t.Run("廃止した種別は受け付けない", func(t *testing.T) {
		for _, typ := range []string{"bug", "phase", "wbs"} {
			d := base(devTicket{Title: "旧種別", Type: typ})
			err := d.validate()
			if err == nil {
				t.Fatalf("type=%q が通ってしまう（3値に減らした。DbDesign.md 6.6）", typ)
			}
			if !strings.Contains(err.Error(), "epic / story / task") {
				t.Errorf("type=%q のエラー文言が古い: %v", typ, err)
			}
		}
	})
}

// 7.6.3 の安全装置。いずれかに掛かったら何もせず終了する。
func TestCheckDevSeedAllowed(t *testing.T) {
	const localURL = "postgres://pb_app:secret@127.0.0.1:5432/pb?sslmode=disable"

	t.Run("PB_ALLOW_DEV_SEED が無ければ中止", func(t *testing.T) {
		t.Setenv(devSeedAllowEnv, "")
		if err := checkDevSeedAllowed(localURL); err == nil {
			t.Fatal("中止するはず")
		}
	})

	t.Run("1 以外の値でも中止", func(t *testing.T) {
		t.Setenv(devSeedAllowEnv, "true")
		if err := checkDevSeedAllowed(localURL); err == nil {
			t.Fatal("中止するはず")
		}
	})

	t.Run("許可されたホストなら通る", func(t *testing.T) {
		t.Setenv(devSeedAllowEnv, "1")
		for _, host := range []string{"127.0.0.1", "localhost", "db"} {
			url := "postgres://pb_app:secret@" + host + ":5432/pb"
			if err := checkDevSeedAllowed(url); err != nil {
				t.Errorf("%s は許可されるはず: %v", host, err)
			}
		}
	})

	t.Run("それ以外のホストは中止", func(t *testing.T) {
		t.Setenv(devSeedAllowEnv, "1")
		for _, url := range []string{
			"postgres://pb_app:secret@db.example.com:5432/pb",
			"postgres://pb_app:secret@10.0.0.5:5432/pb",
			"postgres://pb_app:secret@localhost.example.com:5432/pb",
		} {
			if err := checkDevSeedAllowed(url); err == nil {
				t.Errorf("%s は中止するはず", url)
			}
		}
	})

	// 判定できない形式を「たぶん開発環境」に倒さない。
	t.Run("ホストを判定できなければ中止", func(t *testing.T) {
		t.Setenv(devSeedAllowEnv, "1")
		for _, url := range []string{
			"host=db.example.com user=pb_app dbname=pb",
			"postgres:///pb",
			"",
		} {
			if err := checkDevSeedAllowed(url); err == nil {
				t.Errorf("%q は中止するはず", url)
			}
		}
	})
}

// created_by は POST /projects と同じく project_admin を作成者とみなす。
func TestProjectCreator(t *testing.T) {
	actorIDs := map[string]string{
		"pm@example.com":     "01ACTORPM0000000000000000",
		"member@example.com": "01ACTORMEMBER000000000000",
	}
	p := devProject{Members: []devMember{
		{Email: "member@example.com", Role: "project_member"},
		{Email: "PM@example.com", Role: "project_admin"},
	}}
	if got := projectCreator(p, actorIDs); got != "01ACTORPM0000000000000000" {
		t.Errorf("project_admin の actor.id を返すはず。実際は %q", got)
	}

	// project_admin がいなければ空文字（＝ created_by は NULL）。
	none := devProject{Members: []devMember{{Email: "member@example.com", Role: "project_member"}}}
	if got := projectCreator(none, actorIDs); got != "" {
		t.Errorf("空文字を返すはず。実際は %q", got)
	}
}

func writeTempSeed(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dev-data.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("一時ファイルを作れない: %v", err)
	}
	return path
}
