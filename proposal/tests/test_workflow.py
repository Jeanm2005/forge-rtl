import unittest

from forge_proposal.contracts import Candidate
from forge_proposal.fake import FakeProposer, Step
from forge_proposal.workflow import (
    Budget,
    Evaluation,
    EvaluationError,
    Outcome,
    run_loop,
)

BASELINE = Candidate(b"module mac_candidate; endmodule\n")
BAD = "module mac_candidate; wire bad; endmodule\n"
GOOD = "module mac_candidate; wire good; endmodule\n"


class FakeEvaluator:
    def __init__(self) -> None:
        self.calls: list[tuple[Candidate, str]] = []

    def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
        self.calls.append((candidate, submission_key))
        outcome: Outcome = (
            "counterexample" if candidate.source == BAD.encode() else "passed"
        )
        feedback = (
            "signed overflow at cycle 3"
            if outcome == "counterexample"
            else "baseline passed"
        )
        return Evaluation(
            candidate.digest,
            submission_key,
            outcome,
            outcome == "passed",
            b'{"fixture":true,"job":"' + submission_key.encode() + b'"}',
            feedback,
        )


class WorkflowTest(unittest.TestCase):
    def test_reproducible_run_and_evidence_bytes(self) -> None:
        def execute() -> object:
            return run_loop(
                BASELINE,
                FakeProposer((Step(BAD, "passed"), Step(GOOD, "overflow"))),
                FakeEvaluator(),
                Budget(2, 3),
                "repeat",
            )

        self.assertEqual(execute(), execute())

    def test_evaluation_limit_stops_before_unused_proposal(self) -> None:
        proposer = FakeProposer((Step(BAD, "passed"), Step(GOOD, "overflow")))
        result = run_loop(BASELINE, proposer, FakeEvaluator(), Budget(2, 2), "limit")
        self.assertEqual(result.reason, "evaluation_budget")
        self.assertEqual((result.proposals, result.evaluations), (1, 2))
        self.assertEqual(proposer.calls, 1)
        self.assertFalse(result.eligible)

    def test_counterexample_informs_revision_and_preserves_lineage(self) -> None:
        proposer = FakeProposer(
            (
                Step(BAD, "baseline passed"),
                Step(GOOD, "signed overflow at cycle 3"),
            )
        )
        evaluator = FakeEvaluator()
        result = run_loop(BASELINE, proposer, evaluator, Budget(2, 3), "revision")
        self.assertTrue(result.eligible)
        self.assertEqual((result.proposals, result.evaluations), (2, 3))
        self.assertEqual(len(result.trials), 3)
        self.assertEqual(result.trials[2].parent_sha256, Candidate(BAD.encode()).digest)
        self.assertEqual(
            [key for _, key in evaluator.calls],
            ["revision-0", "revision-1", "revision-2"],
        )
        for trial in result.trials:
            self.assertIsNotNone(trial.evaluation)
            assert trial.evaluation is not None
            assert trial.evaluation.manifest_sha256 is not None
            self.assertEqual(len(trial.evaluation.manifest_sha256), 64)
        self.assertEqual(result.baseline, BASELINE)

    def test_valid_candidate(self) -> None:
        result = run_loop(
            BASELINE,
            FakeProposer((Step(GOOD, "passed"),)),
            FakeEvaluator(),
            Budget(1, 2),
            "valid",
        )
        self.assertEqual(result.reason, "eligible")

    def test_disallowed_patch_consumes_proposal_budget_without_evaluation(self) -> None:
        evaluator = FakeEvaluator()
        proposer = FakeProposer((Step(GOOD, "passed", "rtl/gold/mac_gold.sv"),))
        result = run_loop(BASELINE, proposer, evaluator, Budget(1, 3), "reject")
        self.assertEqual(result.reason, "proposal_budget")
        self.assertEqual(len(evaluator.calls), 1)
        self.assertIsNone(result.trials[-1].candidate)
        self.assertIsNotNone(result.trials[-1].proposal)
        self.assertFalse(result.eligible)

    def test_rejection_feedback_revises_from_unchanged_parent(self) -> None:
        proposer = FakeProposer(
            (
                Step(GOOD, "passed", "Makefile"),
                Step(GOOD, "not allowlisted"),
            )
        )
        result = run_loop(
            BASELINE, proposer, FakeEvaluator(), Budget(2, 2), "reject-revise"
        )
        self.assertTrue(result.eligible)
        self.assertEqual(result.trials[-1].parent_sha256, BASELINE.digest)

    def test_evaluation_budget_includes_baseline(self) -> None:
        proposer = FakeProposer(())
        result = run_loop(BASELINE, proposer, FakeEvaluator(), Budget(2, 1), "baseline")
        self.assertEqual(result.reason, "evaluation_budget")
        self.assertEqual(proposer.calls, 0)
        self.assertFalse(result.eligible)

    def test_zero_proposal_budget(self) -> None:
        result = run_loop(
            BASELINE, FakeProposer(()), FakeEvaluator(), Budget(0, 2), "zero"
        )
        self.assertEqual(result.reason, "proposal_budget")
        self.assertEqual(result.proposals, 0)

    def test_maximum_budget_terminates_without_recursion_error(self) -> None:
        proposer = FakeProposer(
            tuple(
                Step(BAD, "passed" if index == 0 else "overflow")
                for index in range(100)
            )
        )
        result = run_loop(
            BASELINE, proposer, FakeEvaluator(), Budget(100, 101), "maximum"
        )
        self.assertEqual(result.reason, "evaluation_budget")
        self.assertEqual(result.proposals, 100)

    def test_adapter_failure_preserves_history_and_stops(self) -> None:
        class Broken(FakeEvaluator):
            def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
                if submission_key.endswith("-1"):
                    raise EvaluationError("connection lost")
                return super().evaluate(candidate, submission_key)

        result = run_loop(
            BASELINE,
            FakeProposer((Step(GOOD, "passed"),)),
            Broken(),
            Budget(2, 3),
            "error",
        )
        self.assertEqual(result.reason, "evaluation_error")
        self.assertEqual(len(result.trials), 2)
        self.assertEqual(result.trials[-1].rejection, "connection lost")
        self.assertFalse(result.eligible)

    def test_hash_mismatch_fails_closed(self) -> None:
        class Wrong(FakeEvaluator):
            def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
                return Evaluation("0" * 64, "job", "passed", True, b"{}", "passed")

        result = run_loop(BASELINE, FakeProposer(()), Wrong(), Budget(1, 2), "wrong")
        self.assertEqual(result.reason, "evaluation_error")
        self.assertFalse(result.eligible)

    def test_invalid_budgets(self) -> None:
        for proposals, evaluations in ((-1, 1), (101, 1), (1, 0), (1, 102), (True, 1)):
            with (
                self.subTest(proposals=proposals, evaluations=evaluations),
                self.assertRaises(ValueError),
            ):
                Budget(proposals, evaluations)

    def test_nonpassing_outcomes_remain_distinct_and_ineligible(self) -> None:
        outcomes: tuple[Outcome, ...] = (
            "counterexample",
            "inconclusive",
            "timeout",
            "cancelled",
            "infrastructure_error",
        )
        for outcome in outcomes:
            with self.subTest(outcome=outcome):
                evaluation = Evaluation(
                    BASELINE.digest, "job", outcome, False, b"{}", outcome
                )
                self.assertEqual(evaluation.outcome, outcome)
                with self.assertRaises(ValueError):
                    Evaluation(BASELINE.digest, "job", outcome, True, b"{}", outcome)

    def test_script_requires_actual_feedback(self) -> None:
        with self.assertRaises(RuntimeError):
            FakeProposer((Step(GOOD, "counterexample"),)).propose(BASELINE, "passed")
