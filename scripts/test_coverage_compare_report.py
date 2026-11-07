import unittest

from coverage_compare import compare_reports


class CoverageCompareTest(unittest.TestCase):
    def setUp(self):
        self.candidate = {
            "status": "available",
            "total_percent": 60.0,
            "packages": [
                {"name": "pkg/alpha", "statements": 10, "coverage_percent": 60.0},
                {"name": "pkg/new", "statements": 4, "coverage_percent": 50.0},
            ],
        }
        self.baseline = {
            "status": "available",
            "total_percent": 75.0,
            "packages": [
                {"name": "pkg/alpha", "statements": 8, "coverage_percent": 75.0},
                {"name": "pkg/removed", "statements": 5, "coverage_percent": 40.0},
            ],
        }
        self.candidate_metadata = {
            "commit": "pr-sha", "go_version": "go1.27.3", "os": "linux", "arch": "amd64", "command": "go test coverage"
        }
        self.baseline_metadata = {
            "commit": "main-sha", "go_version": "go1.27.3", "os": "linux", "arch": "amd64", "command": "go test coverage"
        }

    def test_reports_package_regressions_and_added_or_removed_packages(self):
        report = compare_reports(self.candidate, self.baseline, self.candidate_metadata, self.baseline_metadata)

        self.assertEqual(report["status"], "available")
        self.assertEqual(report["baseline"]["commit"], "main-sha")
        self.assertEqual(report["candidate"]["commit"], "pr-sha")
        self.assertEqual(report["regression_count"], 1)
        self.assertEqual(report["improvement_count"], 0)
        self.assertEqual(report["new_packages"], ["pkg/new"])
        self.assertEqual(report["removed_packages"], ["pkg/removed"])
        self.assertEqual(
            report["packages"][0],
            {
                "name": "pkg/alpha",
                "status": "regressed",
                "baseline_percent": 75.0,
                "candidate_percent": 60.0,
                "delta_percentage_points": -15.0,
                "baseline_statements": 8,
                "candidate_statements": 10,
            },
        )

    def test_discloses_environment_mismatch_and_missing_baseline(self):
        mismatch = {**self.baseline_metadata, "go_version": "go1.26.0"}
        report = compare_reports(self.candidate, self.baseline, self.candidate_metadata, mismatch)

        self.assertEqual(report["status"], "available")
        self.assertEqual(report["environment_mismatches"], ["go_version"])
        self.assertIn("cautiously", report["environment_notice"])

        unavailable = compare_reports(self.candidate, None, self.candidate_metadata, None)
        self.assertEqual(unavailable["status"], "unavailable")
        self.assertIn("baseline", unavailable["reason"])


if __name__ == "__main__":
    unittest.main()
