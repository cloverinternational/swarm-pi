export type SkillSource = "managed" | "install" | "project" | "user" | "autogen" | "cli" | "builtin";
/**
 * `filePath` is the on-disk SKILL.md. `location` is what the model sees in
 * `<location>`: Swarm renders embedded builtins as `builtin:<name>/SKILL.md`
 * and everything else as the absolute path.
 */
export interface LoadedSkill {
    name: string;
    description: string;
    instructions: string;
    dir: string;
    filePath: string;
    location: string;
    source: SkillSource;
    precedence: number;
    supportFiles: string[];
    disableModelInvocation?: boolean;
    whenToUse?: string;
    category?: string;
    tags?: string[];
    priority?: number;
}
export interface SkillDiagnostic {
    path: string;
    message: string;
}
export interface SkillLoaderOptions {
    cwd?: string;
    home?: string;
    installDir?: string;
    managedDir?: string;
    autogenDir?: string;
    builtinDir?: string | null;
    cliPaths?: string[];
    closed?: boolean;
    allowedNames?: string[];
    allowedSkills?: string[];
}
export interface SkillLoadResult {
    skills: LoadedSkill[];
    diagnostics: SkillDiagnostic[];
    searchPaths: Array<{
        path: string;
        source: SkillSource;
        precedence: number;
    }>;
}
/**
 * Byte-identical mirrors of the skills Swarm embeds in its binary
 * (`swarm-sdk/internal/skills/builtins` plus the programmatic `loop` skill).
 * Registered first at the lowest precedence, exactly like Swarm's
 * `RegisterDefaultSkills(overwrite=false)`: any filesystem skill with the same
 * name replaces the builtin.
 */
export declare const DEFAULT_BUILTIN_SKILLS_DIR: string;
export declare const builtinLocation: (name: string) => string;
/** Upstream-compatible progressive-disclosure index; bodies stay on disk. */
export declare function generateAvailableSkillsXML(skills: LoadedSkill[]): string;
export declare const MAX_AVAILABLE_SKILLS = 60;
export declare const MAX_AVAILABLE_SKILLS_CHARS = 12000;
export declare const MAX_PROMPT_DESCRIPTION_RUNES = 240;
export declare function rankSkillsForContext(skills: LoadedSkill[], query: string): LoadedSkill[];
export declare function generateRankedAvailableSkillsXML(skills: LoadedSkill[], maxSkills?: number, maxChars?: number): string;
declare const parseFrontmatter: (raw: string) => {
    fields: Record<string, string>;
    body: string;
};
declare const validName: (name: string) => boolean;
export declare function resolveSkillFile(skill: LoadedSkill, filePath: string): string;
export declare class SkillLoader {
    private readonly options;
    private watchers;
    constructor(options?: SkillLoaderOptions);
    configure(policy: Pick<SkillLoaderOptions, "closed" | "allowedNames">): void;
    paths(): Array<{
        path: string;
        source: SkillSource;
        precedence: number;
    }>;
    load(): SkillLoadResult;
    find(name: string): LoadedSkill | undefined;
    watch(onChange: (result: SkillLoadResult) => void): () => void;
    closeWatchers(): void;
}
export { parseFrontmatter, validName };
