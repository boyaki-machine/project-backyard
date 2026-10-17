import { expect, test } from '@playwright/test'
import { spawnSync } from 'node:child_process'
import { copyFileSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'

test('Git excludes local credentials while keeping source and secret templates', () => {
  const root = mkdtempSync(resolve(tmpdir(), 'pb-ignore-test-'))
  const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1' }
  const git = (args: string[]) => spawnSync('git', args, { cwd: root, env, encoding: 'utf8' })
  try {
    copyFileSync(resolve('../.gitignore'), resolve(root, '.gitignore'))
    expect(git(['init', '-q']).status).toBe(0)
    const hidden = ['.codex/config.toml', '.codex/auth.json', '.npmrc', 'client/.npmrc',
      '.netrc', 'server/.aws/credentials', '.ssh/id_ed25519', 'client/.docker/config.json',
      'client/store.pfx', 'server/store.jks', 'client/.env', 'deploy/dev/secrets/db_password']
    const visible = ['client/package-lock.json', 'server/go.sum', 'deploy/Dockerfile.db',
      'deploy/dev/secrets/db_password.example', 'deploy/prod/native/secrets/pgpass.example',
      'deploy/prod/k8s/secret.example.yaml']
    for (const file of hidden) expect(git(['check-ignore', '--no-index', '-q', file]).status, file).toBe(0)
    for (const file of visible) expect(git(['check-ignore', '--no-index', '-q', file]).status, file).toBe(1)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
