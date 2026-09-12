<script setup lang="ts">
/**
 * TLS 証明書タブ（`GuiDesign.md` 5.12.1）。pb-3。
 *
 * **設計の正本は `Design.md` 6.6.1**（TLS 終端）と `ApiDesign.md` 11.4〜11.6。
 *
 * **状態の札はサーバが返す `status` をそのまま出す。** 画面が日付から組み立て
 * ない——選定の規則は `Design.md` 6.6.1 の1か所に置く。
 *
 * **証明書の作り方は折りたたみで画面に出す**（利用者の指示、2026-09-11）。
 * 毎回読むものではないので畳むが、**証明書が0枚のときは開いた状態で出す。**
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as settingsApi from '../api/settings'
import type { CertificateStatus, TLSCertificate } from '../api/settings'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import { formatDateTime } from '../lib/datetime'

const items = ref<TLSCertificate[]>([])
const tlsEnabled = ref(false)
const secretKeyPresent = ref(false)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

const certPem = ref('')
const keyPem = ref('')
const submitting = ref(false)

/** 表の直上に出す結果（`GuiDesign.md` 6.4）。登録と削除で使い回す */
const notice = ref('')
const actionError = ref<ApiError | null>(null)

const deleting = ref<TLSCertificate | null>(null)

/** 作り方の折りたたみ。**0枚のときは開いて出す**（5.12.1） */
const showSelfSigned = ref(false)
const showFormal = ref(false)

/** 状態の札（5.12.1） */
const STATUS_LABEL: Record<CertificateStatus, string> = {
  active: '使用中',
  pending: '待機中',
  expired: '期限切れ',
  superseded: '世代交代',
}

/** openssl の1コマンド。**手で打ち写すと subjectAltName を落としやすい**ので、コピーさせる */
const SELF_SIGNED_CMD = `openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \\
  -keyout pb.key -out pb.crt \\
  -subj "/CN=pb.example.com" \\
  -addext "subjectAltName=DNS:pb.example.com,DNS:localhost"`

const CSR_CMD = `openssl req -new -newkey rsa:2048 -nodes \\
  -keyout pb.key -out pb.csr \\
  -subj "/C=JP/ST=Tokyo/O=Example Inc./CN=pb.example.com"`

async function load() {
  loading.value = true
  loadError.value = null
  try {
    const res = await settingsApi.getCertificates()
    items.value = res.items
    tlsEnabled.value = res.tls_enabled
    secretKeyPresent.value = res.secret_key_present
    // 0枚なら作り方を開いて出す（初回は必ず要る）。
    if (res.items.length === 0) {
      showSelfSigned.value = true
      showFormal.value = true
    }
  } catch (e: unknown) {
    loadError.value = asApiError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const canSubmit = computed(
  () => secretKeyPresent.value && certPem.value.trim() !== '' && keyPem.value.trim() !== '',
)

/** 残り日数。**期限切れは画面が見えなくなる事故に直結する**ので気づく面を持たせる */
function daysLeft(c: TLSCertificate): number {
  const ms = new Date(c.not_after).getTime() - Date.now()
  return Math.floor(ms / 86_400_000)
}

/**
 * 消せるか（`ApiDesign.md` 11.6）。
 *
 * **最後の有効な証明書には削除を出さない。** 押せるのに必ず失敗する操作を出さない
 * （5.8.2 と同じ規則）。判定は `status` だけで足りる——`active` 以外は出していない。
 */
function canDelete(c: TLSCertificate): boolean {
  if (c.status !== 'active') return true
  return !tlsEnabled.value
}

async function submit() {
  submitting.value = true
  notice.value = ''
  actionError.value = null
  try {
    const created = await settingsApi.uploadCertificate(certPem.value, keyPem.value)
    certPem.value = ''
    keyPem.value = ''
    notice.value =
      created.status === 'active'
        ? `${created.common_name} を登録しました。この証明書を使い始めます。`
        : `${created.common_name} を登録しました。${formatDateTime(created.not_before)} から自動で使われます。`
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
  } finally {
    submitting.value = false
  }
}

async function confirmDelete() {
  const target = deleting.value
  if (!target) return
  submitting.value = true
  notice.value = ''
  actionError.value = null
  try {
    await settingsApi.deleteCertificate(target.id)
    notice.value = `${target.common_name} を削除しました。`
    deleting.value = null
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
    deleting.value = null
  } finally {
    submitting.value = false
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notice.value = 'コマンドをコピーしました。'
  } catch {
    // クリップボードが使えない環境では何もしない（選択してコピーできる）。
  }
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
}
</script>

<template>
  <div class="tab">
    <p v-if="loading" class="muted">読み込み中…</p>
    <p v-else-if="loadError" class="error" role="alert">{{ loadError.message }}</p>

    <template v-else>
      <p class="state">
        <template v-if="tlsEnabled">いま HTTPS で待ち受けています</template>
        <template v-else
          >いま HTTP で待ち受けています。証明書を登録したあと、「一般」タブの<strong
            >TLS で待ち受ける</strong
          >を有効にして再起動してください</template
        >
      </p>

      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="actionError" class="error" role="alert">{{ actionError.message }}</p>

      <p v-if="items.length === 0" class="muted">まだ証明書が登録されていません。</p>

      <div v-for="c in items" :key="c.id" class="cert" :class="`st-${c.status}`">
        <div class="head">
          <span class="cn">{{ c.common_name }}</span>
          <span class="badge" :class="`st-${c.status}`">{{ STATUS_LABEL[c.status] }}</span>
          <span class="muted kind">{{ c.is_self_signed ? '自己署名' : '認証局発行' }}</span>
        </div>

        <p class="period">
          {{ new Date(c.not_before).toLocaleDateString('ja-JP') }} 〜
          {{ new Date(c.not_after).toLocaleDateString('ja-JP') }}
          <span v-if="c.status === 'active'" :class="{ warn: daysLeft(c) < 30 }">
            （あと {{ daysLeft(c) }} 日）
          </span>
        </p>

        <!-- 「いつから使われるか」は、自動で切り替わることを確かめられる唯一の場所 -->
        <p v-if="c.status === 'pending'" class="from">
          <strong>{{ formatDateTime(c.not_before) }} から自動で使われます</strong>
        </p>

        <p v-if="c.dns_names.length" class="muted san">SAN: {{ c.dns_names.join(', ') }}</p>
        <p class="muted fp">指紋: {{ c.fingerprint }}</p>
        <p class="muted by">
          {{ formatDateTime(c.uploaded_at) }}
          <template v-if="c.uploaded_by">{{ c.uploaded_by.display_name }} が登録</template>
        </p>

        <div class="foot">
          <button v-if="canDelete(c)" type="button" class="link danger" @click="deleting = c">
            削除
          </button>
          <span v-else class="muted"
            >これを消すと HTTPS で待ち受けられなくなります。平文へ戻すには「一般」タブの<strong
              >TLS で待ち受ける</strong
            >を無効にしてください</span
          >
        </div>
      </div>

      <!-- ── 登録 ─────────────────────────────────────── -->
      <section class="upload">
        <h3>証明書を登録する</h3>

        <!-- 選んだ先に何も出ない選択肢を置かない（5.8.2 の規則） -->
        <p v-if="!secretKeyPresent" class="error">
          証明書を登録するには <code>PB_SECRET_KEY</code> の設定が要ります。32バイトを
          base64 で与えてください（<code>openssl rand -base64 32</code>）。
        </p>

        <template v-else>
          <label class="field">
            <span>証明書（PEM）</span>
            <textarea
              v-model="certPem"
              rows="6"
              spellcheck="false"
              placeholder="-----BEGIN CERTIFICATE-----"
            ></textarea>
          </label>
          <label class="field">
            <span>秘密鍵（PEM）</span>
            <textarea
              v-model="keyPem"
              rows="6"
              spellcheck="false"
              placeholder="-----BEGIN PRIVATE KEY-----"
            ></textarea>
          </label>
          <p class="muted hint">
            秘密鍵は暗号化して保存され、<strong>二度と表示されません</strong
            >。手元の鍵を残しておいてください。
          </p>
          <div class="actions">
            <button type="button" class="primary" :disabled="!canSubmit || submitting" @click="submit">
              {{ submitting ? '登録中…' : '登録' }}
            </button>
          </div>
        </template>
      </section>

      <!-- ── 作り方（利用者の指示、2026-09-11）───────────── -->
      <section class="howto">
        <details :open="showSelfSigned">
          <summary>自己署名証明書の作り方（openssl）</summary>
          <p class="muted">
            開発端末や LAN の中で試すときに使います。ブラウザは警告を出しますが、PB の
            動作は認証局発行の証明書と変わりません。
          </p>
          <pre>{{ SELF_SIGNED_CMD }}</pre>
          <button type="button" class="link" @click="copy(SELF_SIGNED_CMD)">コピー</button>
          <ul class="muted">
            <li>
              <code>-nodes</code> は<strong>パスフレーズを付けない</strong
              >指定です。PB はパスフレーズ付きの秘密鍵を受け付けません
            </li>
            <li>
              <strong><code>subjectAltName</code> にアクセスに使うホスト名を必ず入れます。</strong>
              ブラウザが見るのはこちらで、<code>CN</code> だけでは受け付けません
            </li>
          </ul>
          <p class="muted">
            <code>pb.crt</code> を「証明書」、<code>pb.key</code> を「秘密鍵」の欄に貼ります。
          </p>
        </details>

        <details :open="showFormal">
          <summary>認証局が発行した証明書を登録する</summary>
          <p class="muted">
            サイバートラスト・DigiCert・GlobalSign・Let's Encrypt など、発行元によらず
            手順は同じです。
          </p>
          <p class="muted"><strong>① 秘密鍵と CSR を作る</strong></p>
          <pre>{{ CSR_CMD }}</pre>
          <button type="button" class="link" @click="copy(CSR_CMD)">コピー</button>
          <p class="muted">
            <code>pb.key</code> は手元に残し、<strong>発行元には渡しません</strong>。
            <code>pb.csr</code> を発行元の申込画面へ提出します。
          </p>
          <p class="muted"><strong>② 受け取ったものを1つにまとめる</strong></p>
          <p class="muted">
            発行元からはサーバ証明書と中間証明書が別々に届くことが多いです。証明書の欄には
            <strong>サーバ証明書 → 中間証明書の順</strong>で続けて貼ってください。逆にすると
            「証明書が信頼できない」と出ます。<strong>ルート証明書は貼らなくてよい</strong>です。
          </p>
          <p class="muted"><strong>③ 更新するとき</strong></p>
          <p class="muted">
            <strong>古いものを消さずに、新しいものを登録します。</strong> 新しい証明書が
            有効になった時点で自動的に切り替わり、再起動は要りません。
          </p>
        </details>

        <p class="muted docref">
          発行元ごとの申込手順、連鎖の確かめ方、つまずいたときの対処は
          <code>docs/Development.md</code> 14章にあります。
        </p>
      </section>
    </template>

    <Teleport to="body">
      <ConfirmDialog
        v-if="deleting"
        title="証明書を削除しますか？"
        :message="`${deleting.common_name}（${deleting.fingerprint.slice(0, 23)}…）を削除します。この操作は取り消せません。`"
        confirm-label="削除する"
        danger
        :busy="submitting"
        @confirm="confirmDelete"
        @cancel="deleting = null"
      />
    </Teleport>
  </div>
</template>

<style scoped>
.tab {
  padding-block: var(--pb-space-3);
}
.muted {
  color: var(--pb-fg-muted);
}
.notice {
  color: var(--pb-success-fg);
}
.error {
  color: var(--pb-danger-fg);
}
.warn {
  color: var(--pb-warning-fg);
}
.state {
  margin: 0 0 var(--pb-space-3);
}
.cert {
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius-md);
  padding: var(--pb-space-3);
  margin-bottom: var(--pb-space-3);
}
.cert.st-active {
  border-color: var(--pb-accent);
}
.cert.st-expired {
  opacity: 0.7;
}
.head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
}
.cn {
  font-weight: 600;
}
.badge {
  font-size: var(--pb-fs-xs);
  padding: 0 var(--pb-space-2);
  border-radius: var(--pb-radius-sm);
  border: 1px solid var(--pb-border);
}
.badge.st-active {
  border-color: var(--pb-accent);
  color: var(--pb-accent);
}
.badge.st-expired {
  color: var(--pb-danger-fg);
  border-color: var(--pb-danger-fg);
}
.kind,
.period,
.from,
.san,
.fp,
.by {
  font-size: var(--pb-fs-sm);
  margin: var(--pb-space-1) 0 0;
}
.fp {
  font-family: var(--pb-font-mono);
  word-break: break-all;
}
.foot {
  margin-top: var(--pb-space-2);
  font-size: var(--pb-fs-sm);
}
.upload,
.howto {
  margin-top: var(--pb-space-5);
  border-top: 1px solid var(--pb-border);
  padding-top: var(--pb-space-3);
}
.upload h3 {
  font-size: var(--pb-fs-md);
  margin: 0 0 var(--pb-space-2);
}
.field {
  display: block;
  margin-bottom: var(--pb-space-3);
}
.field span {
  display: block;
  font-size: var(--pb-fs-sm);
  margin-bottom: var(--pb-space-1);
}
.field textarea {
  width: 100%;
  font-family: var(--pb-font-mono);
  font-size: var(--pb-fs-sm);
}
.hint {
  font-size: var(--pb-fs-sm);
}
.actions {
  display: flex;
  justify-content: flex-end;
}
.howto details {
  margin-bottom: var(--pb-space-3);
}
.howto summary {
  cursor: pointer;
  font-weight: 600;
}
.howto pre {
  overflow-x: auto;
  background: var(--pb-bg-subtle);
  padding: var(--pb-space-2);
  border-radius: var(--pb-radius-sm);
  font-size: var(--pb-fs-sm);
}
.howto ul {
  font-size: var(--pb-fs-sm);
  padding-left: var(--pb-space-4);
}
.docref {
  font-size: var(--pb-fs-sm);
}
</style>
