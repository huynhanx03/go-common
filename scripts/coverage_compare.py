#!/usr/bin/env python3
"""Compare package coverage reports from a pull request and main baseline."""

import argparse
import json
import sys
from pathlib import Path

ENVIRONMENT_FIELDS = ("go_version", "os", "arch")
METADATA_FIELDS = ("commit", *ENVIRONMENT_FIELDS, "command")


def valid_report(report):
    if not isinstance(report, dict) or report.get("status") != "available":
        return False
    packages = report.get("packages")
    if not isinstance(packages, list):
        return False
    for package in packages:
        if (
            not isinstance(package, dict)
            or not isinstance(package.get("name"), str)
            or not package["name"]
            or type(package.get("statements")) is not int
            or package["statements"] < 0
            or type(package.get("coverage_percent")) not in (int, float)
            or not 0 <= package["coverage_percent"] <= 100
        ):
            return False
    return True


def valid_metadata(metadata):
    return isinstance(metadata, dict) and all(
        isinstance(metadata.get(field), str) and metadata[field] for field in METADATA_FIELDS
    )


def public_metadata(metadata):
    return {key: metadata[key] for key in (*METADATA_FIELDS, "run_id", "created_at") if key in metadata}


def unavailable(reason, candidate_metadata=None, baseline_metadata=None):
    return {
        "status": "unavailable",
        "reason": reason,
        "candidate": public_metadata(candidate_metadata) if isinstance(candidate_metadata, dict) else None,
        "baseline": public_metadata(baseline_metadata) if isinstance(baseline_metadata, dict) else None,
        "packages": [],
    }


def compare_reports(candidate, baseline, candidate_metadata, baseline_metadata):
    if not valid_report(candidate):
        return unavailable("candidate coverage report is missing or invalid", candidate_metadata, baseline_metadata)
    if baseline is None:
        return unavailable("no retained successful main coverage baseline artifact is available", candidate_metadata)
    if not valid_report(baseline):
        return unavailable("main coverage baseline report is missing or invalid", candidate_metadata, baseline_metadata)
    if not valid_metadata(candidate_metadata):
        return unavailable("candidate coverage metadata is missing or invalid", candidate_metadata, baseline_metadata)
    if not valid_metadata(baseline_metadata):
        return unavailable("main coverage baseline metadata is missing or invalid", candidate_metadata, baseline_metadata)

    current_packages = {package["name"]: package for package in candidate["packages"]}
    base_packages = {package["name"]: package for package in baseline["packages"]}
    rows = []
    regression_count = 0
    improvement_count = 0
    new_packages = []
    removed_packages = []
    for name in sorted(current_packages.keys() | base_packages.keys()):
        current = current_packages.get(name)
        base = base_packages.get(name)
        if current is None:
            status = "removed"
            delta = None
            removed_packages.append(name)
        elif base is None:
            status = "new"
            delta = None
            new_packages.append(name)
        else:
            delta = round(current["coverage_percent"] - base["coverage_percent"], 1)
            if delta < 0:
                status = "regressed"
                regression_count += 1
            elif delta > 0:
                status = "improved"
                improvement_count += 1
            else:
                status = "unchanged"
        rows.append(
            {
                "name": name,
                "status": status,
                "baseline_percent": base["coverage_percent"] if base else None,
                "candidate_percent": current["coverage_percent"] if current else None,
                "delta_percentage_points": delta,
                "baseline_statements": base["statements"] if base else None,
                "candidate_statements": current["statements"] if current else None,
            }
        )

    mismatches = [field for field in ENVIRONMENT_FIELDS if candidate_metadata[field] != baseline_metadata[field]]
    total_delta = None
    if type(candidate.get("total_percent")) in (int, float) and type(baseline.get("total_percent")) in (int, float):
        total_delta = round(candidate["total_percent"] - baseline["total_percent"], 1)
    return {
        "status": "available",
        "baseline": public_metadata(baseline_metadata),
        "candidate": public_metadata(candidate_metadata),
        "total_delta_percentage_points": total_delta,
        "package_count": len(current_packages),
        "regression_count": regression_count,
        "improvement_count": improvement_count,
        "unchanged_count": sum(row["status"] == "unchanged" for row in rows),
        "new_packages": new_packages,
        "removed_packages": removed_packages,
        "environment_mismatches": mismatches,
        "environment_notice": (
            "coverage environments differ; interpret package deltas cautiously: {}".format(", ".join(mismatches))
            if mismatches
            else None
        ),
        "packages": rows,
    }


def read_json(path):
    if not path or not Path(path).is_file():
        return None
    try:
        with open(path, encoding="utf-8") as input_file:
            return json.load(input_file)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError):
        return None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate-dir", required=True, help="directory containing coverage-report.json and metadata.json")
    parser.add_argument("--baseline-dir", required=True, help="directory containing main coverage-report.json and metadata.json")
    parser.add_argument("--output", default="-", help="JSON output path, or - for stdout")
    args = parser.parse_args(argv)

    candidate_dir = Path(args.candidate_dir)
    baseline_dir = Path(args.baseline_dir)
    report = compare_reports(
        read_json(candidate_dir / "coverage-report.json"),
        read_json(baseline_dir / "coverage-report.json"),
        read_json(candidate_dir / "metadata.json"),
        read_json(baseline_dir / "metadata.json"),
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
