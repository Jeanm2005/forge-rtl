use forge_worker::{prepare, Candidate, Identity, CANDIDATE_PATH, MAX_SOURCE_BYTES};
use std::{fs, path::PathBuf};

struct Temp(PathBuf);
impl Temp {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!(
            "forge-staging-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir(&path).unwrap();
        Self(path)
    }
}
impl Drop for Temp {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}

#[test]
fn rejects_untrusted_inputs() {
    for id in ["", "..", "a/b", "/tmp", "é", "a b"] {
        assert!(Identity::parse(id).is_err());
    }
    for path in [
        "../rtl/candidate/mac_candidate.sv",
        "rtl/gold/mac_gold.sv",
        "/rtl/candidate/mac_candidate.sv",
    ] {
        assert!(Candidate::replacement(path, b"module x; endmodule".to_vec()).is_err());
    }
    for source in [
        b"`include \"/etc/passwd\"".to_vec(),
        vec![0],
        vec![255],
        vec![],
        vec![b'a'; MAX_SOURCE_BYTES as usize + 1],
    ] {
        assert!(Candidate::replacement(CANDIDATE_PATH, source).is_err());
    }
}

#[test]
fn stages_independent_attempts_and_refuses_overwrite() {
    let temp = Temp::new();
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .to_path_buf();
    let candidate = Candidate::replacement(
        CANDIDATE_PATH,
        b"module mac_candidate; endmodule\n".to_vec(),
    )
    .unwrap();
    let job = Identity::parse("job").unwrap();
    let attempt = Identity::parse("one").unwrap();
    let first = prepare(&root, &temp.0, &job, &attempt, &candidate).unwrap();
    assert!(first.path.join("PREPARED").is_file());
    assert_eq!(
        fs::read(first.path.join("inputs").join(CANDIDATE_PATH)).unwrap(),
        b"module mac_candidate; endmodule\n"
    );
    assert!(fs::metadata(first.path.join("inputs").join(CANDIDATE_PATH))
        .unwrap()
        .permissions()
        .readonly());
    assert!(prepare(&root, &temp.0, &job, &attempt, &candidate).is_err());
    let second = prepare(
        &root,
        &temp.0,
        &job,
        &Identity::parse("two").unwrap(),
        &candidate,
    )
    .unwrap();
    assert_ne!(first.path, second.path);
    assert!(!first.path.join("inputs/.git").exists());
    assert!(!first.path.join("inputs/.tools").exists());
}

#[cfg(unix)]
#[test]
fn rejects_trusted_source_symlinks_before_staging() {
    let temp = Temp::new();
    let trusted = temp.0.join("trusted");
    fs::create_dir(&trusted).unwrap();
    std::os::unix::fs::symlink("/tmp", trusted.join("rtl")).unwrap();
    let candidate =
        Candidate::replacement(CANDIDATE_PATH, b"module x; endmodule".to_vec()).unwrap();
    let id = Identity::parse("test").unwrap();
    assert!(prepare(&trusted, &temp.0, &id, &id, &candidate).is_err());
    assert_eq!(fs::read_dir(&temp.0).unwrap().count(), 1);
}
