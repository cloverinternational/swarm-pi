/**
 * Port of swarm-sdk internal/skills/parser.go ParseSkillMDContent +
 * LoadSkillWithValidation + validator.go, so Pi loads (and rejects) exactly
 * the SKILL.md files Swarm does and reads the same field values.
 *
 * Fidelity notes (all deliberate, all mirrored):
 *  - bufio.ScanLines: split on "\n", drop one trailing "\r" per line.
 *  - Frontmatter opens only when line 1 is exactly "---"; it closes at the
 *    next "---" line. Without frontmatter, line 1 is DROPPED from content
 *    (`frontmatterDone || lineNum > 1`).
 *  - Frontmatter is decoded by gopkg.in/yaml.v3 into a typed struct: string
 *    fields take the scalar text (so `version: 1.0` stays "1.0"), bool fields
 *    accept the 1.1 y/yes/on/n/no/off spellings, type mismatches are decode
 *    errors. On error: sanitizeFrontmatterYAML (quote flat scalars with
 *    ": " etc.), then parseFrontmatterLenient (flat string fields only).
 *  - Instructions = TrimSpace(content). Empty description falls back to the
 *    first markdown paragraph (≤256 bytes, "…" suffix).
 *  - Fatal validation: name required/≤64 bytes/regex, description
 *    required/non-blank/≤1500 BYTES. A SKILL.md without `name:` is skipped
 *    (generateSkillName is unreachable in Swarm).
 */
import { Scalar, YAMLMap, YAMLSeq, isMap, isScalar, isSeq, parseDocument } from "yaml";

export const MAX_NAME_LENGTH = 64;
export const MAX_DESCRIPTION_LENGTH = 1500;
const NAME_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export interface SkillMetadata {
  name: string; description: string; version: string; author: string; category: string;
  tags: string[]; dependencies: string[]; enabled: boolean; priority: number;
  icon: string; homepage: string; license: string; compatibility: string; allowedTools: string;
  whenToUse: string; arguments: string[]; argumentHint: string; context: string; model: string;
  disableModelInvocation: boolean; userInvocable: boolean; effort: string;
}
export const emptyMetadata = (): SkillMetadata => ({
  name: "", description: "", version: "", author: "", category: "", tags: [], dependencies: [], enabled: false, priority: 0,
  icon: "", homepage: "", license: "", compatibility: "", allowedTools: "", whenToUse: "", arguments: [], argumentHint: "",
  context: "", model: "", disableModelInvocation: false, userInvocable: false, effort: "",
});

type FieldKind = "string" | "bool" | "int" | "strings";
const FIELDS: Record<string, [keyof SkillMetadata, FieldKind]> = {
  name: ["name", "string"], description: ["description", "string"], version: ["version", "string"], author: ["author", "string"],
  category: ["category", "string"], tags: ["tags", "strings"], dependencies: ["dependencies", "strings"], enabled: ["enabled", "bool"],
  priority: ["priority", "int"], icon: ["icon", "string"], homepage: ["homepage", "string"], license: ["license", "string"],
  compatibility: ["compatibility", "string"], "allowed-tools": ["allowedTools", "string"], when_to_use: ["whenToUse", "string"],
  arguments: ["arguments", "strings"], "argument-hint": ["argumentHint", "string"], context: ["context", "string"], model: ["model", "string"],
  "disable-model-invocation": ["disableModelInvocation", "bool"], "user-invocable": ["userInvocable", "bool"], effort: ["effort", "string"],
};

class DecodeError extends Error {}
const byteLength = (s: string) => Buffer.byteLength(s, "utf8");

// yaml.v3 resolve(): plain scalars only; quoted scalars are always !!str.
const isPlain = (s: Scalar) => s.type === undefined || s.type === Scalar.PLAIN;
const NULLS = new Set(["", "~", "null", "Null", "NULL"]);
const TRUES = new Set(["true", "True", "TRUE"]), FALSES = new Set(["false", "False", "FALSE"]);
const BOOL11_TRUE = new Set(["y", "Y", "yes", "Yes", "YES", "on", "On", "ON"]);
const BOOL11_FALSE = new Set(["n", "N", "no", "No", "NO", "off", "Off", "OFF"]);
const INT_RE = /^[-+]?(?:0|[1-9][0-9_]*)$/, HEX_RE = /^[-+]?0x[0-9a-fA-F_]+$/, OCT_RE = /^[-+]?0o?[0-7_]+$/, BIN_RE = /^[-+]?0b[01_]+$/;

function scalarText(node: unknown): string | null {
  if (!isScalar(node)) throw new DecodeError("cannot unmarshal non-scalar into string");
  const text = String(node.value ?? "");
  if (isPlain(node) && NULLS.has(text)) return null; // !!null → zero value, no error
  return text;
}
function decodeString(node: unknown): string { return scalarText(node) ?? ""; }
function decodeBool(node: unknown): boolean {
  if (!isScalar(node)) throw new DecodeError("cannot unmarshal non-scalar into bool");
  const text = String(node.value ?? "");
  if (isPlain(node)) {
    if (NULLS.has(text)) return false;
    if (TRUES.has(text)) return true;
    if (FALSES.has(text)) return false;
  }
  if (BOOL11_TRUE.has(text)) return true;
  if (BOOL11_FALSE.has(text)) return false;
  throw new DecodeError(`cannot unmarshal ${JSON.stringify(text)} into bool`);
}
function decodeInt(node: unknown): number {
  if (!isScalar(node)) throw new DecodeError("cannot unmarshal non-scalar into int");
  const text = String(node.value ?? "");
  if (!isPlain(node)) throw new DecodeError("cannot unmarshal !!str into int");
  if (NULLS.has(text)) return 0;
  const clean = text.replace(/_/g, "");
  if (INT_RE.test(text)) return Number.parseInt(clean, 10);
  if (HEX_RE.test(text)) return Number.parseInt(clean.replace(/0x/i, ""), 16) * (clean.startsWith("-") ? -1 : 1);
  if (BIN_RE.test(text)) return Number.parseInt(clean.replace(/0b/i, ""), 2) * (clean.startsWith("-") ? -1 : 1);
  if (OCT_RE.test(text)) return Number.parseInt(clean.replace(/^([-+]?)0o?/, "$1"), 8);
  throw new DecodeError(`cannot unmarshal ${JSON.stringify(text)} into int`);
}
function decodeStrings(node: unknown): string[] {
  if (isScalar(node) && isPlain(node) && NULLS.has(String(node.value ?? ""))) return [];
  if (!isSeq(node)) throw new DecodeError("cannot unmarshal non-sequence into []string");
  return (node as YAMLSeq).items.map(item => decodeString(item));
}

/** yaml.Unmarshal(frontmatter, &SkillMetadata) — throws DecodeError like yaml.v3. */
export function decodeMetadataYAML(text: string): SkillMetadata {
  const doc = parseDocument(text, { schema: "failsafe", uniqueKeys: true, strict: true });
  if (doc.errors.length) throw new DecodeError(doc.errors[0].message);
  const meta = emptyMetadata();
  const root = doc.contents;
  if (root === null || root === undefined) return meta;
  if (isScalar(root) && isPlain(root) && NULLS.has(String(root.value ?? ""))) return meta;
  if (!isMap(root)) throw new DecodeError("cannot unmarshal non-mapping into SkillMetadata");
  for (const pair of (root as YAMLMap).items) {
    const key = isScalar(pair.key) ? String(pair.key.value ?? "") : "";
    const spec = FIELDS[key];
    if (!spec) continue; // unknown keys are ignored (no KnownFields)
    const [field, kind] = spec;
    const value = pair.value;
    if (kind === "string") (meta as any)[field] = decodeString(value);
    else if (kind === "bool") (meta as any)[field] = decodeBool(value);
    else if (kind === "int") (meta as any)[field] = decodeInt(value);
    else (meta as any)[field] = decodeStrings(value);
  }
  return meta;
}

/** parser.go sanitizeFrontmatterYAML. */
export function sanitizeFrontmatterYAML(lines: string[]): [string, boolean] {
  let changed = false;
  const out = lines.slice();
  lines.forEach((raw, i) => {
    if (raw === "" || raw[0] === " " || raw[0] === "\t") return;
    const trimmed = raw.trim();
    if (trimmed === "" || trimmed.startsWith("#")) return;
    const colon = raw.indexOf(":");
    if (colon < 0) return;
    const key = raw.slice(0, colon), val = raw.slice(colon + 1).trim();
    if (val === "") return;
    if ("\"'[{|>&*!#".includes(val[0])) return;
    if (!(val.includes(": ") || val.endsWith(":") || val.includes(" #") || val.includes("\t"))) return;
    const escaped = val.replace(/\\/g, "\\\\").replace(/"/g, "\\\"");
    out[i] = `${key}: "${escaped}"`;
    changed = true;
  });
  return changed ? [out.join("\n"), true] : ["", false];
}

/** parser.go unquoteFrontmatterScalar. */
export function unquoteFrontmatterScalar(s: string): string {
  if (s.length >= 2 && s[0] === "\"" && s[s.length - 1] === "\"") {
    const inner = s.slice(1, -1); let b = "";
    for (let i = 0; i < inner.length; i++) {
      if (inner[i] === "\\" && i + 1 < inner.length) { i++; b += inner[i] === "n" ? "\n" : inner[i] === "r" ? "\r" : inner[i] === "t" ? "\t" : inner[i]; continue; }
      b += inner[i];
    }
    return b;
  }
  if (s.length >= 2 && s[0] === "'" && s[s.length - 1] === "'") return s.slice(1, -1).replace(/''/g, "'");
  return s;
}

/** parser.go parseFrontmatterLenient. */
export function parseFrontmatterLenient(lines: string[]): SkillMetadata | undefined {
  const md = emptyMetadata();
  let recognized = 0;
  for (const raw of lines) {
    const line = raw.trim();
    if (line === "" || line.startsWith("#")) continue;
    const colon = line.indexOf(":");
    if (colon < 0) continue;
    const key = line.slice(0, colon).trim(), value = line.slice(colon + 1).trim();
    switch (key) {
      case "name": md.name = unquoteFrontmatterScalar(value); break;
      case "description": md.description = unquoteFrontmatterScalar(value); break;
      case "version": md.version = unquoteFrontmatterScalar(value); break;
      case "author": md.author = unquoteFrontmatterScalar(value); break;
      case "category": md.category = unquoteFrontmatterScalar(value); break;
      case "license": md.license = unquoteFrontmatterScalar(value); break;
      case "compatibility": md.compatibility = unquoteFrontmatterScalar(value); break;
      case "tags": {
        // strings.Trim(value, "[]") strips any leading/trailing '[' or ']' runs.
        const trimmed = value.replace(/^[\[\]]+/, "").replace(/[\[\]]+$/, "");
        for (const t of trimmed.split(",")) { const tag = unquoteFrontmatterScalar(t.trim()); if (tag !== "") md.tags.push(tag); }
        break;
      }
      default: continue;
    }
    recognized++;
  }
  if (recognized === 0 || (md.name === "" && md.description === "")) return undefined;
  return md;
}

/** parser.go extractDescriptionFromMarkdown (byte-based 256 cap, like Go). */
export function extractDescriptionFromMarkdown(content: string): string {
  let skippedHeading = false;
  const paragraph: string[] = [];
  for (const line of content.split("\n")) {
    const trimmed = line.trim();
    if (!skippedHeading && trimmed.startsWith("# ")) { skippedHeading = true; continue; }
    if (trimmed.startsWith("#")) { if (paragraph.length) break; continue; }
    if (trimmed === "") { if (paragraph.length) break; continue; }
    paragraph.push(trimmed);
  }
  if (!paragraph.length) return "";
  let desc = paragraph.join(" ");
  if (byteLength(desc) > 256) desc = Buffer.from(desc, "utf8").subarray(0, 253).toString("utf8").replace(/\uFFFD+$/, "") + "\u2026";
  return desc;
}

export interface ParsedSkillMD { metadata: SkillMetadata; instructions: string }

/** parser.go ParseSkillMDContent (hooks are not modelled; they never reach the wire). */
export function parseSkillMDContent(data: string): ParsedSkillMD {
  // bufio.ScanLines: a final line without "\n" is still a line; a trailing
  // "\n" does not produce an empty extra line; one trailing "\r" is dropped.
  const rawLines = data.split("\n");
  if (rawLines.length && rawLines[rawLines.length - 1] === "") rawLines.pop();
  const lines = rawLines.map(l => (l.endsWith("\r") ? l.slice(0, -1) : l));
  const frontmatter: string[] = [], contentLines: string[] = [];
  let inFrontmatter = false, frontmatterDone = false;
  lines.forEach((line, index) => {
    const lineNum = index + 1;
    if (lineNum === 1 && line === "---") { inFrontmatter = true; return; }
    if (inFrontmatter && line === "---") { inFrontmatter = false; frontmatterDone = true; return; }
    if (inFrontmatter) frontmatter.push(line);
    else if (frontmatterDone || lineNum > 1) contentLines.push(line);
  });
  let metadata = emptyMetadata();
  if (frontmatter.length) {
    try { metadata = decodeMetadataYAML(frontmatter.join("\n")); }
    catch (error) {
      let recovered = false;
      const [sanitized, changed] = sanitizeFrontmatterYAML(frontmatter);
      if (changed) { try { metadata = decodeMetadataYAML(sanitized); recovered = true; } catch { /* fall through */ } }
      if (!recovered) {
        const lenient = parseFrontmatterLenient(frontmatter);
        if (!lenient) throw new Error(`failed to parse frontmatter: ${(error as Error).message}`);
        metadata = lenient;
      }
    }
  }
  const instructions = contentLines.join("\n").trim();
  if (metadata.description === "" && instructions !== "") metadata.description = extractDescriptionFromMarkdown(instructions);
  return { metadata, instructions };
}

/** validator.go ValidateName. */
export function validateName(name: string): string | undefined {
  if (name === "") return "name is required";
  if (byteLength(name) > MAX_NAME_LENGTH) return `name exceeds maximum length of ${MAX_NAME_LENGTH} characters (got ${byteLength(name)})`;
  if (!NAME_RE.test(name)) {
    if (name.startsWith("-") || name.endsWith("-")) return "name cannot start or end with a hyphen";
    if (name.includes("--")) return "name cannot contain consecutive hyphens";
    if (name.toLowerCase() !== name) return "name must be lowercase";
    return "name must contain only lowercase alphanumeric characters and hyphens";
  }
  return undefined;
}
/** validator.go ValidateDescription (byte length, like Go len()). */
export function validateDescription(desc: string): string | undefined {
  if (desc === "") return "description is required";
  if (desc.trim() === "") return "description cannot be empty or whitespace only";
  if (byteLength(desc) > MAX_DESCRIPTION_LENGTH) return `description exceeds maximum length of ${MAX_DESCRIPTION_LENGTH} characters (got ${byteLength(desc)})`;
  return undefined;
}

/** LoadSkillWithValidation's fatal gate: the first fatal validation error, if any. */
export function fatalValidationError(metadata: SkillMetadata): string | undefined {
  const name = validateName(metadata.name);
  if (name) return `validation error: [error] name: ${name}`;
  const description = validateDescription(metadata.description);
  if (description) return `validation error: [error] description: ${description}`;
  return undefined;
}
