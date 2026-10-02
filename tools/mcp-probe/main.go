// mcp-probe は、MCP クライアントの挙動を確かめるための検証用サーバである（pb-158）。
//
// PB 本体ではない。PB が「状態の変化をエージェントに知らせる」方式を選ぶために、
// 主要なクライアント（Claude Code・Claude Desktop・Codex・VS Code の Copilot）が
// 次のことをどう扱うかを、同じ相手で比べる。
//
//  1. サーバからの通知を受け取るか（旧方式の GET の SSE、新方式の subscriptions/listen）
//  2. 待機中に通知が届いたとき、モデルの発話が始まるか
//  3. 応答を返さないツール呼び出しを、何分まで待つか
//  4. 待ち受けを繰り返したときの消費
//  5. 指示で待ち受けの繰り返しを維持できるか
//
// 話す方式は2つ。旧方式（initialize のハンドシェイク。2025-11-25 以前）と、
// 新方式（リクエストごとの _meta と server/discover。2026-07-28）である。
// 届いたリクエストはすべて記録に残すので、各クライアントがどちらで話すかも分かる。
//
// 状態の変化は、外から POST /admin/event で起こす。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	modernVersion = "2026-07-28"
	stateURI      = "probe://state"
)

var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// ---- 状態 ----------------------------------------------------------------

type event struct {
	Counter int       `json:"counter"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

type hub struct {
	mu      sync.Mutex
	counter int
	last    *event
	changed chan struct{} // 次の出来事で閉じる。待ち受けはこれを待つ
	streams map[*stream]struct{}
	waiters int
}

type stream struct {
	kind   string // "legacy-get" または "modern-listen"
	ch     chan []byte
	filter func(method string) bool
	client string
}

func newHub() *hub {
	return &hub{changed: make(chan struct{}), streams: map[*stream]struct{}{}}
}

func (h *hub) snapshot() (int, *event, chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counter, h.last, h.changed
}

// fire は出来事を1つ起こす。待ち受けを起こし、開いているストリームへ通知を流す。
func (h *hub) fire(msg string, kinds []string) event {
	h.mu.Lock()
	h.counter++
	ev := event{Counter: h.counter, Message: msg, At: time.Now().UTC()}
	h.last = &ev
	close(h.changed)
	h.changed = make(chan struct{})
	targets := make([]*stream, 0, len(h.streams))
	for s := range h.streams {
		targets = append(targets, s)
	}
	h.mu.Unlock()

	for _, kind := range kinds {
		var method string
		var params map[string]any
		switch kind {
		case "resource":
			method, params = "notifications/resources/updated", map[string]any{"uri": stateURI}
		case "message":
			method, params = "notifications/message", map[string]any{
				"level": "info", "logger": "pb",
				"data": fmt.Sprintf("PB の状態が変わった（#%d）：%s", ev.Counter, msg),
			}
		case "tools":
			method, params = "notifications/tools/list_changed", map[string]any{}
		default:
			continue
		}
		for _, s := range targets {
			if !s.filter(method) {
				continue
			}
			p := map[string]any{}
			for k, v := range params {
				p[k] = v
			}
			if s.kind == "modern-listen" {
				p["_meta"] = map[string]any{"io.modelcontextprotocol/subscriptionId": json.RawMessage(s.client)}
			}
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": p})
			select {
			case s.ch <- b:
			default: // 詰まったストリームは飛ばす
			}
		}
	}
	return ev
}

func (h *hub) add(s *stream) {
	h.mu.Lock()
	h.streams[s] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(s *stream) {
	h.mu.Lock()
	delete(h.streams, s)
	h.mu.Unlock()
}

// ---- 記録 ----------------------------------------------------------------

type recorder struct {
	mu  sync.Mutex
	out io.Writer
}

func (r *recorder) write(fields map[string]any) {
	fields["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(fields)
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(r.out, string(b))
}

// ---- JSON-RPC ------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type server struct {
	hub       *hub
	rec       *recorder
	keepalive time.Duration
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "待ち受けるアドレス（localhost に限る）")
	logPath := flag.String("log", "", "記録（JSON Lines）の書き出し先。空なら標準エラー")
	keepalive := flag.Duration("keepalive", 15*time.Second, "SSE のコメント行を送る間隔")
	flag.Parse()

	var out io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		out = f
	}
	s := &server{hub: newHub(), rec: &recorder{out: out}, keepalive: *keepalive}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("POST /admin/event", s.handleEvent)
	mux.HandleFunc("GET /admin/state", s.handleState)
	log.Printf("mcp-probe: http://%s/mcp で待ち受ける（記録：%s）", *addr, map[bool]string{true: *logPath, false: "標準エラー"}[*logPath != ""])
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func (s *server) handleEvent(w http.ResponseWriter, r *http.Request) {
	msg := r.URL.Query().Get("msg")
	if msg == "" {
		msg = "評価が完了した"
	}
	kinds := []string{"resource", "message", "tools"}
	if k := r.URL.Query().Get("kinds"); k != "" {
		kinds = strings.Split(k, ",")
	}
	ev := s.hub.fire(msg, kinds)
	s.rec.write(map[string]any{"dir": "admin", "event": ev, "kinds": kinds})
	writeJSON(w, http.StatusOK, ev)
}

func (s *server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.hub.mu.Lock()
	streams := []map[string]string{}
	for st := range s.hub.streams {
		streams = append(streams, map[string]string{"kind": st.kind, "client": st.client})
	}
	body := map[string]any{"counter": s.hub.counter, "last": s.hub.last, "streams": streams, "waiters": s.hub.waiters}
	s.hub.mu.Unlock()
	writeJSON(w, http.StatusOK, body)
}

func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	hdr := map[string]string{}
	for _, k := range []string{"MCP-Protocol-Version", "Mcp-Method", "Mcp-Name", "Mcp-Session-Id", "User-Agent", "Accept", "Last-Event-ID"} {
		if v := r.Header.Get(k); v != "" {
			hdr[k] = v
		}
	}
	switch r.Method {
	case http.MethodGet:
		s.rec.write(map[string]any{"dir": "in", "http": "GET", "headers": hdr})
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Error(w, "SSE 以外の GET は受けない", http.StatusMethodNotAllowed)
			return
		}
		s.serveStream(w, r, &stream{kind: "legacy-get", ch: make(chan []byte, 16), filter: func(string) bool { return true }, client: r.Header.Get("User-Agent")}, nil)
		return
	case http.MethodPost:
	default:
		s.rec.write(map[string]any{"dir": "in", "http": r.Method, "headers": hdr})
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "本文が読めない", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.rec.write(map[string]any{"dir": "in", "http": "POST", "headers": hdr, "parse_error": err.Error()})
		writeJSON(w, http.StatusOK, rpcError(nil, -32700, "Parse error"))
		return
	}
	var params map[string]any
	_ = json.Unmarshal(req.Params, &params)
	meta, _ := params["_meta"].(map[string]any)
	modern := meta["io.modelcontextprotocol/protocolVersion"] != nil
	s.rec.write(map[string]any{
		"dir": "in", "http": "POST", "headers": hdr, "method": req.Method, "id": req.ID,
		"era": map[bool]string{true: "modern", false: "legacy"}[modern], "params": params,
	})

	if len(req.ID) == 0 { // 通知
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		want, _ := params["protocolVersion"].(string)
		ver := legacyVersions[0]
		for _, v := range legacyVersions {
			if v == want {
				ver = v
			}
		}
		s.reply(w, req, modern, map[string]any{
			"protocolVersion": ver,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{"subscribe": true, "listChanged": true},
				"logging":   map[string]any{},
			},
			"serverInfo":   map[string]any{"name": "pb-mcp-probe", "version": "0.1.0"},
			"instructions": "PB の状態変化の通知を確かめる検証用サーバ。probe_wait で状態の変化を待てる。",
		})
	case "server/discover":
		s.reply(w, req, true, map[string]any{
			"supportedVersions": append([]string{modernVersion}, legacyVersions...),
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": true},
				"resources": map[string]any{"subscribe": true, "listChanged": true},
			},
			"serverInfo": map[string]any{"name": "pb-mcp-probe", "version": "0.1.0"},
		})
	case "ping":
		s.reply(w, req, modern, map[string]any{})
	case "tools/list":
		s.reply(w, req, modern, cacheable(modern, map[string]any{"tools": tools()}))
	case "tools/call":
		s.callTool(w, r, req, params, modern)
	case "resources/list":
		s.reply(w, req, modern, cacheable(modern, map[string]any{"resources": []any{map[string]any{
			"uri": stateURI, "name": "state", "mimeType": "application/json",
			"description": "PB の状態（検証用）。更新されると notifications/resources/updated が出る",
		}}}))
	case "resources/read":
		n, last, _ := s.hub.snapshot()
		b, _ := json.Marshal(map[string]any{"counter": n, "last": last})
		s.reply(w, req, modern, cacheable(modern, map[string]any{"contents": []any{map[string]any{
			"uri": stateURI, "mimeType": "application/json", "text": string(b),
		}}}))
	case "resources/subscribe", "resources/unsubscribe":
		s.reply(w, req, modern, map[string]any{})
	case "subscriptions/listen":
		s.listen(w, r, req, params)
	default:
		status := http.StatusOK
		if modern {
			status = http.StatusNotFound // 2026-07-28：知らないメソッドは 404 と -32601
		}
		writeJSON(w, status, rpcError(req.ID, -32601, "Method not found"))
	}
}

func tools() []any {
	return []any{
		map[string]any{
			"name": "probe_wait",
			"description": "PB の状態が変わるまで待つ（検証用）。状態が変わるか、timeout_seconds が過ぎたら返る。" +
				"since に前回の counter を渡すと、それより後の変化だけを待つ。返った結果の next に次にすることが書いてある。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout_seconds":           map[string]any{"type": "integer", "minimum": 1, "maximum": 3600, "description": "待つ上限（秒）。既定 60"},
					"since":                     map[string]any{"type": "integer", "description": "前回受け取った counter。省略すると今の値"},
					"progress_interval_seconds": map[string]any{"type": "integer", "minimum": 0, "description": "0 より大きければ、この間隔で進捗の通知を送る（progressToken があるときだけ）"},
				},
			},
		},
		map[string]any{
			"name":        "probe_status",
			"description": "PB の状態（検証用）を返す。counter と最後の出来事",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func (s *server) callTool(w http.ResponseWriter, r *http.Request, req rpcRequest, params map[string]any, modern bool) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	switch name {
	case "probe_status":
		n, last, _ := s.hub.snapshot()
		s.reply(w, req, modern, textResult(map[string]any{"counter": n, "last": last}))
	case "probe_wait":
		timeout := intArg(args, "timeout_seconds", 60)
		if timeout > 3600 {
			timeout = 3600
		}
		n, _, changed := s.hub.snapshot()
		since := intArg(args, "since", n)
		interval := intArg(args, "progress_interval_seconds", 0)
		var token any
		if meta, ok := params["_meta"].(map[string]any); ok {
			token = meta["progressToken"]
		}
		s.wait(w, r, req, modern, since, changed, time.Duration(timeout)*time.Second, time.Duration(interval)*time.Second, token)
	default:
		writeJSON(w, http.StatusOK, rpcError(req.ID, -32602, "Unknown tool: "+name))
	}
}

// wait は待ち受けの本体である。始まり・終わり・中断を経過時間つきで記録する——
// クライアントが何分で諦めるか（検証項目3）は、中断の行の elapsed で分かる。
func (s *server) wait(w http.ResponseWriter, r *http.Request, req rpcRequest, modern bool, since int, changed chan struct{}, timeout, interval time.Duration, token any) {
	start := time.Now()
	s.hub.mu.Lock()
	s.hub.waiters++
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		s.hub.waiters--
		s.hub.mu.Unlock()
	}()
	s.rec.write(map[string]any{"dir": "wait", "phase": "start", "id": req.ID, "since": since, "timeout_s": timeout.Seconds()})

	var flusher http.Flusher
	streaming := interval > 0 && token != nil
	if streaming {
		flusher, _ = w.(http.Flusher)
		startSSE(w)
	}
	var tick <-chan time.Time
	if streaming {
		t := time.NewTicker(interval)
		defer t.Stop()
		tick = t.C
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		if n, _, _ := s.hub.snapshot(); n > since {
			break
		}
		select {
		case <-changed:
			_, _, changed = s.hub.snapshot()
			continue
		case <-deadline.C:
		case <-r.Context().Done():
			s.rec.write(map[string]any{"dir": "wait", "phase": "cancelled", "id": req.ID, "elapsed_s": time.Since(start).Seconds(), "err": r.Context().Err().Error()})
			return
		case <-tick:
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{
				"progressToken": token, "progress": time.Since(start).Seconds(), "message": "PB の状態の変化を待っている",
			}})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
			flusher.Flush()
			continue
		}
		break
	}

	n, last, _ := s.hub.snapshot()
	fired := n > since
	next := "状態はまだ変わっていない。もう一度 probe_wait を呼んで待ち続ける（since はこの counter）"
	if fired {
		next = "状態が変わった。last.message を読んで、それに応じて動く"
	}
	result := textResult(map[string]any{"event": fired, "counter": n, "last": last, "elapsed_seconds": int(time.Since(start).Seconds()), "next": next})
	s.rec.write(map[string]any{"dir": "wait", "phase": "end", "id": req.ID, "event": fired, "elapsed_s": time.Since(start).Seconds()})
	if streaming {
		b, _ := json.Marshal(rpcResult(req.ID, withResultType(modern, result)))
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		flusher.Flush()
		return
	}
	s.reply(w, req, modern, result)
}

// listen は 2026-07-28 の subscriptions/listen である。受けると申し出た種類だけを流す。
func (s *server) listen(w http.ResponseWriter, r *http.Request, req rpcRequest, params map[string]any) {
	filt, _ := params["notifications"].(map[string]any)
	ack := map[string]any{}
	wantTools, _ := filt["toolsListChanged"].(bool)
	if wantTools {
		ack["toolsListChanged"] = true
	}
	wantRes := false
	if subs, ok := filt["resourceSubscriptions"].([]any); ok {
		for _, u := range subs {
			if u == stateURI {
				wantRes = true
				ack["resourceSubscriptions"] = []string{stateURI}
			}
		}
	}
	sub := string(req.ID)
	st := &stream{kind: "modern-listen", ch: make(chan []byte, 16), client: sub, filter: func(m string) bool {
		return (m == "notifications/tools/list_changed" && wantTools) || (m == "notifications/resources/updated" && wantRes)
	}}
	first, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{
		"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": json.RawMessage(req.ID)}, "notifications": ack,
	}})
	s.serveStream(w, r, st, first)
}

func (s *server) serveStream(w http.ResponseWriter, r *http.Request, st *stream, first []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "ストリームを張れない", http.StatusInternalServerError)
		return
	}
	startSSE(w)
	if first != nil {
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", first)
	}
	flusher.Flush()
	s.hub.add(st)
	defer s.hub.remove(st)
	start := time.Now()
	s.rec.write(map[string]any{"dir": "stream", "phase": "open", "kind": st.kind, "client": st.client})
	ka := time.NewTicker(s.keepalive)
	defer ka.Stop()
	for {
		select {
		case b := <-st.ch:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
			flusher.Flush()
			s.rec.write(map[string]any{"dir": "out", "kind": st.kind, "body": json.RawMessage(b)})
		case <-ka.C:
			fmt.Fprint(w, ":\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			s.rec.write(map[string]any{"dir": "stream", "phase": "closed", "kind": st.kind, "elapsed_s": time.Since(start).Seconds()})
			return
		}
	}
}

// ---- 応答の形 ------------------------------------------------------------

func (s *server) reply(w http.ResponseWriter, req rpcRequest, modern bool, result map[string]any) {
	writeJSON(w, http.StatusOK, rpcResult(req.ID, withResultType(modern, result)))
}

func withResultType(modern bool, result map[string]any) map[string]any {
	if modern {
		result["resultType"] = "complete"
	}
	return result
}

func cacheable(modern bool, result map[string]any) map[string]any {
	if modern {
		result["ttlMs"] = 0
		result["cacheScope"] = "private"
	}
	return result
}

func textResult(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func intArg(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}
