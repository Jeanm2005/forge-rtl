import json
import unittest
from hashlib import sha256
from pathlib import Path
from unittest.mock import patch

from forge_proposal.contracts import CANDIDATE_PATH, Candidate
from forge_proposal.control import ControlEvaluator
from forge_proposal.workflow import EvaluationError


class ControlEvaluatorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.client = ControlEvaluator(Path("forge-client"), Path("control.sock"))
        self.candidate = Candidate(b"module mac_candidate; endmodule\n")
        self.manifest = {
            "schema_version": 1,
            "job_id": "job",
            "attempt_id": "attempt",
            "outcome": "counterexample",
            "inputs": {
                CANDIDATE_PATH: {
                    "sha256": self.candidate.digest,
                    "bytes": len(self.candidate.source),
                }
            },
            "gates": {"reachable_depth_3": {"outcome": "counterexample"}},
            "generic_cells": None,
        }

    def job(self, manifest: bytes) -> dict[str, object]:
        return {
            "job_id": "job",
            "candidate_sha256": self.candidate.digest,
            "state": "COMPLETED",
            "outcome": "counterexample",
            "eligible": False,
            "attempts": [
                {
                    "attempt_id": "attempt",
                    "state": "FINISHED",
                    "manifest_sha256": sha256(manifest).hexdigest(),
                    "outcome": "counterexample",
                }
            ],
        }

    def test_submission_and_exact_attempt_evidence(self) -> None:
        manifest = json.dumps(self.manifest).encode() + b"\n"
        job = json.dumps(self.job(manifest)).encode()
        queued = json.dumps({**self.job(manifest), "state": "QUEUED"}).encode()
        with (
            patch.object(
                ControlEvaluator,
                "_call",
                side_effect=[queued, job, job, manifest + b"\n"],
            ) as call,
            patch("forge_proposal.control.sleep"),
        ):
            result = self.client.evaluate(self.candidate, "run-0")
        self.assertEqual(result.manifest, manifest)
        self.assertEqual(result.job_record, job)
        self.assertFalse(result.eligible)
        self.assertIn("reachable_depth_3: counterexample", result.feedback)
        self.assertEqual(call.call_args_list[0].args[1:4], ("submit", "-key", "run-0"))
        self.assertEqual(
            call.call_args.args[1:], ("evidence", "-id", "job", "-attempt", "attempt")
        )

    def test_rejects_hash_and_identity_mismatches(self) -> None:
        good = json.dumps(self.manifest).encode()
        wrong = json.dumps({**self.manifest, "attempt_id": "different"}).encode()
        for recorded, retrieved, message in (
            (good, wrong, "hash mismatch"),
            (wrong, wrong, "identity mismatch"),
        ):
            with self.subTest(message=message):
                job = json.dumps(self.job(recorded)).encode()
                with patch(
                    "forge_proposal.control.ControlEvaluator._call",
                    side_effect=[job, job, retrieved + b"\n"],
                ):
                    with self.assertRaisesRegex(EvaluationError, message):
                        self.client.evaluate(self.candidate, "run-0")

    def test_retains_scheduler_cancellation_without_worker_manifest(self) -> None:
        job = json.dumps(
            {
                **self.job(b""),
                "state": "CANCELLED",
                "outcome": "cancelled",
                "attempts": [],
            }
        ).encode()
        with patch.object(ControlEvaluator, "_call", side_effect=[job, job]):
            result = self.client.evaluate(self.candidate, "run-0")
        self.assertEqual(result.job_record, job)
        self.assertEqual(result.outcome, "cancelled")
        self.assertIsNone(result.manifest_sha256)

    def test_rejects_passing_status_without_manifest(self) -> None:
        job = json.dumps(
            {**self.job(b""), "outcome": "passed", "eligible": True, "attempts": []}
        ).encode()
        with patch.object(ControlEvaluator, "_call", side_effect=[job, job]):
            with self.assertRaisesRegex(EvaluationError, "lacks evidence"):
                self.client.evaluate(self.candidate, "run-0")
