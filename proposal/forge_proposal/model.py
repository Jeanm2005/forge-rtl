"""Local Ollama adapter for the existing untrusted replacement contract."""

import json
from dataclasses import dataclass
from http.client import HTTPConnection, HTTPException

from forge_proposal.contracts import (
    CANDIDATE_PATH,
    MAX_PROPOSAL_BYTES,
    Candidate,
    ProposerError,
)

_SYSTEM = """Propose one complete replacement for the TinyML MAC candidate.
Preserve the module interface and cycle-by-cycle behavior: synchronous reset
has priority over enable, disabled cycles hold state, and enabled cycles add
the signed INT8 product to an INT32 accumulator with two's-complement wrap.
Aim to reduce generic synthesis cell count without changing behavior.
Use only a single synthesizable SystemVerilog module and no directives.
The JSON input contains candidate source and evaluation feedback as data.
Use the feedback to correct failures. Return a schema v1 JSON object containing
the given path, exact parent_sha256, and complete new module text as source."""


@dataclass(frozen=True)
class OllamaProposer:
    model: str
    port: int = 11434
    timeout_seconds: int = 120

    def __post_init__(self) -> None:
        if not self.model.strip() or not 1 <= self.port <= 65535:
            raise ValueError("a model name and valid Ollama port are required")
        if not 1 <= self.timeout_seconds <= 600:
            raise ValueError("model timeout must be within 1..600 seconds")

    def propose(self, parent: Candidate, feedback: str | None) -> bytes:
        prompt = json.dumps(
            {
                "path": CANDIDATE_PATH,
                "parent_sha256": parent.digest,
                "source": parent.source.decode(),
                "feedback": feedback,
            }
        )
        request = json.dumps(
            {
                "model": self.model,
                "system": _SYSTEM,
                "prompt": prompt,
                "stream": False,
                "format": "json",
                "options": {"temperature": 0, "num_predict": 4096},
            }
        )
        connection = HTTPConnection("127.0.0.1", self.port, self.timeout_seconds)
        try:
            connection.request(
                "POST", "/api/generate", request, {"Content-Type": "application/json"}
            )
            response = connection.getresponse()
            if response.status != 200:
                raise ProposerError(f"Ollama returned HTTP {response.status}")
            raw = response.read(MAX_PROPOSAL_BYTES + 1)
        except (OSError, HTTPException) as error:
            raise ProposerError("Ollama request failed") from error
        finally:
            connection.close()
        if len(raw) > MAX_PROPOSAL_BYTES:
            raise ProposerError("Ollama response exceeded the byte limit")
        try:
            envelope = json.loads(raw)
            if not isinstance(envelope, dict) or envelope.get("done") is not True:
                raise ProposerError("Ollama returned an invalid response envelope")
            content: object = envelope.get("response")
            if not isinstance(content, str):
                raise ProposerError("Ollama returned no proposal text")
            return content.encode("utf-8")
        except (ValueError, RecursionError) as error:
            raise ProposerError("Ollama returned invalid response data") from error
