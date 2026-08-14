package execx

import (
	"context"
	"io"
	"strings"
)

// Call is one recorded invocation.
type Call struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

func (c Call) Line() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	return c.Name + " " + strings.Join(c.Args, " ")
}

// Fake records commands and replies with whatever Handler returns. It lives in
// the non-test build so every package can drive its own sequences.
type Fake struct {
	Calls   []Call
	Handler func(c Cmd) (Result, error)
}

func (f *Fake) Run(_ context.Context, c Cmd) (Result, error) {
	f.Calls = append(f.Calls, Call{Name: c.Name, Args: c.Args, Dir: c.Dir, Env: c.Env})
	var (
		res Result
		err error
	)
	if f.Handler != nil {
		res, err = f.Handler(c)
	}
	if err == nil && c.Stdout != nil && res.Stdout != "" {
		if _, wErr := io.WriteString(c.Stdout, res.Stdout); wErr != nil {
			return res, wErr
		}
		res.Stdout = ""
	}
	return res, err
}

// Lines renders the recorded calls for assertions.
func (f *Fake) Lines() []string {
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.Line())
	}
	return out
}
