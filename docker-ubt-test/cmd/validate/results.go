package main

import (
	"fmt"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
)

// CheckResult represents the result of a single check.
type CheckResult struct {
	Name    string
	Status  string // "pass", "fail", "warn", "skip"
	Message string
}

// Results holds all validation results.
type Results struct {
	Checks  []CheckResult
	Passed  int
	Failed  int
	Warned  int
	Skipped int
}

// NewResults creates a new Results instance.
func NewResults() *Results {
	return &Results{
		Checks: make([]CheckResult, 0),
	}
}

// Pass records a passing check.
func (r *Results) Pass(name, message string) {
	r.Checks = append(r.Checks, CheckResult{
		Name:    name,
		Status:  "pass",
		Message: message,
	})
	r.Passed++
	fmt.Printf("  %s✓%s %s: %s\n", colorGreen, colorReset, name, message)
}

// Fail records a failing check.
func (r *Results) Fail(name, message string) {
	r.Checks = append(r.Checks, CheckResult{
		Name:    name,
		Status:  "fail",
		Message: message,
	})
	r.Failed++
	fmt.Printf("  %s✗%s %s: %s\n", colorRed, colorReset, name, message)
}

// Warn records a warning.
func (r *Results) Warn(name, message string) {
	r.Checks = append(r.Checks, CheckResult{
		Name:    name,
		Status:  "warn",
		Message: message,
	})
	r.Warned++
	fmt.Printf("  %s!%s %s: %s\n", colorYellow, colorReset, name, message)
}

// Skip records a skipped check.
func (r *Results) Skip(name, message string) {
	r.Checks = append(r.Checks, CheckResult{
		Name:    name,
		Status:  "skip",
		Message: message,
	})
	r.Skipped++
	fmt.Printf("  %s-%s %s: %s\n", colorCyan, colorReset, name, message)
}

// Print outputs the final summary.
func (r *Results) Print() {
	fmt.Println("==========================================")
	fmt.Println("Validation Summary")
	fmt.Println("==========================================")
	fmt.Println()
	fmt.Printf("  %sPassed:%s   %d\n", colorGreen, colorReset, r.Passed)
	fmt.Printf("  %sFailed:%s   %d\n", colorRed, colorReset, r.Failed)
	fmt.Printf("  %sWarnings:%s %d\n", colorYellow, colorReset, r.Warned)
	if r.Skipped > 0 {
		fmt.Printf("  %sSkipped:%s  %d\n", colorCyan, colorReset, r.Skipped)
	}
	fmt.Println()

	if r.Failed == 0 {
		if r.Warned == 0 {
			fmt.Printf("%sAll checks passed! UBT validation successful.%s\n", colorGreen, colorReset)
		} else {
			fmt.Printf("%sValidation passed with warnings. Check details above.%s\n", colorYellow, colorReset)
		}
	} else {
		fmt.Printf("%sValidation failed. Check errors above.%s\n", colorRed, colorReset)
	}
	fmt.Println()
}
