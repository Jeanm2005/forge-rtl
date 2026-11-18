import json
import unittest
from hashlib import sha256
from pathlib import Path
from tempfile import TemporaryDirectory
from zipfile import ZipFile

from forge_proposal.contracts import Candidate
from forge_proposal.demo import export_run
from forge_proposal.workflow import Evaluation, Run, Trial


class RunArchiveTest(unittest.TestCase):
    def test_archive_preserves_bytes_and_refuses_overwrite(self) -> None:
        baseline = Candidate(b"baseline\r\n")
        revision = Candidate(b"revision\n")
        manifest = b'{"schema_version":1}\n'
        job = b'{"job_id":"job"}\n'
        proposal = b'{"source":"revision\\n"}'
        result = Evaluation(
            revision.digest, "job", "passed", True, manifest, "passed", job
        )
        run = Run(
            baseline,
            (
                Trial(baseline.digest, None, b"bad", "invalid proposal", None),
                Trial(baseline.digest, revision, proposal, None, result),
            ),
            2,
            1,
            "eligible",
        )
        with TemporaryDirectory() as directory:
            output = Path(directory) / "run.zip"
            export_run(run, "test", output)
            with self.assertRaises(FileExistsError):
                export_run(run, "other", output)
            with ZipFile(output) as archive:
                self.assertEqual(archive.read("baseline.sv"), baseline.source)
                self.assertEqual(archive.read("trials/0/proposal.json"), b"bad")
                self.assertEqual(archive.read("trials/1/candidate.sv"), revision.source)
                self.assertEqual(archive.read("trials/1/proposal.json"), proposal)
                self.assertEqual(archive.read("trials/1/manifest.json"), manifest)
                self.assertEqual(archive.read("trials/1/job.json"), job)
                index = json.loads(archive.read("run.json"))
                self.assertEqual(index["run_id"], "test")
                self.assertTrue(index["eligible"])
                self.assertEqual(index["trials"][0]["diagnostic"], "invalid proposal")
                self.assertEqual(index["trials"][1]["parent_sha256"], baseline.digest)
                self.assertEqual(
                    index["trials"][1]["evaluation"]["manifest_sha256"],
                    sha256(manifest).hexdigest(),
                )
