import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {resolve, relative} from 'node:path';
import ts from 'typescript';
import {LitAnalyzer, DefaultLitAnalyzerContext, makeConfig} from 'lit-analyzer';
import type {LitDiagnostic} from 'lit-analyzer';

const root = process.cwd(),
  require = createRequire(resolve(root, 'package.json'));
const tool = createRequire(resolve(root, 'tools/lit-check/package.json'));
assert.equal(ts.version, tool('typescript/package.json').version);
assert.equal(process.env.SODA_ANALYSIS_COMPILER, tool.resolve('typescript'));
assert.equal(require('typescript/package.json').version, require('./package.json').devDependencies.typescript);
assert.notEqual(
  ts.version,
  require('typescript/package.json').version,
  'Product and analysis compilers must stay separate'
);
assert.equal(ts.ScriptTarget.ES2022, 9, 'Classic compiler API is required');
const configFile = ts.readConfigFile(resolve(root, 'tsconfig.browser.json'), ts.sys.readFile);
assert(!configFile.error, 'Browser config is unreadable');
const config = ts.parseJsonConfigFileContent(configFile.config, ts.sys, root);
assert.equal(
  config.errors.length,
  0,
  ts.formatDiagnosticsWithColorAndContext(config.errors, {
    getCurrentDirectory: () => root,
    getCanonicalFileName: (p) => p,
    getNewLine: () => '\n',
  })
);
assert(config.fileNames.length > 0, 'Empty browser source inventory');
const authored: unknown = configFile.config.include;
assert(Array.isArray(authored) && authored.length > 0, 'Missing authored browser roots');
for (const pattern of authored) {
  assert(
    typeof pattern === 'string' && /\/(?:\*\*\/)?\*\.ts$/.test(pattern),
    'Review changed browser discovery pattern'
  );
  const directory = resolve(root, pattern.replace(/\/(?:\*\*\/)?\*\.ts$/, ''));
  for (const file of ts.sys.readDirectory(directory, ['.ts'], undefined, ['**/*.ts'])) {
    assert(config.fileNames.includes(file), `Authored browser source skipped by compiler/analyzer: ${file}`);
  }
}
assert(
  Bun.argv.slice(2).every((arg) => arg === '--fixtures'),
  'Unknown checker argument'
);

function analyze(files: string[]): LitDiagnostic[] {
  assert(files.length > 0, 'Empty template input');
  const program = ts.createProgram(files, config.options);
  const context = new DefaultLitAnalyzerContext({getProgram: () => program});
  assert.equal(
    context.ts.version,
    ts.version,
    'Analyzer bare compiler import must resolve to its own classic compiler'
  );
  context.updateConfig(
    makeConfig({
      strict: true,
      cwd: root,
      rules: {'no-unknown-event': 'error'},
      // The analyzer's built-in HTML data omits the native details toggle event.
      customHtmlData: {version: 1.1, tags: [{name: 'details', attributes: [{name: 'ontoggle'}]}]},
    })
  );
  const analyzer = new LitAnalyzer(context),
    diagnostics: LitDiagnostic[] = [];
  for (const file of files) {
    const source = program.getSourceFile(file);
    assert(source, `Skipped source: ${file}`);
    diagnostics.push(...analyzer.getDiagnosticsInFile(source));
  }
  return diagnostics;
}

if (Bun.argv.includes('--fixtures')) {
  const fixtures = resolve(root, 'tools/lit-check/fixtures');
  assert.throws(() => analyze([]), /Empty template input/);
  assert.throws(() => analyze([resolve(fixtures, 'missing-input.ts')]), /Skipped source/);
  const positive = analyze([resolve(fixtures, 'positive.ts')]);
  assert.deepEqual(positive, [], 'Normal typed/static-properties authoring must pass');
  const negatives: Record<string, string> = {
    property: 'no-incompatible-type-binding',
    unknownProperty: 'no-unknown-property',
    boolean: 'no-incompatible-type-binding',
    event: 'no-noncallable-event-binding',
    unknownEvent: 'no-unknown-event',
    customProperty: 'no-incompatible-type-binding',
    directive: 'no-invalid-directive-binding',
    unclosed: 'no-unclosed-tag',
    nullable: 'no-nullable-attribute-binding',
    aria: 'no-incompatible-type-binding',
  };
  for (const [name, rule] of Object.entries(negatives)) {
    const diagnostics = analyze([resolve(fixtures, name + '.ts')]);
    assert(
      diagnostics.some((d) => d.source === rule),
      `${name} failed to detect ${rule}: ${diagnostics.map((d) => d.source)}`
    );
    console.log(`Rejected ${name}: ${rule}`);
  }
  console.log(
    'Positive fixture and ten independent negative contracts passed. Callable event parameter kinds remain unchecked.'
  );
} else {
  // Use the actual compiler inventory, not three hardcoded historical component names.
  let templates = 0;
  for (const file of config.fileNames) {
    const text = ts.sys.readFile(file);
    assert(text !== undefined, `Missing browser source ${file}`);
    const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true);
    const visit = (node: ts.Node): void => {
      if (ts.isTaggedTemplateExpression(node) && ts.isIdentifier(node.tag) && node.tag.text === 'html') templates++;
      if (ts.isImportSpecifier(node) && node.propertyName?.text === 'html')
        assert.equal(node.name.text, 'html', 'Renamed template tags need explicit analyzer coverage');
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  assert(templates > 0, 'No authored Lit templates were found');
  const diagnostics = analyze(config.fileNames);
  for (const d of diagnostics)
    console.error(`${relative(root, d.file.fileName)}:${d.location.start} [${d.severity}/${d.source}] ${d.message}`);
  console.log(
    `Analyzed ${config.fileNames.length} browser sources with classic TS ${ts.version}; product compiler unchanged.`
  );
  // Warnings fail too; a rule must not silently weaken the required gate.
  if (diagnostics.length) process.exit(1);
}
