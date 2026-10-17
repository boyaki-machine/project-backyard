import { expect, test } from '@playwright/test'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'

// ビルドのみ偽装し、実際の配布スクリプトでファイルと参照の整合性を検証する。
for (const target of ['compose', 'k8s', 'docker']) {
  for (const push of [false, true]) {
    test(`release ${target} includes the required images (push=${push})`, () => {
      const repo = resolve('..')
      const root = mkdtempSync(resolve(tmpdir(), 'pb-release-test-'))
      try {
        mkdirSync(resolve(root, 'bin'))
        const log = resolve(root, 'calls')
        writeFileSync(resolve(root, 'bin/docker'), `#!/usr/bin/env python3
import json, os, pathlib, sys
args=sys.argv[1:]
with open(os.environ['PB_RELEASE_TEST_LOG'],'a') as f: f.write(json.dumps(args)+'\\n')
if '--output' in args:
    output=args[args.index('--output')+1]
    dest=next(p[5:] for p in output.split(',') if p.startswith('dest='))
    pathlib.Path(dest).touch()
`, { mode: 0o700 })
        const out = resolve(root, 'out')
        const remote = 'registry.example.com/pb:test'
        const args = [resolve(repo, 'deploy/prod/build-release.sh'), '--target', target,
          '--arch', 'arm64', '--out', out, ...(push ? ['--push', remote] : [])]
        const result = spawnSync('bash', args, { encoding: 'utf8',
          env: { ...process.env, PATH: `${root}/bin:${process.env.PATH}`, PB_RELEASE_TEST_LOG: log } })
        expect(result.error).toBeUndefined()
        expect(result.status, result.stderr).toBe(0)
        const calls: string[][] = readFileSync(log, 'utf8').trim().split('\n').map(line => JSON.parse(line))
        const builds = calls.filter(call => call[0] === 'buildx' && call[1] === 'build')
        const hasDB = target !== 'docker'
        expect(builds).toHaveLength(hasDB ? 2 : 1)
        const version = readFileSync(resolve(repo, 'VERSION'), 'utf8').trim()
        const app = push ? remote : `project-backyard:${version}`
        const db = push ? `${remote}-db` : `project-backyard-db:${version}`
        for (const [index, image] of [app, ...(hasDB ? [db] : [])].entries()) {
          const call = builds[index]!
          expect(call[call.indexOf('--platform') + 1]).toBe('linux/arm64')
          expect(call).toContain('--pull')
          expect(call[call.indexOf('--no-cache-filter') + 1]).toBe(index === 0 ? 'client,server' : 'gosu,db')
          if (push) {
            expect(call).toContain('--push')
            expect(call[call.indexOf('--tag') + 1]).toBe(image)
          } else {
            expect(call[call.indexOf('--output') + 1]).toContain(`name=${image}`)
          }
        }
        expect(readdirSync(out).filter(name => name.endsWith('.tar'))).toHaveLength(push ? 0 : hasDB ? 2 : 1)
        if (hasDB) {
          expect(builds[1]).toContain('--pull')
          expect(builds[1]![builds[1]!.indexOf('--no-cache-filter') + 1]).toBe('gosu,db')
        }
        if (target === 'compose') {
          const compose = readFileSync(resolve(out, 'compose.yaml'), 'utf8')
          expect(compose).toContain(`image: ${app}`)
          expect(compose).toContain(`image: ${db}`)
          expect(compose).not.toContain('__PB_')
          expect(readFileSync(resolve(out, 'initdb/01_roles.sh'), 'utf8')).toBe(readFileSync(resolve(repo, 'deploy/base/initdb/01_roles.sh'), 'utf8'))
        } else if (target === 'k8s') {
          expect(readFileSync(resolve(out, 'k8s/app.yaml'), 'utf8')).toContain(`image: ${app}`)
          const manifest = readFileSync(resolve(out, 'k8s/db.yaml'), 'utf8')
          expect(manifest).toContain(`image: ${db}`)
          expect(manifest).not.toMatch(/__PB_|__INITDB_/)
          const init = readFileSync(resolve(repo, 'deploy/base/initdb/01_roles.sh'), 'utf8')
          for (const line of init.trimEnd().split('\n').filter(Boolean)) expect(manifest).toContain(`    ${line}`)
        } else {
          expect(readFileSync(resolve(out, 'run.sh'), 'utf8')).toContain(app)
        }
      } finally {
        rmSync(root, { recursive: true, force: true })
      }
    })
  }
}
