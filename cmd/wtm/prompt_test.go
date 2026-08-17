package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func answering(lines ...string) *prompter {
	return newPrompter(strings.NewReader(strings.Join(lines, "\n")+"\n"), io.Discard)
}

// An empty answer keeps what is already there: that is what makes an edit a
// series of enters plus the one field being changed.
func TestAskKeepsTheCurrentValueOnAnEmptyAnswer(t *testing.T) {
	p := answering("", "postgres")
	if got, err := p.ask("db service", "db"); err != nil || got != "db" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if got, err := p.ask("db service", "db"); err != nil || got != "postgres" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestAskRequiredInsistsUntilAnswered(t *testing.T) {
	p := answering("", "", "backend")
	got, err := p.askRequired("app service", "")
	if err != nil || got != "backend" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

// A closed stdin (a CI job, a pipe that ran dry) must end the stepper rather
// than loop on an answer that will never come.
func TestPromptsGiveUpOnAClosedInput(t *testing.T) {
	p := newPrompter(strings.NewReader(""), io.Discard)
	if _, err := p.ask("db service", "db"); !errors.Is(err, errNoInput) {
		t.Fatalf("err = %v, want errNoInput", err)
	}
	if _, err := p.askYesNo("enable?", false); !errors.Is(err, errNoInput) {
		t.Fatalf("err = %v, want errNoInput", err)
	}
}

func TestAskYesNoDefaultsToTheCurrentSetting(t *testing.T) {
	p := answering("", "", "n", "yes")
	for _, tc := range []struct {
		current, want bool
	}{{false, false}, {true, true}, {true, false}, {false, true}} {
		got, err := p.askYesNo("enable?", tc.current)
		if err != nil || got != tc.want {
			t.Fatalf("current %v: got %v, err %v", tc.current, got, err)
		}
	}
}

func TestAskPairsReadsUntilAnEmptyLine(t *testing.T) {
	p := answering("DB_NAME={{database}}", "not-a-pair", "DEBUG=1", "")
	got, err := p.askPairs("environment", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["DB_NAME"] != "{{database}}" || got["DEBUG"] != "1" {
		t.Fatalf("pairs = %v", got)
	}
}

// Answering nothing at all leaves the variables the project already had.
func TestAskPairsKeepsTheCurrentSetWhenNothingIsTyped(t *testing.T) {
	current := map[string]string{"DB_NAME": "{{database}}"}
	got, err := answering("").askPairs("environment", current)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["DB_NAME"] != "{{database}}" {
		t.Fatalf("pairs = %v", got)
	}
}
