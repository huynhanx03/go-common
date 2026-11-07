#!/usr/bin/env python3
"""Download the compact PR summary after GitHub makes its artifact visible."""

import argparse
import json
import shutil
import subprocess
import sys
import time
from pathlib import Path

ARTIFACT_NAME = "pr-check-summary"
SUMMARY_NAME = "pr-check-summary.json"
MAX_SUMMARY_BYTES = 100_000
RETRY_ATTEMPTS = 6
RETRY_DELAY_SECONDS = 3


def find_artifact_size(repository, run_id, runner=subprocess.run):
    result = runner(
        ["gh", "api", f"repos/{repository}/actions/runs/{run_id}/artifacts"],
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        return None
    try:
        artifacts = json.loads(result.stdout).get("artifacts", [])
    except (json.JSONDecodeError, AttributeError):
        return None
    if not isinstance(artifacts, list):
        return None
    for artifact in artifacts:
        if not isinstance(artifact, dict):
            continue
        if artifact.get("name") == ARTIFACT_NAME and not artifact.get("expired", False):
            size = artifact.get("size_in_bytes")
            return size if type(size) is int and size >= 0 else None
    return None


def valid_summary(path):
    if not path.is_file() or path.stat().st_size > MAX_SUMMARY_BYTES:
        return False
    try:
        with path.open(encoding="utf-8") as summary_file:
            return isinstance(json.load(summary_file), dict)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return False


def download_summary(repository, run_id, output, runner=subprocess.run, pause=time.sleep):
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.unlink(missing_ok=True)
    download_dir = output.parent / f"{output.stem}-download"

    for attempt in range(RETRY_ATTEMPTS):
        size = find_artifact_size(repository, run_id, runner)
        if size is not None and size > MAX_SUMMARY_BYTES:
            break
        if size is not None:
            shutil.rmtree(download_dir, ignore_errors=True)
            download_dir.mkdir(parents=True, exist_ok=True)
            result = runner(
                [
                    "gh",
                    "run",
                    "download",
                    str(run_id),
                    "--repo",
                    repository,
                    "--name",
                    ARTIFACT_NAME,
                    "--dir",
                    str(download_dir),
                ],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode == 0:
                summary = next(download_dir.rglob(SUMMARY_NAME), None)
                if summary is not None and valid_summary(summary):
                    shutil.copyfile(summary, output)
                    shutil.rmtree(download_dir, ignore_errors=True)
                    return True
        if attempt + 1 < RETRY_ATTEMPTS:
            pause(RETRY_DELAY_SECONDS)

    shutil.rmtree(download_dir, ignore_errors=True)
    return False


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True, help="repository in owner/name form")
    parser.add_argument("--run-id", required=True, help="completed CI workflow run ID")
    parser.add_argument("--output", required=True, help="destination for pr-check-summary.json")
    args = parser.parse_args(argv)

    if not download_summary(args.repository, args.run_id, args.output):
        print("PR check summary artifact unavailable or invalid after retries.", file=sys.stderr)
        return 1
    print("Downloaded PR check summary artifact.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
