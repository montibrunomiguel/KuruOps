import assert from 'node:assert/strict'
import { test } from 'node:test'

import { calledVulnerabilities, evaluate, parseStream, validateExceptions } from './govulncheck-gate.mjs'

const reason = 'a reason that is long enough to count as an explanation'
const exception = (id, expires) => ({ id, expires, reason })

// Shaped like govulncheck -format json: pretty-printed objects, back to back.
const stream = `{
  "config": { "protocol_version": "v1.0.0" }
}
{
  "osv": { "id": "GO-2026-1111", "summary": "has a } brace and a \\" quote" }
}
{
  "osv": { "id": "GO-2026-2222", "summary": "only imported" }
}
{
  "finding": {
    "osv": "GO-2026-1111",
    "trace": [ { "module": "m", "function": "Parse", "package": "p" }, { "module": "app" } ]
  }
}
{
  "finding": { "osv": "GO-2026-1111", "trace": [ { "module": "m", "function": "Other" } ] }
}
{
  "finding": { "osv": "GO-2026-2222", "trace": [ { "module": "m", "package": "p" } ] }
}
`

test('parseStream splits back-to-back objects and survives braces and quotes in strings', () => {
  const objects = parseStream(stream)
  assert.equal(objects.length, 6)
  assert.equal(objects[1].osv.summary, 'has a } brace and a " quote')
})

test('parseStream rejects truncated output instead of reading it as "no findings"', () => {
  assert.throws(() => parseStream('{ "finding": { "osv": "GO-2026-1111"'), /mid-object/)
})

test('calledVulnerabilities keeps only findings that reach a function, once each', () => {
  assert.deepEqual(calledVulnerabilities(parseStream(stream)), [
    { id: 'GO-2026-1111', summary: 'has a } brace and a " quote' },
  ])
})

test('an unlisted vulnerability fails', () => {
  const r = evaluate([{ id: 'GO-2026-1111', summary: '' }], [], '2026-11-12')
  assert.equal(r.failures.length, 1)
  assert.match(r.failures[0].why, /not covered/)
})

test('a listed vulnerability is accepted until its expiry date, inclusive', () => {
  const ex = [exception('GO-2026-1111', '2027-02-12')]
  const v = [{ id: 'GO-2026-1111', summary: '' }]
  assert.equal(evaluate(v, ex, '2027-02-12').failures.length, 0)
  assert.equal(evaluate(v, ex, '2027-02-12').accepted.length, 1)
  const late = evaluate(v, ex, '2027-02-13')
  assert.equal(late.failures.length, 1)
  assert.match(late.failures[0].why, /expired on 2027-02-12/)
})

test('an exception only covers its own ID', () => {
  const ex = [exception('GO-2026-1111', '2027-02-12')]
  const r = evaluate([{ id: 'GO-2026-1111' }, { id: 'GO-2026-9999' }], ex, '2026-11-12')
  assert.deepEqual(r.failures.map((f) => f.id), ['GO-2026-9999'])
})

test('an exception for something no longer reported is flagged as stale', () => {
  const r = evaluate([], [exception('GO-2026-1111', '2027-02-12')], '2026-11-12')
  assert.deepEqual(r.stale, ['GO-2026-1111'])
  assert.equal(r.failures.length, 0)
})

test('validateExceptions insists on a real id, a date and a reason', () => {
  assert.throws(() => validateExceptions({}), /array/)
  assert.throws(() => validateExceptions([exception('nope', '2027-02-12')]), /bad id/)
  assert.throws(() => validateExceptions([exception('GO-2026-1111', 'soon')]), /YYYY-MM-DD/)
  assert.throws(() => validateExceptions([exception('GO-2026-1111', '2027-13-45')]), /YYYY-MM-DD/)
  assert.throws(() => validateExceptions([{ id: 'GO-2026-1111', expires: '2027-02-12', reason: 'ok' }]), /reason/)
  assert.throws(
    () => validateExceptions([exception('GO-2026-1111', '2027-02-12'), exception('GO-2026-1111', '2027-03-01')]),
    /twice/,
  )
  assert.equal(validateExceptions([exception('GO-2026-1111', '2027-02-12')]).length, 1)
})
