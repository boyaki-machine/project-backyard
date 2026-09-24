package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGetAgentScopes は、4.5.9 の応答が 4.5.3 の検証と同じ定義を返すことを見る
// （ApiDesign.md 4.5.9）。
//
// **画面は写しを持たず、この口から引く**ので、見るのは「口が返すもの＝発行時に
// 受け付けるもの」である。
// **ずれると「チェックを付けたほうが狭くなる」**（scopes は絶対指定）。
func TestGetAgentScopes(t *testing.T) {
	rec := httptest.NewRecorder()
	agentHandler(agentFake(t)).getAgentScopes(rec,
		httptest.NewRequest(http.MethodGet, "/agent-scopes", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var got struct {
		Default   []string `json:"default"`
		Grantable []string `json:"grantable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if !slices.Equal(got.Default, agentDefaultScopes) {
		t.Errorf("default = %v, want %v", got.Default, agentDefaultScopes)
	}
	if !slices.Equal(got.Grantable, agentGrantableScopes) {
		t.Errorf("grantable = %v, want %v", got.Grantable, agentGrantableScopes)
	}

	// **口が返す組み合わせは、そのまま発行で受け付けられること。** 画面は
	// default ∪ grantable を送るので、ここが 422 になると押したときだけ落ちる。
	send := append(slices.Clone(got.Default), got.Grantable...)
	resolved, apiErr := resolveAgentScopes(send)
	if apiErr != nil {
		t.Fatalf("default ∪ grantable が発行で拒まれた: %v", apiErr)
	}
	if len(resolved) != len(send) {
		t.Errorf("発行で受け付けた件数 = %d, want %d", len(resolved), len(send))
	}
}

// TestClientHoldsNoDefaultScopes は、画面が既定スコープの写しを持っていないことを見る。
//
// **Go のテストからクライアントのソースを読む唯一の場所である。**
// 写しは権限を足すたびに2回続けて腐ったので、**戻ってきたら落とす。** 名前だけを
// 見るので、別名で書き戻したものは拾えない。
func TestClientHoldsNoDefaultScopes(t *testing.T) {
	const path = "../../../../client/src/lib/agents.ts"
	src, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("%s を読めない: %v", path, err)
	}
	if strings.Contains(string(src), "AGENT_DEFAULT_SCOPES") {
		t.Errorf("%s に AGENT_DEFAULT_SCOPES が戻っている。既定は GET /agent-scopes（ApiDesign.md 4.5.9）から引くこと", path)
	}
}
