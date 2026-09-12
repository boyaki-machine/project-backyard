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
	ID           string    `json:"id"`
	CommonName   string    `json:"common_name"`
	DNSNames     []string  `json:"dns_names"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	SerialNumber string    `json:"serial_number"`
	Fingerprint  string    `json:"fingerprint"`
	IsSelfSigned bool      `json:"is_self_signed"`
	// Status はサーバが決める（11.4）。画面が日付から組み立てない。
	Status     string    `json:"status"`
	UploadedAt time.Time `json:"uploaded_at"`
	UploadedBy *actorRef `json:"uploaded_by"`
}

// certificateListResponse は 11.4 の応答。
type certificateListResponse struct {
	Items []certificateView `json:"items"`
	// TLSEnabled は**実際の待受の状態**である（設定の実効値ではない）。
	TLSEnabled bool `json:"tls_enabled"`
	// SecretKeyPresent は secret_key が与えられているか。**値は返さない。**
	SecretKeyPresent bool `json:"secret_key_present"`
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
	WriteJSON(w, http.StatusOK, h.buildCertificateList(rows))
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

	// **鍵が無ければ登録させない**（11.5 が 409）。暗号化して保存できないので、
	// 受け取っても置き場が無い。
	key, e := h.secretKey()
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

	for _, v := range h.buildCertificateList(rows).Items {
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

// secretKey は secret_key を鍵として読む。無ければ 409 を返す。
func (h *handler) secretKey() ([]byte, *apierr.Error) {
	encoded := h.settings.Snapshot().String(config.KeySecretKey)
	key, err := tlscert.DecodeKey(encoded)
	if err != nil {
		return nil, apierr.New(apierr.Conflict).
			WithMessage("証明書を登録するには PB_SECRET_KEY の設定が要ります（32バイトを base64 で与えてください）").
			WithCause(err)
	}
	return key, nil
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
	key, e := h.secretKey()
	if e != nil {
		return rows
	}
	h.certs.Replace(entriesFrom(rows, key))
	return rows
}

// buildCertificateList は 11.4 の応答を組み立てる。
func (h *handler) buildCertificateList(rows []gen.ListTLSCertificatesRow) certificateListResponse {
	entries := make([]tlscert.Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, tlscert.Entry{
			ID: row.ID, NotBefore: row.NotBefore.Time, NotAfter: row.NotAfter.Time,
		})
	}
	status, _ := tlscert.Select(entries, time.Now())

	items := make([]certificateView, 0, len(rows))
	for _, row := range rows {
		v := certificateView{
			ID:           row.ID,
			CommonName:   row.CommonName,
			DNSNames:     row.DnsNames,
			NotBefore:    row.NotBefore.Time.UTC(),
			NotAfter:     row.NotAfter.Time.UTC(),
			SerialNumber: row.SerialNumber,
			Fingerprint:  row.Fingerprint,
			IsSelfSigned: row.IsSelfSigned,
			Status:       string(status[row.ID]),
			UploadedAt:   row.CreatedAt.Time.UTC(),
		}
		if v.DNSNames == nil {
			v.DNSNames = []string{}
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

	_, err := tlscert.DecodeKey(h.settings.Snapshot().String(config.KeySecretKey))
	return certificateListResponse{
		Items:            items,
		TLSEnabled:       h.tlsActive(),
		SecretKeyPresent: err == nil,
	}
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
