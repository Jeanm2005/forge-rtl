import json
import unittest
from unittest.mock import patch

from forge_proposal.contracts import CANDIDATE_PATH, Candidate
from forge_proposal.model import OllamaProposer
from forge_proposal.workflow import Budget, Evaluation, Outcome, run_loop


def reply(parent: Candidate, source: str, path: str = CANDIDATE_PATH) -> bytes:
    content = {
        "schema_version": 1,
        "path": path,
        "parent_sha256": parent.digest,
        "source": source,
    }
    return json.dumps({"done": True, "response": json.dumps(content)}).encode()


class ModelTest(unittest.TestCase):
    def setUp(self) -> None:
        self.baseline = Candidate(b"baseline")
        self.proposer = OllamaProposer("test")
        self.sources: list[bytes] = []

    def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
        self.sources.append(candidate.source)
        outcome: Outcome = "counterexample" if candidate.source == b"bad" else "passed"
        feedback = f"equivalence: {outcome}"
        return Evaluation(
            candidate.digest,
            submission_key,
            outcome,
            outcome == "passed",
            b"{}",
            feedback,
        )

    def test_model_receives_counterexample_and_revises(self) -> None:
        with patch("forge_proposal.model.HTTPConnection") as http:
            conn = http.return_value
            response = conn.getresponse.return_value
            response.status = 200
            response.read.side_effect = [
                reply(self.baseline, "bad"),
                reply(Candidate(b"bad"), "fixed"),
            ]
            run = run_loop(self.baseline, self.proposer, self, Budget(2, 3), "revision")
            self.assertTrue(run.eligible)
            self.assertEqual(self.sources, [b"baseline", b"bad", b"fixed"])
            method, path, body, _ = conn.request.call_args.args
            self.assertEqual((method, path), ("POST", "/api/generate"))
            prompt = json.loads(json.loads(body)["prompt"])
            self.assertEqual(prompt["parent_sha256"], Candidate(b"bad").digest)
            self.assertEqual(prompt["feedback"], "equivalence: counterexample")

    def test_model_output_passes_through_patch_validation(self) -> None:
        with patch("forge_proposal.model.HTTPConnection") as http:
            response = http.return_value.getresponse.return_value
            response.status = 200
            response.read.side_effect = [
                b'{"done":true,"response":"malformed"}',
                reply(self.baseline, "attack", "rtl/gold/mac_gold.sv"),
            ]
            run = run_loop(self.baseline, self.proposer, self, Budget(2, 3), "rejected")
        self.assertEqual(run.reason, "proposal_budget")
        self.assertEqual(self.sources, [b"baseline"])
        self.assertEqual(run.proposals, 2)
        self.assertIn("invalid proposal JSON", run.trials[1].rejection or "")
        self.assertIn("not allowlisted", run.trials[2].rejection or "")

    def test_transport_failure_retains_prior_evidence(self) -> None:
        with patch("forge_proposal.model.HTTPConnection") as http:
            http.return_value.request.side_effect = TimeoutError
            run = run_loop(self.baseline, self.proposer, self, Budget(2, 3), "timeout")
            http.return_value.close.assert_called_once()
        self.assertEqual(run.reason, "proposal_error")
        self.assertEqual(run.proposals, 1)
        self.assertIsNotNone(run.trials[0].evaluation)
        self.assertEqual(run.trials[1].rejection, "Ollama request failed")
