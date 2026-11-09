use std::fs::{self, OpenOptions};
use std::io::{self, Read, Write};
use std::path::{Component, Path, PathBuf};

pub const CANDIDATE_PATH: &str = "rtl/candidate/mac_candidate.sv";
pub const MAX_SOURCE_BYTES: u64 = 1024 * 1024;
const TRUSTED: &[&str] = &[
    "rtl/gold/mac_gold.sv",
    "tests/tb_mac.sv",
    "tests/mac_mutant.sv",
    "formal/wrapper.sv",
    "formal/reachability.sv",
    "formal/good.eqy",
    "scripts/check.sh",
    "scripts/audit-eqy.mjs",
    "scripts/counts.mjs",
    "Makefile",
];

#[derive(Debug)]
pub enum Error {
    InvalidInput(&'static str),
    Io(io::Error),
}

impl From<io::Error> for Error {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidInput(message) => f.write_str(message),
            Self::Io(error) => error.fmt(f),
        }
    }
}

impl std::error::Error for Error {}

#[derive(Debug, Clone)]
pub struct Identity(String);

impl Identity {
    pub fn parse(value: &str) -> Result<Self, Error> {
        if value.is_empty()
            || value.len() > 64
            || !value
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
        {
            return Err(Error::InvalidInput(
                "identity must be 1..64 ASCII letters, digits, - or _",
            ));
        }
        Ok(Self(value.to_owned()))
    }
}

#[derive(Debug)]
pub struct Candidate {
    source: Vec<u8>,
}

impl Candidate {
    pub fn replacement(path: &str, source: Vec<u8>) -> Result<Self, Error> {
        if path != CANDIDATE_PATH {
            return Err(Error::InvalidInput("candidate path is not allowlisted"));
        }
        if source.is_empty() || source.len() as u64 > MAX_SOURCE_BYTES {
            return Err(Error::InvalidInput(
                "candidate size is outside allowed bounds",
            ));
        }
        let text = std::str::from_utf8(&source)
            .map_err(|_| Error::InvalidInput("candidate must be UTF-8"))?;
        // Fail closed on preprocessing until a sandboxed include policy exists.
        if text.contains('`') || text.contains('\0') {
            return Err(Error::InvalidInput(
                "candidate preprocessing and NUL bytes are forbidden",
            ));
        }
        Ok(Self { source })
    }
}

#[derive(Debug)]
pub struct PreparedAttempt {
    pub path: PathBuf,
}

fn read_regular(root: &Path, relative: &Path) -> Result<Vec<u8>, Error> {
    let mut path = root.to_path_buf();
    for part in relative.components() {
        if !matches!(part, Component::Normal(_)) {
            return Err(Error::InvalidInput(
                "source path must be relative without traversal",
            ));
        }
        path.push(part);
        if fs::symlink_metadata(&path)?.file_type().is_symlink() {
            return Err(Error::InvalidInput("source symlinks are forbidden"));
        }
    }
    if !fs::symlink_metadata(&path)?.is_file() {
        return Err(Error::InvalidInput("source must be a regular file"));
    }
    let mut bytes = Vec::new();
    fs::File::open(path)?
        .take(MAX_SOURCE_BYTES + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_SOURCE_BYTES {
        return Err(Error::InvalidInput("source exceeds size limit"));
    }
    Ok(bytes)
}

pub fn prepare(
    trusted_root: &Path,
    attempts_root: &Path,
    job: &Identity,
    attempt: &Identity,
    candidate: &Candidate,
) -> Result<PreparedAttempt, Error> {
    let mut inputs = Vec::new();
    for relative in TRUSTED {
        inputs.push((*relative, read_regular(trusted_root, Path::new(relative))?));
    }
    inputs.push((CANDIDATE_PATH, candidate.source.clone()));
    let path = attempts_root.join(format!("{}-{}-{}", job.0.len(), job.0, attempt.0));
    fs::create_dir(&path)?;
    for (relative, bytes) in inputs {
        let target = path.join("inputs").join(relative);
        fs::create_dir_all(target.parent().expect("fixed relative file has parent"))?;
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&target)?;
        file.write_all(&bytes)?;
        file.sync_all()?;
        let mut permissions = file.metadata()?.permissions();
        permissions.set_readonly(true);
        file.set_permissions(permissions)?;
    }
    let mut ready = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path.join("PREPARED"))?;
    ready.write_all(b"staging-only; execution and eligibility unavailable\n")?;
    ready.sync_all()?;
    Ok(PreparedAttempt { path })
}
