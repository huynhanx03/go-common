#!/usr/bin/env python3
"""Build bounded, per-job diagnostic excerpts from GitHub Actions logs."""

import argparse
import json
import re
import sys
from pathlib import Path

FAILURE_MARKER = re.compile(
    r"(?im)(::error::|^--- FAIL:|^FAIL(?:\s|$)|^panic:|^fatal(?: error)?:|^error:|"
    r"^Traceback \(most recent call last\):|^FAILED(?:\s|\(|$)|^Exception:|"
    r"\b(?:undefined:|cannot use |no required module|permission denied|operation not permitted|"
    r"AssertionError|TypeError|ImportError|ModuleNotFoundError|ValueError|RuntimeError|KeyError)\b|"
    r"Process completed with exit code [1-9])"
)
CONTEXT_LINES = 2
MAX_MARKER_LINES = 5
MAX_PER_JOB_CHARS = 3000
MAX_TOTAL_CHARS = 12000


def extract_excerpt(log_text, max_chars=MAX_PER_JOB_CHARS):
    lines = log_text.splitlines()
    markers = [index for index, line in enumerate(lines) if FAILURE_MARKER.search(line)]
    if not markers:
        return "no_failure_markers", "", 0, False

    selected = markers[:MAX_MARKER_LINES]
    windows = []
    for marker in selected:
        start = max(0, marker - CONTEXT_LINES)
        end = min(len(lines), marker + CONTEXT_LINES + 1)
        if windows and start <= windows[-1][1]:
            windows[-1] = (windows[-1][0], max(windows[-1][1], end))
        else:
            windows.append((start, end))

    chunks = []
    for start, end in windows:
        chunks.append("--- lines {}-{} ---".format(start + 1, end))
        chunks.extend("{:04d} | {}".format(index + 1, lines[index]) for index in range(start, end))
    excerpt = "\n".join(chunks)
    truncated = len(markers) > len(selected) or len(excerpt) > max_chars
    if len(excerpt) > max_chars:
        suffix = "\n[excerpt truncated]"
        excerpt = excerpt[: max(0, max_chars - len(suffix))] + suffix[:max_chars]
    return "available", excerpt, len(markers), truncated


def build_failure_report(jobs, logs, log_status, run_status="available", reason=None,
                         max_total_chars=MAX_TOTAL_CHARS, max_per_job_chars=MAX_PER_JOB_CHARS):
    if run_status != "available":
        return {
            "status": "unavailable",
            "reason": reason or "failed job metadata is unavailable",
            "jobs": [],
            "total_excerpt_chars": 0,
        }

    remaining = max_total_chars
    output_jobs = []
    for job in jobs:
        job_id = str(job.get("id", ""))
        status = log_status.get(job_id, "unavailable")
        excerpt = ""
        marker_count = 0
        truncated = False
        if status != "available" or job_id not in logs:
            status = "unavailable"
        else:
            status, excerpt, marker_count, truncated = extract_excerpt(logs[job_id], max_per_job_chars)
            if excerpt and len(excerpt) > remaining:
                excerpt = excerpt[:remaining]
                truncated = True
            remaining = max(0, remaining - len(excerpt))
            if excerpt and len(excerpt) == 0:
                truncated = True
        output_jobs.append(
            {
                "job_id": job_id,
                "name": job.get("name", "Unknown job"),
                "conclusion": job.get("conclusion", "unknown"),
                "log_status": status,
                "marker_count": marker_count,
                "excerpt": excerpt,
                "excerpt_truncated": truncated,
            }
        )

    total_chars = sum(len(job["excerpt"]) for job in output_jobs)
    return {"status": "available", "jobs": output_jobs, "total_excerpt_chars": total_chars}


def load_jobs(path):
    with open(path, encoding="utf-8") as input_file:
        payload = json.load(input_file)
    if isinstance(payload, list):
        return "available", None, payload
    if isinstance(payload, dict) and isinstance(payload.get("jobs"), list):
        return payload.get("status", "available"), payload.get("reason"), payload["jobs"]
    raise ValueError("job metadata must be an array or an object with a jobs array")


def read_logs(jobs, logs_dir):
    logs = {}
    statuses = {}
    root = Path(logs_dir)
    for job in jobs:
        job_id = str(job.get("id", ""))
        if not job_id.isdigit():
            statuses[job_id] = "unavailable"
            continue
        status_path = root / (job_id + ".status")
        log_path = root / (job_id + ".log")
        try:
            status = status_path.read_text(encoding="utf-8").strip()
        except OSError:
            status = "unavailable"
        statuses[job_id] = status
        if status == "available":
            try:
                logs[job_id] = log_path.read_text(encoding="utf-8", errors="replace")
            except OSError:
                statuses[job_id] = "unavailable"
    return logs, statuses


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--jobs", required=True, help="JSON array of failed job IDs, names, and conclusions")
    parser.add_argument("--logs-dir", required=True, help="directory containing per-job .log and .status files")
    parser.add_argument("--output", default="-", help="JSON report path, or - for stdout")
    args = parser.parse_args(argv)

    try:
        run_status, reason, jobs = load_jobs(args.jobs)
        logs, statuses = read_logs(jobs, args.logs_dir)
        report = build_failure_report(jobs, logs, statuses, run_status, reason)
    except (OSError, ValueError, json.JSONDecodeError) as error:
        report = build_failure_report([], {}, {}, run_status="unavailable", reason=str(error))

    output = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output == "-":
        sys.stdout.write(output)
    else:
        with open(args.output, "w", encoding="utf-8") as output_file:
            output_file.write(output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
