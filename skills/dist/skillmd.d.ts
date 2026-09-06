export declare const MAX_NAME_LENGTH = 64;
export declare const MAX_DESCRIPTION_LENGTH = 1500;
export interface SkillMetadata {
    name: string;
    description: string;
    version: string;
    author: string;
    category: string;
    tags: string[];
    dependencies: string[];
    enabled: boolean;
    priority: number;
    icon: string;
    homepage: string;
    license: string;
    compatibility: string;
    allowedTools: string;
    whenToUse: string;
    arguments: string[];
    argumentHint: string;
    context: string;
    model: string;
    disableModelInvocation: boolean;
    userInvocable: boolean;
    effort: string;
}
export declare const emptyMetadata: () => SkillMetadata;
/** yaml.Unmarshal(frontmatter, &SkillMetadata) — throws DecodeError like yaml.v3. */
export declare function decodeMetadataYAML(text: string): SkillMetadata;
/** parser.go sanitizeFrontmatterYAML. */
export declare function sanitizeFrontmatterYAML(lines: string[]): [string, boolean];
/** parser.go unquoteFrontmatterScalar. */
export declare function unquoteFrontmatterScalar(s: string): string;
/** parser.go parseFrontmatterLenient. */
export declare function parseFrontmatterLenient(lines: string[]): SkillMetadata | undefined;
/** parser.go extractDescriptionFromMarkdown (byte-based 256 cap, like Go). */
export declare function extractDescriptionFromMarkdown(content: string): string;
export interface ParsedSkillMD {
    metadata: SkillMetadata;
    instructions: string;
}
/** parser.go ParseSkillMDContent (hooks are not modelled; they never reach the wire). */
export declare function parseSkillMDContent(data: string): ParsedSkillMD;
/** validator.go ValidateName. */
export declare function validateName(name: string): string | undefined;
/** validator.go ValidateDescription (byte length, like Go len()). */
export declare function validateDescription(desc: string): string | undefined;
/** LoadSkillWithValidation's fatal gate: the first fatal validation error, if any. */
export declare function fatalValidationError(metadata: SkillMetadata): string | undefined;
