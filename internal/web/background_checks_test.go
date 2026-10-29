package web

import (
	"errors"
	"testing"
)

// NEW_368: the background checks the page shows in one place.

func TestABackgroundCheckRunsOnceAndItsFailureIsKeptUntilItSucceeds(t *testing.T) {
	var board checkBoard
	if !board.begin("k", checkingPrograms, "") || board.begin("k", checkingPrograms, "") {
		t.Fatal("a check already running was started again")
	}
	board.detail("k", "EricW tools")
	if rows, _ := board.snapshot(); len(rows) != 1 || rows[0].Detail != "EricW tools" {
		t.Fatalf("running: %+v", rows)
	}
	board.end("k", errors.New("vis did not answer"), true)
	rows, failures := board.snapshot()
	if len(rows) != 0 || len(failures) != 1 || failures[0].Error != "vis did not answer" || !failures[0].Retry ||
		failures[0].Kind != checkingPrograms {
		t.Fatalf("after a failure: %+v %+v", rows, failures)
	}
	if !board.begin("k", checkingPrograms, "") {
		t.Fatal("a failed check cannot be retried")
	}
	board.end("k", nil, true)
	if _, failures := board.snapshot(); len(failures) != 0 {
		t.Fatalf("a success left the failure: %+v", failures)
	}
}
