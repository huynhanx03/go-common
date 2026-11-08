import unittest

from coverage_report import build_report


class CoverageReportTest(unittest.TestCase):
    def test_reports_statement_weighted_packages_and_uncovered_functions(self):
        profile = """mode: atomic
example.com/project/pkg/alpha/alpha.go:1.1,2.1 2 1
example.com/project/pkg/alpha/alpha.go:3.1,4.1 3 0
example.com/project/pkg/alpha/alpha.go:5.1,5.1 0 1
example.com/project/pkg/beta/beta.go:1.1,2.1 4 0
"""
        functions = """example.com/project/pkg/alpha/alpha.go:1: alphaCovered 40.0%
example.com/project/pkg/alpha/alpha.go:3: alphaMissing 0.0%
example.com/project/pkg/beta/beta.go:1: betaMissing 0.0%
total: (statements) 22.2%
"""

        report = build_report(profile, functions, minimum_percent=65.0)

        self.assertEqual(report["total_percent"], 22.2)
        self.assertEqual(report["minimum_percent"], 65.0)
        self.assertEqual(
            report["packages"],
            [
                {
                    "name": "example.com/project/pkg/beta",
                    "statements": 4,
                    "covered_statements": 0,
                    "coverage_percent": 0.0,
                },
                {
                    "name": "example.com/project/pkg/alpha",
                    "statements": 5,
                    "covered_statements": 2,
                    "coverage_percent": 40.0,
                },
            ],
        )
        self.assertEqual(
            report["zero_covered_functions"],
            [
                {
                    "package": "example.com/project/pkg/beta",
                    "file": "example.com/project/pkg/beta/beta.go",
                    "line": 1,
                    "name": "betaMissing",
                },
                {
                    "package": "example.com/project/pkg/alpha",
                    "file": "example.com/project/pkg/alpha/alpha.go",
                    "line": 3,
                    "name": "alphaMissing",
                },
            ],
        )

    def test_rejects_empty_or_malformed_coverage_data(self):
        for profile in ("", "mode: atomic\nnot-a-profile-line\n"):
            with self.subTest(profile=profile), self.assertRaisesRegex(ValueError, "coverage profile"):
                build_report(profile, "", minimum_percent=65.0)

    def test_rejects_invalid_function_coverage_output(self):
        with self.assertRaisesRegex(ValueError, "function coverage"):
            build_report(
                "mode: atomic\nexample.com/project/pkg/a.go:1.1,2.1 1 0\n",
                "not-a-function-row\n",
                minimum_percent=65.0,
            )


if __name__ == "__main__":
    unittest.main()
