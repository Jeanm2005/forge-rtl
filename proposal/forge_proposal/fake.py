"""Deterministic scripted proposer for workflow tests without a model."""

import json
from dataclasses import dataclass

from forge_proposal.contracts import CANDIDATE_PATH, Candidate


@dataclass(frozen=True)
class Step:
    source: str
    expected_feedback: str
    path: str = CANDIDATE_PATH


class FakeProposer:
    def __init__(self, steps: tuple[Step, ...]) -> None:
        self.steps = steps
        self.calls = 0

    def propose(self, parent: Candidate, feedback: str | None) -> bytes:
        if self.calls >= len(self.steps):
            raise RuntimeError("fake proposer script exhausted")
        step = self.steps[self.calls]
        if feedback is None or step.expected_feedback not in feedback:
            raise RuntimeError("fake proposer did not receive expected feedback")
        self.calls += 1
        return json.dumps(
            {
                "schema_version": 1,
                "path": step.path,
                "parent_sha256": parent.digest,
                "source": step.source,
            },
            sort_keys=True,
        ).encode()
