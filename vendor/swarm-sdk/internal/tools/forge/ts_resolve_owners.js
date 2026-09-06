#!/usr/bin/env node
/**
 * TypeScript Type Owner Resolver for Forge
 *
 * This script is the TypeScript equivalent of Go's type_bootstrap.go.
 * It uses the TypeScript compiler API to type-check a project and outputs
 * a JSON map of every property/method reference with its resolved owner type.
 *
 * Usage:
 *   node ts_resolve_owners.js <project_root> [tsconfig_path]
 *
 * Output (JSON to stdout):
 *   {
 *     "refs": {
 *       "src/lib/ipc/client.ts:42:10": { "name": "send", "owner": "IPCClient", "kind": "method" },
 *       ...
 *     },
 *     "stats": { "files": 28, "refs": 1234, "duration_ms": 450 }
 *   }
 *
 * The Go side reads this JSON and stamps ref.OwnerType on matching GoReference entries.
 */

const ts = require("typescript");
const path = require("path");

const projectRoot = process.argv[2];
if (!projectRoot) {
  process.stderr.write("Usage: ts_resolve_owners.js <project_root> [tsconfig_path]\n");
  process.exit(1);
}

const tsconfigPath = process.argv[3] || findTsConfig(projectRoot);
if (!tsconfigPath) {
  process.stderr.write("No tsconfig.json found under " + projectRoot + "\n");
  process.exit(1);
}

const startTime = Date.now();

// Parse tsconfig.json
const configFile = ts.readConfigFile(tsconfigPath, ts.sys.readFile);
if (configFile.error) {
  process.stderr.write("Error reading tsconfig: " + ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n") + "\n");
  process.exit(1);
}

const configDir = path.dirname(tsconfigPath);
const parsedConfig = ts.parseJsonConfigFileContent(configFile.config, ts.sys, configDir);

// Create program and type checker
const program = ts.createProgram(parsedConfig.fileNames, parsedConfig.options);
const checker = program.getTypeChecker();

const refs = {};
let refCount = 0;
let fileCount = 0;

for (const sourceFile of program.getSourceFiles()) {
  if (sourceFile.isDeclarationFile) continue;
  const filePath = sourceFile.fileName;
  if (filePath.includes("node_modules")) continue;

  const relPath = path.relative(projectRoot, filePath);
  if (relPath.startsWith("..")) continue;

  fileCount++;
  visitNode(sourceFile, sourceFile, relPath);
}

const stats = {
  files: fileCount,
  refs: refCount,
  duration_ms: Date.now() - startTime,
};

process.stderr.write(
  "[Forge TS] Resolved " + refCount + " typed refs across " + fileCount + " files in " + stats.duration_ms + "ms\n"
);

process.stdout.write(JSON.stringify({ refs: refs, stats: stats }) + "\n");

// ─── Helpers ───────────────────────────────────────────────────────────

function visitNode(node, sourceFile, relPath) {
  if (ts.isPropertyAccessExpression(node)) {
    resolvePropertyAccess(node, sourceFile, relPath);
  }
  ts.forEachChild(node, function(child) { visitNode(child, sourceFile, relPath); });
}

function resolvePropertyAccess(node, sourceFile, relPath) {
  var name = node.name;
  if (!name || !ts.isIdentifier(name)) return;

  var identText = name.text;
  var exprType = checker.getTypeAtLocation(node.expression);
  if (!exprType) return;

  var ownerName = getTypeName(exprType);
  if (!ownerName) return;

  var kind = "property";
  if (node.parent && ts.isCallExpression(node.parent) && node.parent.expression === node) {
    kind = "method";
  }

  var pos = sourceFile.getLineAndCharacterOfPosition(name.getStart(sourceFile));
  var lineNum = pos.line + 1;
  var colNum = pos.character + 1;

  var key = relPath + ":" + lineNum + ":" + colNum;
  refs[key] = { name: identText, owner: ownerName, kind: kind };
  refCount++;
}

function getTypeName(type) {
  // Unwrap Promise<T>
  if (type.symbol && type.symbol.name === "Promise" && type.typeArguments && type.typeArguments.length > 0) {
    return getTypeName(type.typeArguments[0]);
  }

  if (type.symbol) {
    var name = type.symbol.getName();
    if (name && name !== "__type" && name !== "__object" && name !== "Array" && !name.startsWith("__")) {
      var cleaned = cleanOwnerName(name);
      if (cleaned) return cleaned;
    }
  }

  // Union types
  if (type.isUnion && type.isUnion()) {
    for (var i = 0; i < type.types.length; i++) {
      var n = getTypeName(type.types[i]);
      if (n) return n;
    }
  }

  // Intersection types
  if (type.isIntersection && type.isIntersection()) {
    for (var j = 0; j < type.types.length; j++) {
      var m = getTypeName(type.types[j]);
      if (m) return m;
    }
  }

  // Apparent type (unwrap type aliases)
  var apparent = checker.getApparentType(type);
  if (apparent !== type && apparent.symbol) {
    var aName = apparent.symbol.getName();
    if (aName && aName !== "__type" && aName !== "__object" && !aName.startsWith("__")) {
      var aCleaned = cleanOwnerName(aName);
      if (aCleaned) return aCleaned;
    }
  }

  return "";
}

/**
 * Clean up owner names:
 * - Strip quote marks from module names: '"fs"' -> 'fs'
 * - Extract package name from full paths
 */
function cleanOwnerName(name) {
  if (name.charAt(0) === '"' && name.charAt(name.length - 1) === '"') {
    name = name.substring(1, name.length - 1);
  }

  if (name.indexOf("/") >= 0) {
    var nmIdx = name.lastIndexOf("node_modules/");
    if (nmIdx >= 0) {
      var afterNm = name.substring(nmIdx + "node_modules/".length);
      var parts = afterNm.split("/");
      if (parts[0] && parts[0].charAt(0) === "@" && parts.length > 1) {
        return parts[0] + "/" + parts[1];
      }
      return parts[0] || "";
    }
    var segments = name.split("/");
    return segments[segments.length - 1] || "";
  }

  return name;
}

function findTsConfig(root) {
  var dir = path.resolve(root);
  for (var i = 0; i < 10; i++) {
    var candidate = path.join(dir, "tsconfig.json");
    if (ts.sys.fileExists(candidate)) {
      return candidate;
    }
    var parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return null;
}
