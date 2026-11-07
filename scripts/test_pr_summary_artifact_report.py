import json
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

from pr_summary_artifact import MAX_SUMMARY_BYTES, download_summary


class PRSummaryArtifactTest(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)
        self.output = self.root / "pr-check-summary.json"

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_waits_for_artifact_visibility_and_finds_downloaded_summary(self):
        api_calls = 0
        pauses = []

        def runner(command, **_kwargs):
            nonlocal api_calls
            if command[1] == "api":
                api_calls += 1
                artifacts = [] if api_calls == 1 else [{"name": "pr-check-summary", "size_in_bytes": 42}]
                return SimpleNamespace(returncode=0, stdout=json.dumps({"artifacts": artifacts}))
            download_dir = Path(command[-1]) / "pr-check-summary"
            download_dir.mkdir(parents=True)
            (download_dir / "pr-check-summary.json").write_text('{"coverage": {"total_percent": 80}}', encoding="utf-8")
            return SimpleNamespace(returncode=0, stdout="")

        downloaded = download_summary("owner/repo", 123, self.output, runner=runner, pause=pauses.append)

        self.assertTrue(downloaded)
        self.assertEqual(api_calls, 2)
        self.assertEqual(pauses, [3])
        self.assertEqual(json.loads(self.output.read_text(encoding="utf-8"))["coverage"]["total_percent"], 80)

    def test_retries_a_failed_download_when_artifact_listing_is_available(self):
        downloads = 0
        pauses = []

        def runner(command, **_kwargs):
            nonlocal downloads
            if command[1] == "api":
                return SimpleNamespace(
                    returncode=0,
                    stdout=json.dumps({"artifacts": [{"name": "pr-check-summary", "size_in_bytes": 12}]}),
                )
            downloads += 1
            if downloads == 1:
                return SimpleNamespace(returncode=1, stdout="")
            download_dir = Path(command[-1])
            (download_dir / "pr-check-summary.json").write_text("{}", encoding="utf-8")
            return SimpleNamespace(returncode=0, stdout="")

        downloaded = download_summary("owner/repo", 123, self.output, runner=runner, pause=pauses.append)

        self.assertTrue(downloaded)
        self.assertEqual(downloads, 2)
        self.assertEqual(pauses, [3])

    def test_rejects_oversized_summary_without_downloading(self):
        commands = []

        def runner(command, **_kwargs):
            commands.append(command)
            return SimpleNamespace(
                returncode=0,
                stdout=json.dumps({"artifacts": [{"name": "pr-check-summary", "size_in_bytes": MAX_SUMMARY_BYTES + 1}]}),
            )

        downloaded = download_summary("owner/repo", 123, self.output, runner=runner, pause=lambda _delay: None)

        self.assertFalse(downloaded)
        self.assertEqual(len(commands), 1)
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
