package mcp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// HTTP は /mcp/{key} の口を返す。プロトコルの層は公式 SDK に任せる（Design.md 8.4 / 8.6。pb-258）。
//
// **SDK が引き受けるのはプロトコルだけである。** 版の交渉（旧方式の initialize と、
// 2026-07-28 の server/discover の両方）、ヘッダの検証、進捗の通知の運び方。
// **ツールの中身は今までどおり REST を内部で叩く**（8.1。tools.go の call）。
//
// **状態を持たない（Stateless）で動かす。** 2026-07-28 はセッションを持たず、
// 旧方式のクライアントにもリクエストごとの一時的なセッションで応じる。GET と DELETE は
// 405 になる——サーバ発の通知を持たないのは今までと同じである（10章：通知はモデルを起こさない）。
func (h *Handler) HTTP() http.Handler {
	inner := sdk.NewStreamableHTTPHandler(h.serverFor, &sdk.StreamableHTTPOptions{
		Stateless: true,
		// **DNS rebinding の対策を切る。** SDK の判定は Host が `localhost` そのものか
		// ループバックの IP かしか見ないので、stg の `pb.localhost:8081` が 403 になる。
		// PB の /mcp は Bearer トークンしか受けない（Cookie を拒む。8.3）ので、
		// 悪意のあるページから再束縛で届く要求には資格情報が載らない。
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        maxBodyBytes,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// **Cookie では通さない**（Design.md 8.3）。SDK の前で判定する。
		p := auth.PrincipalFromContext(r.Context())
		if p == nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
				errors.New("/mcp が Authenticate の後に置かれていない")))
			return
		}
		if p.Source != auth.SourceBearer {
			apierr.Write(w, r, apierr.New(apierr.Unauthenticated).WithCause(
				errors.New("MCP は Bearer トークンだけを受ける（Design.md 8.3）")))
			return
		}
		// **書き込みの期限を、待ち受けの最長に余裕を足した長さへ延ばす。** サーバ全体の
		// WriteTimeout は60秒で、pb_wait（最長4分。wait.go）がその途中で切られる——実機で
		// 3回続けて60秒で切れた（pb-158）。外さずに延ばすのは、止まったストリームを
		// いつまでも抱えないためである。
		if rc := http.NewResponseController(w); rc != nil {
			_ = rc.SetWriteDeadline(time.Now().Add(waitWithProgress + time.Minute))
		}
		inner.ServeHTTP(w, r)
	})
}

// serverFor はリクエストごとに SDK のサーバを組み立てる。
//
// **ツールの処理は元の HTTP リクエストを要る**——REST の内部呼び出しに
// Authorization を引き継ぎ、URL からプロジェクトキーを取るためである（8.3 / 8.4）。
// SDK はリクエストを受けるたびにこの関数を呼ぶので、そのリクエストを包んだ処理を登録する。
//
// **知らないツール名と壊れた引数は JSON-RPC のエラー**（呼び出し側の不具合。知らない名前は
// SDK が -32602 で返す）、**REST が返した 4xx / 5xx は isError のツール結果**（呼び出しの
// 結果。モデルが読んで利用者へ伝える）である（Design.md 8.4）。
func (h *Handler) serverFor(r *http.Request) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: ServerName, Version: h.version}, nil)
	key := chi.URLParam(r, middleware.ProjectKeyURLParam)
	for i := range h.tools {
		t := &h.tools[i]
		if !h.toolVisible(t.Name) {
			continue
		}
		s.AddTool(&sdk.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema},
			func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				if key == "" {
					return nil, &sdkjsonrpc.Error{Code: codeInternalError,
						Message: "ルートに {" + middleware.ProjectKeyURLParam + "} が無い"}
				}
				var res toolResult
				var rpcErr *rpcError
				// ツールの実行中に受けた REST の応答の一時停止のヘッダを、ここで受け取る（pb-309）。
				note := &pausedNote{}
				rr := r.WithContext(context.WithValue(r.Context(), pausedNoteKey{}, note))
				if t.long != nil {
					res, rpcErr = t.long(ctx, h, rr, key, req.Params.Arguments, progressOf(ctx, req))
				} else {
					res, rpcErr = t.call(h, rr, key, req.Params.Arguments)
				}
				if rpcErr != nil {
					// 引数の誤りなどは今までどおり JSON-RPC のエラーで返す（8.4 の表）。
					return nil, &sdkjsonrpc.Error{Code: int64(rpcErr.Code), Message: rpcErr.Message}
				}
				return toSDKResult(note.attach(res)), nil
			})
	}
	return s
}

// toSDKResult は今のツールの結果を SDK の型へ写す。
func toSDKResult(res toolResult) *sdk.CallToolResult {
	out := &sdk.CallToolResult{IsError: res.IsError}
	for _, c := range res.Content {
		out.Content = append(out.Content, &sdk.TextContent{Text: c.Text})
	}
	return out
}

// progressOf は、呼び出しに progressToken があれば進捗の通知を送る関数を返す。無ければ nil。
//
// **progressToken の有無で、待ち受けの長さが変わる**（wait.go）。付けてくるのは Claude Code・
// Codex・Copilot で、Claude Desktop（mcp-remote 経由）は付けない（Delegation.md 10.2）。
func progressOf(ctx context.Context, req *sdk.CallToolRequest) progressFunc {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return nil
	}
	return func(elapsed time.Duration) {
		_ = req.Session.NotifyProgress(ctx, &sdk.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      elapsed.Seconds(),
			Message:       "PB の変化を待っている",
		})
	}
}
