import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { analyzeCommits } from "@semantic-release/commit-analyzer";
import { generateNotes } from "@semantic-release/release-notes-generator";

const modulePath = "github.com/huynhanx03/go-common";
const preset = "conventionalcommits";
const notesOptions = {
  preset,
  presetConfig: {
    types: [
      { type: "feat", section: "Features" },
      { type: "feature", section: "Features" },
      { type: "fix", section: "Bug Fixes" },
      { type: "perf", section: "Performance" },
      { type: "revert", section: "Reverts" },
      { type: "docs", section: "Documentation", hidden: false },
      { type: "refactor", section: "Refactoring", hidden: false },
      { type: "test", section: "Tests", hidden: false },
      { type: "ci", section: "CI", hidden: false },
      { type: "build", section: "Build", hidden: false },
      { type: "chore", section: "Maintenance", hidden: false },
    ],
  },
};

export default {
  branches: ["release"],
  tagFormat: "v${version}",
  plugins: [
    {
      async analyzeCommits(_options, context) {
        const type = await analyzeCommits({
          preset,
          releaseRules: [
            { breaking: true, release: "major" },
            { type: "feature", release: "minor" },
          ],
        }, context);
        // Every new change on release gets a version, including docs/CI/raw commits.
        return type ?? (context.commits.length > 0 ? "patch" : null);
      },
      async verifyRelease(_options, { cwd, nextRelease }) {
        const source = await readFile(join(cwd, "go.mod"), "utf8");
        const actual = source.match(/^module\s+"?([^"\s]+)"?/m)?.[1];
        const expected = moduleFor(nextRelease.version);
        if (actual !== expected) {
          throw new Error(`Refusing ${nextRelease.gitTag}: go.mod must declare module ${expected}; found ${actual ?? "no module directive"}. Prepare the Go import migration before releasing.`);
        }
      },
      async generateNotes(_options, context) {
        const notes = await generateNotes(notesOptions, context);
        const { version, gitTag } = context.nextRelease;
        return `${notes}\n### Upgrade\n\n\`\`\`sh\ngo get ${moduleFor(version)}@${gitTag}\ngo mod tidy\ngo test ./...\n\`\`\`\n`;
      },
    },
    [fileURLToPath(import.meta.resolve("@semantic-release/github")), {
      successCommentCondition: false,
      failCommentCondition: false,
      releasedLabels: false,
    }],
  ],
};

function moduleFor(version) {
  const major = Number(version.split(".")[0]);
  return major >= 2 ? `${modulePath}/v${major}` : modulePath;
}
