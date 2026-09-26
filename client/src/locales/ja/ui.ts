export function translate(source: string): string {
  return source
}

export const defaultStatusNames: Record<string, string> = {
  todo: '未着手',
  in_progress: '進行中',
  review: 'レビュー中',
  approval: '承認待ち',
  done: '完了',
}
