#!/usr/bin/env python3
"""Compare current Go benchmark output with a compatible main-branch artifact."""

import argparse
import json
import subprocess
import sys
from pathlib import Path

METADATA_FIELDS = ("format_version", "commit", "created_at", "go_version", "os", "arch", "benchmark_count", "command")
COMPARISON_FIELDS = ("go_version", "os", "arch", "command")
MAX_COMPARISON_CHARS = 20000


def valid_metadata(metadata):
    return (
        isinstance(metadata, dict)
        and metadata.get("format_version") == 1
        and all(isinstance(metadata.get(field), str) and metadata[field] for field in METADATA_FIELDS if field != "format_version" and field != "benchmark_count")
        and isinstance(metadata.get("benchmark_count"), int)
        and metadata["benchmark_count"] > 0
        and metadata.get("command") == "make benchmarks"
    )


def public_metadata(metadata):
    return {key: metadata[key] for key in (*METADATA_FIELDS, "run_id", "artifact_format") if key in metadata}


def unavailable(reason, candidate_metadata=None, baseline_metadata=None):
    return {
        "status": "unavailable",
        "reason": reason,
        "candidate": public_metadata(candidate_metadata) if isinstance(candidate_metadata, dict) else None,
        "baseline": public_metadata(baseline_metadata) if isinstance(baseline_metadata, dict) else None,
        "comparison": None,
    }


def build_report(candidate_path, candidate_metadata, baseline_path, baseline_metadata, benchstat_command):
    if not valid_metadata(candidate_metadata):
        return unavailable("candidate benchmark metadata is missing or invalid", candidate_metadata, baseline_metadata)
    if candidate_metadata["benchmark_count"] != 10:
        return unavailable("candidate benchmark run must contain ten samples", candidate_metadata, baseline_metadata)
    if baseline_path is None or not Path(baseline_path).is_file():
        return unavailable("no retained successful main benchmark baseline artifact is available", candidate_metadata)
    if not valid_metadata(baseline_metadata):
        return unavailable("baseline benchmark metadata is missing or invalid", candidate_metadata, baseline_metadata)

    mismatches = [
        field
        for field in COMPARISON_FIELDS
        if candidate_metadata[field] != baseline_metadata[field]
    ]
    if mismatches:
        return {
            "status": "incomparable",
            "reason": "environment or command mismatch: {}".format(", ".join(mismatches)),
            "candidate": public_metadata(candidate_metadata),
            "baseline": public_metadata(baseline_metadata),
            "comparison": None,
        }

    candidate = Path(candidate_path)
    baseline = Path(baseline_path)
    if not candidate.is_file() or candidate.stat().st_size == 0 or baseline.stat().st_size == 0:
        return unavailable("candidate or baseline benchmark output is empty", candidate_metadata, baseline_metadata)

    command = list(benchstat_command or ["benchstat"])
    command.extend(("main={}".format(baseline), "pull-request={}".format(candidate)))
    try:
        result = subprocess.run(command, check=False, capture_output=True, text=True)
    except OSError as error:
        return unavailable("benchstat could not run: {}".format(error), candidate_metadata, baseline_metadata)
    if result.returncode != 0:
        detail = result.stderr.strip() or "exit status {}".format(result.returncode)
        return unavailable("benchstat failed: {}".format(detail), candidate_metadata, baseline_metadata)
    comparison = result.stdout.strip()
    if not comparison:
        return unavailable("benchstat returned no comparison output", candidate_metadata, baseline_metadata)

    truncated = len(comparison) > MAX_COMPARISON_CHARS
    sample_counts_match = candidate_metadata["benchmark_count"] == baseline_metadata["benchmark_count"]
    return {
        "status": "available",
        "reason": None,
        "candidate": public_metadata(candidate_metadata),
        "baseline": public_metadata(baseline_metadata),
        "comparison": comparison[:MAX_COMPARISON_CHARS],
        "comparison_truncated": truncated,
        "sample_counts_match": sample_counts_match,
        "sample_count_notice": None if sample_counts_match else "baseline and candidate sample counts differ; interpret p-values cautiously",
        "method": "golang.org/x/perf/cmd/benchstat",
        "interpretation": "benchstat uses p-values; '~' denotes no statistically significant difference.",
    }


def read_json(path):
    if not path or not Path(path).is_file():
        return None
    try:
        with open(path, encoding="utf-8") as input_file:
            return json.load(input_file)
    except (OSError, json.JSONDecodeError):
        return None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True, help="current benchmark output path")
    parser.add_argument("--candidate-metadata", required=True, help="current benchmark metadata JSON path")
    parser.add_argument("--baseline-dir", help="directory containing benchmark.txt and metadata.json from main")
    parser.add_argument("--benchstat", default="benchstat", help="benchstat executable path")
    parser.add_argument("--output", default="-", help="JSON report path, or - for stdout")
    args = parser.parse_args(argv)

    baseline_dir = Path(args.baseline_dir) if args.baseline_dir else None
    baseline_path = baseline_dir / "benchmark.txt" if baseline_dir else None
    baseline_metadata_path = baseline_dir / "metadata.json" if baseline_dir else None
    report = build_report(
        args.candidate,
        read_json(args.candidate_metadata),
        baseline_path,
        read_json(baseline_metadata_path),
        [args.benchstat],
    )
    output = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.output == "-":
        sys.stdout.write(output)
    else:
        with open(args.output, "w", encoding="utf-8") as output_file:
            output_file.write(output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
