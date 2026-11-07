#!/usr/bin/env python3
"""Build a detailed, package-level report from Go coverprofile data."""

import argparse
import json
import re
import subprocess
import sys
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP
from pathlib import PurePosixPath

PROFILE_ROW = re.compile(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+)\s+(\d+)\s+(\d+)$")
FUNCTION_ROW = re.compile(r"^(.+):(\d+):\s+(\S+)\s+([0-9]+(?:\.[0-9]+)?)%$")


def rounded_percent(numerator, denominator):
    if denominator == 0:
        return 0.0
    value = Decimal(numerator) * Decimal(100) / Decimal(denominator)
    return float(value.quantize(Decimal("0.1"), rounding=ROUND_HALF_UP))


def package_for(path):
    normalized = path.replace("\\", "/")
    return PurePosixPath(normalized).parent.as_posix()


def parse_profile(profile_text):
    lines = profile_text.splitlines()
    if not lines or not lines[0].startswith("mode: "):
        raise ValueError("coverage profile must begin with a mode header")

    packages = defaultdict(lambda: {"statements": 0, "covered_statements": 0})
    rows = 0
    for line_number, line in enumerate(lines[1:], start=2):
        if not line:
            continue
        match = PROFILE_ROW.fullmatch(line)
        if not match:
            raise ValueError("coverage profile has an invalid row at line {}".format(line_number))
        path, _start_line, _start_col, _end_line, _end_col, statement_text, count_text = match.groups()
        statements = int(statement_text)
        count = int(count_text)
        if statements < 0 or count < 0:
            raise ValueError("coverage profile has invalid counts at line {}".format(line_number))
        if statements == 0:
            continue
        package = packages[package_for(path)]
        package["statements"] += statements
        if count > 0:
            package["covered_statements"] += statements
        rows += 1

    if rows == 0:
        raise ValueError("coverage profile has no statement rows")
    return packages


def parse_zero_covered_functions(function_text):
    functions = []
    for line_number, line in enumerate(function_text.splitlines(), start=1):
        if not line or line.startswith("total:"):
            continue
        match = FUNCTION_ROW.fullmatch(line)
        if not match:
            raise ValueError("function coverage has an invalid row at line {}".format(line_number))
        path, source_line, name, percent_text = match.groups()
        if Decimal(percent_text) == 0:
            functions.append(
                {
                    "package": package_for(path),
                    "file": path.replace("\\", "/"),
                    "line": int(source_line),
                    "name": name,
                }
            )
    return sorted(functions, key=lambda row: (row["package"], row["file"], row["line"], row["name"]))


def build_report(profile_text, function_text, minimum_percent):
    raw_packages = parse_profile(profile_text)
    packages = []
    total_statements = 0
    covered_statements = 0
    for name, counts in raw_packages.items():
        statements = counts["statements"]
        covered = counts["covered_statements"]
        total_statements += statements
        covered_statements += covered
        packages.append(
            {
                "name": name,
                "statements": statements,
                "covered_statements": covered,
                "coverage_percent": rounded_percent(covered, statements),
            }
        )

    packages.sort(key=lambda row: (row["coverage_percent"], row["name"]))
    package_coverage = {row["name"]: row["coverage_percent"] for row in packages}
    zero_covered_functions = parse_zero_covered_functions(function_text)
    zero_covered_functions.sort(
        key=lambda row: (
            package_coverage.get(row["package"], 100.0),
            row["package"],
            row["file"],
            row["line"],
            row["name"],
        )
    )
    return {
        "status": "available",
        "total_percent": rounded_percent(covered_statements, total_statements),
        "minimum_percent": float(minimum_percent),
        "statements": total_statements,
        "covered_statements": covered_statements,
        "packages": packages,
        "zero_covered_functions": zero_covered_functions,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True, help="Go coverage profile path")
    parser.add_argument("--minimum", type=float, default=65.0, help="required total coverage percentage")
    parser.add_argument("--output", default="-", help="JSON output path, or - for stdout")
    parser.add_argument("--go", default="go", help="Go executable used for go tool cover")
    args = parser.parse_args(argv)

    try:
        with open(args.profile, encoding="utf-8") as profile_file:
            profile_text = profile_file.read()
        result = subprocess.run(
            [args.go, "tool", "cover", "-func={}".format(args.profile)],
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or "exit status {}".format(result.returncode)
            raise ValueError("go tool cover failed: {}".format(detail))
        report = build_report(profile_text, result.stdout, args.minimum)
        output = json.dumps(report, indent=2, sort_keys=True) + "\n"
        if args.output == "-":
            sys.stdout.write(output)
        else:
            with open(args.output, "w", encoding="utf-8") as output_file:
                output_file.write(output)
    except (OSError, ValueError) as error:
        print("coverage report error: {}".format(error), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
