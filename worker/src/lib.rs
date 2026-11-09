pub mod evidence;
pub mod sandbox;
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
    "formal/candidate-reachability.sv",
    "formal/good.eqy",
    "scripts/check.sh",
    "scripts/audit-eqy.mjs",
    "scripts/counts.mjs",
    "Makefile",
    "scripts/worker-run.sh",
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
        if text.contains('"')
            || text.contains('\\')
            || text.contains("//")
            || text.contains("/*")
            || text.contains("(*")
        {
            return Err(Error::InvalidInput(
                "candidate strings, escaped identifiers, comments, and attributes are forbidden",
            ));
        }
        let tokens: Vec<_> = text
            .split(|c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '$')
            .filter(|token| !token.is_empty())
            .collect();
        let forbidden = [
            "initial", "final", "import", "export", "bind", "assume", "restrict", "assert",
            "cover", "force", "release", "defparam", "specify", "config", "library",
        ];
        for token in &tokens {
            if forbidden.contains(token)
                || token.contains('$')
                    && !["$signed", "$unsigned", "$clog2", "$bits"].contains(token)
            {
                return Err(Error::InvalidInput(
                    "candidate contains an unsupported behavioral or formal construct",
                ));
            }
        }
        if tokens.iter().filter(|token| **token == "module").count() != 1
            || tokens.iter().filter(|token| **token == "endmodule").count() != 1
            || tokens.first() != Some(&"module")
            || tokens.get(1) != Some(&"mac_candidate")
        {
            return Err(Error::InvalidInput(
                "candidate must define only module mac_candidate",
            ));
        }
        Ok(Self { source })
    }
}

#[derive(Debug)]
pub struct PreparedAttempt {
    pub path: PathBuf,
    pub job: Identity,
    pub attempt: Identity,
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

pub(crate) fn sync_directories(root: &Path) -> Result<(), Error> {
    for entry in fs::read_dir(root)? {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            sync_directories(&entry.path())?;
        }
    }
    fs::File::open(root)?.sync_all()?;
    Ok(())
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
    fs::create_dir(path.join("inputs/build"))?;
    sync_directories(&path.join("inputs"))?;
    let mut ready = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path.join("PREPARED"))?;
    ready.write_all(b"inputs prepared; not a verification result\n")?;
    ready.sync_all()?;
    fs::File::open(&path)?.sync_all()?;
    Ok(PreparedAttempt {
        path,
        job: job.clone(),
        attempt: attempt.clone(),
    })
}
