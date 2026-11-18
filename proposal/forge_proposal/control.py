"""Evaluate through the existing Go gRPC client and validate recorded evidence."""

import json
import math
import subprocess
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from tempfile import TemporaryDirectory
from time import monotonic, sleep
from typing import TypeGuard

from forge_proposal.contracts import CANDIDATE_PATH, Candidate
from forge_proposal.workflow import Evaluation, EvaluationError, Outcome

_ACTIVE = {"QUEUED", "RUNNING"}
_TERMINAL = {"COMPLETED", "AWAITING_REVIEW", "CANCELLED", "TIMED_OUT"}


def _is_outcome(value: object) -> TypeGuard[Outcome]:
    return isinstance(value, str) and value in {
        "passed",
        "verification_failed",
        "counterexample",
        "inconclusive",
        "timeout",
        "cancelled",
        "infrastructure_error",
        "output_limit",
    }


def _object(value: object) -> dict[str, object]:
    if not isinstance(value, dict):
        raise EvaluationError("expected a control-plane JSON object")
    return dict(value)


def _decode(raw: bytes) -> dict[str, object]:
    try:
        return _object(json.loads(raw))
    except (ValueError, RecursionError) as error:
        raise EvaluationError("invalid control-plane JSON") from error


@dataclass(frozen=True)
class ControlEvaluator:
    client: Path
    socket: Path
    timeout_seconds: int = 1800
    max_attempts: int = 2
    poll_seconds: float = 0.5

    def __post_init__(self) -> None:
        if not 1 <= self.timeout_seconds <= 3600 or not 1 <= self.max_attempts <= 3:
            raise ValueError("invalid control-plane job budget")
        if not math.isfinite(self.poll_seconds) or self.poll_seconds <= 0:
            raise ValueError("poll interval must be positive and finite")

    def _call(self, deadline: float, operation: str, *flags: str) -> bytes:
        remaining = deadline - monotonic()
        if remaining <= 0:
            raise EvaluationError("control-plane evaluation deadline exceeded")
        try:
            result = subprocess.run(
                [
                    str(self.client.resolve()),
                    "-socket",
                    str(self.socket.resolve()),
                    *flags,
                    operation,
                ],
                capture_output=True,
                timeout=min(remaining, 30),
                check=True,
            )
        except subprocess.CalledProcessError as error:
            detail = error.stderr[:512].decode(errors="replace").strip()
            raise EvaluationError(
                f"control-plane {operation} failed: {detail}"
            ) from error
        except (OSError, subprocess.TimeoutExpired) as error:
            raise EvaluationError(f"control-plane {operation} unavailable") from error
        return result.stdout

    def evaluate(self, candidate: Candidate, submission_key: str) -> Evaluation:
        deadline = monotonic() + self.timeout_seconds + 30
        with TemporaryDirectory(prefix="forge-candidate-") as directory:
            source = Path(directory) / "candidate.sv"
            source.write_bytes(candidate.source)
            raw = self._call(
                deadline,
                "submit",
                "-key",
                submission_key,
                "-candidate",
                str(source),
                "-timeout",
                str(self.timeout_seconds),
                "-max-attempts",
                str(self.max_attempts),
            )
        job = _decode(raw)
        job_id = job.get("job_id")
        if not isinstance(job_id, str) or not job_id:
            raise EvaluationError("missing control-plane job identity")
        while True:
            if (
                job.get("job_id") != job_id
                or job.get("candidate_sha256") != candidate.digest
            ):
                raise EvaluationError("control-plane job identity mismatch")
            state = job.get("state")
            if isinstance(state, str) and state in _TERMINAL:
                break
            if not isinstance(state, str) or state not in _ACTIVE:
                raise EvaluationError("unknown control-plane job state")
            sleep(min(self.poll_seconds, max(0, deadline - monotonic())))
            raw = self._call(deadline, "get", "-id", job_id)
            job = _decode(raw)

        # Submit may replay a terminal job. Get revalidates current eligibility.
        raw = self._call(deadline, "get", "-id", job_id)
        job = _decode(raw)
        return self._result(deadline, job_id, candidate, job, raw)

    def _result(
        self,
        deadline: float,
        job_id: str,
        candidate: Candidate,
        job: dict[str, object],
        raw: bytes,
    ) -> Evaluation:
        state = job.get("state")
        outcome = job.get("outcome")
        eligible = job.get("eligible")
        if (
            job.get("job_id") != job_id
            or job.get("candidate_sha256") != candidate.digest
            or not isinstance(state, str)
            or state not in _TERMINAL
            or not _is_outcome(outcome)
            or type(eligible) is not bool
            or (eligible and outcome != "passed")
            or (state == "CANCELLED" and outcome != "cancelled")
            or (state == "TIMED_OUT" and outcome != "timeout")
        ):
            raise EvaluationError("invalid terminal control-plane result")
        attempts = job.get("attempts")
        if not isinstance(attempts, list):
            raise EvaluationError("missing control-plane attempt inventory")
        attempt = _object(attempts[-1]) if attempts else {}
        digest = attempt.get("manifest_sha256", "")
        manifest = None
        feedback = f"Outcome: {outcome}"
        if digest:
            manifest, detail = self._manifest(
                deadline, job_id, candidate, attempt, outcome, digest
            )
            if detail:
                feedback += f"\n{detail}"
        elif outcome not in {"cancelled", "timeout", "infrastructure_error"}:
            raise EvaluationError("terminal verification outcome lacks evidence")
        return Evaluation(
            candidate.digest, job_id, outcome, eligible, manifest, feedback, raw
        )

    def _manifest(
        self,
        deadline: float,
        job_id: str,
        candidate: Candidate,
        attempt: dict[str, object],
        outcome: Outcome,
        digest: object,
    ) -> tuple[bytes, str]:
        attempt_id = attempt.get("attempt_id")
        if (
            not isinstance(attempt_id, str)
            or not attempt_id
            or attempt.get("state") != "FINISHED"
            or attempt.get("outcome") != outcome
        ):
            raise EvaluationError("manifest does not identify a finished attempt")
        # forge-client appends one display newline after verifying the RPC hash.
        output = self._call(deadline, "evidence", "-id", job_id, "-attempt", attempt_id)
        manifest = output.removesuffix(b"\n")
        if len(manifest) > 8 * 1024 * 1024 or sha256(manifest).hexdigest() != digest:
            raise EvaluationError("control-plane manifest hash mismatch")
        data = _decode(manifest)
        inputs = _object(data.get("inputs"))
        source = _object(inputs.get(CANDIDATE_PATH))
        if (
            type(data.get("schema_version")) is not int
            or data.get("schema_version") != 1
            or data.get("job_id") != job_id
            or data.get("attempt_id") != attempt_id
            or data.get("outcome") != outcome
            or source.get("sha256") != candidate.digest
            or type(source.get("bytes")) is not int
            or source.get("bytes") != len(candidate.source)
        ):
            raise EvaluationError("control-plane manifest identity mismatch")
        gates = _object(data.get("gates"))
        details = [
            f"{name}: {status}"
            for name, gate in sorted(gates.items())
            if isinstance(status := _object(gate).get("outcome"), str)
            and status
            in {
                "passed",
                "verification_failed",
                "counterexample",
                "inconclusive",
                "timeout",
                "cancelled",
                "infrastructure_error",
                "output_limit",
                "failed",
                "skipped",
            }
        ]
        cells = (
            _object(data["generic_cells"])
            if data.get("generic_cells") is not None
            else {}
        )
        gold, count = cells.get("gold"), cells.get("candidate")
        if type(gold) is int and type(count) is int:
            details.append(f"Generic cells: gold {gold}, candidate {count}")
        return manifest, "\n".join(details)[:8000]
