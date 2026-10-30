import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const result = spawnSync(process.execPath, [require.resolve("next/dist/bin/next"), "build", ...process.argv.slice(2)], {
  stdio: "inherit",
  env: { ...process.env, NEXT_EXPORT: "1" },
});

if (result.error) throw result.error;
process.exit(result.status ?? 1);
