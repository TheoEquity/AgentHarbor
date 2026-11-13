import { execFileSync, spawnSync } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import { relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const [scope, base, head] = process.argv.slice(2);
const workspaces = ["packages/core", "packages/ui", "packages/views", "packages/plugin-sdk", "apps/web", "apps/desktop"];
const maxLines = 500;
const maxComplexity = 20;

if (!["frontend", "backend"].includes(scope) || !base || !head) {
  throw new Error("Usage: node scripts/check-changed-code.mjs <frontend|backend> <base> <head>");
}

function git(...args) {
  return execFileSync("git", args, { cwd: root, encoding: "utf8" });
}

// A manually triggered run checks the selected commit. A branch's first push
// has no previous tree, so check everything introduced by that push.
const range = /^0+$/.test(base)
  ? [git("hash-object", "-t", "tree", "-w", "--stdin").trim(), head]
  : [base === "parent" ? git("rev-parse", `${head}^`).trim() : base, head];
const changed = git("diff", "--name-only", "--diff-filter=ACMRT", "-z", ...range, "--")
  .split("\0").filter((file) => file && lstatSync(resolve(root, file)).isFile());

function warning(file, message) {
  const escape = (value, pattern) => value.replace(pattern, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);
  console.log(`::warning file=${escape(file, /[%\r\n,:]/g)}::${escape(message, /[%\r\n]/g)}`);
}

function businessSource(file, source) {
  return !/(?:^|\/)(?:__tests__|__fixtures__|fixtures|testdata|tests?|generated|locales|i18n|translations|\.next|\.source)(?:\/|\.|$)|(?:\.test|\.spec|\.stories|\.d|\.generated)\.[cm]?[jt]sx?$|_test\.go$/.test(file)
    && !/Code generated[^\r\n]*DO NOT EDIT|@generated|This file (?:was|is) (?:auto[- ]?)?generated/i.test(source.slice(0, 1024));
}

function warnAboutSize(file, source) {
  const lines = source.replace(/\r?\n$/, "").split(/\r?\n/).length;
  if (lines > maxLines) warning(file, `${lines} lines (recommended maximum: ${maxLines})`);
}

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, encoding: "utf8", maxBuffer: 16 * 1024 * 1024 });
  if (result.error) throw result.error;
  if (result.stderr) process.stderr.write(result.stderr);
  if (result.status !== 0) {
    if (result.stdout) process.stderr.write(result.stdout);
    throw new Error(`${command} failed with status ${result.status ?? result.signal}`);
  }
  return result.stdout;
}

if (scope === "backend") {
  const files = changed.filter((file) => file.startsWith("server/") && file.endsWith(".go"));
  for (const file of files) {
    const source = readFileSync(resolve(root, file), "utf8");
    if (businessSource(file, source)) warnAboutSize(file, source);
  }
  if (files.length) {
    const unformatted = run("gofmt", ["-l", ...files.map((file) => `./${file}`)], root);
    if (unformatted) {
      console.error(`Run gofmt on these changed Go files:\n${unformatted.trimEnd()}`);
      process.exitCode = 1;
    }
  }
} else {
  for (const workspace of workspaces) {
    const files = changed.filter((file) => file.startsWith(`${workspace}/`) && /\.[jt]sx?$/.test(file))
      .filter((file) => {
        const source = readFileSync(resolve(root, file), "utf8");
        if (!businessSource(file, source)) return false;
        warnAboutSize(file, source);
        return true;
      });
    if (!files.length) continue;

    const cwd = resolve(root, workspace);
    const output = run(process.execPath, [
      resolve(root, "node_modules/eslint/bin/eslint.js"), "--no-warn-ignored", "--rule", `complexity: [warn, ${maxComplexity}]`,
      "--format", "json", "--", ...files.map((file) => file.slice(workspace.length + 1)),
    ], cwd);
    for (const result of JSON.parse(output)) {
      for (const message of result.messages) {
        if (message.ruleId !== "complexity") continue;
        const file = relative(root, result.filePath).split(sep).join("/");
        warning(file, `Line ${message.line}: ${message.message}`);
      }
    }
  }
}
