package mcp

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// NewSDK はプロトコルの層を公式 SDK に任せたハンドラを返す（Design.md 8.4 / 8.6。pb-258）。
//
// **SDK が引き受けるのはプロトコルだけである。** 版の交渉（旧方式の initialize と、
// 2026-07-28 の server/discover の両方）、ヘッダの検証、進捗の通知の運び方。
// **ツールの中身は今までどおり REST を内部で叩く**（8.1。tools.go の call）。
//
// **状態を持たない（Stateless）で動かす。** 2026-07-28 はセッションを持たず、
// 旧方式のクライアントにもリクエストごとの一時的なセッションで応じる。GET と DELETE は
// 405 になる——サーバ発の通知を持たないのは今までと同じである（10章：通知はモデルを起こさない）。
func NewSDK(rest http.Handler, version string) http.Handler {
	h := New(rest, version)
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
		inner.ServeHTTP(w, r)
	})
}

// serverFor はリクエストごとに SDK のサーバを組み立てる。
//
// **ツールの処理は元の HTTP リクエストを要る**——REST の内部呼び出しに
// Authorization を引き継ぎ、URL からプロジェクトキーを取るためである（8.3 / 8.4）。
// SDK はリクエストを受けるたびにこの関数を呼ぶので、そのリクエストを包んだ処理を登録する。
func (h *Handler) serverFor(r *http.Request) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: ServerName, Version: h.version}, nil)
	key := chi.URLParam(r, middleware.ProjectKeyURLParam)
	for i := range h.tools {
		t := &h.tools[i]
		s.AddTool(&sdk.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema},
			func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				if key == "" {
					return nil, &sdkjsonrpc.Error{Code: codeInternalError,
						Message: "ルートに {" + middleware.ProjectKeyURLParam + "} が無い"}
				}
				res, rpcErr := t.call(h, r, key, req.Params.Arguments)
				if rpcErr != nil {
					// 引数の誤りなどは今までどおり JSON-RPC のエラーで返す（8.4 の表）。
					return nil, &sdkjsonrpc.Error{Code: int64(rpcErr.Code), Message: rpcErr.Message}
				}
				return toSDKResult(res), nil
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
