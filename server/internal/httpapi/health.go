// Package httpapi は REST API のルータと、/api/v1 の外にある
// エンドポイントを持つ（ApiDesign.md 2章）。
//
// /api/v1 配下のエンドポイントは v1 パッケージにある（Design.md 4.1）。
package httpapi

import (
	"encoding/json"
	"net/http"
)

// HealthPath は唯一 /api/v1 の外に置くエンドポイント（ApiDesign.md 2.11）。
// 監視・オーケストレータから叩くものであり、APIのバージョニングに従わせない。
const HealthPath = "/healthcheck"

// healthResponse は 2.11 の固定応答。
type healthResponse struct {
	Status string `json:"status"`
	// Version は PB_HEALTH_SHOW_VERSION=true のときだけ出す。
	Version string `json:"version,omitempty"`
}

// health は認証不要・副作用なし・DB非依存の固定応答を返す。
//
// DBの疎通を見ないのは、DB断がプロセスの再起動では復旧しないためである。
// liveness に含めると不要な再起動ループを招く（Design.md 10.2）。
func health(version string, showVersion bool) http.HandlerFunc {
	body := healthResponse{Status: "OK"}
	if showVersion {
		body.Version = version
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// v1.WriteJSON を使わないのは、/healthcheck が「唯一 /api/v1 の外に置く
		// エンドポイント」（ApiDesign.md 2.11）であり、ルータを持つ本パッケージが
		// v1 を import する向きを保つため（逆向きにすると循環する）。
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}
}
