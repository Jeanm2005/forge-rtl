use forge_worker::sandbox::{execute, ExecutionOutcome, Limits};
use forge_worker::{prepare, Candidate, Identity, PreparedAttempt, CANDIDATE_PATH};
use std::fs;
use std::path::PathBuf;
use std::sync::atomic::AtomicBool;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

struct Fixture(PathBuf, PreparedAttempt);
impl Fixture {
    fn new(script: &str) -> Self {
        let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .parent()
            .unwrap()
            .to_owned();
        let path = std::env::temp_dir().join(format!(
            "forge-sandbox-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&path).unwrap();
        let candidate =
            Candidate::replacement(CANDIDATE_PATH, fs::read(root.join(CANDIDATE_PATH)).unwrap())
                .unwrap();
        let id = Identity::parse("fixture").unwrap();
        let attempt = prepare(&root, &path, &id, &id, &candidate).unwrap();
        let runner = attempt.path.join("inputs/scripts/worker-run.sh");
        fs::remove_file(&runner).unwrap();
        fs::write(runner, script).unwrap();
        Self(path, attempt)
    }
    fn toolchain(&self) -> PathBuf {
        std::env::var_os("FORGE_TOOLCHAIN")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../.tools/oss-cad-suite")
            })
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}

#[test]
fn invalid_limits_fail_before_launch() {
    assert!(Limits {
        tasks: 0,
        ..Limits::default()
    }
    .validate()
    .is_err());
    assert!(Limits {
        wall_seconds: 0,
        ..Limits::default()
    }
    .validate()
    .is_err());
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn sandbox_enforces_mount_environment_network_and_storage_boundary() {
    let f = Fixture::new(
        r#"set -eu
python3 - <<'PY'
import os, socket
assert not os.path.exists('/home/jeanm')
assert not os.path.exists('/run/user')
assert os.environ['HOME'] == '/nonexistent'
assert set(os.listdir('/sys')) == set() if os.path.exists('/sys') else True
assert set(socket.if_nameindex()) == {(1, 'lo')}
import resource
assert resource.getrlimit(resource.RLIMIT_CPU) == (240, 240)
assert resource.getrlimit(resource.RLIMIT_FSIZE) == (67108864, 67108864)
try:
    open('rtl/gold/mac_gold.sv', 'w')
    raise AssertionError('writable reference')
except OSError:
    pass
for path in ['/unexpected', '/dev/unexpected']:
    try:
        open(path, 'w')
        raise AssertionError('writable sandbox root or device directory')
    except OSError:
        pass
try:
    open('/tmp/full', 'wb').write(b'x' * (2 * 1024 * 1024))
    raise AssertionError('unbounded tmpfs')
except OSError:
    pass
PY
printf PASS > build/status
tar --format=ustar -cf - -C build .
"#,
    );
    let limits = Limits {
        temporary_bytes: 1024 * 1024,
        ..Limits::default()
    };
    let result = execute(&f.1, &f.toolchain(), &limits, &AtomicBool::new(false)).unwrap();
    assert_eq!(
        result.outcome,
        ExecutionOutcome::Completed,
        "{}",
        fs::read_to_string(result.log).unwrap()
    );
    assert!(fs::metadata(result.archive).unwrap().len() > 0);
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn timeout_kills_service_process_tree() {
    let f = Fixture::new("sleep 100 &\nwait\n");
    let limits = Limits {
        wall_seconds: 2,
        ..Limits::default()
    };
    let result = execute(&f.1, &f.toolchain(), &limits, &AtomicBool::new(false)).unwrap();
    assert_eq!(result.outcome, ExecutionOutcome::Timeout);
    assert!(result.duration_ms < 10000);
    assert_service_empty(&result.unit);
}

fn assert_service_empty(unit: &str) {
    let output = std::process::Command::new("/usr/bin/systemctl")
        .args(["--user", "show", "--property=MainPID", "--value", unit])
        .output()
        .unwrap();
    assert!(
        String::from_utf8_lossy(&output.stdout).trim().is_empty()
            || String::from_utf8_lossy(&output.stdout).trim() == "0"
    );
    let output = std::process::Command::new("/usr/bin/systemctl")
        .args(["--user", "show", "--property=ControlGroup", "--value", unit])
        .output()
        .unwrap();
    let group = String::from_utf8_lossy(&output.stdout);
    if !group.trim().is_empty() {
        let processes = PathBuf::from("/sys/fs/cgroup")
            .join(group.trim().trim_start_matches('/'))
            .join("cgroup.procs");
        if let Ok(contents) = fs::read_to_string(processes) {
            assert!(contents.trim().is_empty());
        }
    }
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn cancellation_kills_service_process_tree() {
    let f = Fixture::new("sleep 100 &\nwait\n");
    let cancelled = AtomicBool::new(false);
    let result = std::thread::scope(|scope| {
        scope.spawn(|| {
            std::thread::sleep(Duration::from_secs(1));
            cancelled.store(true, std::sync::atomic::Ordering::SeqCst);
        });
        execute(&f.1, &f.toolchain(), &Limits::default(), &cancelled).unwrap()
    });
    assert_eq!(result.outcome, ExecutionOutcome::Cancelled);
    assert_service_empty(&result.unit);
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn output_limit_and_tool_failure_are_distinct() {
    let f = Fixture::new("exec yes\n");
    let limits = Limits {
        archive_bytes: 1024,
        ..Limits::default()
    };
    let result = execute(&f.1, &f.toolchain(), &limits, &AtomicBool::new(false)).unwrap();
    assert_eq!(result.outcome, ExecutionOutcome::OutputLimit);
    assert_eq!(fs::metadata(result.archive).unwrap().len(), 1024);
    assert_service_empty(&result.unit);
    let f = Fixture::new("exit 19\n");
    let result = execute(
        &f.1,
        &f.toolchain(),
        &Limits::default(),
        &AtomicBool::new(false),
    )
    .unwrap();
    assert_eq!(result.outcome, ExecutionOutcome::InfrastructureError);
    assert_eq!(result.exit_code, Some(19));
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn aggregate_memory_limit_kills_attempt() {
    let f = Fixture::new("exec python3 -c 'x = bytearray(512 * 1024 * 1024)'\n");
    let limits = Limits {
        memory_bytes: 64 * 1024 * 1024,
        ..Limits::default()
    };
    let result = execute(&f.1, &f.toolchain(), &limits, &AtomicBool::new(false)).unwrap();
    assert_eq!(result.outcome, ExecutionOutcome::InfrastructureError);
    assert_eq!(result.service_result, "oom-kill");
    assert_service_empty(&result.unit);
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn aggregate_task_limit_is_enforced() {
    let f = Fixture::new(
        r#"exec python3 - <<'PY'
import subprocess
children = []
try:
    for _ in range(100):
        children.append(subprocess.Popen(['/bin/sleep', '100']))
except OSError:
    assert 0 < len(children) < 32
else:
    raise AssertionError('task limit not enforced')
finally:
    for child in children:
        child.kill()
    for child in children:
        child.wait()
PY
"#,
    );
    let limits = Limits {
        tasks: 32,
        ..Limits::default()
    };
    let result = execute(&f.1, &f.toolchain(), &limits, &AtomicBool::new(false)).unwrap();
    assert_eq!(
        result.outcome,
        ExecutionOutcome::Completed,
        "{}",
        fs::read_to_string(result.log).unwrap()
    );
    assert_service_empty(&result.unit);
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn terminal_manifests_preserve_failures_and_cancellation_without_overwrite() {
    use forge_worker::evidence::{evaluate, Outcome};
    for (script, cancelled, expected) in [
        ("exit 19\n", false, Outcome::InfrastructureError),
        ("printf garbage\n", false, Outcome::InfrastructureError),
        ("sleep 100\n", true, Outcome::Cancelled),
    ] {
        let f = Fixture::new(script);
        let manifest = evaluate(
            &f.1,
            &f.toolchain(),
            &Limits::default(),
            &AtomicBool::new(cancelled),
        )
        .unwrap();
        assert_eq!(manifest.outcome, expected);
        let path = f.1.path.join("manifest.json");
        let original = fs::read(&path).unwrap();
        assert!(!f.1.path.join("manifest.pending.json").exists());
        if !manifest.artifacts.is_empty() {
            assert!(f.1.path.join("artifacts").is_dir());
        }
        assert!(evaluate(
            &f.1,
            &f.toolchain(),
            &Limits::default(),
            &AtomicBool::new(false)
        )
        .is_err());
        assert_eq!(fs::read(path).unwrap(), original);
    }
}

#[test]
#[ignore = "requires host user systemd, cgroups, and Bubblewrap"]
fn cli_sigterm_records_cancellation() {
    use forge_worker::evidence::{Manifest, Outcome};
    let f = Fixture::new("sleep 100 &\nwait\n");
    let mut child = std::process::Command::new(env!("CARGO_BIN_EXE_forge-worker"))
        .arg("evaluate")
        .arg(f.1.path.join("inputs"))
        .arg(&f.0)
        .args(["signal-job", "signal-attempt"])
        .arg(f.1.path.join("inputs").join(CANDIDATE_PATH))
        .arg(f.toolchain())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .unwrap();
    let pid = child.id();
    std::thread::sleep(Duration::from_secs(1));
    assert_eq!(unsafe { libc::kill(pid as i32, libc::SIGTERM) }, 0);
    let start = std::time::Instant::now();
    while child.try_wait().unwrap().is_none() {
        if start.elapsed() > Duration::from_secs(10) {
            child.kill().unwrap();
            panic!("CLI ignored SIGTERM");
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(
        output.status.code(),
        Some(2),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let path = String::from_utf8(output.stdout).unwrap();
    let manifest: Manifest = serde_json::from_slice(&fs::read(path.trim()).unwrap()).unwrap();
    assert_eq!(manifest.outcome, Outcome::Cancelled);
    assert_service_empty(&manifest.execution.unwrap().unit);
}
