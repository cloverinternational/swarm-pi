declare module "node:crypto" {
  interface Hash { update(data: string, encoding?: string): Hash; digest(encoding: "hex"): string; }
  export function createHash(algorithm: string): Hash;
}
