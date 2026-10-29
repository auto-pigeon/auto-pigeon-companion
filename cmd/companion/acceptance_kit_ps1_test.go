package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PowerShell's constant and read-only automatic variables. Variable names are
// case-insensitive, so "$shellId = ..." is an assignment to $ShellId and stops
// the script before it does anything (NEW_368: the kit refused to start on a
// real Windows machine, under Windows PowerShell 5.1, with "Cannot overwrite
// variable ShellId because it is read-only or constant").
var powershellReadOnlyVariables = []string{
	"ShellId", "PID", "PSHOME", "Host", "HOME", "Error", "ExecutionContext",
	"PSVersionTable", "PSCulture", "PSUICulture", "true", "false", "null",
	"PSEdition", "IsWindows", "IsLinux", "IsMacOS", "IsCoreCLR",
}

func TestTheWindowsKitAssignsNoReadOnlyPowerShellVariable(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "acceptance", "run-acceptance.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	assignment := regexp.MustCompile(`(?im)^\s*\$(` + strings.Join(powershellReadOnlyVariables, "|") + `)\s*=[^=]`)
	for _, found := range assignment.FindAllString(string(script), -1) {
		t.Errorf("run-acceptance.ps1 assigns a read-only PowerShell variable: %q", strings.TrimSpace(found))
	}
}
