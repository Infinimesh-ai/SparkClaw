import path from "node:path";
import ts from "typescript";

const root = process.cwd();
const configPath = ts.findConfigFile(root, ts.sys.fileExists, "tsconfig.json");
if (!configPath) throw new Error("apps/webchat/tsconfig.json was not found");

const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
if (configFile.error) {
  throw new Error(ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n"));
}
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, path.dirname(configPath));
const program = ts.createProgram(parsed.fileNames, parsed.options);
const checker = program.getTypeChecker();
const englishPath = path.join(root, "src", "i18n", "en.ts");
const englishSource = program.getSourceFile(englishPath);
if (!englishSource) throw new Error("src/i18n/en.ts is not part of the TypeScript program");

const englishDeclaration = englishSource.statements.find((statement) =>
  ts.isVariableStatement(statement) && statement.declarationList.declarations.some((declaration) =>
    ts.isIdentifier(declaration.name) && declaration.name.text === "en"
  )
);
const englishVariable = englishDeclaration?.declarationList.declarations.find((declaration) =>
  ts.isIdentifier(declaration.name) && declaration.name.text === "en"
);
if (!englishVariable?.initializer) throw new Error("src/i18n/en.ts must export const en");

function declarationFor(symbol) {
  return symbol.valueDeclaration ?? symbol.declarations?.[0];
}

function translationKey(symbol) {
  const declaration = declarationFor(symbol);
  if (!declaration || declaration.getSourceFile() !== englishSource) return "";
  return `${declaration.pos}:${declaration.end}`;
}

function translationProperties(type) {
  return checker.getPropertiesOfType(type).filter((symbol) => Boolean(translationKey(symbol)));
}

const leaves = new Map();
const descendants = new Map();

function collect(type, prefix = []) {
  const collected = new Set();
  for (const symbol of translationProperties(type)) {
    const declaration = declarationFor(symbol);
    const key = translationKey(symbol);
    const propertyType = checker.getTypeOfSymbolAtLocation(symbol, declaration);
    const childProperties = translationProperties(propertyType);
    const dottedPath = [...prefix, symbol.name];
    if (childProperties.length === 0) {
      leaves.set(key, dottedPath.join("."));
      collected.add(key);
      continue;
    }
    const childLeaves = collect(propertyType, dottedPath);
    descendants.set(key, childLeaves);
    for (const child of childLeaves) collected.add(child);
  }
  return collected;
}

collect(checker.getTypeAtLocation(englishVariable.initializer));
const used = new Set();

function markLeaf(symbol) {
  const key = symbol && translationKey(symbol);
  if (key && leaves.has(key)) used.add(key);
}

function markBranch(symbol) {
  const key = symbol && translationKey(symbol);
  for (const leaf of descendants.get(key) ?? []) used.add(leaf);
}

function isReceiver(node) {
  const parent = node.parent;
  return (ts.isPropertyAccessExpression(parent) && parent.expression === node) ||
    (ts.isElementAccessExpression(parent) && parent.expression === node);
}

function inspect(node) {
  if (ts.isPropertyAccessExpression(node)) {
    const symbol = checker.getSymbolAtLocation(node.name);
    markLeaf(symbol);
    if (!isReceiver(node)) markBranch(symbol);
  } else if (ts.isElementAccessExpression(node)) {
    const expressionType = checker.getTypeAtLocation(node.expression);
    if (ts.isStringLiteralLike(node.argumentExpression)) {
      const symbol = checker.getPropertyOfType(expressionType, node.argumentExpression.text);
      markLeaf(symbol);
      markBranch(symbol);
    } else {
      for (const symbol of translationProperties(expressionType)) {
        markLeaf(symbol);
        markBranch(symbol);
      }
    }
  }
  ts.forEachChild(node, inspect);
}

for (const sourceFile of program.getSourceFiles()) {
  const relative = path.relative(root, sourceFile.fileName);
  if (relative.startsWith("..") || !relative.startsWith(`src${path.sep}`)) continue;
  if (relative.startsWith(`src${path.sep}i18n${path.sep}`)) continue;
  if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(relative)) continue;
  inspect(sourceFile);
}

const unused = [...leaves]
  .filter(([key]) => !used.has(key))
  .map(([, dottedPath]) => dottedPath)
  .sort();

if (unused.length > 0) {
  console.error("Unused English translation keys:");
  for (const dottedPath of unused) console.error(`  ${dottedPath}`);
  process.exitCode = 1;
} else {
  console.log(`Checked ${leaves.size} translation keys; all are used by production code.`);
}
