package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var trustedInputs = []string{"rtl/gold/mac_gold.sv", "tests/tb_mac.sv", "tests/mac_mutant.sv", "formal/wrapper.sv", "formal/reachability.sv", "formal/candidate-reachability.sv", "formal/good.eqy", "scripts/check.sh", "scripts/audit-eqy.mjs", "scripts/counts.mjs", "Makefile", "scripts/worker-run.sh"}

type Limits struct {
	WallSeconds    uint64 `json:"wall_seconds"`
	MemoryBytes    uint64 `json:"memory_bytes"`
	Tasks          uint32 `json:"tasks"`
	CPUPercent     uint32 `json:"cpu_percent"`
	CPUSeconds     uint64 `json:"cpu_seconds_per_process"`
	WritableBytes  uint64 `json:"writable_bytes"`
	TemporaryBytes uint64 `json:"temporary_bytes"`
	FileBytes      uint64 `json:"file_bytes"`
	ArchiveBytes   uint64 `json:"archive_bytes"`
	LogBytes       uint64 `json:"log_bytes"`
}

func DefaultLimits() Limits {
	return Limits{900, 2 * 1024 * 1024 * 1024, 128, 200, 240, 512 * 1024 * 1024, 64 * 1024 * 1024, 64 * 1024 * 1024, 128 * 1024 * 1024, 8 * 1024 * 1024}
}

type FileEvidence struct {
	SHA   string `json:"sha256"`
	Bytes uint64 `json:"bytes"`
}
type Gate struct {
	Outcome  string  `json:"outcome"`
	Exit     *uint32 `json:"exit_code"`
	Duration *uint64 `json:"duration_ms"`
	Depth    *uint32 `json:"bounded_depth"`
}
type Execution struct {
	Outcome       string `json:"outcome"`
	Exit          *int   `json:"exit_code"`
	Duration      uint64 `json:"duration_ms"`
	Unit          string `json:"unit"`
	ServiceResult string `json:"service_result"`
	Archive       string `json:"archive"`
	Log           string `json:"log"`
}
type Manifest struct {
	Version          uint32                  `json:"schema_version"`
	JobID            string                  `json:"job_id"`
	AttemptID        string                  `json:"attempt_id"`
	Recorded         uint64                  `json:"recorded_unix_seconds"`
	Outcome          string                  `json:"outcome"`
	Diagnostic       *string                 `json:"diagnostic"`
	Limits           Limits                  `json:"limits"`
	ConfigurationSHA string                  `json:"configuration_sha256"`
	Worker           FileEvidence            `json:"worker"`
	Inputs           map[string]FileEvidence `json:"inputs"`
	Artifacts        map[string]FileEvidence `json:"artifacts"`
	Transport        map[string]FileEvidence `json:"transport"`
	Toolchain        map[string]string       `json:"toolchain"`
	Versions         *string                 `json:"versions"`
	Seed             string                  `json:"simulation_seed"`
	Strategy         string                  `json:"proof_strategy"`
	Gates            map[string]Gate         `json:"gates"`
	Cells            map[string]uint64       `json:"generic_cells"`
	Execution        *Execution              `json:"execution"`
}
type Validator struct {
	Inputs           map[string]FileEvidence
	Worker           FileEvidence
	Toolchain        map[string]string
	Limits           Limits
	ConfigurationSHA string
	PolicySHA        string
}

func hashRegular(root, relative string, max uint64) (FileEvidence, error) {
	file, err := openRegular(root, relative, max)
	if err != nil {
		return FileEvidence{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, int64(max)+1))
	if err != nil {
		return FileEvidence{}, err
	}
	if uint64(size) > max {
		return FileEvidence{}, errors.New("evidence exceeds hash limit")
	}
	return FileEvidence{hex.EncodeToString(hash.Sum(nil)), uint64(size)}, nil
}
func evidenceOf(data []byte) FileEvidence { return FileEvidence{digest(data), uint64(len(data))} }
func NewValidator(root, worker, toolchain string) (*Validator, error) {
	v := &Validator{Inputs: map[string]FileEvidence{}, Toolchain: map[string]string{}, Limits: DefaultLimits()}
	for _, relative := range trustedInputs {
		data, err := regularFile(root, relative, MaxSource)
		if err != nil {
			return nil, err
		}
		v.Inputs[relative] = evidenceOf(data)
	}
	data, err := os.ReadFile(worker)
	if err != nil {
		return nil, err
	}
	v.Worker = evidenceOf(data)
	for _, relative := range []string{"VERSION", ".forge-toolchain-sha256"} {
		data, err = regularFile(toolchain, relative, 1024)
		if err != nil {
			return nil, err
		}
		v.Toolchain[relative] = strings.TrimSpace(string(data))
	}
	limits, _ := json.Marshal(v.Limits)
	v.ConfigurationSHA = digest(limits)
	policy, _ := json.Marshal(v)
	v.PolicySHA = digest(policy)
	return v, nil
}
func (v *Validator) Check(job *Job, attempt *Attempt, root string) (completion, []byte, error) {
	raw, err := regularFile(root, "manifest.json", 8*1024*1024)
	if err != nil {
		return completion{}, nil, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&m); err != nil {
		return completion{}, nil, err
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return completion{}, nil, errors.New("trailing manifest data")
	}
	if m.Version != 1 || m.JobID != job.ID || m.AttemptID != attempt.ID || m.ConfigurationSHA != v.ConfigurationSHA || m.Limits != v.Limits || m.Worker != v.Worker || m.Recorded == 0 {
		return completion{}, nil, errors.New("evidence identity/configuration mismatch")
	}
	if m.Seed != "1badf00d" || m.Strategy != "EQY SBY smtbmc bitwuzla induction depth 10; public matches only; first-transition reset" {
		return completion{}, nil, errors.New("unreviewed seed or proof strategy")
	}
	if len(m.Toolchain) != len(v.Toolchain) {
		return completion{}, nil, errors.New("missing toolchain identity")
	}
	for name, value := range v.Toolchain {
		if m.Toolchain[name] != value {
			return completion{}, nil, errors.New("toolchain identity mismatch")
		}
	}
	if len(m.Inputs) != len(v.Inputs)+1 {
		return completion{}, nil, errors.New("missing or extra staged inputs")
	}
	for relative, expected := range v.Inputs {
		if m.Inputs[relative] != expected {
			return completion{}, nil, fmt.Errorf("trusted input changed: %s", relative)
		}
	}
	if m.Inputs[CandidatePath] != (FileEvidence{job.SourceSHA, uint64(len(job.Source))}) {
		return completion{}, nil, errors.New("candidate input mismatch")
	}
	if len(m.Artifacts) > 4096 || len(m.Transport) != 2 {
		return completion{}, nil, errors.New("incomplete or oversized artifact inventory")
	}
	var total uint64
	for category, files := range map[string]map[string]FileEvidence{"inputs": m.Inputs, "artifacts": m.Artifacts, "": m.Transport} {
		for relative, expected := range files {
			if category == "" && relative != "artifacts.tar" && relative != "execution.log" {
				return completion{}, nil, errors.New("unexpected transport path")
			}
			max := v.Limits.FileBytes
			if category == "" {
				max = v.Limits.LogBytes
				if relative == "artifacts.tar" {
					max = v.Limits.ArchiveBytes
				}
			}
			if expected.Bytes > max {
				return completion{}, nil, errors.New("oversized artifact inventory entry")
			}
			actual, err := hashRegular(filepath.Join(root, category), relative, max)
			if err != nil {
				return completion{}, nil, err
			}
			if actual != expected {
				return completion{}, nil, fmt.Errorf("evidence hash mismatch: %s", relative)
			}
			if category == "artifacts" {
				total += expected.Bytes
				if total > v.Limits.ArchiveBytes {
					return completion{}, nil, errors.New("artifact inventory exceeds total limit")
				}
			}
		}
	}
	allowed := map[string]bool{"passed": true, "verification_failed": true, "counterexample": true, "inconclusive": true, "timeout": true, "cancelled": true, "infrastructure_error": true, "output_limit": true}
	if !allowed[m.Outcome] {
		return completion{}, nil, errors.New("unknown evidence outcome")
	}
	result := completion{Outcome: m.Outcome, Retryable: m.Outcome == "infrastructure_error"}
	if m.Diagnostic != nil {
		result.Diagnostic = *m.Diagnostic
	}
	if m.Outcome == "passed" {
		required := []string{"tools", "simulation", "equivalence", "equivalence_audit", "reachable_depth_3", "synthesis"}
		if len(m.Gates) != len(required) || m.Execution == nil || m.Execution.Outcome != "completed" || m.Execution.Exit == nil || *m.Execution.Exit != 0 || m.Execution.ServiceResult != "success" || m.Execution.Unit != evalUnit(attempt.ID) {
			return completion{}, nil, errors.New("passing evidence lacks completed execution")
		}
		for _, name := range required {
			g, ok := m.Gates[name]
			if !ok || g.Outcome != "passed" || g.Exit == nil || *g.Exit != 0 || g.Duration == nil {
				return completion{}, nil, fmt.Errorf("missing explicit passing gate: %s", name)
			}
		}
		if depth := m.Gates["reachable_depth_3"].Depth; depth == nil || *depth != 3 {
			return completion{}, nil, errors.New("bounded SAT depth missing")
		}
		if m.Versions == nil || len(strings.Split(strings.TrimSpace(*m.Versions), "\n")) != 8 || len(m.Cells) != 2 {
			return completion{}, nil, errors.New("passing evidence lacks versions or counts")
		}
		for _, name := range []string{"gold", "candidate"} {
			if _, ok := m.Cells[name]; !ok {
				return completion{}, nil, errors.New("synthesis counts missing")
			}
		}
		for _, path := range []string{"sim/result.log", "formal/PASS", "formal/matched.ids", "synth/comparison.json", "synth/gold/netlist.v", "synth/gold/netlist.json", "synth/gold/stat.json", "synth/candidate/netlist.v", "synth/candidate/netlist.json", "synth/candidate/stat.json"} {
			if _, ok := m.Artifacts[path]; !ok {
				return completion{}, nil, fmt.Errorf("required artifact missing: %s", path)
			}
		}
		result.Eligible = true
	}
	return result, raw, nil
}
func (s *Store) PublishEvidence(attemptID string, raw []byte) (string, string, error) {
	relative := filepath.Join("evidence", attemptID, "manifest.json")
	path := filepath.Join(s.root, relative)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return "", "", errors.New("evidence already published or inaccessible")
	}
	if err := atomicWrite(path, raw); err != nil {
		return "", "", err
	}
	if err := syncDir(filepath.Join(s.root, "evidence")); err != nil {
		return "", "", err
	}
	return relative, digest(raw), nil
}
func syncDir(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
