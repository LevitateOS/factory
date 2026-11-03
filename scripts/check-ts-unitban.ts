// TS unitban: unit testing is bureaucracy (AGENTS.md). Fails on test-runner
// usage outside the allowlisted browser tests (tools/unitban/allow-ts.txt):
// R1a (tests/ or *.test.ts): value imports from test runners, except the
//     lifecycle hooks {before, after, beforeEach, afterEach, beforeAll,
//     afterAll} the kept browser drivers need. Type-only imports pass.
// R1b (all other .ts): any import from a test runner, hooks included.
// R2 (tests/ or *.test.ts): calls to undeclared test globals
//     {test, describe, it, expect, before, after, beforeEach, afterEach,
//     beforeAll, afterAll}. Locally declared names are excluded, so helper
//     functions that happen to share a name do not trip the gate.
// Run `bun scripts/check-ts-unitban.ts --self-test` for inline fixtures.
import {readFile} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import assert from 'node:assert/strict';
// Classic compiler API (root typescript is v7 native without it); shared
// with tools/lit-check, which pins this copy.
import * as ts from '../tools/lit-check/node_modules/typescript/lib/typescript.js';

const ROOT = new URL('..', import.meta.url).pathname.replace(/\/$/, '');
const ALLOW = `${ROOT}/tools/unitban/allow-ts.txt`;
const RUNNERS = new Set([
  'node:test',
  'node:test/reporters',
  'bun:test',
  'vitest',
  'jest',
  '@jest/globals',
  'mocha',
  'ava',
  'tap',
  'tape',
  'uvu',
]);
const HOOKS = new Set(['before', 'after', 'beforeEach', 'afterEach', 'beforeAll', 'afterAll']);
const GLOBALS = new Set(['test', 'describe', 'it', 'expect', ...HOOKS]);
const DEFINERS = new Set(['test', 'describe', 'it']);

interface Finding {
  path: string;
  line: number;
  rule: string;
  text: string;
}

function inTestScope(path: string): boolean {
  return path.startsWith('tests/') || path.endsWith('.test.ts') || path.endsWith('.test.tsx');
}

function declaredNames(source: ts.SourceFile): Set<string> {
  const names = new Set<string>();
  const visit = (node: ts.Node): void => {
    if (
      (ts.isFunctionDeclaration(node) ||
        ts.isClassDeclaration(node) ||
        ts.isInterfaceDeclaration(node) ||
        ts.isTypeAliasDeclaration(node) ||
        ts.isEnumDeclaration(node)) &&
      node.name !== undefined
    ) {
      names.add(node.name.text);
    } else if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
      names.add(node.name.text);
    } else if (ts.isImportSpecifier(node)) {
      names.add(node.name.text);
    } else if (ts.isImportClause(node) && node.name !== undefined) {
      names.add(node.name.text);
    } else if (ts.isNamespaceImport(node)) {
      names.add(node.name.text);
    } else if (ts.isParameter(node) && ts.isIdentifier(node.name)) {
      names.add(node.name.text);
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return names;
}

function findingsFor(path: string, text: string, allow: Set<string>): Finding[] {
  const findings: Finding[] = [];
  if (allow.has(path)) return findings;
  const source = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true);
  const lineOf = (node: ts.Node): number => source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
  const scope = inTestScope(path);

  const checkImport = (module: string, node: ts.Node, names: string[], typeOnly: boolean): void => {
    if (!RUNNERS.has(module)) return;
    if (!scope) {
      findings.push({path, line: lineOf(node), rule: 'R1b', text: `test-runner import '${module}' outside tests/`});
      return;
    }
    for (const name of names) {
      if (typeOnly || HOOKS.has(name)) continue;
      findings.push({path, line: lineOf(node), rule: 'R1a', text: `test-runner import '${name}' from '${module}'`});
    }
    if (names.length === 0 && !typeOnly) {
      findings.push({path, line: lineOf(node), rule: 'R1a', text: `test-runner import from '${module}'`});
    }
  };

  const declared = declaredNames(source);
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
      const module = node.moduleSpecifier.text;
      const clause = node.importClause;
      if (clause === undefined) {
        checkImport(module, node, [], false);
      } else if (clause.isTypeOnly) {
        return;
      } else if (clause.namedBindings === undefined) {
        if (clause.name !== undefined) checkImport(module, node, [clause.name.text], false);
      } else if (ts.isNamespaceImport(clause.namedBindings)) {
        checkImport(module, node, [], false);
      } else {
        const names = clause.namedBindings.elements
          .filter((e) => !e.isTypeOnly)
          .map((e) => (e.propertyName !== undefined ? e.propertyName.text : e.name.text));
        checkImport(module, node, names, names.length === 0 && clause.namedBindings.elements.length > 0);
      }
    } else if (
      scope &&
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      GLOBALS.has(node.expression.text) &&
      !declared.has(node.expression.text)
    ) {
      findings.push({
        path,
        line: lineOf(node),
        rule: 'R2',
        text: `bare test-global call '${node.expression.text}(...)'`,
      });
    } else if (
      scope &&
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      ts.isIdentifier(node.expression.expression) &&
      DEFINERS.has(node.expression.expression.text) &&
      !declared.has(node.expression.expression.text)
    ) {
      findings.push({
        path,
        line: lineOf(node),
        rule: 'R2',
        text: `bare test-global call '${node.expression.expression.text}.${node.expression.name.text}(...)'`,
      });
    } else if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'require') {
      const [first] = node.arguments;
      if (first !== undefined && ts.isStringLiteral(first) && RUNNERS.has(first.text)) {
        findings.push({
          path,
          line: lineOf(node),
          rule: scope ? 'R1a' : 'R1b',
          text: `test-runner require('${first.text}')`,
        });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return findings;
}

async function loadAllow(): Promise<Set<string>> {
  const text = await readFile(ALLOW, 'utf8');
  return new Set(
    text
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l !== '' && !l.startsWith('#'))
  );
}

async function main(): Promise<void> {
  const allow = await loadAllow();
  const proc = Bun.spawnSync(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z', '*.ts', '*.tsx'], {
    cwd: ROOT,
  });
  if (proc.exitCode !== 0) throw new Error('git ls-files failed');
  const files = proc.stdout
    .toString()
    .split('\0')
    .filter((f) => f !== '' && existsSync(`${ROOT}/${f}`));
  const findings: Finding[] = [];
  for (const path of files) {
    const text = await readFile(`${ROOT}/${path}`, 'utf8');
    findings.push(...findingsFor(path, text, allow));
  }
  if (findings.length > 0) {
    for (const f of findings) console.log(`${f.path}:${f.line}: [${f.rule}] ${f.text}`);
    console.log(
      `ts-unitban failed: ${findings.length} finding(s). Unit tests are banned (AGENTS.md); see docs/development/why-no-unit-tests.md.`
    );
    process.exit(1);
  }
  console.log(`ts-unitban passed: no test-runner usage outside ${allow.size} allowlisted files.`);
}

function selfTest(): void {
  const allow = new Set(['tests/keep.test.ts']);
  const cases: [string, string, string, number][] = [
    ['R1a import', 'tests/fake.test.ts', `import test from 'node:test';\ntest('x', () => {});`, 1],
    ['R1a hooks pass', 'tests/fix.ts', `import {before, after} from 'node:test';\nbefore(() => {});`, 0],
    [
      'R1a type-only pass',
      'tests/fix.ts',
      `import type {TestContext} from 'node:test';\nconst x: TestContext | null = null;`,
      0,
    ],
    ['R1a namespace', 'tests/fake.test.ts', `import * as t from 'node:test';\nconsole.log(t);`, 1],
    ['R1b prod import', 'frontend/x.ts', `import {test} from 'node:test';\nconsole.log(test);`, 1],
    ['R1b prod hooks', 'frontend/x.ts', `import {before} from 'node:test';\nconsole.log(before);`, 1],
    ['R2 bare call', 'tests/y.ts', `test('x', () => {});`, 1],
    ['R2 allowlisted', 'tests/keep.test.ts', `test('x', () => {});`, 0],
    ['R2 declared local', 'tests/y.ts', `function test() {}\ntest();`, 0],
    ['R2 method call', 'tests/y.ts', `const o = {test() {}};\no.test();`, 0],
    ['R2 focused', 'tests/y.ts', `test.only('x', () => {});`, 1],
    ['R2 comment+string', 'tests/y.ts', `// test(\nconst s = "test(";\nconsole.log(s);`, 0],
    ['R1a require', 'tests/y.ts', `const t = require('node:test');\nconsole.log(t);`, 1],
  ];
  for (const [name, path, text, want] of cases) {
    const got = findingsFor(path, text, allow).length;
    assert.equal(got, want, `${name}: want ${want}, got ${got}`);
  }
  console.log(`ts-unitban self-test passed: ${cases.length} cases.`);
}

if (import.meta.main) {
  if (process.argv.includes('--self-test')) {
    selfTest();
  } else {
    await main();
  }
}
