import sys
import tempfile
import unittest
from pathlib import Path

from benchmark_report import build_report


class BenchmarkReportTest(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.candidate = self.root / "candidate.txt"
        self.baseline = self.root / "baseline.txt"
        self.candidate.write_text("BenchmarkEncode-8 100 120 ns/op\n", encoding="utf-8")
        self.baseline.write_text("BenchmarkEncode-8 100 150 ns/op\n", encoding="utf-8")
        self.candidate_metadata = {
            "format_version": 1,
            "commit": "pr-commit",
            "created_at": "2026-11-07T00:00:00Z",
            "go_version": "go1.27.3",
            "os": "linux",
            "arch": "amd64",
            "benchmark_count": 10,
            "command": "make benchmarks",
        }
        self.baseline_metadata = {
            **self.candidate_metadata,
            "commit": "main-commit",
            "run_id": 123,
        }

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_missing_baseline_is_unavailable_without_running_benchstat(self):
        report = build_report(self.candidate, self.candidate_metadata, None, None, ["missing-benchstat"])

        self.assertEqual(report["status"], "unavailable")
        self.assertIn("baseline", report["reason"])
        self.assertEqual(report["candidate"]["commit"], "pr-commit")

    def test_environment_mismatch_is_incomparable(self):
        metadata = {**self.baseline_metadata, "go_version": "go1.26.0"}

        report = build_report(self.candidate, self.candidate_metadata, self.baseline, metadata, ["missing-benchstat"])

        self.assertEqual(report["status"], "incomparable")
        self.assertIn("go_version", report["reason"])
        self.assertIsNone(report["comparison"])

    def test_compares_legacy_baseline_with_fewer_samples_and_reports_the_difference(self):
        fake_benchstat = self.root / "benchstat.py"
        fake_benchstat.write_text("print('BenchmarkEncode-8 -20.00% (p=0.01 n=10)')\n", encoding="utf-8")
        baseline_metadata = {**self.baseline_metadata, "benchmark_count": 5}

        report = build_report(
            self.candidate,
            self.candidate_metadata,
            self.baseline,
            baseline_metadata,
            [sys.executable, str(fake_benchstat)],
        )

        self.assertEqual(report["status"], "available")
        self.assertFalse(report["sample_counts_match"])
        self.assertIn("interpret p-values cautiously", report["sample_count_notice"])
        self.assertEqual(report["candidate"]["benchmark_count"], 10)
        self.assertEqual(report["baseline"]["benchmark_count"], 5)

    def test_candidate_must_keep_ten_samples(self):
        metadata = {**self.candidate_metadata, "benchmark_count": 5}

        report = build_report(self.candidate, metadata, self.baseline, self.baseline_metadata, ["missing-benchstat"])

        self.assertEqual(report["status"], "unavailable")
        self.assertIn("ten samples", report["reason"])

    def test_preserves_significant_and_inconclusive_benchstat_results(self):
        fake_benchstat = self.root / "benchstat.py"
        fake_benchstat.write_text(
            "import pathlib, sys\n"
            "assert pathlib.Path(sys.argv[-2].split('=', 1)[1]).is_file()\n"
            "assert pathlib.Path(sys.argv[-1].split('=', 1)[1]).is_file()\n"
            "print('BenchmarkFast-8 -20.00% (p=0.001 n=10)')\n"
            "print('BenchmarkStable-8 ~ (p=0.400 n=10)')\n",
            encoding="utf-8",
        )

        report = build_report(
            self.candidate,
            self.candidate_metadata,
            self.baseline,
            self.baseline_metadata,
            [sys.executable, str(fake_benchstat)],
        )

        self.assertEqual(report["status"], "available")
        self.assertIn("p=0.001", report["comparison"])
        self.assertIn("~ (p=0.400 n=10)", report["comparison"])
        self.assertEqual(report["baseline"]["commit"], "main-commit")

    def test_missing_benchstat_is_reported_as_unavailable(self):
        report = build_report(
            self.candidate,
            self.candidate_metadata,
            self.baseline,
            self.baseline_metadata,
            [str(self.root / "missing-benchstat")],
        )

        self.assertEqual(report["status"], "unavailable")
        self.assertIn("benchstat could not run", report["reason"])

    def test_invalid_baseline_metadata_does_not_fail_comparison_job(self):
        report = build_report(self.candidate, self.candidate_metadata, self.baseline, {"commit": "main"}, ["missing-benchstat"])

        self.assertEqual(report["status"], "unavailable")
        self.assertIn("metadata", report["reason"])


if __name__ == "__main__":
    unittest.main()
