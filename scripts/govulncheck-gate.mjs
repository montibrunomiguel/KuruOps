#!/usr/bin/env node
// Runs govulncheck over the backend and fails on any vulnerability the code
// actually calls -- the same bar `govulncheck ./...` applies in text mode (exit
// 3) -- except for the IDs listed in backend/govulncheck-exceptions.json.
//
// Why this exists: an advisory can be fixed in code yet stay "affected" forever
// in the Go vulnerability database when the entry never records a fixed
// version (GO-2026-6909 / crewjam/saml is the case that prompted this). The
// only built-in answer is to switch the scan off. This keeps it on and lets one
// known ID through, on the record:
//
//   - every exception carries a reason and an expiry date; past the date it
//     fails again, so an acceptance can't be forgotten
//   - an exception for an ID the scan no longer reports is flagged as stale
//   - any other vulnerability in called code still fails the build
//
// Run from anywhere: `node scripts/govulncheck-gate.mjs` (needs govulncheck on
// PATH). `node --test scripts/` runs the unit tests for the decision logic.

import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const BACKEND = join(dirname(fileURLToPath(import.meta.url)), '..', 'backend')
const EXCEPTIONS_FILE = join(BACKEND, 'govulncheck-exceptions.json')

const ID_PATTERN = /^GO-\d{4}-\d{4,}$/
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/

// govulncheck -format json prints a stream of pretty-printed objects back to
// back, not a JSON array and not one per line. Split on top-level braces.
export function parseStream(text) {
  const objects = []
  let depth = 0
  let start = -1
  let inString = false
  let escaped = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inString) {
      if (escaped) escaped = false
      else if (c === '\\') escaped = true
      else if (c === '"') inString = false
      continue
    }
    if (c === '"') inString = true
    else if (c === '{') {
      if (depth === 0) start = i
      depth++
    } else if (c === '}') {
      depth--
      if (depth === 0) objects.push(JSON.parse(text.slice(start, i + 1)))
    }
  }
  if (depth !== 0 || inString) throw new Error('govulncheck output ended mid-object')
  return objects
}

// A finding whose first trace frame names a function is one the code reaches;
// the module- and package-level ones only say a dependency is present.
export function calledVulnerabilities(objects) {
  const summaries = new Map()
  const called = new Set()
  for (const o of objects) {
    if (o.osv) summaries.set(o.osv.id, o.osv.summary ?? '')
    if (o.finding?.trace?.[0]?.function) called.add(o.finding.osv)
  }
  return [...called].sort().map((id) => ({ id, summary: summaries.get(id) ?? '' }))
}

export function validateExceptions(list) {
  if (!Array.isArray(list)) throw new Error('exceptions file must be a JSON array')
  const seen = new Set()
  for (const e of list) {
    if (!ID_PATTERN.test(e?.id ?? '')) throw new Error(`exception has a bad id: ${JSON.stringify(e)}`)
    if (seen.has(e.id)) throw new Error(`${e.id} is listed twice`)
    seen.add(e.id)
    if (!DATE_PATTERN.test(e.expires ?? '') || Number.isNaN(Date.parse(e.expires))) {
      throw new Error(`${e.id}: "expires" must be a YYYY-MM-DD date`)
    }
    if (typeof e.reason !== 'string' || e.reason.trim().length < 20) {
      throw new Error(`${e.id}: "reason" must explain why the risk is accepted`)
    }
  }
  return list
}

// today is a YYYY-MM-DD string; an exception is good through its expiry date.
export function evaluate(vulns, exceptions, today) {
  const byId = new Map(exceptions.map((e) => [e.id, e]))
  const failures = []
  const accepted = []
  for (const v of vulns) {
    const e = byId.get(v.id)
    if (!e) failures.push({ ...v, why: 'not covered by an exception' })
    else if (e.expires < today) failures.push({ ...v, why: `exception expired on ${e.expires}` })
    else accepted.push({ ...v, expires: e.expires, reason: e.reason })
  }
  const reported = new Set(vulns.map((v) => v.id))
  const stale = exceptions.filter((e) => !reported.has(e.id)).map((e) => e.id)
  return { failures, accepted, stale }
}

function main() {
  const exceptions = validateExceptions(JSON.parse(readFileSync(EXCEPTIONS_FILE, 'utf8')))

  const run = spawnSync('govulncheck', ['-format', 'json', './...'], {
    cwd: BACKEND,
    encoding: 'utf8',
    maxBuffer: 512 * 1024 * 1024,
  })
  if (run.error) throw new Error(`could not run govulncheck: ${run.error.message}`)
  // In JSON mode govulncheck exits 0 even with findings; anything else is the
  // tool itself failing, which must not read as "no vulnerabilities".
  if (run.status !== 0) throw new Error(`govulncheck exited ${run.status}:\n${run.stderr}`)

  const today = new Date().toISOString().slice(0, 10)
  const { failures, accepted, stale } = evaluate(calledVulnerabilities(parseStream(run.stdout)), exceptions, today)

  for (const a of accepted) {
    console.log(`accepted ${a.id} until ${a.expires}: ${a.summary}\n  ${a.reason}`)
  }
  for (const id of stale) {
    console.log(`note: ${id} is in govulncheck-exceptions.json but no longer reported -- remove it`)
  }
  if (failures.length > 0) {
    for (const f of failures) {
      console.error(`FAIL ${f.id} (${f.why}): ${f.summary}\n  https://pkg.go.dev/vuln/${f.id}`)
    }
    console.error(`\n${failures.length} vulnerability(ies) in called code. Run \`govulncheck -show verbose ./...\` in backend/ for traces.`)
    process.exit(1)
  }
  console.log(`govulncheck: no unaccepted vulnerabilities in called code (${accepted.length} accepted exception(s)).`)
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    main()
  } catch (err) {
    console.error(`govulncheck-gate: ${err.message}`)
    process.exit(2)
  }
}
