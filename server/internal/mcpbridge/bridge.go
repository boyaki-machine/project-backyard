// Package mcpbridge forwards a local stdio MCP session to a PB HTTP MCP endpoint.
package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is deliberately small: the bridge is not a general HTTP proxy.
type Config struct {
	URL          string
	TokenEnvName string
	CAFile       string
}

// CAFileEnv is intentionally separate from the PB token.  It lets a Codex
// configuration stay portable while the trust anchor remains a local file.
const CAFileEnv = "PB_MCP_CA_FILE"

// Run copies one JSON-RPC message per line between stdio and PB.
func Run(ctx context.Context, in io.Reader, out io.Writer, cfg Config) error {
	endpoint, err := validate(cfg)
	if err != nil {
		return err
	}
	token := os.Getenv(cfg.TokenEnvName)
	if token == "" {
		return fmt.Errorf("環境変数 %s にPBトークンがありません", cfg.TokenEnvName)
	}
	client, err := newClient(cfg.CAFile)
	if err != nil {
		return err
	}

	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 64*1024), 10<<20)
	for s.Scan() {
		line := bytesTrim(s.Bytes())
		if len(line) == 0 {
			continue
		}
		contentType, body, err := forward(ctx, client, endpoint.String(), token, line)
		if err != nil {
			return err
		}
		messages, err := decodeMessages(contentType, body)
		if err != nil {
			return fmt.Errorf("PB MCPの応答を解釈できない: %w", err)
		}
		for _, m := range messages {
			// stdioは1行1メッセージ。SSEの複数data行をまとめたJSONも1行に戻す。
			var compact bytes.Buffer
			if err := json.Compact(&compact, m); err != nil {
				return fmt.Errorf("PB MCPの応答がJSONではない: %w", err)
			}
			if _, err := out.Write(append(compact.Bytes(), '\n')); err != nil {
				return fmt.Errorf("Codexへ応答を書けない: %w", err)
			}
		}
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("Codexからの要求を読めない: %w", err)
	}
	return nil
}

func validate(cfg Config) (*url.URL, error) {
	if cfg.TokenEnvName == "" {
		return nil, errors.New("--token-env を指定してください")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("--url には https のPB MCP URLを指定してください")
	}
	return u, nil
}

func newClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		caFile = os.Getenv(CAFileEnv)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("CA証明書を読めない: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA証明書に有効なPEMがありません")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}, nil
}

func forward(ctx context.Context, client *http.Client, endpoint, token string, body []byte) (contentType string, _ []byte, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", nil, fmt.Errorf("PB要求を作れない: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("PB MCPへ接続できない: %w", err)
	}
	defer res.Body.Close()
	body, err = io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return "", nil, fmt.Errorf("PB MCPの応答を読めない: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", nil, fmt.Errorf("PB MCPがHTTP %dを返しました", res.StatusCode)
	}
	return res.Header.Get("Content-Type"), body, nil
}

func bytesTrim(b []byte) []byte { return bytes.TrimSpace(b) }

// decodeMessages は PB MCP の応答本文から、Codex へそのまま1行ずつ渡す
// JSON-RPCメッセージを取り出す。
//
// **公式 SDK は既定で text/event-stream を返す**（通知を運べるようにするため。
// pb-262）。bridge は1行＝1メッセージの単純な中継なので、SSE であれば
// 各イベントの data を1メッセージとして取り出し、素の JSON であればそのまま
// 1件として扱う。
func decodeMessages(contentType string, body []byte) ([][]byte, error) {
	mediaType, _, _ := strings.Cut(contentType, ";")
	if strings.TrimSpace(mediaType) == "text/event-stream" {
		return decodeSSEMessages(body), nil
	}
	trimmed := bytesTrim(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	return [][]byte{trimmed}, nil
}

// decodeSSEMessages は SSE 本文からイベントごとの data フィールドを取り出す。
// `:` で始まる行（コメント）と、data を持たないイベントは読み飛ばす。
func decodeSSEMessages(body []byte) [][]byte {
	var messages [][]byte
	// SSE は CRLF・LF・CR のいずれも改行として扱う（HTML Standard 9.2.5）。
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, rawEvent := range strings.Split(normalized, "\n\n") {
		var dataLines []string
		for _, line := range strings.Split(rawEvent, "\n") {
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				dataLines = append(dataLines, strings.TrimPrefix(data, " "))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		messages = append(messages, []byte(strings.Join(dataLines, "\n")))
	}
	return messages
}
