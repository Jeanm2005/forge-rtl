"""Run the deterministic MAC revision example against the local control plane."""

import argparse
import json
import os
from pathlib import Path
from tempfile import TemporaryDirectory
from zipfile import ZIP_DEFLATED, ZipFile

from forge_proposal.contracts import Candidate, Proposer, load_baseline
from forge_proposal.control import ControlEvaluator
from forge_proposal.fake import FakeProposer, Step
from forge_proposal.model import OllamaProposer
from forge_proposal.workflow import Budget, Run, run_loop

BUDGET = Budget(proposals=2, evaluations=3)


def export_run(run: Run, run_id: str, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    with TemporaryDirectory(prefix=".forge-run-", dir=destination.parent) as directory:
        staged = Path(directory) / "run.zip"
        with ZipFile(staged, "w", compression=ZIP_DEFLATED) as archive:
            archive.writestr("baseline.sv", run.baseline.source)
            trials = []
            for index, trial in enumerate(run.trials):
                prefix = f"trials/{index}"
                candidate = trial.candidate
                evaluation = trial.evaluation
                if candidate is not None:
                    archive.writestr(f"{prefix}/candidate.sv", candidate.source)
                if trial.proposal is not None:
                    archive.writestr(f"{prefix}/proposal.json", trial.proposal)
                if evaluation is not None:
                    if evaluation.job_record is not None:
                        archive.writestr(f"{prefix}/job.json", evaluation.job_record)
                    if evaluation.manifest is not None:
                        archive.writestr(f"{prefix}/manifest.json", evaluation.manifest)
                trials.append(
                    {
                        "directory": prefix,
                        "parent_sha256": trial.parent_sha256,
                        "candidate_sha256": candidate.digest if candidate else None,
                        "diagnostic": trial.rejection,
                        "evaluation": {
                            "job_id": evaluation.job_id,
                            "outcome": evaluation.outcome,
                            "eligible": evaluation.eligible,
                            "manifest_sha256": evaluation.manifest_sha256,
                            "feedback": evaluation.feedback,
                        }
                        if evaluation
                        else None,
                    }
                )
            summary = {
                "schema_version": 1,
                "run_id": run_id,
                "baseline_sha256": run.baseline.digest,
                "proposals": run.proposals,
                "evaluations": run.evaluations,
                "reason": run.reason,
                "eligible": run.eligible,
                "trials": trials,
            }
            archive.writestr("run.json", json.dumps(summary, indent=2) + "\n")
        with staged.open("rb") as file:
            os.fsync(file.fileno())
        os.link(staged, destination)
        directory_fd = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--client", type=Path, default=Path("build/forge-client"))
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--model", help="local Ollama model name")
    parser.add_argument("--ollama-port", type=int, default=11434)
    args = parser.parse_args()
    if args.output.exists() or args.output.is_symlink():
        parser.error("output already exists")

    root: Path = args.root
    baseline = load_baseline(root)
    proposer: Proposer
    if args.model:
        proposer = OllamaProposer(args.model, port=args.ollama_port)
    else:
        mutant = (root / "tests/mac_mutant.sv").read_text(encoding="utf-8")
        mutant = mutant.replace("module mac_mutant", "module mac_candidate", 1)
        fixed = mutant.replace("16'b0, product", "{16{product[15]}}, product", 1)
        if fixed == mutant or Candidate(fixed.encode()).digest == baseline.digest:
            parser.error(
                "expected a distinct sign-extension revision of the MAC mutant"
            )
        proposer = FakeProposer(
            (Step(mutant, "Outcome: passed"), Step(fixed, "Outcome: counterexample"))
        )
    run = run_loop(
        baseline,
        proposer,
        ControlEvaluator(args.client, args.socket, timeout_seconds=180, max_attempts=1),
        BUDGET,
        args.run_id,
    )
    export_run(run, args.run_id, args.output)
    outcomes = [
        trial.evaluation.outcome if trial.evaluation else None for trial in run.trials
    ]
    print(f"{run.reason}: {outcomes}; archive {args.output}")
    if args.model:
        return 0 if run.eligible else 1
    return (
        0 if run.eligible and outcomes == ["passed", "counterexample", "passed"] else 1
    )


if __name__ == "__main__":
    raise SystemExit(main())
