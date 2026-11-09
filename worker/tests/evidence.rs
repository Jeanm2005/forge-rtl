use forge_worker::evidence::{extract_archive, parse_reports, Manifest, Outcome};
use forge_worker::sandbox::Limits;
use serde_json::json;
use std::fs;
use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!(
            "forge-evidence-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&path).unwrap();
        let f = Self(path);
        for name in [
            "tools",
            "simulation",
            "equivalence",
            "equivalence_audit",
            "reachable",
            "synthesis",
        ] {
            f.write(&format!("worker/{name}.exit"), "0\n");
            f.write(&format!("worker/{name}.duration_ms"), "12\n");
        }
        f.write(
            "versions.txt",
            "Verilator 1\nYosys 1\nEQY 1\nSBY 1\n0.9.1\ng++ 1\nGNU Make 1\nv24\n",
        );
        f.write(
            "sim/result.log",
            "PASS cycles=273187 seed=1badf00d positive_wraps=1 negative_wraps=1\n",
        );
        f.write("formal/PASS", "");
        f.write(
            "formal/matched.ids",
            "mac_formal acc acc\nmac_formal clk clk\n",
        );
        f.write(
            "formal/strategies/mac_formal.acc/induction/status",
            "PASS\n",
        );
        f.write("worker/equivalence_audit.log", "PASS EQY matching audit: public ports only, 1 completed partitions, 0 counterexamples\n");
        f.write(
            "reachable/yosys.log",
            "SAT proof finished - no model found: SUCCESS!\n",
        );
        let mut comparison = json!({"delta": {"total_cells": 0}});
        for variant in ["gold", "candidate"] {
            f.write(
                &format!("synth/{variant}/netlist.v"),
                "module fixture; endmodule\n",
            );
            let top = format!("mac_{variant}");
            f.write(&format!("synth/{variant}/netlist.json"), &json!({"modules": {&top: {"attributes": {"top": "1"}, "cells": {"one": {"type": "$_AND_"}}}}}).to_string());
            f.write(&format!("synth/{variant}/stat.json"), &json!({"modules": {format!("\\{top}"): {"num_cells": 1, "num_cells_by_type": {"$_AND_": 1}}}}).to_string());
            comparison[variant] = json!({"total_cells": 1, "cells_by_type": {"$_AND_": 1}});
        }
        f.write("synth/comparison.json", &comparison.to_string());
        f
    }
    fn write(&self, relative: &str, contents: &str) {
        let path = self.0.join(relative);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, contents).unwrap();
    }
    fn parse(&self) -> Result<Manifest, forge_worker::Error> {
        let mut manifest = blank();
        parse_reports(&self.0, &mut manifest)?;
        Ok(manifest)
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}
fn blank() -> Manifest {
    serde_json::from_value(json!({
        "schema_version": 1, "job_id": "job", "attempt_id": "attempt", "recorded_unix_seconds": 1,
        "outcome": "infrastructure_error", "diagnostic": null, "limits": Limits::default(),
        "configuration_sha256": "", "worker": {"sha256": "", "bytes": 0}, "inputs": {}, "artifacts": {}, "transport": {},
        "toolchain": {}, "versions": null, "simulation_seed": "1badf00d", "proof_strategy": "", "gates": {}, "generic_cells": null, "execution": null
    })).unwrap()
}

#[test]
fn requires_each_gate_and_exact_scoreboard_success() {
    let f = Fixture::new();
    assert_eq!(f.parse().unwrap().outcome, Outcome::Passed);
    f.write("sim/result.log", "PASS\n");
    assert!(f.parse().is_err());
    f.write(
        "sim/result.log",
        "PASS cycles=273187 seed=1badf00d positive_wraps=1 negative_wraps=1\n",
    );
    fs::remove_file(f.0.join("worker/synthesis.exit")).unwrap();
    assert!(f.parse().is_err());
}

#[test]
fn rejects_malformed_metadata_and_synthesis_reports() {
    for (relative, contents) in [
        ("worker/simulation.exit", "-1"),
        ("worker/synthesis.duration_ms", "NaN"),
        ("synth/comparison.json", "{}"),
        ("synth/gold/stat.json", "{broken"),
        ("synth/candidate/netlist.json", "{\"modules\": {}}"),
        ("versions.txt", "Verilator 1"),
    ] {
        let f = Fixture::new();
        f.write(relative, contents);
        assert!(f.parse().is_err(), "accepted {relative}");
    }
}

#[test]
fn rejects_internal_cutpoints_and_incomplete_or_conflicting_proofs() {
    for (relative, contents) in [
        ("formal/FAIL", ""),
        ("formal/matched.ids", "mac_formal raw_acc raw_acc\n"),
        (
            "formal/strategies/mac_formal.acc/induction/status",
            "UNKNOWN\n",
        ),
        (
            "reachable/yosys.log",
            "SAT proof finished - model found: FAIL!\n",
        ),
    ] {
        let f = Fixture::new();
        f.write(relative, contents);
        assert!(f.parse().is_err(), "accepted {relative}");
    }
}

#[test]
fn inconclusive_timeout_and_infrastructure_remain_distinct() {
    let f = Fixture::new();
    f.write("worker/equivalence.exit", "2");
    f.write("worker/equivalence_audit.exit", "1");
    fs::remove_file(f.0.join("formal/PASS")).unwrap();
    f.write("formal/logfile.txt", "Unproven partitions\n");
    assert_eq!(f.parse().unwrap().outcome, Outcome::Inconclusive);
    f.write("worker/equivalence.exit", "124");
    assert_eq!(f.parse().unwrap().outcome, Outcome::Timeout);
    f.write("worker/equivalence.exit", "16");
    f.write("formal/logfile.txt", "ERROR: tool failure\n");
    assert_eq!(f.parse().unwrap().outcome, Outcome::InfrastructureError);
}

#[test]
fn counterexamples_require_completed_partition_and_traces() {
    let f = Fixture::new();
    f.write("worker/equivalence.exit", "2");
    fs::remove_file(f.0.join("formal/PASS")).unwrap();
    f.write("formal/FAIL", "");
    f.write(
        "formal/strategies/mac_formal.acc/induction/status",
        "FAIL\n",
    );
    assert!(f.parse().is_err());
    f.write(
        "formal/strategies/mac_formal.acc/induction/engine_0/trace.vcd",
        "trace",
    );
    assert_eq!(f.parse().unwrap().outcome, Outcome::Counterexample);
}

#[test]
fn rejects_artifact_links_and_traversal() {
    let f = Fixture::new();
    for (index, path, kind) in [
        (0, "../../escape", tar::EntryType::Regular),
        (1, "link", tar::EntryType::Symlink),
        (2, "hard", tar::EntryType::Link),
    ] {
        let archive = f.0.join(format!("bad-{index}.tar"));
        let mut builder = tar::Builder::new(fs::File::create(&archive).unwrap());
        let mut header = tar::Header::new_ustar();
        header.set_entry_type(kind);
        header.set_size(0);
        header.set_mode(0o600);
        if index == 0 {
            header.as_mut_bytes()[..path.len()].copy_from_slice(path.as_bytes());
        } else {
            header.set_path(path).unwrap();
            header.set_link_name("/etc/passwd").unwrap();
        }
        header.set_cksum();
        builder.append(&header, std::io::empty()).unwrap();
        builder.finish().unwrap();
        assert!(extract_archive(
            &archive,
            &f.0.join(format!("out-{index}")),
            &Limits::default()
        )
        .is_err());
    }
    assert!(!f.0.parent().unwrap().join("escape").exists());
}
