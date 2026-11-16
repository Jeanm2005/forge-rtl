import json
import tempfile
import unittest
from pathlib import Path

from forge_proposal.contracts import (
    CANDIDATE_PATH,
    MAX_SOURCE_BYTES,
    Candidate,
    ProposalError,
    load_baseline,
    parse_replacement,
)


class ContractsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.parent = Candidate(b"module mac_candidate; endmodule\n")
        self.document: dict[str, object] = {
            "schema_version": 1,
            "path": CANDIDATE_PATH,
            "parent_sha256": self.parent.digest,
            "source": "module mac_candidate; wire a; endmodule\n",
        }

    def parse(self) -> Candidate:
        return parse_replacement(
            json.dumps(self.document).encode(), self.parent
        ).candidate

    def test_replacement_preserves_parent_and_exact_bytes(self) -> None:
        candidate = self.parse()
        self.assertEqual(candidate.source.decode(), self.document["source"])
        self.assertNotEqual(candidate.digest, self.parent.digest)
        self.assertEqual(self.parent.source, b"module mac_candidate; endmodule\n")

    def test_protected_and_noncanonical_paths(self) -> None:
        for path in (
            "rtl/gold/mac_gold.sv",
            "formal/mac.sby",
            "Makefile",
            "../" + CANDIDATE_PATH,
            "/" + CANDIDATE_PATH,
            "rtl/candidate/../candidate/mac_candidate.sv",
            "rtl\\candidate\\mac_candidate.sv",
        ):
            with self.subTest(path=path), self.assertRaises(ProposalError):
                self.document["path"] = path
                self.parse()

    def test_invalid_schema_parent_and_source(self) -> None:
        for key, value in (
            ("schema_version", True),
            ("schema_version", 2),
            ("parent_sha256", "0" * 64),
            ("source", 1),
            ("source", ""),
            ("source", '`include "/etc/passwd"'),
            ("source", "\0"),
            ("source", "\ud800"),
        ):
            with self.subTest(key=key, value=value), self.assertRaises(ProposalError):
                document = self.document.copy()
                document[key] = value
                parse_replacement(json.dumps(document).encode(), self.parent)

    def test_malformed_and_ambiguous_json(self) -> None:
        for payload in (b"{", b"[]", b"\xff", b'{"path":1,"path":2}'):
            with self.subTest(payload=payload), self.assertRaises(ProposalError):
                parse_replacement(payload, self.parent)
        self.document["command"] = "make"
        with self.assertRaises(ProposalError):
            self.parse()

    def test_source_bound(self) -> None:
        with self.assertRaises(ProposalError):
            Candidate(b"x" * (MAX_SOURCE_BYTES + 1))

    def test_baseline_symlinks_and_unchanged_checkout(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / CANDIDATE_PATH
            path.parent.mkdir(parents=True)
            path.write_bytes(self.parent.source)
            self.assertEqual(load_baseline(root), self.parent)
            self.parse()
            self.assertEqual(path.read_bytes(), self.parent.source)
            path.unlink()
            path.symlink_to(root / "outside.sv")
            with self.assertRaises(ProposalError):
                load_baseline(root)
            path.unlink()
            path.parent.rmdir()
            path.parent.symlink_to(root)
            with self.assertRaises(ProposalError):
                load_baseline(root)


if __name__ == "__main__":
    unittest.main()
