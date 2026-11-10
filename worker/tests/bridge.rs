use std::io::Write;
use std::process::{Command, Stdio};

#[test]
fn validation_contract_accepts_rtl_without_staging_or_execution() {
    for (source, accepted) in [
        ("module mac_candidate; endmodule", true),
        ("module mac_candidate; initial $system(0); endmodule", false),
        ("module mac_gold; endmodule", false),
    ] {
        let mut child = Command::new(env!("CARGO_BIN_EXE_forge-worker"))
            .arg("validate")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        child
            .stdin
            .take()
            .unwrap()
            .write_all(source.as_bytes())
            .unwrap();
        let output = child.wait_with_output().unwrap();
        assert_eq!(output.status.success(), accepted);
        if accepted {
            let value: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
            assert_eq!(
                value,
                serde_json::json!({"schema_version": 1, "valid": true})
            );
        } else {
            assert!(output.stdout.is_empty());
            assert!(!output.stderr.is_empty());
        }
    }
}
