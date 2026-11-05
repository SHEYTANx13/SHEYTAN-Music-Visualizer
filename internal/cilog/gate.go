package cilog

// gate.go — authoritative per-gate status derivation for the
// consolidated report.
//
// Run 41915116613 exposed the failure class this file exists to
// prevent: the consolidated summary listed NINE failed Windows gates
// while the failure index contained FOUR actual nonzero command
// records, and five of the listed "failures" had explicit
// `EXIT_CODE: 0 PASS` records.
//
// The single authoritative rule implemented here:
//
//      A gate's status comes ONLY from its own structured command
//      record's `EXIT_CODE: <n> PASS|FAIL` line, parsed strictly inside
//      one `===== COMMAND BEGIN =====` … `===== COMMAND END =====` block.
//      A gate with an explicit `EXIT_CODE: 0 PASS` can never be listed as
//      failed. A gate blocked by a prerequisite and recorded as
//      `SKIPPED: gate <name> (<reason>)` is SKIPPED, never a failed
//      command. The failed-gate count equals the number of distinct
//      failed gates listed. Repeated log copies are deduplicated without
//      losing source provenance.
//
// Structured records (written by scripts/ci/capture.sh and
// scripts/ci/Capture-Step.ps1) look like:
//
//      ===== COMMAND BEGIN =====
//      JOB: phase2-windows-build
//      STEP: go-test
//      STARTED: ...
//      WORKDIR: ...
//      COMMAND: ...
//      TOOL_CONTEXT: ...
//      --- OUTPUT (stdout+stderr from first byte) ---
//      <command output, any content>
//      --- END OUTPUT ---
//      EXIT_CODE: 1 FAIL
//      ENDED: ...
//      ===== COMMAND END =====
//
// The EXIT_CODE line is only authoritative INSIDE such a block; the
// same text appearing in a run-summary or failure-index section is a
// copy, not a command record, and is ignored for gate status. Text
// such as `ERROR:` inside a record's OUTPUT section describes that
// command's captured evidence only — it never changes the gate status,
// which the exit code owns.

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GateStatus is the status of one distinct gate.
type GateStatus string

const (
	GatePass    GateStatus = "PASS"
	GateFail    GateStatus = "FAIL"
	GateSkipped GateStatus = "SKIPPED"
)

// GateRecord is one distinct gate with deduplicated provenance.
type GateRecord struct {
	// Job is the DECLARED manifest job name (the artifact namespace),
	// not the build script's internal job id.
	Job string
	// Step is the structured STEP: field of the command record (or the
	// gate name of a SKIPPED line).
	Step string
	// ExitCode is the worst (highest) exit code observed across the
	// gate's records; 0 for skipped gates.
	ExitCode int
	// Status is PASS, FAIL or SKIPPED under the authoritative rule.
	Status GateStatus
	// Reason carries the SKIPPED reason when Status is SKIPPED.
	Reason string
	// Wheres lists every provenance of this gate (file:line inside its
	// artifact namespace), sorted and deduplicated. Copies from
	// repeated log sections never create a second gate.
	Wheres []string
}

var (
	reRecordBegin = regexp.MustCompile(`^===== COMMAND BEGIN =====\s*$`)
	reRecordEnd   = regexp.MustCompile(`^===== COMMAND END =====\s*$`)
	reStepField   = regexp.MustCompile(`^STEP: (.*)$`)
	reExitCode    = regexp.MustCompile(`^EXIT_CODE: (\d+) (PASS|FAIL)\b`)
	reSkippedGate = regexp.MustCompile(`^SKIPPED: gate (\S+) \((.*)\)\s*$`)
)

// gateScan is the per-file parse result: candidate command records
// (job-internal step id, exit code, provenance) plus skipped-gate
// lines.
type gateScan struct {
	steps   []gateStepObs
	skipped []gateSkipObs
}

type gateStepObs struct {
	job      string
	step     string
	exitCode int
	pass     bool
	where    string
}

type gateSkipObs struct {
	job    string
	gate   string
	reason string
	where  string
}

// parseGateFile scans one captured log file for structured records.
func parseGateFile(job, artifact, rel, abs string) (gateScan, error) {
	var sc gateScan
	fh, err := os.Open(abs)
	if err != nil {
		return sc, err
	}
	defer fh.Close()
	br := bufio.NewScanner(fh)
	br.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	inRecord := false
	step := ""
	for br.Scan() {
		lineNo++
		line := br.Text()
		switch {
		case reRecordBegin.MatchString(line):
			inRecord = true
			step = ""
		case reRecordEnd.MatchString(line):
			inRecord = false
			step = ""
		case inRecord && reExitCode.MatchString(line):
			m := reExitCode.FindStringSubmatch(line)
			code, cerr := strconv.Atoi(m[1])
			if cerr != nil {
				continue // never fabricate: unparseable codes are ignored here
			}
			sc.steps = append(sc.steps, gateStepObs{
				job:      job,
				step:     step,
				exitCode: code,
				pass:     m[2] == "PASS",
				where:    "[" + job + " | " + artifact + "/" + rel + ":" + strconv.Itoa(lineNo) + "]",
			})
		case inRecord && reStepField.MatchString(line):
			step = strings.TrimSpace(reStepField.FindStringSubmatch(line)[1])
		case !inRecord && reSkippedGate.MatchString(line):
			m := reSkippedGate.FindStringSubmatch(line)
			sc.skipped = append(sc.skipped, gateSkipObs{
				job:    job,
				gate:   m[1],
				reason: m[2],
				where:  "[" + job + " | " + artifact + "/" + rel + ":" + strconv.Itoa(lineNo) + "]",
			})
		}
	}
	return sc, br.Err()
}

// ParseGates reduces per-file scans into distinct gates, one per
// (declared job, step). The authoritative rule decides the status:
// any record with a nonzero exit code makes the gate FAIL (worst case
// wins across copies); only EXIT_CODE: 0 records make it PASS; an
// explicit SKIPPED line for a gate with no command record makes it
// SKIPPED (dependency-blocked, not a command failure).
func ParseGates(files []logFile) ([]GateRecord, error) {
	var all gateScan
	for _, f := range files {
		sc, err := parseGateFile(f.job.Name, f.artifact, f.rel, f.abs)
		if err != nil {
			return nil, err
		}
		all.steps = append(all.steps, sc.steps...)
		all.skipped = append(all.skipped, sc.skipped...)
	}

	type key struct{ job, step string }
	order := []key{}
	gates := map[key]*GateRecord{}
	get := func(k key) *GateRecord {
		if g, ok := gates[k]; ok {
			return g
		}
		g := &GateRecord{Job: k.job, Step: k.step}
		gates[k] = g
		order = append(order, k)
		return g
	}

	for _, obs := range all.steps {
		k := key{obs.job, obs.step}
		g := get(k)
		g.Wheres = appendUnique(g.Wheres, obs.where)
		if obs.pass && obs.exitCode == 0 {
			continue // a PASS record can never make a gate FAIL
		}
		if obs.exitCode > 0 {
			g.Status = GateFail
			if obs.exitCode > g.ExitCode {
				g.ExitCode = obs.exitCode
			}
		}
		// A PASS verdict line with a nonzero code (EXIT_CODE: 3 PASS)
		// would be a capture-contract violation; treat the CODE as
		// authoritative: nonzero = FAIL.
		if !obs.pass && obs.exitCode == 0 {
			g.Status = GateFail
		}
	}
	// Skipped lines only apply to gates that have no command record;
	// a real record outranks a skip marker (defensive: the build
	// scripts never emit both).
	for _, obs := range all.skipped {
		k := key{obs.job, obs.gate}
		if _, hasRecord := gates[k]; hasRecord {
			continue
		}
		g := get(k)
		g.Status = GateSkipped
		g.Reason = obs.reason
		g.Wheres = appendUnique(g.Wheres, obs.where)
	}

	out := make([]GateRecord, 0, len(order))
	for _, k := range order {
		g := gates[k]
		if g.Status == "" {
			g.Status = GatePass
		}
		sort.Strings(g.Wheres)
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := gateJobOrder(files, out[i].Job), gateJobOrder(files, out[j].Job)
		if pi != pj {
			return pi < pj
		}
		if out[i].Status != out[j].Status {
			// FAIL first, then SKIPPED, then PASS: the gates that need
			// attention lead the table.
			return gateStatusRank(out[i].Status) < gateStatusRank(out[j].Status)
		}
		return out[i].Step < out[j].Step
	})
	return out, nil
}

func gateStatusRank(s GateStatus) int {
	switch s {
	case GateFail:
		return 0
	case GateSkipped:
		return 1
	default:
		return 2
	}
}

// gateJobOrder returns the manifest position of a declared job name
// (fallback: alphabetical position after all known jobs).
func gateJobOrder(files []logFile, job string) int {
	seen := []string{}
	for _, f := range files {
		if !containsStr(seen, f.job.Name) {
			seen = append(seen, f.job.Name)
		}
	}
	for i, n := range seen {
		if n == job {
			return i
		}
	}
	return len(seen)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	if containsStr(list, s) {
		return list
	}
	return append(list, s)
}
