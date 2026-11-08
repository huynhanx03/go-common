import json
import tempfile
import unittest
from pathlib import Path

from ci_failure_report import build_failure_report, main


class CIFailureReportTest(unittest.TestCase):
    def test_extracts_bounded_context_around_go_failure_markers(self):
        log = """Set up job
Run unit tests
pkg/cache/cache_test.go:42: expected hit
--- FAIL: TestCacheHit (0.00s)
    cache_test.go:43: got miss
cleanup context
cleanup complete
"""
        report = build_failure_report(
            [{"id": 123, "name": "Unit tests", "conclusion": "failure"}],
            {"123": log},
            {"123": "available"},
        )

        job = report["jobs"][0]
        self.assertEqual(job["name"], "Unit tests")
        self.assertEqual(job["conclusion"], "failure")
        self.assertEqual(job["log_status"], "available")
        self.assertEqual(job["marker_count"], 1)
        self.assertIn("--- FAIL: TestCacheHit", job["excerpt"])
        self.assertIn("got miss", job["excerpt"])
        self.assertNotIn("cleanup complete", job["excerpt"])

    def test_keeps_failures_grouped_and_marks_unavailable_logs(self):
        jobs = [
            {"id": 101, "name": "Race tests", "conclusion": "failure"},
            {"id": 102, "name": "Integration", "conclusion": "timed_out"},
        ]
        report = build_failure_report(
            jobs,
            {"101": "go test\nFAIL\t./pkg/cache\n"},
            {"101": "available", "102": "unavailable"},
        )

        self.assertEqual([job["name"] for job in report["jobs"]], ["Race tests", "Integration"])
        self.assertEqual(report["jobs"][0]["log_status"], "available")
        self.assertIn("./pkg/cache", report["jobs"][0]["excerpt"])
        self.assertEqual(report["jobs"][1]["log_status"], "unavailable")
        self.assertEqual(report["jobs"][1]["excerpt"], "")

    def test_reports_no_markers_and_passing_runs_without_inventing_causes(self):
        report = build_failure_report(
            [{"id": 7, "name": "Quality", "conclusion": "failure"}],
            {"7": "lint started\nchecks completed\n"},
            {"7": "available"},
        )
        passing = build_failure_report([], {}, {})

        self.assertEqual(report["jobs"][0]["log_status"], "no_failure_markers")
        self.assertEqual(report["jobs"][0]["excerpt"], "")
        self.assertEqual(passing, {"status": "available", "jobs": [], "total_excerpt_chars": 0})

    def test_caps_total_log_excerpt_size_and_marks_omitted_jobs(self):
        jobs = [
            {"id": index, "name": "Job {}".format(index), "conclusion": "failure"}
            for index in range(1, 5)
        ]
        logs = {
            str(index): "\n".join(["noise"] * 50 + ["::error::root cause"] + ["context"] * 50)
            for index in range(1, 5)
        }
        statuses = {str(index): "available" for index in range(1, 5)}

        report = build_failure_report(jobs, logs, statuses, max_total_chars=240, max_per_job_chars=200)

        total = sum(len(job["excerpt"]) for job in report["jobs"])
        self.assertLessEqual(total, 240)
        self.assertEqual(report["total_excerpt_chars"], total)
        self.assertTrue(any(job["excerpt_truncated"] for job in report["jobs"]))
        self.assertTrue(all(job["name"] for job in report["jobs"]))

    def test_cli_combines_job_metadata_with_individual_log_files(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            logs = root / "logs"
            logs.mkdir()
            (root / "jobs.json").write_text(
                json.dumps([{"id": 55, "name": "Integration", "conclusion": "failure"}]),
                encoding="utf-8",
            )
            (logs / "55.status").write_text("available", encoding="utf-8")
            (logs / "55.log").write_text("start\nError: container did not become healthy\n", encoding="utf-8")
            output = root / "report.json"

            result = main(["--jobs", str(root / "jobs.json"), "--logs-dir", str(logs), "--output", str(output)])

            report = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result, 0)
            self.assertEqual(report["jobs"][0]["name"], "Integration")
            self.assertIn("container did not become healthy", report["jobs"][0]["excerpt"])

    def test_cli_ignores_non_numeric_job_ids_when_reading_log_files(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            logs = root / "logs"
            logs.mkdir()
            (root / "secret.status").write_text("available", encoding="utf-8")
            (root / "secret.log").write_text("Error: outside the log directory", encoding="utf-8")
            (root / "jobs.json").write_text(
                json.dumps([{"id": "../secret", "name": "Untrusted", "conclusion": "failure"}]),
                encoding="utf-8",
            )
            output = root / "report.json"

            main(["--jobs", str(root / "jobs.json"), "--logs-dir", str(logs), "--output", str(output)])

            job = json.loads(output.read_text(encoding="utf-8"))["jobs"][0]
            self.assertEqual(job["log_status"], "unavailable")
            self.assertEqual(job["excerpt"], "")

    def test_run_metadata_failure_is_explicit(self):
        report = build_failure_report([], {}, {}, run_status="unavailable", reason="GitHub API unavailable")

        self.assertEqual(report["status"], "unavailable")
        self.assertEqual(report["reason"], "GitHub API unavailable")
        self.assertEqual(report["jobs"], [])


if __name__ == "__main__":
    unittest.main()
