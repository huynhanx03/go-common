import { execFileSync } from "node:child_process";
import { writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import semver from "semver";
import { releasePlugin } from "./release.config.mjs";

export async function createReleasePreview({ commits, lastRelease, headSha, cwd, repositoryUrl }) {
  if (commits.length === 0) return null;

  const releaseType = await releasePlugin.analyzeCommits({}, { commits, logger: { log() {} } });
  if (!releaseType) return null;

  const version = lastRelease.version
    ? semver.inc(lastRelease.version, releaseType)
    : "1.0.0";
  if (!version) throw new Error(`Cannot calculate a ${releaseType} release from ${lastRelease.version}`);

  const nextRelease = { version, gitTag: `v${version}`, gitHead: headSha };
  await releasePlugin.verifyRelease({}, { cwd, nextRelease });

  const notes = await releasePlugin.generateNotes({}, {
    commits,
    lastRelease,
    nextRelease,
    options: { repositoryUrl },
    cwd,
  });

  return { releaseType, version, gitTag: nextRelease.gitTag, notes };
}

export async function createReleasePreviewFromGit({ cwd = process.cwd(), repositoryUrl }) {
  const headSha = git(["rev-parse", "HEAD"], cwd);
  const lastRelease = getLastRelease(headSha, cwd);
  const range = lastRelease.gitTag ? `${lastRelease.gitTag}..${headSha}` : headSha;
  const commits = readCommits(range, cwd);
  return createReleasePreview({ commits, lastRelease, headSha, cwd, repositoryUrl });
}

function git(args, cwd) {
  return execFileSync("git", args, { cwd, encoding: "utf8" }).trim();
}

function readCommits(range, cwd) {
  const output = git(["log", "--reverse", "--format=%H%x1f%B%x1e", range], cwd);
  return output.split("\x1e").flatMap((record) => {
    const separator = record.indexOf("\x1f");
    if (separator < 0) return [];
    const hash = record.slice(0, separator).trim();
    const message = record.slice(separator + 1).trim();
    return hash && message ? [{ hash, message }] : [];
  });
}

function getLastRelease(headSha, cwd) {
  const tags = git(["tag", "--merged", headSha, "--list", "v*"], cwd)
    .split("\n")
    .map((tag) => ({ tag, version: semver.valid(tag) }))
    .filter(({ version }) => version && semver.prerelease(version) === null)
    .sort((left, right) => semver.rcompare(left.version, right.version));
  const lastTag = tags[0]?.tag;
  if (!lastTag) return { version: null, gitTag: null, gitHead: null };

  return {
    version: semver.valid(lastTag),
    gitTag: lastTag,
    gitHead: git(["rev-list", "-n", "1", lastTag], cwd),
  };
}

async function main() {
  const cwd = process.cwd();
  const repositoryUrl = `${process.env.GITHUB_SERVER_URL ?? "https://github.com"}/${process.env.GITHUB_REPOSITORY}`;
  const preview = await createReleasePreviewFromGit({ cwd, repositoryUrl });
  const output = process.env.RELEASE_PREVIEW_OUTPUT;
  if (!output) throw new Error("RELEASE_PREVIEW_OUTPUT is required");
  await writeFile(output, JSON.stringify(preview), "utf8");
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  await main();
}
