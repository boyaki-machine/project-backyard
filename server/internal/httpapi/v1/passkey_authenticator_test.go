package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
)

// softAuthenticator はパスキーのテストで使う、ソフトウェアの認証器（pb-104）。
//
// **本物の認証器とブラウザが作る応答を、テストの中で組み立てる。** go-webauthn は
// 署名・rpIdHash・origin・UV を実際に検証するので、形だけを真似た値では通らない
// ——通るなら検証が効いていないことになる。ブラウザを通した確認は CDP の
// 仮想認証器で別に行う（Development.md 8章）。
//
// **CBOR は必要な型（整数・バイト列・文字列・マップ）だけを手で書く。**
// テストのために依存を足さない。
type softAuthenticator struct {
	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
	signCount    uint32
	origin       string

	// 既定は UV あり・同期なし。テストごとに1つだけ崩す。
	userVerified   bool
	backupEligible bool
	backupState    bool
}

// テストのルータは httptest.NewRequest の既定の Host（example.com）で叩かれる。
// RP ID と origin はそこから導かれる（Design.md 6.8.3）。
const (
	testRPID   = "example.com"
	testOrigin = "http://example.com"
)

func newSoftAuthenticator(t *testing.T, userID string) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("鍵を作れない: %v", err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatalf("credential_id を作れない: %v", err)
	}
	return &softAuthenticator{
		key: key, credentialID: id, userHandle: []byte(userID),
		origin: testOrigin, userVerified: true,
	}
}

// authenticator data のフラグ（WebAuthn §6.1）。
const (
	adFlagUP = 0x01
	adFlagUV = 0x04
	adFlagBE = 0x08
	adFlagBS = 0x10
	adFlagAT = 0x40
)

func (a *softAuthenticator) authData(rpID string, attested bool) []byte {
	flags := byte(adFlagUP)
	if a.userVerified {
		flags |= adFlagUV
	}
	if a.backupEligible {
		flags |= adFlagBE
	}
	if a.backupState {
		flags |= adFlagBS
	}
	if attested {
		flags |= adFlagAT
	}

	h := sha256.Sum256([]byte(rpID))
	out := append([]byte{}, h[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.signCount)
	if attested {
		out = append(out, make([]byte, 16)...) // AAGUID。attestation none では全0でよい
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.credentialID)))
		out = append(out, a.credentialID...)
		out = append(out, a.coseKey()...)
	}
	return out
}

// coseKey は EC2 / P-256 / ES256 の COSE_Key（RFC 9053）。user_passkey.public_key に入る値である。
func (a *softAuthenticator) coseKey() []byte {
	pub, err := a.key.PublicKey.ECDH()
	if err != nil {
		panic(err)
	}
	point := pub.Bytes() // 0x04 || X || Y
	return cborMap(
		cborInt(1), cborInt(2), // kty: EC2
		cborInt(3), cborInt(-7), // alg: ES256
		cborInt(-1), cborInt(1), // crv: P-256
		cborInt(-2), cborBytes(point[1:33]),
		cborInt(-3), cborBytes(point[33:65]),
	)
}

func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]any{
		"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false,
	})
	if err != nil {
		panic(err)
	}
	return b
}

// register は navigator.credentials.create() の応答（toJSON() の形）を作る。
func (a *softAuthenticator) register(rpID, challenge string) string {
	attestation := cborMap(
		cborText("fmt"), cborText("none"),
		cborText("attStmt"), cborMap(),
		cborText("authData"), cborBytes(a.authData(rpID, true)),
	)
	return mustJSON(map[string]any{
		"id": b64url(a.credentialID), "rawId": b64url(a.credentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64url(a.clientData("webauthn.create", challenge)),
			"attestationObject": b64url(attestation),
			"transports":        []string{"internal"},
		},
		"authenticatorAttachment": "platform",
		"clientExtensionResults":  map[string]any{},
	})
}

// assert は navigator.credentials.get() の応答（toJSON() の形）を作る。
//
// 署名は authenticatorData || SHA-256(clientDataJSON) に対する ES256（WebAuthn §7.2 の手順20）。
func (a *softAuthenticator) assert(rpID, challenge string) string {
	authData := a.authData(rpID, false)
	clientData := a.clientData("webauthn.get", challenge)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		panic(err)
	}
	return mustJSON(map[string]any{
		"id": b64url(a.credentialID), "rawId": b64url(a.credentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64url(clientData),
			"authenticatorData": b64url(authData),
			"signature":         b64url(sig),
			"userHandle":        b64url(a.userHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
}

// ── 最小の CBOR（RFC 8949）─────────────────────────────────

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	default:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(n))
	}
}

func cborInt(v int) []byte {
	if v >= 0 {
		return cborHead(0, uint64(v))
	}
	return cborHead(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }

func cborText(s string) []byte { return append(cborHead(3, uint64(len(s))), s...) }

// cborMap はキーと値を交互に並べた引数からマップを作る。
func cborMap(kv ...[]byte) []byte {
	out := cborHead(5, uint64(len(kv)/2))
	for _, part := range kv {
		out = append(out, part...)
	}
	return out
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
