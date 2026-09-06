import { describe, expect, it } from "vitest";
import { extractDescriptionFromMarkdown, fatalValidationError, parseSkillMDContent, sanitizeFrontmatterYAML, validateDescription, validateName } from "../src/skillmd.js";

describe("SKILL.md parsing mirrors swarm-sdk parser.go", () => {
  it("decodes typed fields like yaml.v3 into SkillMetadata", () => {
    const { metadata, instructions } = parseSkillMDContent("---\nname: demo\ndescription: >\n  folded\n  text\nversion: 1.0\npriority: 7\ndisable-model-invocation: yes\nwhen_to_use: when demoing\ntags:\n  - a\n  - 'b'\nunknown: ignored\n---\n\n# Title\n\nBody line\n\n");
    expect(metadata.name).toBe("demo");
    expect(metadata.description).toBe("folded text\n");
    expect(metadata.version).toBe("1.0");
    expect(metadata.priority).toBe(7);
    expect(metadata.disableModelInvocation).toBe(true);
    expect(metadata.whenToUse).toBe("when demoing");
    expect(metadata.tags).toEqual(["a", "b"]);
    expect(instructions).toBe("# Title\n\nBody line");
  });
  it("drops line 1 when there is no frontmatter and strips one trailing CR per line", () => {
    expect(parseSkillMDContent("first\r\nsecond\r\nthird\r\n").instructions).toBe("second\nthird");
    expect(parseSkillMDContent("---\r\nname: x\r\ndescription: y\r\n---\r\nbody\r\n").metadata).toMatchObject({ name: "x", description: "y" });
  });
  it("falls back to the first markdown paragraph for an empty description (256-byte cap)", () => {
    const { metadata } = parseSkillMDContent("---\nname: x\n---\n# Heading\n\nfirst para line one\nline two\n\nsecond para\n");
    expect(metadata.description).toBe("first para line one line two");
    expect(extractDescriptionFromMarkdown("x".repeat(300))).toBe("x".repeat(253) + "…");
    expect(extractDescriptionFromMarkdown("## only heading\n")).toBe("");
  });
  it("recovers legacy frontmatter: sanitize quotes flat ': ' scalars, then lenient flat fields", () => {
    const [sanitized, changed] = sanitizeFrontmatterYAML(["name: x", "description: Foo: bar \"q\"", "hooks:", "  PreToolUse: []"]);
    expect(changed).toBe(true);
    expect(sanitized).toBe("name: x\ndescription: \"Foo: bar \\\"q\\\"\"\nhooks:\n  PreToolUse: []");
    expect(parseSkillMDContent("---\nname: x\ndescription: Foo: bar\n---\nbody").metadata.description).toBe("Foo: bar");
    // A type error (string into []string) has nothing to sanitize → lenient parser keeps flat fields only.
    const lenient = parseSkillMDContent("---\nname: x\ndescription: 'it''s'\ntags: a, \"b\"\nwhen_to_use: lost\n---\nbody").metadata;
    expect(lenient).toMatchObject({ name: "x", description: "it's", tags: ["a", "b"], whenToUse: "" });
    expect(() => parseSkillMDContent("---\n- just: a list\n---\nbody")).toThrow(/failed to parse frontmatter/);
  });
  it("applies Swarm's fatal validation: name required, description non-blank and ≤1500 bytes", () => {
    expect(validateName("")).toBe("name is required");
    expect(validateName("Bad")).toBe("name must be lowercase");
    expect(validateName("a--b")).toBe("name cannot contain consecutive hyphens");
    expect(validateName("-a")).toBe("name cannot start or end with a hyphen");
    expect(validateName("a".repeat(65))).toMatch(/exceeds maximum length of 64/);
    expect(validateDescription("   ")).toBe("description cannot be empty or whitespace only");
    expect(validateDescription("é".repeat(800))).toMatch(/got 1600/);
    expect(validateDescription("x".repeat(1400))).toBeUndefined();
    expect(fatalValidationError(parseSkillMDContent("---\ndescription: d\n---\nbody").metadata)).toBe("validation error: [error] name: name is required");
    expect(fatalValidationError(parseSkillMDContent("---\nname: ok\n---\n").metadata)).toBe("validation error: [error] description: description is required");
  });
});
