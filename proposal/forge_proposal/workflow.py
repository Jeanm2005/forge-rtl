"""Bounded proposal graph with an injected control-plane evaluation boundary."""

from dataclasses import dataclass
from hashlib import sha256
from typing import Literal, Protocol, TypedDict

from langgraph.graph import END, START, StateGraph

from forge_proposal.contracts import (
    Candidate,
    ProposalError,
    Proposer,
    ProposerError,
    parse_replacement,
)

Outcome = Literal[
    "passed",
    "verification_failed",
    "output_limit",
    "counterexample",
    "inconclusive",
    "timeout",
    "cancelled",
    "infrastructure_error",
]
StopReason = Literal[
    "eligible",
    "proposal_budget",
    "evaluation_budget",
    "evaluation_error",
    "proposal_error",
]


@dataclass(frozen=True)
class Evaluation:
    candidate_sha256: str
    job_id: str
    outcome: Outcome
    eligible: bool
    manifest: bytes | None
    feedback: str
    job_record: bytes | None = None

    def __post_init__(self) -> None:
        outcomes = {
            "passed",
            "verification_failed",
            "output_limit",
            "counterexample",
            "inconclusive",
            "timeout",
            "cancelled",
            "infrastructure_error",
        }
        if self.outcome not in outcomes or not self.job_id:
            raise ValueError("evaluation requires a known outcome and job identity")
        for record in (self.manifest, self.job_record):
            if record is not None and (not isinstance(record, bytes) or not record):
                raise ValueError("evaluation records must be nonempty immutable bytes")
        if self.manifest is None and (
            self.job_record is None
            or self.outcome not in {"timeout", "cancelled", "infrastructure_error"}
        ):
            raise ValueError("evaluation requires immutable recorded evidence")
        if type(self.eligible) is not bool or (
            self.eligible and self.outcome != "passed"
        ):
            raise ValueError("only an explicitly passing evaluation can be eligible")
        if len(self.feedback) > 8192:
            raise ValueError("evaluation feedback exceeds limit")

    @property
    def manifest_sha256(self) -> str | None:
        return sha256(self.manifest).hexdigest() if self.manifest is not None else None


class EvaluationError(RuntimeError):
    """The adapter could not obtain a recorded control-plane result."""


class Evaluator(Protocol):
    def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
        """Submit idempotently and retrieve recorded evidence; never run EDA here."""


@dataclass(frozen=True)
class Budget:
    proposals: int
    evaluations: int

    def __post_init__(self) -> None:
        if (
            type(self.proposals) is not int
            or type(self.evaluations) is not int
            or not 0 <= self.proposals <= 100
            or not 1 <= self.evaluations <= 101
        ):
            raise ValueError("budgets require 0..100 proposals and 1..101 evaluations")


@dataclass(frozen=True)
class Trial:
    parent_sha256: str
    candidate: Candidate | None
    proposal: bytes | None
    rejection: str | None
    evaluation: Evaluation | None


@dataclass(frozen=True)
class Run:
    baseline: Candidate
    trials: tuple[Trial, ...]
    proposals: int
    evaluations: int
    reason: StopReason

    @property
    def eligible(self) -> bool:
        last = self.trials[-1]
        return (
            self.reason == "eligible"
            and last.evaluation is not None
            and last.evaluation.eligible
        )


class _State(TypedDict):
    parent: Candidate
    payload: bytes | None
    candidate: Candidate | None
    feedback: str | None
    trials: tuple[Trial, ...]
    proposals: int
    evaluations: int
    reason: StopReason | None


def run_loop(
    baseline: Candidate,
    proposer: Proposer,
    evaluator: Evaluator,
    budget: Budget,
    run_id: str,
) -> Run:
    """Run once; run_id must be stable for retries and unique for a new run."""
    if not 1 <= len(run_id) <= 64 or any(
        character
        not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
        for character in run_id
    ):
        raise ValueError("run_id must be 1..64 ASCII letters, digits, - or _")

    def evaluate(state: _State) -> dict[str, object]:
        candidate = state["candidate"]
        assert candidate is not None
        sequence = state["evaluations"]
        try:
            evidence = evaluator.evaluate(candidate, f"{run_id}-{sequence}")
            if evidence.candidate_sha256 != candidate.digest:
                raise EvaluationError("evidence candidate hash mismatch")
        except EvaluationError as error:
            trial = Trial(
                state["parent"].digest, candidate, state["payload"], str(error), None
            )
            return {
                "trials": (*state["trials"], trial),
                "evaluations": sequence + 1,
                "reason": "evaluation_error",
            }
        trial = Trial(
            state["parent"].digest, candidate, state["payload"], None, evidence
        )
        return {
            "trials": (*state["trials"], trial),
            "evaluations": sequence + 1,
            "parent": candidate,
            "feedback": evidence.feedback,
            "reason": "eligible" if sequence > 0 and evidence.eligible else None,
        }

    def propose(state: _State) -> dict[str, object]:
        proposals = state["proposals"] + 1
        try:
            payload = proposer.propose(state["parent"], state["feedback"])
        except ProposerError as error:
            trial = Trial(state["parent"].digest, None, None, str(error), None)
            return {
                "trials": (*state["trials"], trial),
                "proposals": proposals,
                "reason": "proposal_error",
            }
        return {"payload": payload, "proposals": proposals}

    def validate(state: _State) -> dict[str, object]:
        payload = state["payload"]
        assert payload is not None
        try:
            candidate = parse_replacement(payload, state["parent"]).candidate
        except ProposalError as error:
            return {
                "candidate": None,
                "feedback": str(error),
                "trials": (
                    *state["trials"],
                    Trial(state["parent"].digest, None, payload, str(error), None),
                ),
            }
        return {"candidate": candidate}

    def decide(state: _State) -> dict[str, object]:
        if state["reason"] is not None:
            return {}
        if state["evaluations"] >= budget.evaluations:
            return {"reason": "evaluation_budget"}
        if state["proposals"] >= budget.proposals:
            return {"reason": "proposal_budget"}
        return {}

    def route(state: _State) -> str:
        return END if state["reason"] is not None else "propose"

    graph = StateGraph(_State)
    graph.add_node("evaluate", evaluate)
    graph.add_node("propose", propose)
    graph.add_node("validate", validate)
    graph.add_node("decide", decide)
    graph.add_edge(START, "evaluate")
    graph.add_edge("evaluate", "decide")
    graph.add_conditional_edges("decide", route, {END: END, "propose": "propose"})
    graph.add_conditional_edges(
        "propose",
        lambda state: END if state["reason"] is not None else "validate",
        {END: END, "validate": "validate"},
    )
    graph.add_conditional_edges(
        "validate",
        lambda state: "evaluate" if state["candidate"] is not None else "decide",
        {"evaluate": "evaluate", "decide": "decide"},
    )
    initial: _State = {
        "parent": baseline,
        "candidate": baseline,
        "payload": None,
        "feedback": None,
        "trials": (),
        "proposals": 0,
        "evaluations": 0,
        "reason": None,
    }
    final = graph.compile().invoke(
        initial, {"recursion_limit": 4 * budget.proposals + 5}
    )
    reason: StopReason = final["reason"]
    return Run(
        baseline, final["trials"], final["proposals"], final["evaluations"], reason
    )
