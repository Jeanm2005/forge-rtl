use forge_worker::{prepare, Candidate, Identity, CANDIDATE_PATH, MAX_SOURCE_BYTES};
use std::io::Read;
use std::path::Path;

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.len() != 5 {
        return Err(
            "usage: forge-worker TRUSTED_ROOT ATTEMPTS_ROOT JOB_ID ATTEMPT_ID CANDIDATE_FILE"
                .into(),
        );
    }
    let job = Identity::parse(&args[2])?;
    let attempt = Identity::parse(&args[3])?;
    let metadata = std::fs::symlink_metadata(&args[4])?;
    if !metadata.is_file() {
        return Err("candidate must be a regular file, not a symlink".into());
    }
    let mut source = Vec::new();
    std::fs::File::open(&args[4])?
        .take(MAX_SOURCE_BYTES + 1)
        .read_to_end(&mut source)?;
    let candidate = Candidate::replacement(CANDIDATE_PATH, source)?;
    let prepared = prepare(
        Path::new(&args[0]),
        Path::new(&args[1]),
        &job,
        &attempt,
        &candidate,
    )?;
    println!("prepared {} (no tools executed)", prepared.path.display());
    Ok(())
}

fn main() {
    if let Err(error) = run() {
        eprintln!("forge-worker: {error}");
        std::process::exit(1);
    }
}
