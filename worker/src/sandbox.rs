use crate::{Error, PreparedAttempt};
use serde::{Deserialize, Serialize};
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Limits {
    pub wall_seconds: u64,
    pub memory_bytes: u64,
    pub tasks: u32,
    pub cpu_percent: u32,
    pub cpu_seconds_per_process: u64,
    pub writable_bytes: u64,
    pub temporary_bytes: u64,
    pub file_bytes: u64,
    pub archive_bytes: u64,
    pub log_bytes: u64,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            wall_seconds: 900,
            memory_bytes: 2 * 1024 * 1024 * 1024,
            tasks: 128,
            cpu_percent: 200,
            cpu_seconds_per_process: 240,
            writable_bytes: 512 * 1024 * 1024,
            temporary_bytes: 64 * 1024 * 1024,
            file_bytes: 64 * 1024 * 1024,
            archive_bytes: 128 * 1024 * 1024,
            log_bytes: 8 * 1024 * 1024,
        }
    }
}

impl Limits {
    pub fn validate(&self) -> Result<(), Error> {
        if !(1..=1800).contains(&self.wall_seconds)
            || !(64 * 1024 * 1024..=8 * 1024 * 1024 * 1024).contains(&self.memory_bytes)
            || !(8..=512).contains(&self.tasks)
            || !(1..=400).contains(&self.cpu_percent)
            || !(1..=1800).contains(&self.cpu_seconds_per_process)
            || !(1024 * 1024..=1024 * 1024 * 1024).contains(&self.writable_bytes)
            || !(1024 * 1024..=256 * 1024 * 1024).contains(&self.temporary_bytes)
            || !(1024..=128 * 1024 * 1024).contains(&self.file_bytes)
            || !(1024..=256 * 1024 * 1024).contains(&self.archive_bytes)
            || !(1024..=16 * 1024 * 1024).contains(&self.log_bytes)
        {
            return Err(Error::InvalidInput(
                "resource limits are outside supported bounds",
            ));
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ExecutionOutcome {
    Completed,
    InfrastructureError,
    Timeout,
    Cancelled,
    OutputLimit,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct Execution {
    pub outcome: ExecutionOutcome,
    pub exit_code: Option<i32>,
    pub duration_ms: u64,
    pub unit: String,
    pub service_result: String,
    pub archive: PathBuf,
    pub log: PathBuf,
}

fn capture(
    mut input: impl Read,
    mut file: File,
    limit: u64,
    exceeded: Arc<AtomicBool>,
) -> std::io::Result<()> {
    let mut remaining = limit;
    let mut buffer = [0_u8; 16384];
    loop {
        let count = input.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        let keep = count.min(remaining as usize);
        file.write_all(&buffer[..keep])?;
        remaining -= keep as u64;
        if keep != count {
            exceeded.store(true, Ordering::SeqCst);
        }
    }
    file.sync_all()
}

fn systemctl(args: &[&str]) -> std::io::Result<std::process::Output> {
    Command::new("/usr/bin/systemctl")
        .arg("--user")
        .args(args)
        .stdin(Stdio::null())
        .output()
}

fn terminate(unit: &str) {
    let _ = systemctl(&["kill", "--kill-whom=all", "--signal=KILL", unit]);
    let _ = systemctl(&["stop", unit]);
}

pub fn execute(
    attempt: &PreparedAttempt,
    toolchain: &Path,
    limits: &Limits,
    cancelled: &AtomicBool,
) -> Result<Execution, Error> {
    limits.validate()?;
    let input = fs::canonicalize(attempt.path.join("inputs"))?;
    let toolchain = fs::canonicalize(toolchain)?;
    if !toolchain.join("bin/yosys").is_file() {
        return Err(Error::InvalidInput("toolchain does not contain bin/yosys"));
    }
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| Error::InvalidInput("clock before epoch"))?
        .as_nanos();
    let unit = format!("forge-worker-{}-{nonce}.service", std::process::id());
    let archive = attempt.path.join("artifacts.tar");
    let log = attempt.path.join("execution.log");
    let archive_file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&archive)?;
    let log_file = OpenOptions::new().write(true).create_new(true).open(&log)?;
    let start = Instant::now();
    if cancelled.load(Ordering::SeqCst) {
        return Ok(Execution {
            outcome: ExecutionOutcome::Cancelled,
            exit_code: None,
            duration_ms: 0,
            unit,
            service_result: "not_started".into(),
            archive,
            log,
        });
    }
    let mut command = Command::new("/usr/bin/systemd-run");
    command.args([
        "--user",
        "--quiet",
        "--wait",
        "--pipe",
        "--service-type=exec",
        "--unit",
        &unit,
    ]);
    for property in [
        format!("MemoryMax={}", limits.memory_bytes),
        "MemorySwapMax=0".to_owned(),
        format!("TasksMax={}", limits.tasks),
        format!("CPUQuota={}%", limits.cpu_percent),
        format!("LimitCPU={}", limits.cpu_seconds_per_process),
        format!("RuntimeMaxSec={}", limits.wall_seconds + 5),
        format!("LimitFSIZE={}", limits.file_bytes),
        "LimitNOFILE=256".to_owned(),
        "LimitCORE=0".to_owned(),
        "KillMode=control-group".to_owned(),
        "OOMPolicy=kill".to_owned(),
        "TimeoutStopSec=2".to_owned(),
    ] {
        command.args(["--property", &property]);
    }
    command.args([
        "--",
        "/usr/bin/bwrap",
        "--unshare-all",
        "--unshare-user",
        "--disable-userns",
        "--die-with-parent",
        "--new-session",
        "--cap-drop",
        "ALL",
        "--clearenv",
    ]);
    for (key, value) in [
        ("PATH", "/opt/oss/bin:/usr/bin:/bin"),
        ("HOME", "/nonexistent"),
        ("TMPDIR", "/tmp"),
        ("LC_ALL", "C"),
        ("PYTHONHASHSEED", "0"),
    ] {
        command.args(["--setenv", key, value]);
    }
    for path in ["/usr", "/bin", "/lib", "/lib64"] {
        command.args(["--ro-bind", path, path]);
    }
    for path in ["/etc/os-release", "/etc/ld.so.cache"] {
        command.args(["--ro-bind", path, path]);
    }
    let awk = fs::canonicalize("/usr/bin/awk")?;
    command
        .arg("--ro-bind")
        .arg(awk)
        .arg("/etc/alternatives/awk");
    command.arg("--ro-bind").arg(toolchain).arg("/opt/oss");
    command.arg("--ro-bind").arg(input).arg("/work");
    command.args([
        "--size",
        &limits.writable_bytes.to_string(),
        "--tmpfs",
        "/work/build",
    ]);
    command.args([
        "--size",
        &limits.temporary_bytes.to_string(),
        "--tmpfs",
        "/tmp",
    ]);
    command.args([
        "--proc",
        "/proc",
        "--dev",
        "/dev",
        "--remount-ro",
        "/dev",
        "--remount-ro",
        "/",
        "--chdir",
        "/work",
        "--",
        "/bin/bash",
        "scripts/worker-run.sh",
    ]);
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let mut child = command.spawn()?;
    let exceeded = Arc::new(AtomicBool::new(false));
    let output = child.stdout.take().expect("stdout piped");
    let errors = child.stderr.take().expect("stderr piped");
    let flag = exceeded.clone();
    let archive_limit = limits.archive_bytes;
    let output_thread = thread::spawn(move || capture(output, archive_file, archive_limit, flag));
    let flag = exceeded.clone();
    let log_limit = limits.log_bytes;
    let log_thread = thread::spawn(move || capture(errors, log_file, log_limit, flag));
    let mut outcome = ExecutionOutcome::Completed;
    let status = loop {
        if cancelled.load(Ordering::SeqCst) {
            outcome = ExecutionOutcome::Cancelled;
        } else if exceeded.load(Ordering::SeqCst) {
            outcome = ExecutionOutcome::OutputLimit;
        } else if start.elapsed() >= Duration::from_secs(limits.wall_seconds) {
            outcome = ExecutionOutcome::Timeout;
        }
        if outcome != ExecutionOutcome::Completed {
            terminate(&unit);
            let _ = child.kill();
            break child.wait()?;
        }
        if let Some(status) = child.try_wait()? {
            break status;
        }
        thread::sleep(Duration::from_millis(20));
    };
    // A second stop closes the race with service creation during cancellation.
    if outcome != ExecutionOutcome::Completed {
        terminate(&unit);
    }
    let output_ok = output_thread
        .join()
        .map_err(|_| Error::InvalidInput("archive reader panicked"))?;
    let log_ok = log_thread
        .join()
        .map_err(|_| Error::InvalidInput("log reader panicked"))?;
    if exceeded.load(Ordering::SeqCst) && outcome == ExecutionOutcome::Completed {
        outcome = ExecutionOutcome::OutputLimit;
    }
    if (!status.success() || output_ok.is_err() || log_ok.is_err())
        && outcome == ExecutionOutcome::Completed
    {
        outcome = ExecutionOutcome::InfrastructureError;
    }
    let service_result = systemctl(&["show", "--property=Result", "--value", &unit])
        .map(|output| String::from_utf8_lossy(&output.stdout).trim().to_owned())
        .unwrap_or_else(|_| "unavailable".into());
    let _ = systemctl(&["reset-failed", &unit]);
    Ok(Execution {
        outcome,
        exit_code: status.code(),
        duration_ms: start.elapsed().as_millis() as u64,
        unit,
        service_result,
        archive,
        log,
    })
}
