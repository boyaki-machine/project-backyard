<script setup lang="ts">
import { uiLocaleTag, uiText } from '../locales/ui'
/**
 * TLS 証明書タブ（`GuiDesign.md` 5.12.1）。
 *
 * **設計の正本は `Design.md` 6.6.1**（TLS 終端）と `ApiDesign.md` 11.4〜11.6。
 *
 * **状態の札はサーバが返す `status` をそのまま出す。** 画面が日付から組み立て
 * ない——選定の規則は `Design.md` 6.6.1 の1か所に置く。
 *
 * **証明書の作り方は折りたたみで画面に出す。**
 * 毎回読むものではないので畳むが、**証明書が0枚のときは開いた状態で出す。**
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as settingsApi from '../api/settings'
import type {
  CertificateStatus,
  Setting,
  TLSCertificate,
  TLSCertificateList,
} from '../api/settings'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import { formatDateTime } from '../lib/datetime'

// `?raw` は Vite の機能で、型は `vite/client` に含まれる（`version.ts` と同じ読み方）。
import selfSignedCmdRaw from './tls-self-signed.sh?raw'

const items = ref<TLSCertificate[]>([])
/** **実際に TLS で待ち受けているか。** 設定の実効値ではない（`ApiDesign.md` 11.4） */
const tlsEnabled = ref(false)
const listenUrl = ref('')

/**
 * 接続に使うホスト名と、いま出す証明書との突き合わせ（`ApiDesign.md` 11.4）。
 *
 * **判定は画面で行わない。** 画面が持つ `dns_names` には IP アドレスが入らない
 * ——照合すると、**`https://127.0.0.1:8443` で繋ぐ構成が「覆っていない」と
 * 判定できず、黙って通る。**
 *
 * `listenHost` は `0.0.0.0` と `::` では `null` である。**あれらは待受の表記で
 * あって接続先のホスト名ではない。**
 */
const listenHost = ref<string | null>(null)
const listenHostMatch = ref<TLSCertificateList['listen_host_match']>('no_certificate')

/**
 * 設定 `tls_enabled`（`GuiDesign.md` 5.12.1）。
 *
 * **TLS タブで切り替える**。一般タブの設定一覧には置かない——
 * **関係するものが2つのタブに分かれているのは筋が悪い。**
 *
 * **`tlsEnabled`（実際の待受）とは別物である。** **切り替えは即時に反映される**
 * ので通常は一致するが、**証明書を読めないと切り替えが起きず**、設定だけが
 * 有効になる（`Design.md` 6.6.1）。
 */
const tlsSetting = ref<Setting | null>(null)
const togglingTls = ref(false)
const secretKeyPresent = ref(true)
/** 鍵の出どころ（`env` / `generated`）。**生成なら代償を画面に出す** */
const secretKeyOrigin = ref('')
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

/**
 * **0枚のときだけ貼り付け欄を一覧より先に出す。**
 *
 * 初回は貼る場所がすぐ見つかり、運用中は期限と使用中が先頭に来る。
 * 順が状態で変わるのは代償だが、**どちらの場面でも「いま見たいもの」が上に来る**
 * ほうを採った。**位置は CSS の order で変える**——v-if で2か所に書くと、
 * 同じ markup が2つになり片方だけ直す事故を生む。
 */
const uploadFirst = computed(() => items.value.length === 0)

/** 状態の札（5.12.1） */
const statusLabelSources: Record<CertificateStatus, string> = {
  active: '使用中',
  pending: '待機中',
  expired: '期限切れ',
  superseded: '世代交代',
}

function statusLabel(status: CertificateStatus): string {
  return uiText(statusLabelSources[status])
}

/**
 * openssl の1コマンド。**手で打ち写すと subjectAltName を落としやすい**ので、コピーさせる。
 *
 * **本文は tls-self-signed.sh に置く。** サーバの試験（`tlscert` の
 * `TestSelfSignedCommand`）が同じファイルを実際に走らせ、出来た証明書の拡張領域を
 * 確かめる——画面と試験で別々に持つと、片方だけ直す事故を生む。
 */
const SELF_SIGNED_CMD = selfSignedCmdRaw.trim()

const CSR_CMD = `openssl req -new -newkey rsa:2048 -nodes \\
  -keyout pb.key -out pb.csr \\
  -subj "/C=JP/ST=Tokyo/O=Example Inc./CN=pb.example.com"`

async function load() {
  loading.value = true
  loadError.value = null
  try {
    const [res, settings] = await Promise.all([
      settingsApi.getCertificates(),
      settingsApi.getSettings(),
    ])
    tlsSetting.value = settings.items.find((s) => s.key === 'tls_enabled') ?? null
    items.value = res.items
    tlsEnabled.value = res.tls_enabled
    listenUrl.value = res.listen_url
    listenHost.value = res.listen_host
    listenHostMatch.value = res.listen_host_match
    secretKeyPresent.value = res.secret_key_present
    secretKeyOrigin.value = res.secret_key_origin
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

const canSubmit = computed(() => certPem.value.trim() !== '' && keyPem.value.trim() !== '')

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
        ? uiText("{value0} を登録しました。この証明書を使い始めます。", { value0: created.common_name })
        : uiText("{value0} を登録しました。{value1} から自動で使われます。", { value0: created.common_name, value1: formatDateTime(created.not_before) })
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
  } finally {
    submitting.value = false
  }
}

/** 有効化後の待受（`https://…`）。**押す前に何になるかを示す** */
const listenUrlIfEnabled = computed(() => listenUrl.value.replace(/^https?:/, 'https:'))

/** いま出している1枚（`status === 'active'`）。**必ず1枚以下である**（11.4） */
const activeCert = computed(() => items.value.find((c) => c.status === 'active') ?? null)

/**
 * いま出す証明書が自己署名か。
 *
 * **自己署名だと、その証明書を持っていないクライアントは繋げない**。
 * **ブラウザは警告を出して続行できるが、
 * エージェントは黙って失敗する**ので、画面を見ている人には分からない。
 */
const activeIsSelfSigned = computed(() => activeCert.value?.is_self_signed === true)

/**
 * 一覧と警告に出す SAN。
 *
 * **`dns_names` だけでは足りない**。あれは `DNS:` の SAN しか持たないので、
 * **IP で繋ぐ構成では「SAN: localhost」と出しながら「127.0.0.1 を覆っています」と
 * 言う**ことになり、利用者が画面だけで検算できない。
 */
function sanText(c: TLSCertificate): string {
  return [...c.dns_names, ...c.ip_addresses].join(', ')
}

/**
 * 復号できない証明書の件数（`ApiDesign.md` 11.4）。
 *
 * **暗号鍵の出どころが変わると、登録済みの証明書を復号できなくなる。**
 * 気づくのが「次に TLS で起動したとき」では遅い——**そのとき起動は失敗する。**
 */
const undecryptableCount = computed(() => items.value.filter((c) => !c.decryptable).length)

/** 1枚も復号できないか。**このまま有効にすると起動に失敗する**（`Design.md` 6.6.1） */
const noneDecryptable = computed(
  () => items.value.length > 0 && undecryptableCount.value === items.value.length,
)

/** 証明書を取り出す URL（11.7）。**HTTPS にする前に取っておける**のが要点である */
const zipUrl = settingsApi.certificateZipUrl

/** クライアントに信頼させる環境変数。**Node.js のクライアントで実証済み** */
const TRUST_ENV = 'export NODE_EXTRA_CA_CERTS=/absolute/path/to/pb.crt'

/**
 * `tls_enabled` を切り替える。
 *
 * **有効にするときだけ確認を挟む。** 誤ると画面へ到達できなくなるためで、
 * 無効化は平文へ戻る操作なので締め出されない（`GuiDesign.md` 5.12.1）。
 */
async function toggleTls(next: boolean) {
  togglingTls.value = true
  notice.value = ''
  actionError.value = null
  try {
    await settingsApi.putSettings([{ key: 'tls_enabled', value: next ? 'true' : 'false' }])
    // **切り替えは即時に効く**。**この画面はもう届かない**——
    // ブラウザは同じスキームで叩き続けるので、読み直さずに新しい URL を案内する。
    // **確認と期限を必ず書く**。
    // 「開き直してください」だけでは、**押さないと300秒で戻ることが伝わらない。**
    const confirmNote =
      uiText("開き直したら画面下の「アクセスできました」を押してください。") +
      uiText("押さないまま300秒が過ぎると、元の設定へ戻ります。")
    notice.value = next
      ? uiText("TLS で待ち受けるようにしました。{value0} で開き直してください。{value1}", { value0: listenUrlIfEnabled.value, value1: confirmNote }) +
        uiText("（切り替わらなかったときは証明書を確かめてください）")
      : uiText("平文で待ち受けるようにしました。{value0} で開き直してください。{value1}", { value0: listenUrl.value.replace(/^https:/, 'http:'), value1: confirmNote })
  } catch (e: unknown) {
    actionError.value = asApiError(e)
  } finally {
    togglingTls.value = false
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
    notice.value = uiText("{value0} を削除しました。", { value0: target.common_name })
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
    notice.value = uiText("コマンドをコピーしました。")
  } catch {
    // クリップボードが使えない環境では何もしない（選択してコピーできる）。
  }
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}
</script>

<template>
  <div class="tab">
    <p v-if="loading" class="muted">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="loadError" class="error" role="alert">{{ loadError.message }}</p>

    <template v-else>
      <!--
        ① 動作状況。**説明文ではなく値を出す。**
        待受のスキームとホストとポートがそのまま読めるようにする。
      -->
      <dl class="status">
        <dt>{{ $ui('動作状況') }}</dt>
        <dd>
          <code :class="{ secure: tlsEnabled }">{{ listenUrl }}</code>
          <span class="muted note">
            <template v-if="tlsEnabled">{{ $ui('TLS で終端しています') }}</template>
            <template v-else>{{ $ui('TLS は無効です') }}</template>
          </span>
        </dd>
      </dl>

      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="actionError" class="error" role="alert">{{ actionError.message }}</p>

      <!--
        ①' TLS の切り替え。**TLS タブで扱う**。
        設定の実効値と実際の待受はずれるので、**両方を出す。**
      -->
      <section v-if="tlsSetting" class="block toggle">
        <h3>{{ $ui('TLS で待ち受ける') }}</h3>
        <p v-if="!tlsSetting.editable" class="muted"> {{ $ui('この設定は') }}{{
            tlsSetting.source === 'env' ? $ui("環境変数 {value0}", { value0: tlsSetting.env_key }) : $ui("設定ファイル")
          }}{{ $ui('で固定されています（いまの値:') }} {{ tlsSetting.value }}）
        </p>
        <template v-else>
          <!--
            **押す前に「何になるか」を示す**。
            「エンドポイントがどのアドレス・ドメインなのか、ポート番号は何番なのか
            わからないので、有効ボタンを押す前に確認がしたい」。
          -->
          <dl v-if="tlsSetting.value !== 'true'" class="preview">
            <dt>{{ $ui('有効にすると') }}</dt>
            <dd><code>{{ listenUrlIfEnabled }}</code> {{ $ui('で待ち受けます') }}</dd>
          </dl>

          <!--
            **鍵の出どころが変わると、登録済みの証明書を復号できなくなる**。
            **このまま有効にして再起動すると起動に失敗する**ので、押す前に出す。
          -->
          <p v-if="noneDecryptable" class="warn"> {{ $ui('⚠ 登録済みの証明書は') }}<strong>{{ $ui('別の鍵で暗号化されています。') }}</strong> {{ $ui('このままでは TLS を有効にできません。元の鍵（') }}<code>PB_SECRET_KEY</code>{{ $ui('）に 戻すか、証明書を登録し直してください。') }} </p>
          <p v-else-if="undecryptableCount > 0" class="warn"> {{ $ui('⚠ 一部の証明書を復号できません（') }}{{ undecryptableCount }} {{ $ui('件）。 別の鍵で暗号化されています。') }} </p>

          <!--
            証明書の名前とアクセス先が合っているか。**判定はサーバが返す**
            （`ApiDesign.md` 11.4）。画面は結果を出すだけである。
          -->
          <p v-if="listenHostMatch === 'uncovered'" class="warn"> {{ $ui('⚠ いま使う証明書は') }} <code>{{ listenHost }}</code> {{ $ui('を覆っていません（証明書の名前:') }} {{ (activeCert && sanText(activeCert)) || '—' }}）。
            <strong>{{ $ui('このままではブラウザが警告を出します。') }}</strong> {{ $ui('アクセスに使うホスト名を SAN に含む証明書を登録してください。') }} </p>
          <p v-else-if="listenHostMatch === 'covered'" class="ok"> {{ $ui('✓ いま使う証明書は') }} <code>{{ listenHost }}</code> {{ $ui('を覆っています') }} </p>
          <!--
            **`0.0.0.0` は待受の表記であって接続先のホスト名ではない**。
            「判定できません」とだけ書くと、利用者は `0.0.0.0` を証明書に入れて
            解決しようとする——**入れても一致しない**ことまで書く。
          -->
          <p v-else-if="listenHostMatch === 'unspecific'" class="warn"> {{ $ui('すべてのアドレスで待ち受けています（') }}<code>{{ listenUrl }}</code>）。
            <strong>{{ $ui('アクセスに使うホスト名が証明書に入っているか確かめてください。') }}</strong>
            <code>0.0.0.0</code> {{ $ui('を証明書に入れても一致しません。') }} </p>

          <!--
            **自己署名だとエージェントが黙って繋がらなくなる**。
            **有効化は止めない**——前段にプロキシを置く構成やブラウザだけの
            用途では困らない。
          -->
          <template v-if="activeIsSelfSigned">
            <p class="warn"> {{ $ui('⚠ この証明書は自己署名です。ブラウザは警告を出し、') }}<strong
                >{{ $ui('エージェントの MCP 接続は失敗します。') }}</strong
              > {{ $ui('繋がるようにするには、証明書をクライアントへ渡す設定が要ります。') }} </p>
            <details class="trust">
              <summary>{{ $ui('クライアントに信頼させる') }}</summary>
              <ol class="muted">
                <li>
                  <strong>{{ $ui('証明書を保存します。') }}</strong>{{ $ui('下の一覧の「保存（zip）」から取れます。') }} <strong>{{ $ui('展開して出てくる') }} <code>.crt</code> {{ $ui('を渡します。') }}</strong>
                  <strong>{{ $ui('HTTPS にする前に取っておいてください') }}</strong
                  >{{ $ui('——HTTPS にしたあとは、繋げないクライアントからは取れません') }} </li>
                <li>
                  <strong>{{ $ui('クライアントに渡します。') }}</strong>{{ $ui('Node.js で動くクライアント （Claude Code など）は次の環境変数を読みます') }} <pre>{{ TRUST_ENV }}</pre>
                  <button type="button" class="link" @click="copy(TRUST_ENV)">{{ $ui('コピー') }}</button>
                </li>
                <li>
                  <strong>{{ $ui('クライアントを再起動します。') }}</strong>{{ $ui('環境変数は起動時にしか 読まれません') }} </li>
              </ol>
              <p class="muted"> {{ $ui('切り分けの手順は') }} <code>docs/Development.md</code> {{ $ui('14.5 にあります。') }} </p>
            </details>
          </template>

          <div class="actions-left">
            <button
              type="button"
              :class="tlsSetting.value === 'true' ? 'secondary' : 'primary'"
              :disabled="togglingTls"
              @click="toggleTls(tlsSetting.value !== 'true')"
            >
              {{ tlsSetting.value === 'true' ? $ui("無効にする") : $ui("有効にする") }}
            </button>
            <span class="muted">{{ $ui('設定値:') }} {{ tlsSetting.value === 'true' ? $ui("有効") : $ui("無効") }}</span>
          </div>
          <!--
            **切り替えは即時である**。それでもずれるのは、証明書を
            読めずに切り替えが起きなかったときで、**再起動しても同じところで
            失敗する**（`GuiDesign.md` 5.12.1）。
          -->
          <p v-if="(tlsSetting.value === 'true') !== tlsEnabled" class="warn"> {{ $ui('⚠ 設定と実際の待受がずれています。') }}<strong>{{ $ui('証明書を確かめてください。') }}</strong> {{ $ui('再起動しても直りません。') }} </p>
          <p v-if="tlsSetting.value === 'true' && items.length === 0" class="error"> {{ $ui('証明書が1枚も登録されていません。この状態では') }}<strong>{{ $ui('TLS に切り替わりません') }}</strong
            >{{ $ui('。証明書を登録するか、TLS を無効に戻してください。') }} </p>
        </template>
      </section>

      <!--
        ② 貼り付け。**DOM は1つだけ置き、位置は CSS の order で変える。**
        0枚のときだけ一覧より先に来る。
      -->
      <section class="block upload" :class="{ 'upload-first': uploadFirst }">
        <h3>{{ $ui('証明書を登録する') }}</h3>

        <!--
          **欄を隠さず、無効化して理由を添える**。
          欄ごと隠すと、**「貼る場所がない」と読めてしまう**
          ——5.8.2 の「選んだ先に何も出ない選択肢を置かない」は、欄そのものへは
          当てない。**押せないボタンは出さないが、貼る場所は見せる。**
        -->
        <!--
          **鍵は PB が用意するので、登録の前に利用者が何かする必要はない。**
          ただし**既定では代償があるので隠さない。**
        -->
        <p v-if="secretKeyOrigin === 'generated'" class="muted note"> {{ $ui('秘密鍵は PB が生成した鍵で暗号化されます。その鍵は DB にあるため、') }}<strong
            >{{ $ui('データベースのバックアップを持ち出せる人は秘密鍵も取り出せます') }}</strong
          >{{ $ui('。それを防ぐには') }} <code>PB_SECRET_KEY</code> {{ $ui('を与えてください（') }}<code
            >openssl rand -base64 32</code
          >）。
        </p>
        <p v-else-if="secretKeyOrigin === 'env'" class="muted note"> {{ $ui('秘密鍵は') }} <code>PB_SECRET_KEY</code> {{ $ui('で与えられた鍵で暗号化されます。') }} </p>
        <p v-if="!secretKeyPresent" class="error"> {{ $ui('いまは登録できません。暗号鍵を用意できていません。') }} </p>

        <label class="field">
          <span>{{ $ui('証明書（.crt / PEM 形式）') }}</span>
          <textarea
            v-model="certPem"
            rows="6"
            spellcheck="false"
            placeholder="-----BEGIN CERTIFICATE-----"
          ></textarea>
        </label>
        <label class="field">
          <span>{{ $ui('秘密鍵（.key / PEM 形式）') }}</span>
          <textarea
            v-model="keyPem"
            rows="6"
            spellcheck="false"
            placeholder="-----BEGIN PRIVATE KEY-----"
          ></textarea>
        </label>
        <p class="muted hint"> {{ $ui('秘密鍵は暗号化して保存され、') }}<strong>{{ $ui('二度と表示されません') }}</strong
          >{{ $ui('。手元の鍵を残しておいてください。') }} </p>
        <div class="actions">
          <button
            type="button"
            class="primary"
            :disabled="!canSubmit || submitting"
            @click="submit"
          >
            {{ submitting ? $ui("登録中…") : $ui("登録") }}
          </button>
        </div>
      </section>

      <!-- ③ 登録済みの証明書 -->
      <section class="block list">
        <h3>{{ $ui('登録済みの証明書') }}</h3>
        <p v-if="items.length === 0" class="muted">{{ $ui('まだ証明書が登録されていません。') }}</p>

        <div v-for="c in items" :key="c.id" class="cert" :class="`st-${c.status}`">
          <div class="head">
            <span class="cn">{{ c.common_name }}</span>
            <span class="badge" :class="`st-${c.status}`">{{ statusLabel(c.status) }}</span>
            <span class="muted kind">{{ c.is_self_signed ? $ui("自己署名") : $ui("認証局発行") }}</span>
          </div>

          <!--
            **状態の札は消さない。** `status` は日付で決まる値であり、復号の可否は
            別の軸である（`ApiDesign.md` 11.4）。**選定はその行を選んでいて、
            出せないのは鍵のせいである**、というのがここで伝えたいことである。
          -->
          <p v-if="!c.decryptable" class="warn"> {{ $ui('⚠ 別の鍵で暗号化されています。') }}<strong>{{ $ui('この証明書は出せません') }}</strong>
          </p>

          <p class="period">
            {{ new Date(c.not_before).toLocaleDateString(uiLocaleTag()) }} 〜
            {{ new Date(c.not_after).toLocaleDateString(uiLocaleTag()) }}
            <span v-if="c.status === 'active'" :class="{ warn: daysLeft(c) < 30 }"
              >{{ $ui('（あと') }} {{ daysLeft(c) }} {{ $ui('日）') }}</span
            >
          </p>

          <!-- 「いつから使われるか」は、自動で切り替わることを確かめられる唯一の場所 -->
          <p v-if="c.status === 'pending'" class="from">
            <strong>{{ formatDateTime(c.not_before) }} {{ $ui('から自動で使われます') }}</strong>
          </p>

          <p v-if="sanText(c)" class="muted san">SAN: {{ sanText(c) }}</p>
          <p class="muted fp">{{ $ui('指紋:') }} {{ c.fingerprint }}</p>
          <p class="muted by">
            {{ formatDateTime(c.uploaded_at) }}
            <template v-if="c.uploaded_by">{{ c.uploaded_by.display_name }} {{ $ui('が登録') }}</template>
          </p>

          <div class="foot">
            <!--
              **取り出せることが循環を断つ**——証明書をクライアントへ渡す
              まで、エージェントは PB へ繋げない。`Content-Disposition` はサーバが
              付けるので、画面は Blob を組み立てない。
            -->
            <a class="save" :href="zipUrl(c.id)" download>{{ $ui('保存（zip）') }}</a>
            <button v-if="canDelete(c)" type="button" class="link danger" @click="deleting = c"> {{ $ui('削除') }} </button>
            <span v-else class="muted"
              >{{ $ui('これを消すと HTTPS で待ち受けられなくなります。平文へ戻すには「一般」タブの') }}<strong
                >{{ $ui('TLS で待ち受ける') }}</strong
              >{{ $ui('を無効にしてください') }}</span
            >
          </div>
        </div>
      </section>

      <!-- ④ 作り方。**セクションを分け、必要なところだけ開く** -->
      <section class="block howto">
        <h3>{{ $ui('証明書の作り方') }}</h3>

        <details :open="showSelfSigned">
          <summary>{{ $ui('自己署名証明書を作る（openssl）') }}</summary>
          <p class="muted"> {{ $ui('開発端末や LAN の中で試すときに使います。ブラウザは警告を出しますが、PB の動作は 認証局発行の証明書と変わりません。') }} </p>
          <pre>{{ SELF_SIGNED_CMD }}</pre>
          <button type="button" class="link" @click="copy(SELF_SIGNED_CMD)">{{ $ui('コピー') }}</button>
          <ul class="muted">
            <li>
              <code>-nodes</code> {{ $ui('は') }}<strong>{{ $ui('パスフレーズを付けない') }}</strong
              >{{ $ui('指定です。PB はパスフレーズ付きの秘密鍵を受け付けません') }} </li>
            <li>
              <strong><code>subjectAltName</code> {{ $ui('にアクセスに使うホスト名を必ず入れます。') }}</strong> {{ $ui('ブラウザが見るのはこちらで、') }}<code>CN</code> {{ $ui('だけでは受け付けません') }} </li>
            <li>
              <code>basicConstraints</code>・<code>keyUsage</code>・<code>extendedKeyUsage</code> {{ $ui('は、') }}<strong>{{ $ui('サーバ証明書としてだけ使える') }}</strong>{{ $ui('ようにする指定です。CA として他の証明書に署名することはできません') }} </li>
          </ul>
          <p class="muted">
            <code>pb.crt</code> {{ $ui('を「証明書」、') }}<code>pb.key</code> {{ $ui('を「秘密鍵」の欄に貼ります。') }} </p>
        </details>

        <details :open="showFormal">
          <summary>{{ $ui('認証局が発行した証明書を登録する') }}</summary>
          <p class="muted"> {{ $ui('サイバートラスト・DigiCert・GlobalSign・Let\'s Encrypt など、発行元によらず手順は 同じです。') }} </p>
          <p class="muted"><strong>{{ $ui('① 秘密鍵と CSR を作る') }}</strong></p>
          <pre>{{ CSR_CMD }}</pre>
          <button type="button" class="link" @click="copy(CSR_CMD)">{{ $ui('コピー') }}</button>
          <p class="muted">
            <code>pb.key</code> {{ $ui('は手元に残し、') }}<strong>{{ $ui('発行元には渡しません') }}</strong>。<code
              >pb.csr</code
            > {{ $ui('を発行元の申込画面へ提出します。') }} </p>
          <p class="muted"><strong>{{ $ui('② 受け取ったものを1つにまとめる') }}</strong></p>
          <p class="muted"> {{ $ui('発行元からはサーバ証明書と中間証明書が別々に届くことが多いです。証明書の欄には') }}<strong
              >{{ $ui('サーバ証明書 → 中間証明書の順') }}</strong
            >{{ $ui('で続けて貼ってください。逆にすると「証明書が信頼できない」と出ます。') }}<strong
              >{{ $ui('ルート証明書は貼らなくてよい') }}</strong
            >{{ $ui('です。') }} </p>
          <p class="muted"><strong>{{ $ui('③ 更新するとき') }}</strong></p>
          <p class="muted">
            <strong>{{ $ui('古いものを消さずに、新しいものを登録します。') }}</strong> {{ $ui('新しい証明書が有効になった時点で自動的に切り替わり、再起動は要りません。') }} </p>
        </details>

        <p class="muted docref"> {{ $ui('発行元ごとの申込手順、連鎖の確かめ方、つまずいたときの対処は') }} <code>docs/Development.md</code> {{ $ui('14章にあります。') }} </p>
      </section>
    </template>

    <Teleport to="body">
      <ConfirmDialog
        v-if="deleting"
        :title="$ui('証明書を削除しますか？')"
        :message="$ui('{value0}（{value1}…）を削除します。この操作は取り消せません。', { value0: deleting.common_name, value1: deleting.fingerprint.slice(0, 23) })"
        :confirm-label="$ui('削除する')"
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
  /* スクロールは親（AppSettingsPage の .page-body）が持つ */
  padding-block: var(--pb-space-3);

  /*
   * **並びは order で決める。**
   * ①動作状況 → ②貼り付け → ③一覧 → ④作り方。
   * **証明書が1枚以上あるときだけ、貼り付けを一覧の後ろへ送る。**
   */
  display: flex;
  flex-direction: column;
}
.status {
  order: 1;
}
.notice,
.error {
  order: 2;
}
.toggle {
  order: 2;
}
.actions-left {
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
}
.preview {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  margin: 0 0 var(--pb-space-2);
  flex-wrap: wrap;
}
.preview dt {
  font-weight: 600;
  flex: none;
}
.preview dd {
  margin: 0;
}
.preview code {
  font-family: var(--pb-font-mono);
  word-break: break-all;
}
.ok {
  color: var(--pb-text);
  font-size: 12px;
  margin: var(--pb-space-1) 0;
}
.note {
  font-size: 12px;
  margin: 0 0 var(--pb-space-2);
}
.upload {
  order: 4;
}
.upload.upload-first {
  order: 3;
}
.list {
  order: 3;
}
.howto {
  order: 5;
}

/* ① 動作状況。**説明文ではなく値を出す** */
.status {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-3);
  margin: 0 0 var(--pb-space-4);
  flex-wrap: wrap;
}
.status dt {
  font-weight: 600;
  flex: none;
}
.status dd {
  margin: 0;
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
  min-width: 0;
}
.status code {
  font-family: var(--pb-font-mono);
  font-size: 14px;
  word-break: break-all;
}
.status code.secure {
  color: var(--pb-text);
}
.status .note {
  font-size: 12px;
}

.block {
  margin-bottom: var(--pb-space-6);
}
.block h3 {
  font-size: 14px;
  margin: 0 0 var(--pb-space-2);
  border-bottom: 1px solid var(--pb-border);
  padding-bottom: var(--pb-space-1);
}
.muted {
  color: var(--pb-text-muted);
}
.notice {
  color: var(--pb-text);
}
.error {
  color: var(--pb-danger-text);
}
.warn {
  color: var(--pb-warning-text);
}
.cert {
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
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
  font-size: 11px;
  padding: 0 var(--pb-space-2);
  border-radius: var(--pb-radius);
  border: 1px solid var(--pb-border);
}
.badge.st-active {
  border-color: var(--pb-accent);
  color: var(--pb-accent);
}
.badge.st-expired {
  color: var(--pb-danger-text);
  border-color: var(--pb-danger-text);
}
.kind,
.period,
.from,
.san,
.fp,
.by {
  font-size: 12px;
  margin: var(--pb-space-1) 0 0;
}
.fp {
  font-family: var(--pb-font-mono);
  word-break: break-all;
}
.foot {
  margin-top: var(--pb-space-2);
  font-size: 12px;
  display: flex;
  gap: var(--pb-space-3);
  align-items: baseline;
  flex-wrap: wrap;
}
.save {
  color: var(--pb-accent);
}
/* **自己署名の手順は畳んで出す。** 毎回読むものではない（5.12.1） */
.trust {
  margin: var(--pb-space-2) 0;
}
.trust summary {
  cursor: pointer;
}
.trust ol {
  padding-left: var(--pb-space-4);
}
.trust li {
  margin-bottom: var(--pb-space-2);
}
.trust pre {
  white-space: pre-wrap;
  word-break: break-all;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: var(--pb-elevated);
  padding: var(--pb-space-2);
  border-radius: var(--pb-radius);
  margin: var(--pb-space-1) 0;
}
.field {
  display: block;
  margin-bottom: var(--pb-space-3);
}
.field span {
  display: block;
  font-size: 12px;
  margin-bottom: var(--pb-space-1);
}
.field textarea {
  width: 100%;
  font-family: var(--pb-font-mono);
  font-size: 12px;
}
.field textarea:disabled {
  /* **欄は見せるが触れないことを見た目で示す** */
  background: var(--pb-surface);
  cursor: not-allowed;
}
.hint {
  font-size: 12px;
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
  background: var(--pb-surface);
  padding: var(--pb-space-2);
  border-radius: var(--pb-radius);
  font-size: 12px;
}
.howto ul {
  font-size: 12px;
  padding-left: var(--pb-space-4);
}
.docref {
  font-size: 12px;
}
</style>
