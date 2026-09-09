package v1

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// clientScopesPath は画面が持つ既定スコープの写し（pb-90）。
//
// **Go のテストからクライアントのソースを読む唯一の場所である。** 常道ではないが、
// **写しのずれは片方だけを見ても分からない**——両方を同時に開ける場所が要る。
// 写しそのものを無くす案は pb-93 にある。
const clientScopesPath = "../../../../client/src/lib/agents.ts"

// clientScopesRe は AGENT_DEFAULT_SCOPES の配列本体を取り出す。
var clientScopesRe = regexp.MustCompile(
	`(?s)export const AGENT_DEFAULT_SCOPES = \[(.*?)\] as const`)

// clientScopeItemRe は配列の中の 'key' を1件ずつ拾う。
var clientScopeItemRe = regexp.MustCompile(`'([^']+)'`)

// TestAgentDefaultScopesMatchClient は、サーバの既定スコープと画面の写しが
// 一致することを見る（ApiDesign.md 4.5.3。pb-90）。
//
// **ずれると「チェックを付けたほうが狭くなる」。** 画面は doc.edit の
// チェックが入ったときだけ scopes を送り、その中身がこの写しである。
// scopes は絶対指定なので（resolveAgentScopes）、**写しが1件足りないと、
// doc.edit つきで発行したトークンだけがその1件を失う。** 発行は成功し、
// 画面にも何も出ないため、エージェントが 403 を踏むまで誰も気づかない。
//
// **実際に2回続けて起きた**——ticket.reference.edit（0027／pb-68）と
// ticket.self_edit（0029／pb-75）。どちらも権限を足した本人が写しを直していない。
func TestAgentDefaultScopesMatchClient(t *testing.T) {
	src, err := os.ReadFile(filepath.FromSlash(clientScopesPath))
	if err != nil {
		t.Fatalf("画面の写しを読めない（%s）: %v", clientScopesPath, err)
	}

	m := clientScopesRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s に AGENT_DEFAULT_SCOPES の配列が見つからない。"+
			"定数名か書き方を変えたなら、この検査も一緒に直すこと", clientScopesPath)
	}

	var client []string
	for _, item := range clientScopeItemRe.FindAllSubmatch(m[1], -1) {
		client = append(client, string(item[1]))
	}

	// **並びまで見る。** どちらも昇順で持つと決めてあり（agentDefaultScopes の
	// コメント）、並びが揃っていれば応答と突き合わせるときに並べ替えが要らない。
	if len(client) != len(agentDefaultScopes) {
		t.Fatalf("既定スコープの件数が違う: サーバ %d 件 / 画面 %d 件\n"+
			"  サーバ: %v\n  画面  : %v\n"+
			"  権限を足したら %s も直すこと",
			len(agentDefaultScopes), len(client), agentDefaultScopes, client, clientScopesPath)
	}
	for i, want := range agentDefaultScopes {
		if client[i] != want {
			t.Errorf("既定スコープの %d 件目が違う: サーバ %q / 画面 %q\n"+
				"  権限を足したら %s も直すこと（昇順で持つ）",
				i+1, want, client[i], clientScopesPath)
		}
	}
}
