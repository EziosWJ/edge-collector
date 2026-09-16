package script

import (
	"errors"
	"fmt"

	"go.starlark.net/resolve"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Error is a classified error. Source and runtime diagnostics expose the best
// known file, line, and column directly for API adapters.
type Error struct {
	Class    ErrorClass
	Message  string
	Filename string
	Line     int
	Column   int
	Cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	location := ""
	if e.Filename != "" && e.Line > 0 && e.Column > 0 {
		location = fmt.Sprintf("%s:%d:%d: ", e.Filename, e.Line, e.Column)
	} else if e.Filename != "" && e.Line > 0 {
		location = fmt.Sprintf("%s:%d: ", e.Filename, e.Line)
	}
	if e.Class == "" {
		return location + e.Message
	}
	return location + string(e.Class) + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newClassifiedError(class ErrorClass, message string, cause error) *Error {
	return &Error{Class: class, Message: message, Cause: cause}
}

func newPositionedError(class ErrorClass, message, filename string, line, column int, cause error) *Error {
	return &Error{
		Class:    class,
		Message:  message,
		Filename: filename,
		Line:     line,
		Column:   column,
		Cause:    cause,
	}
}

// ClassOf returns the first classified error in err's unwrap chain.
func ClassOf(err error) ErrorClass {
	for err != nil {
		if classified, ok := err.(*Error); ok {
			if classified == nil {
				return ""
			}
			return classified.Class
		}
		err = errors.Unwrap(err)
	}
	return ""
}

func classifySourceError(err error) *Error {
	if err == nil {
		return nil
	}
	filename, line, column, _ := sourceErrorPosition(err)
	return newPositionedError(ErrorClassScriptCompile, err.Error(), filename, line, column, err)
}

func sourceErrorPosition(err error) (filename string, line, column int, ok bool) {
	switch e := err.(type) {
	case syntax.Error:
		return e.Pos.Filename(), int(e.Pos.Line), int(e.Pos.Col), true
	case *syntax.Error:
		if e == nil {
			return "", 0, 0, false
		}
		return e.Pos.Filename(), int(e.Pos.Line), int(e.Pos.Col), true
	case resolve.Error:
		return e.Pos.Filename(), int(e.Pos.Line), int(e.Pos.Col), true
	case resolve.ErrorList:
		if len(e) == 0 {
			return "", 0, 0, false
		}
		return e[0].Pos.Filename(), int(e[0].Pos.Line), int(e[0].Pos.Col), true
	}
	return "", 0, 0, false
}

func classifyRuntimeError(err error) *Error {
	if err == nil {
		return nil
	}

	filename, line, column := runtimeErrorPosition(err)
	message := err.Error()
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) && evalErr != nil {
		message = evalErr.Backtrace()
	}
	if existing := classifiedErrorInChain(err); existing != nil {
		if existing.Filename != "" || existing.Line != 0 || existing.Column != 0 {
			return existing
		}
		copy := *existing
		copy.Filename = filename
		copy.Line = line
		copy.Column = column
		return &copy
	}
	return newPositionedError(ErrorClassScriptRuntime, message, filename, line, column, err)
}

func positionedCompileError(pos syntax.Position, message string) *Error {
	if !pos.IsValid() {
		return newClassifiedError(ErrorClassScriptCompile, message, nil)
	}
	return newPositionedError(ErrorClassScriptCompile, message, pos.Filename(), int(pos.Line), int(pos.Col), nil)
}

func runtimeErrorPosition(err error) (filename string, line, column int) {
	var evalErr *starlark.EvalError
	if !errors.As(err, &evalErr) || evalErr == nil {
		return "", 0, 0
	}
	// A builtin frame generally has the synthetic <builtin> filename. Walk
	// back to the first user frame so host failures point at the script call.
	for i := len(evalErr.CallStack) - 1; i >= 0; i-- {
		position := evalErr.CallStack[i].Pos
		if !position.IsValid() || position.Filename() == "<builtin>" {
			continue
		}
		return position.Filename(), int(position.Line), int(position.Col)
	}
	return "", 0, 0
}

// NewHostError lets an acquisition adapter preserve a stable Modbus error
// class when translating a transport or exception failure into Host.Error.
func NewHostError(class ErrorClass, err error) error {
	if err == nil {
		return nil
	}
	if class == "" {
		class = ErrorClassModbusTransport
	}
	return newClassifiedError(class, err.Error(), err)
}

func classifiedErrorInChain(err error) *Error {
	for err != nil {
		if classified, ok := err.(*Error); ok {
			if classified == nil {
				return nil
			}
			return classified
		}
		err = errors.Unwrap(err)
	}
	return nil
}
