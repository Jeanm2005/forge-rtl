use forge_worker::evidence::{evaluate, Outcome};
use forge_worker::sandbox::Limits;
use forge_worker::{prepare, Candidate, Identity, CANDIDATE_PATH, MAX_SOURCE_BYTES};
use std::io::Read;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};

static CANCELLED: AtomicBool = AtomicBool::new(false);
extern "C" fn cancel(_: libc::c_int) {
    CANCELLED.store(true, Ordering::SeqCst);
}

fn run() -> Result<bool, Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let evaluating = args.first().map(String::as_str) == Some("evaluate");
    if !(evaluating && (args.len() == 7 || args.len() == 8)
        || args.first().map(String::as_str) == Some("stage") && args.len() == 6)
    {
        return Err("usage: forge-worker stage TRUSTED_ROOT ATTEMPTS_ROOT JOB_ID ATTEMPT_ID CANDIDATE_FILE\n       forge-worker evaluate TRUSTED_ROOT ATTEMPTS_ROOT JOB_ID ATTEMPT_ID CANDIDATE_FILE TOOLCHAIN [LIMITS_JSON]".into());
    }
    let job = Identity::parse(&args[3])?;
    let attempt = Identity::parse(&args[4])?;
    let metadata = std::fs::symlink_metadata(&args[5])?;
    if !metadata.is_file() {
        return Err("candidate must be a regular file, not a symlink".into());
    }
    let mut source = Vec::new();
    std::fs::File::open(&args[5])?
        .take(MAX_SOURCE_BYTES + 1)
        .read_to_end(&mut source)?;
    let candidate = Candidate::replacement(CANDIDATE_PATH, source)?;
    let limits = if args.len() == 8 {
        let mut bytes = Vec::new();
        std::fs::File::open(&args[7])?
            .take(16385)
            .read_to_end(&mut bytes)?;
        if bytes.len() > 16384 {
            return Err("limit configuration too large".into());
        }
        serde_json::from_slice::<Limits>(&bytes)?
    } else {
        Limits::default()
    };
    limits.validate()?;
    let prepared = prepare(
        Path::new(&args[1]),
        Path::new(&args[2]),
        &job,
        &attempt,
        &candidate,
    )?;
    if !evaluating {
        println!("prepared {} (no tools executed)", prepared.path.display());
        return Ok(true);
    }
    // The handlers only set a lock-free atomic; service teardown runs in the supervisor.
    unsafe {
        if libc::signal(libc::SIGINT, cancel as *const () as libc::sighandler_t) == libc::SIG_ERR
            || libc::signal(libc::SIGTERM, cancel as *const () as libc::sighandler_t)
                == libc::SIG_ERR
        {
            return Err("cannot install cancellation handlers".into());
        }
    }
    let manifest = evaluate(&prepared, Path::new(&args[6]), &limits, &CANCELLED)?;
    println!("{}", prepared.path.join("manifest.json").display());
    Ok(matches!(
        manifest.outcome,
        Outcome::Passed
            | Outcome::Counterexample
            | Outcome::VerificationFailed
            | Outcome::Inconclusive
    ))
}

fn main() {
    match run() {
        Ok(true) => (),
        Ok(false) => std::process::exit(2),
        Err(error) => {
            eprintln!("forge-worker: {error}");
            std::process::exit(1);
        }
    }
}
