// Command checkcov fails when a package's statement coverage drops below its
// committed per-package floor.
//
// It parses a `go test -coverprofile` file directly: `go tool cover -func`
// prints per-function percentages, which cannot be aggregated into
// per-package statement coverage. Statements are summed per package directory
// and compared against testdata/coverage/floors.json, which records the
// measurement each floor was set from and a one-line reason for it.
//
// One repository-wide floor let a well-covered package subsidise a thin one:
// the signing path was averaged together with the CLI and the examples, so a
// regression in the path that produces signatures could hide behind the rest.
// The signing-path packages (the root package and internal/xdrcopy) carry
// floors held strictly above the 60% repository-wide floor this scheme
// replaced, and the checker enforces that minimum as policy.
//
// Packages with no tests by design carry no floor: they are named under
// "excluded" with a reason, so an exclusion is a visible decision rather than
// a floor of zero.
//
// Usage:
//
//	go test -coverprofile=coverage.out -covermode=atomic ./...
//	go list -f '{{.ImportPath}}' ./... > packages.txt
//	go run ./scripts/checkcov coverage.out testdata/coverage/floors.json packages.txt
//
// The package list is a file input rather than a call to `go list` inside the
// checker, so the checker is hermetic and its tests need no Go source tree.
//
// Exit status: 0 every package met its floor, 1 a floor was missed or the
// floor table and the tree disagree, 2 usage or parse error.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Floor is the committed minimum for one package.
type Floor struct {
	// Measured is the coverage percentage observed when the floor was set.
	// The checker refuses a floor above it: floors come from measurement,
	// not aspiration.
	Measured float64 `json:"measured"`
	// Floor is the minimum coverage percentage the package must keep.
	Floor float64 `json:"floor"`
	// Reason is the one-line justification for the floor.
	Reason string `json:"reason"`
	// SigningPath marks the root package and internal/xdrcopy, whose floors
	// the policy holds strictly above the repository-wide floor this scheme
	// replaced.
	SigningPath bool `json:"signing_path,omitempty"`
}

// Policy is the rule every floor must satisfy, committed next to the floors
// so the rationale travels with the numbers.
type Policy struct {
	// SigningPathFloorMinExclusive is the repository-wide floor this scheme
	// replaced. A signing-path floor must stay strictly above it, so a
	// regression in the path that produces signatures cannot be lowered
	// back to the old gate.
	SigningPathFloorMinExclusive float64 `json:"signing_path_floor_min_exclusive"`
	// Text states the policy in prose. Not interpreted by the checker.
	Text string `json:"text,omitempty"`
}

// File is the on-disk floors document.
type File struct {
	// Note documents the toolchain, machine, date and measurement command.
	// Not interpreted.
	Note string `json:"note,omitempty"`
	// Policy is the signing-path rule the table must satisfy.
	Policy Policy `json:"policy"`
	// Packages maps an import path to its floor.
	Packages map[string]Floor `json:"packages"`
	// Excluded maps an import path to the reason it carries no floor: the
	// packages with no tests by design. An exclusion is explicit, never a
	// floor of zero.
	Excluded map[string]string `json:"excluded"`
}

// coverage is the statement count aggregated over one package's files.
type coverage struct {
	covered int
	total   int
}

// percent is the package's coverage as a percentage in [0, 100]. Only call it
// when total is non-zero.
func (c coverage) percent() float64 {
	return 100 * float64(c.covered) / float64(c.total)
}

// profileLine matches one statement block in a coverage profile, e.g.:
//
//	github.com/soroauth/soroauth-go/address.go:39.52,43.16 2 19594
//
// The file name is greedy, so the match settles on the last colon that
// precedes the numeric block positions; a path that itself contains a colon
// (a Windows drive letter, say) still parses.
var profileLine = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is main without the process boundary, so tests can assert the exact
// exit code and the exact stdout and stderr a failure produces.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 4 {
		fmt.Fprintln(stderr, "usage: checkcov <coverage.out> <floors.json> <packages.txt>")
		return 2
	}
	measured, err := parseProfile(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "checkcov: %v\n", err)
		return 2
	}
	floors, err := loadFloors(args[2])
	if err != nil {
		fmt.Fprintf(stderr, "checkcov: %v\n", err)
		return 2
	}
	pkgs, err := loadPackages(args[3])
	if err != nil {
		fmt.Fprintf(stderr, "checkcov: %v\n", err)
		return 2
	}

	rep := evaluate(measured, floors, pkgs)
	for _, line := range rep.lines {
		fmt.Fprintln(stdout, line)
	}
	if rep.failed {
		fmt.Fprintln(stderr, "\ncheckcov: one or more per-package coverage floors were missed.")
		fmt.Fprintln(stderr, "Fix the regression; do not lower a floor to make CI green. An")
		fmt.Fprintln(stderr, "intentional drop updates floors.json in the same commit, with a")
		fmt.Fprintln(stderr, "fresh measurement named in the floor's reason.")
		return 1
	}
	if rep.checked == 0 {
		fmt.Fprintln(stderr, "checkcov: no packages were checked against a floor")
		return 1
	}
	fmt.Fprintf(stdout, "\ncheckcov: %d packages met their floors, %d excluded\n", rep.checked, rep.excluded)
	return 0
}

// report is the outcome of evaluating one coverage profile against the floor
// table: one line per package in packages.txt order, then the counter.
type report struct {
	lines    []string
	failed   bool
	checked  int
	excluded int
}

func (r *report) fail(format string, args ...any) {
	r.lines = append(r.lines, "FAIL "+fmt.Sprintf(format, args...))
	r.failed = true
}

// evaluate compares each measured package against its floor and reports every
// disagreement between the floor table and the tree. It is pure: the caller
// decides how to print and which exit code the report maps to.
func evaluate(measured map[string]coverage, floors *File, pkgs []string) report {
	var rep report

	// The report follows packages.txt order, because `go list` emits a sorted
	// list; a package seen only in the profile is appended, sorted, so the
	// output does not depend on map iteration.
	seen := make(map[string]bool, len(pkgs)+len(measured))
	var ordered []string
	for _, pkg := range pkgs {
		if !seen[pkg] {
			seen[pkg] = true
			ordered = append(ordered, pkg)
		}
	}
	var extras []string
	for pkg := range measured {
		if !seen[pkg] {
			seen[pkg] = true
			extras = append(extras, pkg)
		}
	}
	sort.Strings(extras)
	ordered = append(ordered, extras...)

	for _, pkg := range ordered {
		if reason, excluded := floors.Excluded[pkg]; excluded {
			rep.excluded++
			if cov, ok := measured[pkg]; ok && cov.covered > 0 {
				rep.fail("%s: excluded but now measures %.2f%% coverage; give it a floor or document why the exclusion still holds", pkg, cov.percent())
			}
			rep.lines = append(rep.lines, fmt.Sprintf("skip %-55s excluded: %s", pkg, reason))
			continue
		}
		floor, ok := floors.Packages[pkg]
		if !ok {
			rep.fail("%s: no floor in floors.json and not listed under excluded (packages with no tests are excluded by name, never given a floor of zero)", pkg)
			continue
		}
		cov := measured[pkg]
		if cov.total == 0 {
			rep.fail("%s: no statement counts in the coverage profile", pkg)
			continue
		}
		rep.checked++
		got := cov.percent()
		var problems []string
		if got < floor.Floor {
			problems = append(problems, fmt.Sprintf("coverage %.2f%% < floor %.2f%% (measured %.2f%% when the floor was set)", got, floor.Floor, floor.Measured))
		}
		if floor.SigningPath && floor.Floor <= floors.Policy.SigningPathFloorMinExclusive {
			problems = append(problems, fmt.Sprintf("signing-path floor %.2f%% is not strictly above the policy minimum of %.2f%%", floor.Floor, floors.Policy.SigningPathFloorMinExclusive))
		}
		if floor.Floor > floor.Measured {
			problems = append(problems, fmt.Sprintf("floor %.2f%% is above the measured %.2f%% it was set from (floors come from measurement, not aspiration)", floor.Floor, floor.Measured))
		}
		if len(problems) > 0 {
			for _, problem := range problems {
				rep.fail("%s: %s", pkg, problem)
			}
			continue
		}
		mark := ""
		if floor.SigningPath {
			mark = "  signing-path"
		}
		rep.lines = append(rep.lines, fmt.Sprintf("ok   %-55s coverage=%.2f%% floor=%.2f%%%s", pkg, got, floor.Floor, mark))
	}

	// A floor or an exclusion for a package that no longer exists is a gate
	// that has silently stopped checking something: the same reason checkbench
	// fails on a budgeted benchmark that is missing from the output.
	for _, pkg := range sortedKeys(floors.Packages) {
		if !seen[pkg] {
			rep.fail("%s: floor in floors.json but no such package in packages.txt or the coverage profile (removed or renamed; update the table)", pkg)
		}
	}
	for _, pkg := range sortedKeys(floors.Excluded) {
		if !seen[pkg] {
			rep.fail("%s: excluded in floors.json but no such package in packages.txt or the coverage profile (removed or renamed; update the table)", pkg)
		}
	}
	return rep
}

// parseProfile reads a coverage profile and aggregates its statement counts
// by package directory: the package is the file's import path minus its last
// element, e.g. github.com/soroauth/soroauth-go/address.go belongs to
// github.com/soroauth/soroauth-go.
func parseProfile(path string) (map[string]coverage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]coverage{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if i == 0 {
			if err := parseMode(line); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			continue
		}
		if line == "" {
			continue
		}
		m := profileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s:%d: cannot parse coverage line %q", path, i+1, line)
		}
		stmts, err := strconv.Atoi(m[6])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: parsing statement count in %q: %w", path, i+1, line, err)
		}
		count, err := strconv.Atoi(m[7])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: parsing execution count in %q: %w", path, i+1, line, err)
		}
		file := m[1]
		if ignoredPath(file) {
			continue
		}
		slash := strings.LastIndex(file, "/")
		if slash < 0 {
			return nil, fmt.Errorf("%s:%d: cannot determine a package directory from file %q", path, i+1, file)
		}
		pkg := file[:slash]
		c := out[pkg]
		c.total += stmts
		if count > 0 {
			c.covered += stmts
		}
		out[pkg] = c
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no coverage lines found (is the profile empty?)", path)
	}
	return out, nil
}

// parseMode validates the profile's mode line. The mode is not used for the
// comparison — any mode marks statements as covered when count is non-zero —
// but an unknown or missing one means the file is not a coverage profile.
func parseMode(line string) error {
	mode, ok := strings.CutPrefix(line, "mode: ")
	switch {
	case !ok:
		return fmt.Errorf("missing mode line (want \"mode: set\", \"mode: count\" or \"mode: atomic\")")
	case mode != "set" && mode != "count" && mode != "atomic":
		return fmt.Errorf("unknown coverage mode %q (want set, count or atomic)", mode)
	}
	return nil
}

// ignoredPath reports whether a coverage-profile file path or an import path
// lies under a node_modules directory. `npm ci` at the repository root installs
// node_modules/flatted/golang, a Go package of its own that `go list ./...`
// and the coverage profile then contain on a developer machine; CI never
// measures it, and it is a dependency's source, not this module's. Filtering
// keeps a local `make coverage` in agreement with CI instead of failing on a
// package the floor table must not carry.
func ignoredPath(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == "node_modules" {
			return true
		}
	}
	return false
}

// loadFloors reads and validates the floor table: floors without a reason,
// packages that are both floored and excluded, and a missing policy minimum
// are all table errors rather than silent passes.
func loadFloors(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(f.Packages) == 0 {
		return nil, fmt.Errorf("%s has no per-package floors", path)
	}
	if f.Policy.SigningPathFloorMinExclusive <= 0 {
		return nil, fmt.Errorf("%s: policy.signing_path_floor_min_exclusive must be greater than 0", path)
	}
	for _, pkg := range sortedKeys(f.Packages) {
		if strings.TrimSpace(f.Packages[pkg].Reason) == "" {
			return nil, fmt.Errorf("%s: floor for %s has no reason", path, pkg)
		}
		if _, both := f.Excluded[pkg]; both {
			return nil, fmt.Errorf("%s: %s is both floored and excluded", path, pkg)
		}
	}
	for _, pkg := range sortedKeys(f.Excluded) {
		if strings.TrimSpace(f.Excluded[pkg]) == "" {
			return nil, fmt.Errorf("%s: exclusion for %s has no reason", path, pkg)
		}
	}
	return &f, nil
}

// loadPackages reads the `go list -f '{{.ImportPath}}' ./...` output: one
// import path per line, with blank lines and #-comments ignored so a
// hand-written list can annotate itself.
func loadPackages(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || ignoredPath(line) {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no packages", path)
	}
	return out, nil
}

// sortedKeys returns m's keys in sorted order, so every report that walks a
// map does so deterministically.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
