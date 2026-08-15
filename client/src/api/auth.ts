/**
 * 認証まわりのエンドポイント（`ApiDesign.md` 3.1 / 3.2 / 4.1）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { components } from './schema'

/** `POST /auth/login` と `GET /me` が返す共通の本体（`ApiDesign.md` 3.1） */
export type Session = components['schemas']['Session']
export type Actor = components['schemas']['Actor']
export type SessionProject = components['schemas']['SessionProject']

/**
 * ログイン。成功すると `pb_session` と `pb_csrf` が Set-Cookie される。
 *
 * 応答には `GET /me` と同じ内容が入るため、続けて `/me` を呼ぶ必要はない。
 */
export function login(email: string, password: string): Promise<Session> {
  return api.post<Session>('/auth/login', { email, password })
}

/** ログアウト。204 を返し、2つの Cookie が削除される（`ApiDesign.md` 3.2） */
export function logout(): Promise<void> {
  return api.post<void>('/auth/logout')
}

/**
 * 自分自身と実効権限（`ApiDesign.md` 4.1）。
 *
 * `allowUnauthenticated` を付けるのは、起動時に「ログイン済みか」を確かめる
 * ために呼ぶためである。未ログインなら 401 が正常な応答であり、
 * セッション失効の共通処理を走らせてはならない。
 */
export function me(): Promise<Session> {
  return api.get<Session>('/me', { allowUnauthenticated: true })
}
