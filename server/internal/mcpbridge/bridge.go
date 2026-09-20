// Package mcpbridge forwards a local stdio MCP session to a PB HTTP MCP endpoint.
package mcpbridge

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
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
		response, err := forward(ctx, client, endpoint.String(), token, line)
		if err != nil {
			return err
		}
		if _, err := out.Write(append(response, '\n')); err != nil {
			return fmt.Errorf("Codexへ応答を書けない: %w", err)
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

func forward(ctx context.Context, client *http.Client, endpoint, token string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("PB要求を作れない: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PB MCPへ接続できない: %w", err)
	}
	defer res.Body.Close()
	body, err = io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("PB MCPの応答を読めない: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("PB MCPがHTTP %dを返しました", res.StatusCode)
	}
	return body, nil
}

func bytesTrim(b []byte) []byte { return []byte(strings.TrimSpace(string(b))) }
