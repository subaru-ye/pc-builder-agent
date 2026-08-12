export function parseComposePS(output) {
  const trimmed = output.trim();
  if (!trimmed) return [];
  if (trimmed.startsWith("[")) return JSON.parse(trimmed);
  return trimmed.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
}

export function assertComposeServices(rows, expected = ["postgres", "redis"]) {
  for (const service of expected) {
    const row = rows.find((candidate) => candidate.Service === service);
    if (!row) throw new Error(`Docker Compose service ${service} 未启动`);
    if (String(row.State).toLowerCase() !== "running") {
      throw new Error(`Docker Compose service ${service} state=${row.State ?? "unknown"}`);
    }
    if (row.Health && String(row.Health).toLowerCase() !== "healthy") {
      throw new Error(`Docker Compose service ${service} health=${row.Health}`);
    }
  }
}
