package auth

import (
	"slices"
	"testing"
)

// システムロールとプロジェクトロールの権限は和集合になる（Design.md 6.4.1）。
func TestEffectivePermissionsUnion(t *testing.T) {
	got := EffectivePermissions(
		[]string{"project.view", "ticket.view"},
		[]string{"ticket.close", "ticket.view"},
		nil,
	)
	want := []string{"project.view", "ticket.close", "ticket.view"}
	if !slices.Equal(got, want) {
		t.Errorf("got = %v, want %v", got, want)
	}
}

// 結果は昇順・重複なしで返る（応答とテストの比較を安定させるため）。
func TestEffectivePermissionsSortedAndDeduped(t *testing.T) {
	got := EffectivePermissions(
		[]string{"ticket.view", "project.view", "ticket.view"},
		[]string{"project.view"},
		nil,
	)
	want := []string{"project.view", "ticket.view"}
	if !slices.Equal(got, want) {
		t.Errorf("got = %v, want %v", got, want)
	}
}

// scopes が空なら縮小しない（Design.md 6.4.1「空スライスは絞り込みなし」）。
func TestEffectivePermissionsEmptyScopesDoNotNarrow(t *testing.T) {
	role := []string{"project.view", "ticket.view"}
	for _, scopes := range [][]string{nil, {}} {
		got := EffectivePermissions(role, nil, scopes)
		if !slices.Equal(got, role) {
			t.Errorf("scopes=%v: got = %v, want %v", scopes, got, role)
		}
	}
}

// scopes は上限であり、ロールが持たない権限を足すことはできない。
func TestEffectivePermissionsScopesCannotGrant(t *testing.T) {
	got := EffectivePermissions(
		[]string{"ticket.view"},
		nil,
		[]string{"ticket.view", "user.manage"},
	)
	want := []string{"ticket.view"}
	if !slices.Equal(got, want) {
		t.Errorf("got = %v, want %v（スコープが権限を増やしている）", got, want)
	}
}

// scopes は縮小する。
func TestEffectivePermissionsScopesNarrow(t *testing.T) {
	got := EffectivePermissions(
		[]string{"project.view", "ticket.view", "ticket.close"},
		[]string{"ticket.delete"},
		[]string{"ticket.view", "ticket.close"},
	)
	want := []string{"ticket.close", "ticket.view"}
	if !slices.Equal(got, want) {
		t.Errorf("got = %v, want %v", got, want)
	}
}

// 権限キーと対応しない語彙（Design.md 6.5 のエージェント既定スコープ）は
// 何も通さない。対応表が未定義のうちは安全側へ倒す。
func TestEffectivePermissionsUnknownScopeVocabularyGrantsNothing(t *testing.T) {
	got := EffectivePermissions(
		[]string{"ticket.view", "ticket.create"},
		nil,
		[]string{"ticket:read", "note:write"},
	)
	if len(got) != 0 {
		t.Errorf("got = %v, want []（未定義の語彙で権限が通っている）", got)
	}
}

// システムロールを持たないアクター（エージェント）は空になる。
func TestEffectivePermissionsNoRole(t *testing.T) {
	if got := EffectivePermissions(nil, nil, nil); len(got) != 0 {
		t.Errorf("got = %v, want []", got)
	}
}

func TestHasPermission(t *testing.T) {
	effective := EffectivePermissions([]string{"ticket.view", "project.view"}, nil, nil)
	if !HasPermission(effective, "ticket.view") {
		t.Error("持っているはずの権限が見つからない")
	}
	if HasPermission(effective, "user.manage") {
		t.Error("持っていない権限が通った")
	}
}
