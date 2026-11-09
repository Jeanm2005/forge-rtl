use forge_worker::evidence::{hash_file, Manifest, Outcome};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::{SystemTime, UNIX_EPOCH};

#[test]
#[ignore = "requires pinned EDA suite, user systemd, cgroups, and Bubblewrap"]
fn good_and_mutant_reproduce_phase_one() {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .to_owned();
    let run = root.join("build/worker-integration").join(
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
            .to_string(),
    );
    fs::create_dir_all(&run).unwrap();
    let toolchain = std::env::var_os("FORGE_TOOLCHAIN")
        .map(PathBuf::from)
        .unwrap_or_else(|| root.join(".tools/oss-cad-suite"));
    let mutant = fs::read_to_string(root.join("tests/mac_mutant.sv"))
        .unwrap()
        .replace("module mac_mutant", "module mac_candidate");
    fs::write(run.join("mutant.sv"), mutant).unwrap();
    let mut manifests = Vec::new();
    for (name, source, expected) in [
        (
            "good",
            root.join("rtl/candidate/mac_candidate.sv"),
            Outcome::Passed,
        ),
        ("mutant", run.join("mutant.sv"), Outcome::Counterexample),
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_forge-worker"))
            .arg("evaluate")
            .arg(&root)
            .arg(&run)
            .args(["mac-baseline", name])
            .arg(source)
            .arg(&toolchain)
            .output()
            .unwrap();
        let path = String::from_utf8(output.stdout).unwrap();
        eprintln!("{name}: {}", path.trim());
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let path = Path::new(path.trim());
        let manifest: Manifest = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        assert_eq!(manifest.outcome, expected, "{manifest:#?}");
        assert_eq!(manifest.schema_version, 1);
        assert_eq!(manifest.generic_cells.as_ref().unwrap()["gold"], 988);
        assert_eq!(
            manifest.inputs["rtl/candidate/mac_candidate.sv"].sha256,
            hash_file(
                &path
                    .parent()
                    .unwrap()
                    .join("inputs/rtl/candidate/mac_candidate.sv")
            )
            .unwrap()
            .sha256
        );
        for (relative, evidence) in &manifest.artifacts {
            assert_eq!(
                evidence.sha256,
                hash_file(&path.parent().unwrap().join("artifacts").join(relative))
                    .unwrap()
                    .sha256
            );
        }
        if name == "good" {
            assert_eq!(manifest.gates["simulation"].outcome, Outcome::Passed);
            assert_eq!(manifest.gates["equivalence"].outcome, Outcome::Passed);
            assert_eq!(manifest.generic_cells.as_ref().unwrap()["candidate"], 988);
        } else {
            assert_eq!(
                manifest.gates["simulation"].outcome,
                Outcome::VerificationFailed
            );
            assert_eq!(
                manifest.gates["equivalence"].outcome,
                Outcome::Counterexample
            );
            assert_eq!(
                manifest.gates["reachable_depth_3"].outcome,
                Outcome::Counterexample
            );
            assert!(manifest
                .artifacts
                .contains_key("reachable/counterexample.vcd"));
            assert!(manifest
                .artifacts
                .contains_key("reachable/counterexample.json"));
        }
        manifests.push(manifest);
    }
    assert_eq!(
        manifests[0].configuration_sha256,
        manifests[1].configuration_sha256
    );
    for (path, hash) in &manifests[0].inputs {
        if path != "rtl/candidate/mac_candidate.sv" {
            assert_eq!(hash.sha256, manifests[1].inputs[path].sha256);
        }
    }
}
