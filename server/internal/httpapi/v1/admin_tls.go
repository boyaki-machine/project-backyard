// TLS 証明書API（ApiDesign.md 11.4〜11.6）。pb-3。
//
//	GET    /api/v1/admin/tls/certificates       11.4
//	POST   /api/v1/admin/tls/certificates       11.5
//	DELETE /api/v1/admin/tls/certificates/:id   11.6
//
// **必要権限はいずれも system.settings**（11章と同じ）。
//
// **Design.md 10.3 の第3層である。** 第2層（app_setting）と別のエンドポイントに
// したのは、値が秘密である点と有効期間で選ばれる点が違うためである。
//
// **秘密鍵はどの応答にも現れない。** 2.5 の設計方針6「秘密は一度しか返さない」
// より強く、**一度も返さない**（11.4）。
package v1

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// certificateView は 11.4 の items[] の要素。
type certificateView struct {
	ID         string   `json:"id"`
	CommonName string   `json:"common_name"`
	DNSNames   []string `json:"dns_names"`
	// IPAddresses は IP の SAN（11.4）。**列には無く、cert_pem から採る**——
	// dns_names は DNS: の SAN しか持たないので、**これを出さないと画面の
	// 「SAN」欄と突き合わせの結果が食い違って見える**（pb-100 の実画面で踏んだ）。
	IPAddresses  []string  `json:"ip_addresses"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	SerialNumber string    `json:"serial_number"`
	Fingerprint  string    `json:"fingerprint"`
	IsSelfSigned bool      `json:"is_self_signed"`
	// Status はサーバが決める（11.4）。画面が日付から組み立てない。
	Status string `json:"status"`
	// Decryptable はいまの鍵で秘密鍵を復号できるか（11.4。pb-98）。
	//
	// **key_id の突き合わせでは検出できない**——行の key_id は常に v1 で、
	// 鍵の出どころを記録していない。**行ごとに復号を試して決める。**
	//
	// **status とは別の軸である。** status は日付で決まり、こちらは鍵で決まる。
	// **active なのに復号できない**という状態がありうる。
	Decryptable bool      `json:"decryptable"`
	UploadedAt  time.Time `json:"uploaded_at"`
	UploadedBy  *actorRef `json:"uploaded_by"`
}

// certificateListResponse は 11.4 の応答。
type certificateListResponse struct {
	Items []certificateView `json:"items"`
	// TLSEnabled は**実際の待受の状態**である（設定の実効値ではない）。
	TLSEnabled bool `json:"tls_enabled"`
	// ListenURL は**いま待ち受けているスキームとアドレス**（11.4）。
	// 画面の先頭にそのまま出す。**設定値から画面が組み立てない**——
	// 待受の変更には再起動が要るので、両者は再起動をまたぐとずれる。
	ListenURL string `json:"listen_url"`
	// ListenHost は接続に使うホスト名。**0.0.0.0 と :: では null である**（11.4）。
	// **あれらは待受の表記であって接続先のホスト名ではない**（pb-100 で実測）。
	ListenHost *string `json:"listen_host"`
	// ListenHostMatch はいま出す証明書が ListenHost を覆っているか（11.4）。
	//
	// **判定はサーバが行い、画面は結果を出すだけである。** 画面が dns_names と
	// 照合していたときは IP の SAN が抜け落ちていた（pb-100）。
	ListenHostMatch tlscert.ListenHostMatch `json:"listen_host_match"`
	// SecretKeyPresent は鍵が使える状態か。**PB が作るので通常は真である。**
	SecretKeyPresent bool `json:"secret_key_present"`
	// SecretKeyOrigin は鍵の出どころ（env / generated）。
	// **画面に代償を出すために要る**——生成した鍵は DB にあるので、
	// pg_dump に鍵と暗号文の両方が入る（Design.md 6.6.1）。
	SecretKeyOrigin string `json:"secret_key_origin"`
}

// certificateUploadRequest は 11.5 のリクエスト本体。
//
// **multipart ではなく JSON で受ける**（11.5）。PEM はテキストであり、
// 鍵をファイルとして持っていない利用者（発行元の画面からコピーしただけ）が詰まる。
type certificateUploadRequest struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

// listTLSCertificates は GET /api/v1/admin/tls/certificates を処理する（11.4）。
func (h *handler) listTLSCertificates(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListTLSCertificates(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("証明書を引けない: %w", err)))
		return
	}
	// **鍵の出どころを画面へ返す。** 生成した鍵は DB にあるので、
	// pg_dump に鍵と暗号文の両方が入ることを画面が伝える（6.6.1）。
	key, origin, e := h.secretKey(r.Context())
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	WriteJSON(w, http.StatusOK, h.buildCertificateList(rows, key, origin))
}

// uploadTLSCertificate は POST /api/v1/admin/tls/certificates を処理する（11.5）。
func (h *handler) uploadTLSCertificate(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /admin/tls/certificates が認証ミドルウェアを通っていない")))
		return
	}

	var req certificateUploadRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **鍵は PB が用意する。** 無ければ作って DB へ保存する（6.6.1）。
	key, _, e := h.secretKey(r.Context())
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	parsed, err := tlscert.Parse(req.CertPEM, req.KeyPEM, time.Now())
	if err != nil {
		var v *tlscert.ErrValidation
		if errors.As(err, &v) {
			apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "cert_pem", Code: "invalid", Message: v.Msg,
			}))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	ciphertext, nonce, err := tlscert.Seal(key, strings.TrimSpace(req.KeyPEM)+"\n")
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("秘密鍵を暗号化できない: %w", err)))
		return
	}

	id := ulidgen.New()
	ctx := r.Context()
	rec := audit.FromRequest(r)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if _, err := q.CreateTLSCertificate(ctx, gen.CreateTLSCertificateParams{
			ID:            id,
			CommonName:    parsed.CommonName,
			DnsNames:      parsed.DNSNames,
			NotBefore:     pgtype.Timestamptz{Time: parsed.NotBefore, Valid: true},
			NotAfter:      pgtype.Timestamptz{Time: parsed.NotAfter, Valid: true},
			SerialNumber:  parsed.SerialNumber,
			Fingerprint:   parsed.Fingerprint,
			IsSelfSigned:  parsed.IsSelfSigned,
			CertPem:       parsed.CertPEM,
			KeyCiphertext: ciphertext,
			KeyNonce:      nonce,
			KeyID:         tlscert.CurrentKeyID,
			UploadedBy:    pgtype.Text{String: p.ActorID, Valid: true},
		}); err != nil {
			// **同じ指紋は 409**（11.5）。更新のつもりで同じ PEM を貼った
			// 利用者に、その場で気づかせる。
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return apierr.New(apierr.Conflict).
					WithMessage("この証明書は既に登録されています")
			}
			return fmt.Errorf("証明書を作れない: %w", err)
		}

		// **PEM と秘密鍵を detail に入れない**（2.10）。audit_log は
		// 長期保存される記録である。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TLSCertificateUpload,
			Result:     audit.Success,
			TargetType: "tls_certificate",
			TargetID:   id,
			Detail: map[string]any{
				"fingerprint": parsed.Fingerprint,
				"common_name": parsed.CommonName,
				"not_before":  parsed.NotBefore,
				"not_after":   parsed.NotAfter,
			},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// 出す証明書を選び直す。**登録した瞬間から次の接続で効く**（6.6.1）。
	rows := h.reloadCertificates(r)

	key, origin, _ := h.secretKey(r.Context())
	for _, v := range h.buildCertificateList(rows, key, origin).Items {
		if v.ID == id {
			WriteJSON(w, http.StatusCreated, v)
			return
		}
	}
	// 書いた直後に引けないことは起こらないが、起きたら 500 にする。
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("登録した証明書を引き直せない（id=%s）", id)))
}

// deleteTLSCertificate は DELETE /api/v1/admin/tls/certificates/:id を処理する（11.6）。
func (h *handler) deleteTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rows, err := h.q.ListTLSCertificates(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("証明書を引けない: %w", err)))
		return
	}

	found := false
	remaining := make([]tlscert.Entry, 0, len(rows))
	for _, row := range rows {
		if row.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, tlscert.Entry{
			ID:        row.ID,
			NotBefore: row.NotBefore.Time,
			NotAfter:  row.NotAfter.Time,
		})
	}
	if !found {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}

	// **最後の有効な証明書を消させない**（11.6）。消せてしまうと、その瞬間から
	// ハンドシェイクが失敗し、画面から復旧できなくなる。
	if h.tlsActive() {
		if _, activeID := tlscert.Select(remaining, time.Now()); activeID == "" {
			apierr.Write(w, r, apierr.New(apierr.Conflict).
				WithMessage("これを消すと有効な証明書が無くなり、HTTPS で待ち受けられなくなります。平文へ戻すには先に「TLS で待ち受ける」を無効にしてください"))
			return
		}
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		cur, err := q.GetTLSCertificate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(apierr.NotFound)
		} else if err != nil {
			return fmt.Errorf("証明書を引けない: %w", err)
		}
		if _, err := q.DeleteTLSCertificate(ctx, id); err != nil {
			return fmt.Errorf("証明書を消せない: %w", err)
		}
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TLSCertificateDelete,
			Result:     audit.Success,
			TargetType: "tls_certificate",
			TargetID:   id,
			Detail: map[string]any{
				"fingerprint": cur.Fingerprint,
				"common_name": cur.CommonName,
			},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	h.reloadCertificates(r)
	w.WriteHeader(http.StatusNoContent)
}

// secretKey は使う暗号鍵とその出どころを返す（Design.md 6.6.1）。
//
//	PB_SECRET_KEY があればそれ ＞ DB の行 ＞ 生成して DB へ保存
//
// **利用者の操作を要らなくするため、無ければ PB が作る。** 改訂前は環境変数を
// 必須にして 409 を返していたが、**証明書を1枚登録するために環境変数の設定と
// 再起動を要求する形は「設定は WebGUI を第一の口とする」方針と矛盾していた**
// （stg での利用者の指摘、2026-09-12）。
func (h *handler) secretKey(ctx context.Context) ([]byte, tlscert.KeyOrigin, *apierr.Error) {
	encoded := h.settings.Snapshot().String(config.KeySecretKey)
	key, origin, err := tlscert.ResolveKey(ctx, SecretStore{Q: h.q}, encoded)
	if err != nil {
		return nil, "", apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("暗号鍵を用意できない: %w", err))
	}
	return key, origin, nil
}

// tlsActive は実際に TLS で待ち受けているかを返す。
func (h *handler) tlsActive() bool { return h.certs != nil && h.tlsListening }

// reloadCertificates は証明書を引き直し、出すものを選び直す。
//
// **復号に失敗した行は落として続ける。** 鍵を交換したあとに古い行が残っている
// 場合などで、1行のために全部を出せなくなるのを避ける。
func (h *handler) reloadCertificates(r *http.Request) []gen.ListTLSCertificatesRow {
	rows, err := h.q.ListTLSCertificates(r.Context())
	if err != nil {
		return nil
	}
	if h.certs == nil {
		return rows
	}
	key, _, e := h.secretKey(r.Context())
	if e != nil {
		return rows
	}
	h.certs.Replace(entriesFrom(rows, key))
	return rows
}

// buildCertificateList は 11.4 の応答を組み立てる。
func (h *handler) buildCertificateList(rows []gen.ListTLSCertificatesRow, key []byte, origin tlscert.KeyOrigin) certificateListResponse {
	entries := make([]tlscert.Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, tlscert.Entry{
			ID: row.ID, NotBefore: row.NotBefore.Time, NotAfter: row.NotAfter.Time,
		})
	}
	status, activeID := tlscert.Select(entries, time.Now())

	items := make([]certificateView, 0, len(rows))
	for _, row := range rows {
		v := certificateView{
			ID:           row.ID,
			CommonName:   row.CommonName,
			DNSNames:     row.DnsNames,
			IPAddresses:  tlscert.IPAddresses(row.CertPem),
			NotBefore:    row.NotBefore.Time.UTC(),
			NotAfter:     row.NotAfter.Time.UTC(),
			SerialNumber: row.SerialNumber,
			Fingerprint:  row.Fingerprint,
			IsSelfSigned: row.IsSelfSigned,
			Status:       string(status[row.ID]),
			Decryptable:  decryptable(row, key),
			UploadedAt:   row.CreatedAt.Time.UTC(),
		}
		if v.DNSNames == nil {
			v.DNSNames = []string{}
		}
		if v.IPAddresses == nil {
			v.IPAddresses = []string{}
		}
		if row.UploadedBy.Valid {
			v.UploadedBy = &actorRef{
				ID:          row.UploadedBy.String,
				Kind:        row.UploadedByKind.String,
				DisplayName: row.UploadedByDisplayName.String,
			}
		}
		items = append(items, v)
	}

	host := tlscert.ListenHost(h.listenURL)
	resp := certificateListResponse{
		Items:            items,
		TLSEnabled:       h.tlsActive(),
		ListenURL:        h.listenURL,
		ListenHostMatch:  tlscert.MatchNoCertificate,
		SecretKeyPresent: origin != "",
		SecretKeyOrigin:  string(origin),
	}
	if host != "" {
		resp.ListenHost = &host
	}

	// **突き合わせるのは、いま出している1枚だけである。** 待機中のものは
	// まだ誰にも提示されていないので、いま繋がるかどうかを左右しない。
	if activeID != "" {
		switch {
		case host == "":
			resp.ListenHostMatch = tlscert.MatchUnspecific
		case tlscert.CoversHost(activePEM(rows, activeID), host):
			resp.ListenHostMatch = tlscert.MatchCovered
		default:
			resp.ListenHostMatch = tlscert.MatchUncovered
		}
	}
	return resp
}

// decryptable はいまの鍵でこの行の秘密鍵を復号できるかを返す（11.4。pb-98）。
//
// **X509KeyPair までは見ない。** 見たいのは**鍵の出どころが変わっていないか**で
// あり、登録時に証明書と鍵の対応は検証済みである（11.5）。
func decryptable(row gen.ListTLSCertificatesRow, key []byte) bool {
	if len(key) == 0 {
		return false
	}
	_, err := tlscert.Open(key, row.KeyCiphertext, row.KeyNonce)
	return err == nil
}

// activePEM はいま出している1枚の PEM を取り出す。
func activePEM(rows []gen.ListTLSCertificatesRow, activeID string) string {
	for _, row := range rows {
		if row.ID == activeID {
			return row.CertPem
		}
	}
	return ""
}

// downloadTLSCertificate は GET /api/v1/admin/tls/certificates/:id/certificate.zip
// を処理する（11.7）。
//
// **証明書だけを返す。** 秘密鍵はこの口にも現れない（11.4）。
//
// **zip に包む**（pb-108）。`.crt` をそのまま返すとブラウザが拒み、**PB は 200 を
// 返しているので失敗がどこにも残らない。**
//
// **監査ログを残さない。** 証明書は接続してきた誰にでも提示されるもので、
// 取り出せること自体は秘密の漏洩にあたらない（11.7）。
func (h *handler) downloadTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	row, err := h.q.GetTLSCertificatePEM(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	} else if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("証明書を引けない: %w", err)))
		return
	}

	blob, err := tlscert.Zip(tlscert.FileName(row.CommonName), row.CertPem)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// **ブラウザの保存にそのまま乗せる**（11.7）。画面が Blob を組み立てなくて済む。
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+tlscert.ZipName(row.CommonName)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

// loadPair は行から tls.Certificate を作る。**秘密鍵をここで復号する。**
//
// 復号に失敗するのは、鍵が違うか行が壊れているときである（GCM は認証付き）。
func loadPair(row gen.ListTLSCertificatesRow, key []byte) (*tls.Certificate, error) {
	keyPEM, err := tlscert.Open(key, row.KeyCiphertext, row.KeyNonce)
	if err != nil {
		slog.Warn("証明書の秘密鍵を復号できないため、この行を使わない",
			slog.String("certificate_id", row.ID),
			slog.String("key_id", row.KeyID),
			slog.String("error", err.Error()))
		return nil, err
	}
	pair, err := tls.X509KeyPair([]byte(row.CertPem), []byte(keyPEM))
	if err != nil {
		slog.Warn("証明書と秘密鍵が対応しないため、この行を使わない",
			slog.String("certificate_id", row.ID), slog.String("error", err.Error()))
		return nil, err
	}
	return &pair, nil
}

// entriesFrom は行から選定の対象を作る。**復号に失敗した行は落とす。**
func entriesFrom(rows []gen.ListTLSCertificatesRow, key []byte) []tlscert.Entry {
	out := make([]tlscert.Entry, 0, len(rows))
	for _, row := range rows {
		pair, err := loadPair(row, key)
		if err != nil {
			continue
		}
		out = append(out, tlscert.Entry{
			ID: row.ID, NotBefore: row.NotBefore.Time, NotAfter: row.NotAfter.Time, Pair: pair,
		})
	}
	return out
}
