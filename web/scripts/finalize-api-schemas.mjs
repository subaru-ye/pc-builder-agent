import { readFileSync, writeFileSync } from "node:fs";

// openapi-zod-client emits Zod 3 records and unannotated recursive schemas.
// Keep generated validation derived from OpenAPI while adapting it to Zod 4.
const path = new URL("../src/lib/api/generated-schemas.ts", import.meta.url);
let source = readFileSync(path, "utf8");
source = source.replace(/z\.record\((?!z\.string\(\), )/g, "z.record(z.string(), ");
if (source.includes("const RequirementField = z.lazy(")) {
  source = 'import type { components } from "./generated";\n' + source;
  source = source.replace("const RequirementField = z.lazy(", 'const RequirementField: z.ZodType<components["schemas"]["RequirementField"]> = z.lazy(');
}
writeFileSync(path, source, "utf8");
