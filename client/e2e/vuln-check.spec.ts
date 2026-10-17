import { expect, test } from '@playwright/test'
import { execFileSync, spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'

// 実通信なしで検査の対象と失敗時の継続を確かめる。対象一覧はリポジトリの正本から読む。
for (const failGo of [false, true]) {
  test(`vulnerability check covers every Go module and tool, even on failure=${failGo}`, () => {
    const repo = resolve('..')
    const root = realpathSync(mkdtempSync(resolve(tmpdir(), 'pb-vuln-test-')))
    try {
      const mods = execFileSync('git', ['ls-files', '**/go.mod'], { cwd: repo, encoding: 'utf8' }).trim().split('\n')
      mkdirSync(resolve(root, 'bin'))
      mkdirSync(resolve(root, 'client'))
      mkdirSync(resolve(root, 'deploy/prod'), { recursive: true })
      writeFileSync(resolve(root, 'VERSION'), '0.0.0\n')
      writeFileSync(resolve(root, 'deploy/prod/build-release.sh'), 'goose_tags="test"\n')
      for (const mod of mods) mkdirSync(resolve(root, dirname(mod)), { recursive: true })
      const log = resolve(root, 'calls')
      const record = '#!/bin/sh\nprintf "%s\t" "$PWD" "$@" >> "$PB_VULN_TEST_LOG"\nprintf "\n" >> "$PB_VULN_TEST_LOG"\n'
      writeFileSync(resolve(root, 'bin/go'), record + 'exit "${PB_VULN_TEST_FAIL_GO:-0}"\n', { mode: 0o700 })
      writeFileSync(resolve(root, 'bin/npm'), record, { mode: 0o700 })
      const result = spawnSync('make', ['--no-print-directory', '-f', resolve(repo, 'Makefile'), 'vuln-check'], {
        cwd: root, encoding: 'utf8', env: { ...process.env, PATH: `${root}/bin:${process.env.PATH}`,
          PB_VULN_TEST_LOG: log, PB_VULN_TEST_FAIL_GO: failGo ? '1' : '0' },
      })
      expect(result.error).toBeUndefined()
      expect(result.status === 0).toBe(!failGo)
      const calls = readFileSync(log, 'utf8').trim().split('\n').map(line => line.trim().split('\t'))
      const go = calls.filter(call => call[1] === 'tool' && call[2] === 'govulncheck')
      const covered = go.map(call => {
        const i = call.indexOf('-C')
        return resolve(call[0]!, i < 0 ? '.' : call[i + 1]!)
      })
      for (const mod of mods) expect(covered).toContain(resolve(root, dirname(mod)))
      const toolMod = readFileSync(resolve(repo, 'server/tools/go.mod'), 'utf8')
      const tools = toolMod.match(/^tool \(([\s\S]*?)\)/m)![1]!.trim().split(/\s+/)
      const toolCalls = go.filter(call => call[0] === resolve(root, 'server/tools') && !call.includes('-C')).flat()
      for (const tool of tools) expect(toolCalls).toContain(tool)
      expect(calls.some(call => call[0] === resolve(root, 'client') && call[1] === 'audit')).toBe(true)
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })
}
