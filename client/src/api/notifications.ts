/**
 * 自分宛ての通知（`ApiDesign.md` 4.8）。画面は `GuiDesign.md` 5.16。
 *
 * **通知を作る口は無い。** 通知はサーバの中で使い手が作る（`DbDesign.md` 6.33）。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { components } from './schema'

/** 通知1件（4.8.1） */
export type Notification = components['schemas']['Notification']

/** `GET /me/notifications` の応答 */
export type NotificationList = components['schemas']['NotificationList']

/** 一覧の絞り込み（4.8.1） */
export type NotificationFilter = 'all' | 'unread'

/** 自分宛ての通知を新しい順に読む（4.8.1） */
export function listNotifications(filter: NotificationFilter, page: number, perPage: number): Promise<NotificationList> {
  const q = new URLSearchParams({ filter, page: String(page), per_page: String(perPage) })
  return api.get<NotificationList>(`/me/notifications?${q.toString()}`)
}

/** 未読の数（4.8.2）。**どの画面にいても30秒ごとに引く** */
export function unreadNotificationCount(): Promise<components['schemas']['NotificationUnreadCount']> {
  return api.get('/me/notifications/unread-count')
}

/** 既読にする・未読に戻す（4.8.3） */
export function setNotificationRead(id: string, read: boolean): Promise<Notification> {
  return api.patch<Notification>(`/me/notifications/${encodeURIComponent(id)}`, { read })
}

/** すべて既読にする（4.8.4） */
export function readAllNotifications(): Promise<components['schemas']['NotificationReadAll']> {
  return api.post('/me/notifications/read-all')
}

/** 削除する（4.8.5。受けた人・出した人） */
export function deleteNotification(id: string): Promise<void> {
  return api.del<void>(`/me/notifications/${encodeURIComponent(id)}`)
}
