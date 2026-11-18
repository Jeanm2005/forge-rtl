"""Immutable, versioned file replacements for the MAC proposal loop."""

import json
from dataclasses import dataclass
from hashlib import sha256
from pathlib import Path
from typing import Protocol

CANDIDATE_PATH = "rtl/candidate/mac_candidate.sv"
MAX_SOURCE_BYTES = 1024 * 1024
MAX_PROPOSAL_BYTES = MAX_SOURCE_BYTES * 6 + 4096


class ProposalError(ValueError):
    """The proposal does not satisfy the replacement contract."""


@dataclass(frozen=True)
class Candidate:
    source: bytes

    def __post_init__(self) -> None:
        if not isinstance(self.source, bytes):
            raise ProposalError("source must be immutable bytes")
        if not 0 < len(self.source) <= MAX_SOURCE_BYTES:
            raise ProposalError("source size is outside allowed bounds")
        try:
            text = self.source.decode("utf-8")
        except UnicodeDecodeError as error:
            raise ProposalError("source must be UTF-8") from error
        if "`" in text or "\0" in text:
            raise ProposalError("preprocessing and NUL bytes are forbidden")

    @property
    def digest(self) -> str:
        return sha256(self.source).hexdigest()


@dataclass(frozen=True)
class Replacement:
    parent_sha256: str
    candidate: Candidate


def _unique_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ProposalError("duplicate JSON field")
        result[key] = value
    return result


def parse_replacement(payload: bytes, parent: Candidate) -> Replacement:
    """Parse one complete file replacement, bound to its exact parent."""
    if len(payload) > MAX_PROPOSAL_BYTES:
        raise ProposalError("proposal is too large")
    try:
        data = json.loads(payload, object_pairs_hook=_unique_object)
    except (ValueError, UnicodeDecodeError, RecursionError) as error:
        raise ProposalError("invalid proposal JSON") from error
    fields = {"schema_version", "path", "parent_sha256", "source"}
    if not isinstance(data, dict) or set(data) != fields:
        raise ProposalError("proposal fields do not match schema")
    if type(data["schema_version"]) is not int or data["schema_version"] != 1:
        raise ProposalError("unsupported proposal schema")
    if data["path"] != CANDIDATE_PATH:
        raise ProposalError("candidate path is not allowlisted")
    if data["parent_sha256"] != parent.digest:
        raise ProposalError("proposal parent does not match current candidate")
    source = data["source"]
    if not isinstance(source, str):
        raise ProposalError("source must be text")
    try:
        candidate = Candidate(source.encode("utf-8"))
    except UnicodeEncodeError as error:
        raise ProposalError("source must be UTF-8") from error
    return Replacement(parent.digest, candidate)


def load_baseline(root: Path) -> Candidate:
    """Read a trusted checkout without following candidate-path symlinks."""
    current = root
    for component in CANDIDATE_PATH.split("/"):
        current = current / component
        if current.is_symlink():
            raise ProposalError("baseline path contains a symlink")
    if not current.is_file():
        raise ProposalError("baseline must be a regular file")
    with current.open("rb") as stream:
        return Candidate(stream.read(MAX_SOURCE_BYTES + 1))


class ProposerError(RuntimeError):
    """The proposer failed before producing a replacement."""


class Proposer(Protocol):
    def propose(self, parent: Candidate, feedback: str | None) -> bytes:
        """Return an untrusted versioned replacement document."""
