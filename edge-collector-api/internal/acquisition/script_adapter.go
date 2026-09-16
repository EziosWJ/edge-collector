package acquisition

import (
	"context"
	"errors"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
)

// NewScriptRuntimeValidator adapts the controlled Starlark compiler to the
// management service's structured validation contract. Validation compiles
// the draft only; it never invokes after_poll or performs device I/O.
func NewScriptRuntimeValidator(runtime *script.Runtime) ScriptValidator {
	return ScriptValidatorFunc(func(ctx context.Context, source string) (ScriptValidationResult, error) {
		if runtime == nil {
			return ScriptValidationResult{}, ErrScriptValidatorUnavailable
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return ScriptValidationResult{}, err
			}
		}
		err := runtime.Validate(source)
		if err == nil {
			return ScriptValidationResult{Valid: true, Errors: []ScriptValidationError{}}, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ScriptValidationResult{}, err
		}
		return ScriptValidationResult{Valid: false, Errors: []ScriptValidationError{scriptValidationError(err)}}, nil
	})
}

func scriptValidationError(err error) ScriptValidationError {
	result := ScriptValidationError{Message: err.Error()}
	var classified *script.Error
	if errors.As(err, &classified) && classified != nil {
		result.Filename = classified.Filename
		result.Line = classified.Line
		result.Column = classified.Column
		if classified.Message != "" {
			result.Message = classified.Message
		}
	}
	return result
}
