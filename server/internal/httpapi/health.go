package httpapi

import "net/http"

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
		WriteJSON(w, http.StatusOK, body)
	}
}
