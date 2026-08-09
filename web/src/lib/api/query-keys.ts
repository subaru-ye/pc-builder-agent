export const queryKeys = {
  sessions: ["sessions"] as const,
  session: (id: string) => ["session", id] as const,
  run: (id: string) => ["run", id] as const,
  builds: (id: string) => ["builds", id] as const,
  build: (id: string, version: number) => ["build", id, version] as const,
  diff: (id: string, from: number, to: number) => ["diff", id, from, to] as const,
  shares: (id: string, version: number) => ["shares", id, version] as const,
  readiness: ["readiness"] as const,
};
