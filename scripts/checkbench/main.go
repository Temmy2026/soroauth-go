// Command checkbench fails when a signing-path benchmark exceeds its
// committed alloc or byte budget.
//
// It parses `go test -bench` output (with -benchmem) and compares each
// benchmark's allocs/op and B/op against testdata/bench/budgets.json.
// ns/op is printed for humans but never gates the check: wall-clock time on
// shared CI runners is too noisy to be evidence of a regression, while
// allocation counts are deterministic for a given Go version.
//
// Usage:
//
//	go test -run '^$' -bench . -benchmem -count=1 . | tee /tmp/bench.out
//	go run ./scripts/checkbench /tmp/bench.out testdata/bench/budgets.json
//
// Exit status: 0 all budgets met, 1 a budget exceeded or a benchmark is
// missing, 2 usage or parse error.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Budget is the ceiling for one benchmark. A missing field means "no gate".
//
// Operation and Arm are optional metadata: when set they name the cost in the
// terms the gate is organised by ("signing", "delegates"), so a failure reads
// as "signing/delegates exceeded its baseline" instead of an opaque benchmark
// name. They do not affect the comparison, only how it is reported.
type Budget struct {
	Operation string `json:"operation,omitempty"`
	Arm       string `json:"arm,omitempty"`
	Allocs    *int   `json:"allocs,omitempty"`
	Bytes     *int   `json:"bytes,omitempty"`
}

// File is the on-disk budget document.
type File struct {
	// Note documents why the numbers are what they are. Not interpreted.
	Note    string            `json:"note,omitempty"`
	Budgets map[string]Budget `json:"budgets"`
}

// result is one parsed benchmark line.
type result struct {
	name    string
	allocs  int
	bytes   int
	nsPerOp float64
}

// benchLine matches e.g.:
//
//	BenchmarkAuthorizeEntry/v2-2   8080   153681 ns/op   5224 B/op   72 allocs/op
var benchLine = regexp.MustCompile(
	`^(Benchmark\S+)\s+\d+\s+([0-9.]+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op`)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: checkbench <bench.out> <budgets.json>\n")
		os.Exit(2)
	}
	results, err := parseBench(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkbench: %v\n", err)
		os.Exit(2)
	}
	budgets, err := loadBudgets(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkbench: %v\n", err)
		os.Exit(2)
	}

	// Index results by budget name. Go appends "-<GOMAXPROCS>" to the printed
	// name (e.g. BenchmarkPreimage-2); strip it so budgets do not depend on how
	// many CPUs the runner has.
	byName := make(map[string]result, len(results))
	for _, r := range results {
		byName[stripParallel(r.name)] = r
	}

	failed := false
	checked := 0
	for name, budget := range budgets.Budgets {
		r, ok := byName[name]
		if !ok {
			fmt.Printf("FAIL %-45s benchmark %s not found in output\n", budgetLabel(name, budget), name)
			failed = true
			continue
		}
		checked++
		problems := []string{}
		if budget.Allocs != nil && r.allocs > *budget.Allocs {
			problems = append(problems, fmt.Sprintf("allocs/op %d > baseline %d", r.allocs, *budget.Allocs))
		}
		if budget.Bytes != nil && r.bytes > *budget.Bytes {
			problems = append(problems, fmt.Sprintf("B/op %d > baseline %d", r.bytes, *budget.Bytes))
		}
		if len(problems) > 0 {
			fmt.Printf("FAIL %-45s %s\n", budgetLabel(name, budget), strings.Join(problems, "; "))
			fmt.Printf("     %-45s benchmark %s  (%.0f ns/op)\n", "", name, r.nsPerOp)
			failed = true
			continue
		}
		fmt.Printf("ok   %-45s allocs=%d baseline=%s  bytes=%d baseline=%s  (%.0f ns/op)\n",
			budgetLabel(name, budget), r.allocs, fmtBudget(budget.Allocs), r.bytes, fmtBudget(budget.Bytes), r.nsPerOp)
	}

	// Every observed benchmark that has a budget was checked; also warn about
	// budgeted names we never saw (already failed above) and observed names
	// with no budget (informational — new benches need a budget).
	budgeted := make(map[string]bool, len(budgets.Budgets))
	for name := range budgets.Budgets {
		budgeted[name] = true
	}
	for _, r := range results {
		if !budgeted[stripParallel(r.name)] {
			fmt.Printf("WARN %-45s no budget in budgets.json (add one)\n", stripParallel(r.name))
		}
	}

	if checked == 0 {
		fmt.Fprintln(os.Stderr, "checkbench: no budgeted benchmarks matched the output")
		os.Exit(1)
	}
	if failed {
		fmt.Fprintln(os.Stderr, "\ncheckbench: one or more budgets exceeded.")
		fmt.Fprintln(os.Stderr, "If the new cost is intentional, raise the budget in the same commit")
		fmt.Fprintln(os.Stderr, "and say why in the commit body. Never lower a budget to make CI green")
		fmt.Fprintln(os.Stderr, "without measuring first.")
		os.Exit(1)
	}
	fmt.Printf("\ncheckbench: %d budgets met\n", checked)
}

func fmtBudget(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

// budgetLabel renders a budget's human-facing identity: its operation and arm
// when the budget declares them, otherwise the benchmark name. Printing this
// instead of the raw benchmark path is what makes a failure read as
// "signing/delegates exceeded its baseline" rather than an opaque Go test name.
//
// Operation and Arm never affect the comparison; a budget with neither set
// still works and just falls back to the benchmark name.
func budgetLabel(name string, b Budget) string {
	switch {
	case b.Operation == "" && b.Arm == "":
		return name
	case b.Arm == "":
		return b.Operation
	case b.Operation == "":
		return b.Arm
	default:
		return b.Operation + "/" + b.Arm
	}
}

// stripParallel removes the trailing "-<n>" that `go test -bench` appends for
// GOMAXPROCS, e.g. "BenchmarkPreimage-2" → "BenchmarkPreimage".
func stripParallel(name string) string {
	i := strings.LastIndexByte(name, '-')
	if i <= 0 {
		return name
	}
	suffix := name[i+1:]
	if suffix == "" {
		return name
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return name
		}
	}
	return name[:i]
}

func parseBench(path string) ([]result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []result
	for _, line := range strings.Split(string(data), "\n") {
		m := benchLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ns, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parsing ns/op in %q: %w", line, err)
		}
		b, err := strconv.Atoi(m[3])
		if err != nil {
			return nil, fmt.Errorf("parsing B/op in %q: %w", line, err)
		}
		a, err := strconv.Atoi(m[4])
		if err != nil {
			return nil, fmt.Errorf("parsing allocs/op in %q: %w", line, err)
		}
		out = append(out, result{name: m[1], nsPerOp: ns, bytes: b, allocs: a})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no benchmark lines with -benchmem stats found in %s", path)
	}
	return out, nil
}

func loadBudgets(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	// Allow // comments? JSON does not. Keep pure JSON.
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(f.Budgets) == 0 {
		return nil, fmt.Errorf("%s has no budgets", path)
	}
	return &f, nil
}
