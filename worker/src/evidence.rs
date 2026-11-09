use crate::sandbox::{execute, Execution, ExecutionOutcome, Limits};
use crate::{Error, PreparedAttempt};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Component, Path};
use std::sync::atomic::AtomicBool;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Passed,
    VerificationFailed,
    Counterexample,
    Inconclusive,
    Timeout,
    Cancelled,
    InfrastructureError,
    OutputLimit,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct Gate {
    pub outcome: Outcome,
    pub exit_code: u32,
    pub duration_ms: u64,
    pub bounded_depth: Option<u32>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct FileEvidence {
    pub sha256: String,
    pub bytes: u64,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct Manifest {
    pub schema_version: u32,
    pub job_id: String,
    pub attempt_id: String,
    pub recorded_unix_seconds: u64,
    pub outcome: Outcome,
    pub diagnostic: Option<String>,
    pub limits: Limits,
    pub configuration_sha256: String,
    pub worker: FileEvidence,
    pub inputs: BTreeMap<String, FileEvidence>,
    pub artifacts: BTreeMap<String, FileEvidence>,
    pub transport: BTreeMap<String, FileEvidence>,
    pub toolchain: BTreeMap<String, String>,
    pub versions: Option<String>,
    pub simulation_seed: String,
    pub proof_strategy: String,
    pub gates: BTreeMap<String, Gate>,
    pub generic_cells: Option<BTreeMap<String, u64>>,
    pub execution: Option<Execution>,
}

pub fn hash_file(path: &Path) -> Result<FileEvidence, Error> {
    let mut file = File::open(path)?;
    let mut hasher = Sha256::new();
    let mut buffer = [0_u8; 65536];
    let mut bytes = 0;
    loop {
        let count = file.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        bytes += count as u64;
        hasher.update(&buffer[..count]);
    }
    Ok(FileEvidence {
        sha256: format!("{:x}", hasher.finalize()),
        bytes,
    })
}

fn inventory(root: &Path) -> Result<BTreeMap<String, FileEvidence>, Error> {
    fn visit(
        root: &Path,
        directory: &Path,
        files: &mut BTreeMap<String, FileEvidence>,
    ) -> Result<(), Error> {
        for entry in fs::read_dir(directory)? {
            let entry = entry?;
            let kind = entry.file_type()?;
            if kind.is_dir() {
                visit(root, &entry.path(), files)?;
            } else if kind.is_file() {
                files.insert(
                    entry
                        .path()
                        .strip_prefix(root)
                        .expect("inventory under root")
                        .to_string_lossy()
                        .into_owned(),
                    hash_file(&entry.path())?,
                );
            } else {
                return Err(Error::InvalidInput("nonregular evidence file"));
            }
        }
        Ok(())
    }
    let mut files = BTreeMap::new();
    visit(root, root, &mut files)?;
    Ok(files)
}

pub fn extract_archive(archive: &Path, destination: &Path, limits: &Limits) -> Result<(), Error> {
    if fs::metadata(archive)?.len() > limits.archive_bytes {
        return Err(Error::InvalidInput("archive exceeds limit"));
    }
    fs::create_dir(destination)?;
    let mut tar = tar::Archive::new(File::open(archive)?);
    let mut seen = BTreeSet::new();
    let mut total = 0_u64;
    for (count, entry) in tar.entries()?.enumerate() {
        if count >= 4096 {
            return Err(Error::InvalidInput("too many artifact entries"));
        }
        let mut entry = entry?;
        let relative = entry.path()?.into_owned();
        if relative.as_os_str().len() > 512 || relative.components().count() > 32 {
            return Err(Error::InvalidInput("artifact path too long"));
        }
        let mut normalized = std::path::PathBuf::new();
        for part in relative.components() {
            match part {
                Component::CurDir => (),
                Component::Normal(name) => normalized.push(name),
                _ => return Err(Error::InvalidInput("unsafe artifact path")),
            }
        }
        if !seen.insert(normalized.clone()) {
            return Err(Error::InvalidInput("duplicate artifact path"));
        }
        let kind = entry.header().entry_type();
        let target = destination.join(&normalized);
        if kind.is_dir() {
            fs::create_dir_all(target)?;
        } else if kind.is_file() && !normalized.as_os_str().is_empty() {
            let size = entry.size();
            total = total
                .checked_add(size)
                .ok_or(Error::InvalidInput("artifact size overflow"))?;
            if size > limits.file_bytes || total > limits.archive_bytes {
                return Err(Error::InvalidInput("artifact size exceeds limit"));
            }
            fs::create_dir_all(target.parent().expect("file under destination"))?;
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&target)?;
            let written = std::io::copy(&mut entry, &mut file)?;
            if written != size {
                return Err(Error::InvalidInput("truncated artifact"));
            }
            file.sync_all()?;
        } else {
            return Err(Error::InvalidInput(
                "artifact links and special files forbidden",
            ));
        }
    }
    Ok(())
}

fn text(root: &Path, relative: &str) -> Result<String, Error> {
    let file = File::open(root.join(relative))?;
    if file.metadata()?.len() > 8 * 1024 * 1024 {
        return Err(Error::InvalidInput("report exceeds parser limit"));
    }
    let mut value = String::new();
    file.take(8 * 1024 * 1024 + 1).read_to_string(&mut value)?;
    Ok(value)
}

fn gate(root: &Path, name: &str) -> Result<Gate, Error> {
    let exit_code = text(root, &format!("worker/{name}.exit"))?
        .trim()
        .parse::<u32>()
        .map_err(|_| Error::InvalidInput("malformed stage exit"))?;
    let duration_ms = text(root, &format!("worker/{name}.duration_ms"))?
        .trim()
        .parse::<u64>()
        .map_err(|_| Error::InvalidInput("malformed stage duration"))?;
    if exit_code > 255 || duration_ms > 1_800_000 {
        return Err(Error::InvalidInput("stage metadata out of range"));
    }
    Ok(Gate {
        outcome: match exit_code {
            0 => Outcome::Passed,
            124 => Outcome::Timeout,
            _ => Outcome::InfrastructureError,
        },
        exit_code,
        duration_ms,
        bounded_depth: None,
    })
}

fn nonempty(root: &Path, relative: &str) -> bool {
    fs::metadata(root.join(relative))
        .map(|m| m.is_file() && m.len() > 0)
        .unwrap_or(false)
}

fn contains_trace(root: &Path) -> Result<bool, Error> {
    Ok(inventory(root)?
        .iter()
        .any(|(p, evidence)| p.ends_with("trace.vcd") && evidence.bytes > 0))
}

fn validate_eqy(root: &Path, failed: bool) -> Result<(), Error> {
    let matches = text(root, "formal/matched.ids")?;
    let allowed = ["clk", "rst", "en", "a", "b", "acc"];
    let mut acc = false;
    for line in matches
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty() && !line.starts_with('#'))
    {
        let fields: Vec<_> = line.split_whitespace().collect();
        if fields.len() != 3
            || fields[0] != "mac_formal"
            || fields[1] != fields[2]
            || !allowed.contains(&fields[1])
        {
            return Err(Error::InvalidInput("untrusted EQY internal cut point"));
        }
        acc |= fields[1] == "acc";
    }
    if !acc {
        return Err(Error::InvalidInput("EQY output match missing"));
    }
    let mut partitions = 0;
    let mut failures = 0;
    for partition in fs::read_dir(root.join("formal/strategies"))? {
        let partition = partition?;
        if !partition.file_type()?.is_dir() {
            return Err(Error::InvalidInput("unexpected EQY strategy entry"));
        }
        partitions += 1;
        match text(&partition.path(), "induction/status")?.trim() {
            "PASS" => (),
            "FAIL" if failed && contains_trace(&partition.path())? => failures += 1,
            _ => {
                return Err(Error::InvalidInput(
                    "EQY partition lacks completed proof or counterexample",
                ))
            }
        }
    }
    if partitions == 0 || failed && failures == 0 {
        return Err(Error::InvalidInput("EQY completed partitions missing"));
    }
    Ok(())
}

pub fn parse_reports(root: &Path, manifest: &mut Manifest) -> Result<(), Error> {
    let tools = gate(root, "tools")?;
    let tools_passed = tools.outcome == Outcome::Passed;
    manifest.gates.insert("tools".into(), tools);
    if !tools_passed {
        return Err(Error::InvalidInput(
            "tool prerequisites or version commands failed",
        ));
    }
    let versions = text(root, "versions.txt")?;
    for required in ["Verilator ", "Yosys ", "EQY ", "SBY ", "g++ ", "GNU Make "] {
        if !versions.contains(required) {
            return Err(Error::InvalidInput("missing tool version evidence"));
        }
    }
    if versions.lines().count() != 8 || versions.lines().any(|line| line.trim().is_empty()) {
        return Err(Error::InvalidInput("incomplete tool version evidence"));
    }
    manifest.versions = Some(versions);
    let mut simulation = gate(root, "simulation")?;
    if simulation.exit_code == 0 {
        let result = text(root, "sim/result.log")?;
        let pass = "PASS cycles=273187 seed=1badf00d positive_wraps=1 negative_wraps=1";
        if result.lines().filter(|line| *line == pass).count() != 1 || result.contains("%Error") {
            return Err(Error::InvalidInput(
                "simulation success lacks exact scoreboard evidence",
            ));
        }
    } else if simulation.outcome != Outcome::Timeout {
        let result = text(root, "sim/result.log").unwrap_or_default();
        if result.contains("Assertion failed in tb_mac.tick: cycle=")
            && result.contains("expected=")
        {
            simulation.outcome = Outcome::VerificationFailed;
        }
    }
    manifest.gates.insert("simulation".into(), simulation);
    let mut equivalence = gate(root, "equivalence")?;
    let mut audit = gate(root, "equivalence_audit")?;
    let pass = root.join("formal/PASS").is_file();
    let fail = root.join("formal/FAIL").is_file();
    if pass && fail {
        return Err(Error::InvalidInput("contradictory EQY terminal markers"));
    }
    let audit_log = text(root, "worker/equivalence_audit.log")?;
    if audit.outcome == Outcome::Passed
        && audit_log.starts_with("PASS EQY matching audit: public ports only, ")
    {
        match (equivalence.exit_code, pass, fail) {
            (0, true, false) => {
                validate_eqy(root, false)?;
                equivalence.outcome = Outcome::Passed;
            }
            (2, false, true) if contains_trace(&root.join("formal"))? => {
                validate_eqy(root, true)?;
                equivalence.outcome = Outcome::Counterexample
            }
            _ => return Err(Error::InvalidInput("EQY exit/marker/trace disagreement")),
        }
    } else if equivalence.outcome != Outcome::Timeout {
        let log = text(root, "formal/logfile.txt").unwrap_or_default();
        if log.contains("Unproven") || log.contains("UNKNOWN") {
            equivalence.outcome = Outcome::Inconclusive;
        } else {
            equivalence.outcome = Outcome::InfrastructureError;
        }
    }
    if matches!(
        equivalence.outcome,
        Outcome::Timeout | Outcome::Inconclusive
    ) {
        audit.outcome = equivalence.outcome;
    }
    manifest.gates.insert("equivalence".into(), equivalence);
    manifest.gates.insert("equivalence_audit".into(), audit);
    let mut reachable = gate(root, "reachable")?;
    reachable.bounded_depth = Some(3);
    if reachable.exit_code == 0 {
        let log = text(root, "reachable/yosys.log")?;
        let counterexample = log.contains("SAT proof finished - model found: FAIL!");
        let bounded_pass = log.contains("SAT proof finished - no model found: SUCCESS!");
        if counterexample == bounded_pass {
            return Err(Error::InvalidInput(
                "missing or contradictory bounded SAT result",
            ));
        }
        if counterexample {
            if !nonempty(root, "reachable/counterexample.vcd")
                || !nonempty(root, "reachable/counterexample.json")
            {
                return Err(Error::InvalidInput("bounded counterexample traces missing"));
            }
            reachable.outcome = Outcome::Counterexample;
        }
    }
    manifest.gates.insert("reachable_depth_3".into(), reachable);
    let synthesis = gate(root, "synthesis")?;
    if synthesis.outcome == Outcome::Passed {
        manifest.generic_cells = Some(parse_synthesis(root)?);
    }
    manifest.gates.insert("synthesis".into(), synthesis);
    let outcomes: Vec<_> = manifest.gates.values().map(|g| g.outcome).collect();
    manifest.outcome = if outcomes.contains(&Outcome::InfrastructureError) {
        Outcome::InfrastructureError
    } else if outcomes.contains(&Outcome::Timeout) {
        Outcome::Timeout
    } else if outcomes.contains(&Outcome::Counterexample) {
        Outcome::Counterexample
    } else if outcomes.contains(&Outcome::VerificationFailed) {
        Outcome::VerificationFailed
    } else if outcomes.contains(&Outcome::Inconclusive) {
        Outcome::Inconclusive
    } else {
        Outcome::Passed
    };
    Ok(())
}

fn json(root: &Path, path: &str) -> Result<Value, Error> {
    serde_json::from_str(&text(root, path)?)
        .map_err(|_| Error::InvalidInput("malformed synthesis JSON"))
}

fn parse_synthesis(root: &Path) -> Result<BTreeMap<String, u64>, Error> {
    let comparison = json(root, "synth/comparison.json")?;
    let mut counts = BTreeMap::new();
    for variant in ["gold", "candidate"] {
        let top = format!("mac_{variant}");
        let netlist = json(root, &format!("synth/{variant}/netlist.json"))?;
        let modules = netlist["modules"]
            .as_object()
            .ok_or(Error::InvalidInput("missing synthesized modules"))?;
        if modules.len() != 1
            || !modules.contains_key(&top)
            || !nonempty(root, &format!("synth/{variant}/netlist.v"))
        {
            return Err(Error::InvalidInput(
                "unexpected synthesis top or missing netlist",
            ));
        }
        let cells = modules[&top]["cells"]
            .as_object()
            .ok_or(Error::InvalidInput("missing netlist cells"))?;
        let mut types = BTreeMap::<String, u64>::new();
        for cell in cells.values() {
            let kind = cell["type"]
                .as_str()
                .ok_or(Error::InvalidInput("missing cell type"))?;
            *types.entry(kind.to_owned()).or_default() += 1;
        }
        let stats = json(root, &format!("synth/{variant}/stat.json"))?;
        let stats = &stats["modules"][format!("\\{top}")];
        if stats["num_cells"].as_u64() != Some(cells.len() as u64)
            || comparison[variant]["total_cells"].as_u64() != Some(cells.len() as u64)
            || serde_json::to_value(&types)
                .map_err(|_| Error::InvalidInput("cell serialization failed"))?
                != stats["num_cells_by_type"]
            || comparison[variant]["cells_by_type"] != stats["num_cells_by_type"]
        {
            return Err(Error::InvalidInput(
                "synthesis statistics/netlist disagreement",
            ));
        }
        counts.insert(variant.into(), cells.len() as u64);
    }
    if comparison["delta"]["total_cells"].as_i64()
        != Some(counts["candidate"] as i64 - counts["gold"] as i64)
    {
        return Err(Error::InvalidInput("synthesis delta disagreement"));
    }
    Ok(counts)
}

pub fn evaluate(
    attempt: &PreparedAttempt,
    toolchain: &Path,
    limits: &Limits,
    cancelled: &AtomicBool,
) -> Result<Manifest, Error> {
    limits.validate()?;
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(attempt.path.join("EVALUATING"))?
        .sync_all()?;
    let configuration = serde_json::to_vec(limits)
        .map_err(|_| Error::InvalidInput("limit serialization failed"))?;
    let mut manifest = Manifest {
        schema_version: 1, job_id: attempt.job.0.clone(), attempt_id: attempt.attempt.0.clone(),
        recorded_unix_seconds: SystemTime::now().duration_since(UNIX_EPOCH).map_err(|_| Error::InvalidInput("clock before epoch"))?.as_secs(),
        outcome: Outcome::InfrastructureError, diagnostic: None, limits: limits.clone(),
        configuration_sha256: format!("{:x}", Sha256::digest(configuration)),
        worker: hash_file(&std::env::current_exe()?)?, inputs: inventory(&attempt.path.join("inputs"))?,
        artifacts: BTreeMap::new(), transport: BTreeMap::new(), toolchain: BTreeMap::new(), versions: None,
        simulation_seed: "1badf00d".into(), proof_strategy: "EQY SBY smtbmc bitwuzla induction depth 10; public matches only; first-transition reset".into(),
        gates: BTreeMap::new(), generic_cells: None, execution: None,
    };
    for file in ["VERSION", ".forge-toolchain-sha256"] {
        if let Ok(value) = text(toolchain, file) {
            manifest.toolchain.insert(file.into(), value.trim().into());
        }
    }
    match execute(attempt, toolchain, limits, cancelled) {
        Ok(execution) => {
            manifest.outcome = match execution.outcome {
                ExecutionOutcome::Completed => Outcome::InfrastructureError,
                ExecutionOutcome::InfrastructureError => Outcome::InfrastructureError,
                ExecutionOutcome::Timeout => Outcome::Timeout,
                ExecutionOutcome::Cancelled => Outcome::Cancelled,
                ExecutionOutcome::OutputLimit => Outcome::OutputLimit,
            };
            for file in ["artifacts.tar", "execution.log"] {
                manifest
                    .transport
                    .insert(file.into(), hash_file(&attempt.path.join(file))?);
            }
            let pending = attempt.path.join("artifacts.pending");
            match extract_archive(&execution.archive, &pending, limits) {
                Ok(()) => {
                    crate::sync_directories(&pending)?;
                    let published = attempt.path.join("artifacts");
                    fs::rename(&pending, &published)?;
                    File::open(&attempt.path)?.sync_all()?;
                    manifest.artifacts = inventory(&published)?;
                    if execution.outcome == ExecutionOutcome::Completed {
                        if let Err(error) = parse_reports(&published, &mut manifest) {
                            manifest.outcome = Outcome::InfrastructureError;
                            manifest.diagnostic = Some(error.to_string());
                        }
                    }
                }
                Err(error) => {
                    if execution.outcome == ExecutionOutcome::Completed {
                        manifest.outcome = Outcome::InfrastructureError;
                    }
                    manifest.diagnostic = Some(format!("artifact validation: {error}"));
                }
            }
            manifest.execution = Some(execution);
        }
        Err(error) => manifest.diagnostic = Some(error.to_string()),
    }
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(attempt.path.join("manifest.pending.json"))?;
    let bytes = serde_json::to_vec_pretty(&manifest)
        .map_err(|_| Error::InvalidInput("manifest serialization failed"))?;
    file.write_all(&bytes)?;
    file.sync_all()?;
    fs::rename(
        attempt.path.join("manifest.pending.json"),
        attempt.path.join("manifest.json"),
    )?;
    File::open(&attempt.path)?.sync_all()?;
    Ok(manifest)
}
