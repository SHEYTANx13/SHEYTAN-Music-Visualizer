package cilog

// collect_conclusion_test.go — run-45678113841 contract fixtures for the
// COMPLETE GitHub job-conclusion vocabulary.
//
// Hosted run 45678113841 evidence: needs.build-macos.result concluded
// "abandoned" (the macos-26 runner was lost mid-job) and the historical
// four-value whitelist (success|failure|cancelled|skipped) failed the
// manifest writer closed, so the run manifest never existed, the
// collector refused (rc=2), and the emergency fallback published
// UNVERIFIED coverage. These fixtures pin the repaired contract:
//
//   - "abandoned" is a valid manifest result; a required job that
//     concluded abandoned is a named failure ("runner lost mid-job"),
//     its missing log part is classified as runner-lost (not the
//     generic missing-artifact message), and the run stays red;
//   - neutral / timed_out / action_required / startup_failure are valid
//     manifest results and non-success required jobs fail the run;
//   - genuinely invalid values ("green") remain a hard manifest error.
//
// The manifest-writer side of the same contract lives in
// scripts/ci/tests/test-ci-plumbing.sh (H30/H31/H45 fixtures).

import (
	"strings"
	"testing"
)

// TestAbandonedConclusionRecordedAndNamed (run 45678113841 signature): a
// required job that concluded abandoned with no log part must be
// recorded verbatim, named as runner-lost, and must keep the run red
// with incomplete coverage — never a hard collector error, never a
// silently-passing run.
func TestAbandonedConclusionRecordedAndNamed(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{name: "build-macos", result: ResultAbandoned, artifact: "macos-logs-part", required: true},
	})
	res, err := Collect(opts)
	if err != nil {
		t.Fatalf("Collect must not hard-error on the abandoned conclusion: %v", err)
	}
	report := readFile(t, opts.ReportPath)

	if res.Overall != OverallFail {
		t.Fatalf("a required abandoned job must fail the run; overall=%s", res.Overall)
	}
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("a lost runner means no log part; coverage=%s", res.Coverage)
	}
	if len(res.FailedJobs) != 1 || !strings.Contains(res.FailedJobs[0], "abandoned (runner lost mid-job)") {
		t.Fatalf("failed jobs = %v, want the runner-lost classification", res.FailedJobs)
	}
	if len(res.MissingSources) != 1 ||
		!strings.Contains(res.MissingSources[0], "conclusion: abandoned - runner lost mid-job") {
		t.Fatalf("missing sources = %v, want the runner-lost missing-log classification", res.MissingSources)
	}
	assertRecordContract(t, report)
	if !strings.Contains(report, "build-macos") {
		t.Fatal("the abandoned job must be named in the consolidated report")
	}
}

// TestExtendedConclusionVocabularyAccepted: every documented GitHub job
// conclusion outside the historical four is a valid manifest result, and
// a required job carrying it (with no log part) fails the run.
func TestExtendedConclusionVocabularyAccepted(t *testing.T) {
	for _, result := range []string{ResultNeutral, ResultTimedOut, ResultActionRequired, ResultStartupFailure} {
		t.Run(result, func(t *testing.T) {
			opts, _ := fixture(t, []tJob{
				{name: "build-macos", result: result, artifact: "macos-logs-part", required: true},
			})
			res, err := Collect(opts)
			if err != nil {
				t.Fatalf("Collect hard-errored on conclusion %q: %v", result, err)
			}
			if res.Overall != OverallFail {
				t.Fatalf("required job with conclusion %q must fail the run; overall=%s", result, res.Overall)
			}
			if len(res.FailedJobs) != 1 || !strings.Contains(res.FailedJobs[0], result) {
				t.Fatalf("failed jobs = %v, want the verbatim %q conclusion", res.FailedJobs, result)
			}
		})
	}
}

// TestGarbageResultStillHardError: the vocabulary expansion must not
// open the door to arbitrary garbage — a value that is not a GitHub job
// conclusion remains a hard manifest error.
func TestGarbageResultStillHardError(t *testing.T) {
	for _, garbage := range []string{"green", "ok", "IN_PROGRESS", ""} {
		opts, _ := fixture(t, []tJob{{name: "j", result: garbage, artifact: ""}})
		if _, err := Collect(opts); err == nil {
			t.Fatalf("result %q must remain a hard manifest error", garbage)
		}
	}
}

// TestDeriveFailuresNamesAbandoned: the failure derivation names the
// runner-lost class for abandoned rows while keeping other unknown
// conclusions verbatim.
func TestDeriveFailuresNamesAbandoned(t *testing.T) {
	failed, _ := deriveFailures(RunMeta{
		Jobs: []JobSpec{
			{Name: "build-macos", Result: ResultAbandoned, Required: true},
			{Name: "build-windows", Result: "stale", Required: true},
			{Name: "build-linux", Result: ResultSuccess, Required: true},
		},
	}, nil)
	if len(failed) != 2 {
		t.Fatalf("failed = %v, want exactly two rows", failed)
	}
	if !strings.Contains(failed[0], "abandoned (runner lost mid-job)") {
		t.Fatalf("abandoned row not named: %v", failed)
	}
	if !strings.Contains(failed[1], "stale") {
		t.Fatalf("unknown conclusion must stay verbatim: %v", failed)
	}
}
