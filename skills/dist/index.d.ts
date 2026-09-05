export type SkillSource = "managed" | "install" | "project" | "user" | "autogen" | "cli";
export interface LoadedSkill {
    name: string;
    description: string;
    instructions: string;
    dir: string;
    filePath: string;
    source: SkillSource;
    precedence: number;
    supportFiles: string[];
    disableModelInvocation?: boolean;
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
/** Upstream-compatible progressive-disclosure index; bodies stay on disk. */
export declare function generateAvailableSkillsXML(skills: LoadedSkill[]): string;
declare const parseFrontmatter: (raw: string) => {
    fields: Record<string, string>;
    body: string;
};
declare const validName: (name: string) => boolean;
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
